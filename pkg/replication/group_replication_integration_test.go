//go:build integration

package replication_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// These tests pin docs/COMPAT.md entries 29, 30, 33 and 37 and the async
// facts' behavior on Group Replication members, against the topology of
// tests/docker/compose_group_replication.yaml. None calls t.Parallel: the
// servers are shared; some tests change the cluster's membership, its mode or
// its channels; and some change the standalone ACTIVE server's transaction
// size limit, its mode, or whether its plugin is installed.

// Statements spelled independently of the library's constants.
const (
	grSQLShowReplicaStatus     = "SHOW REPLICA STATUS"
	grSQLRecoveryChannelStatus = "SHOW REPLICA STATUS FOR CHANNEL 'group_replication_recovery'"
	grSQLApplierChannelStatus  = "SHOW REPLICA STATUS FOR CHANNEL 'group_replication_applier'"
	grSQLGroupNameVariable     = "SELECT @@GLOBAL.group_replication_group_name"
	grSQLShowGroupName         = "SHOW GLOBAL VARIABLES WHERE Variable_name = 'group_replication_group_name'"
	grSQLTableGroupName        = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name'"
	grSQLSizeLimitVariable = "SELECT @@GLOBAL.group_replication_transaction_size_limit"
	grSQLShowSizeLimit     = "SHOW GLOBAL VARIABLES WHERE Variable_name = 'group_replication_transaction_size_limit'"
	grSQLTableSizeLimit    = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_transaction_size_limit'"
	grSQLSetSizeLimit = "SET GLOBAL group_replication_transaction_size_limit = "
	grSQLModeVariable = "SELECT @@GLOBAL.group_replication_single_primary_mode"
	grModeOn          = "ON"  // single-primary, as performance_schema spells it
	grModeOff         = "OFF" // multi-primary
	grSQLShowMode     = "SHOW GLOBAL VARIABLES WHERE Variable_name = 'group_replication_single_primary_mode'"
	grSQLTableMode    = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_single_primary_mode'"
	grSQLPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication' LIMIT 18446744073709551615"
	grSQLAllVariables = "SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME LIKE 'group\\_replication\\_%' LIMIT 18446744073709551615"
	grSQLAllPersisted = "SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.persisted_variables " +
		"LIMIT 18446744073709551615"
	grSQLUninstallPlugin    = "UNINSTALL PLUGIN group_replication"
	grSQLInstallPlugin      = "INSTALL PLUGIN group_replication SONAME 'group_replication.so'"
	grSQLRecoveryConnection = "SELECT SERVICE_STATE FROM performance_schema.replication_connection_status " +
		"WHERE CHANNEL_NAME = 'group_replication_recovery'"
	grSQLExecuted           = "SELECT @@GLOBAL.gtid_executed"
	grSQLWaitFor            = "SELECT WAIT_FOR_EXECUTED_GTID_SET(?, 60)"
	grSQLShowReplicas       = "SHOW REPLICAS"
	grSQLConfiguredChannels = "SELECT CHANNEL_NAME, HOST FROM performance_schema.replication_connection_configuration"

	// Server error numbers these pins assert, from the MySQL error reference.
	erUnknownSystemVariable   = 1193 // ER_UNKNOWN_SYSTEM_VARIABLE
	erReplicaChannelOp        = 3139 // SHOW REPLICA STATUS refused for group_replication_applier
	erOptionPreventsStatement = 1290 // ER_OPTION_PREVENTS_STATEMENT, here super_read_only

	grExtChannel      = testsupport.ExtChannel
	grRecoveryChannel = "group_replication_recovery"
	// grRecoveryPlaceholderHost is the HOST a joined member's recovery channel
	// carries between recoveries: a six-character string, not SQL NULL.
	grRecoveryPlaceholderHost = "<NULL>"
	grPluginOn                = "ACTIVE"
	grPluginOff               = "DISABLED"
	grThreadYes               = "Yes"
)

// TestCompat30GroupReplicationPluginStatesIntegration pins COMPAT entry 30 and the
// GroupReplication fact on all four server states, each read by a USAGE-only
// account. The expected transaction size limit and mode are what root reads
// with @@GLOBAL on the same server, never a default written here: the library
// does not depend on the server's default.
//
// It is also T1, the differential: in each state both facts' PluginStatus
// equals the account's own direct information_schema.PLUGINS read, and the
// group name's row is absent exactly where the plugin is absent or
// DISABLED. It is the tripwire for a series that moves group_name into the
// server, as happened to group_replication_consistency: that server would
// report ACTIVE without the plugin.
func TestCompat30GroupReplicationPluginStatesIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	t.Run("not installed", func(t *testing.T) {
		admin, _ := testsupport.GroupReplicationStandalone(t, "grabsent")
		account := testsupport.CreateMySQLAccount(t, admin, "grabsent")
		usage := testsupport.MySQLDBAs(t, account)

		assertGroupNameRows(t, usage, 0, "an absent plugin's variable")
		assertSizeLimitRows(t, usage, 0, "an absent plugin's limit")
		assertModeRows(t, usage, 0, "an absent plugin's mode")
		assertServerError(t, usage, grSQLGroupNameVariable, erUnknownSystemVariable)
		assertServerError(t, usage, grSQLSizeLimitVariable, erUnknownSystemVariable)
		assertServerError(t, usage, grSQLModeVariable, erUnknownSystemVariable)
		assertGroupReplication(t, usage, replication.GroupReplication{})
		assertPluginStatusDifferential(t, usage, "")
	})

	t.Run("disabled", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, topology.Disabled, "grdisabled")
		usage := testsupport.MySQLDBAsOn(t, topology.DisabledDSN, account)

		assertGroupNameRows(t, usage, 0, "a DISABLED plugin's variable (it hides the variable)")
		assertSizeLimitRows(t, usage, 0, "a DISABLED plugin's limit (it hides the variable)")
		assertModeRows(t, usage, 0, "a DISABLED plugin's mode (it hides the variable)")
		assertServerError(t, usage, grSQLGroupNameVariable, erUnknownSystemVariable)
		assertServerError(t, usage, grSQLSizeLimitVariable, erUnknownSystemVariable)
		assertServerError(t, usage, grSQLModeVariable, erUnknownSystemVariable)
		assertGroupReplication(t, usage, replication.GroupReplication{PluginStatus: grPluginOff})
		assertPluginStatusDifferential(t, usage, grPluginOff)
	})

	t.Run("installed, never configured", func(t *testing.T) {
		limit := globalSizeLimit(t, topology.Installed)
		mode := globalMode(t, topology.Installed)
		account := testsupport.CreateMySQLAccount(t, topology.Installed, "grinstalled")
		usage := testsupport.MySQLDBAsOn(t, topology.InstalledDSN, account)

		// The fact reads "" from one row with an empty value here, and from
		// zero rows on the two states above; only the raw count tells them
		// apart.
		assertGroupNameRows(t, usage, 1, "an ACTIVE, unnamed plugin's variable (registered, empty)")
		assertSizeLimitRows(t, usage, 1, "an ACTIVE plugin's limit (registered)")
		assertModeRows(t, usage, 1, "an ACTIVE plugin's mode (registered)")
		assertGroupReplication(t, usage, replication.GroupReplication{
			PluginStatus: grPluginOn, TransactionSizeLimit: limit, TransactionSizeLimitKnown: true,
			SinglePrimaryMode: mode, SinglePrimaryModeKnown: true,
		})
		assertPluginStatusDifferential(t, usage, grPluginOn)
	})

	t.Run("cluster member", func(t *testing.T) {
		var name string
		if err := topology.Primary.QueryRowContext(t.Context(), grSQLGroupNameVariable).Scan(&name); err != nil {
			t.Fatalf("read the group name as root: %v", err)
		}
		if name == "" {
			t.Fatal("the cluster's group name is empty")
		}

		limit := globalSizeLimit(t, topology.Primary)
		account := testsupport.CreateMySQLAccount(t, topology.Primary, "grmember")
		usage := testsupport.MySQLDBAsOn(t, topology.PrimaryDSN, account)
		assertSizeLimitRows(t, usage, 1, "a member's limit")
		assertModeRows(t, usage, 1, "a member's mode")
		assertGroupReplication(t, usage, replication.GroupReplication{
			PluginStatus: grPluginOn, GroupName: name,
			TransactionSizeLimit: limit, TransactionSizeLimitKnown: true,
			SinglePrimaryMode: globalMode(t, topology.Primary), SinglePrimaryModeKnown: true,
		})
		assertPluginStatusDifferential(t, usage, grPluginOn)

		// Each member reports its own limit and mode; nothing here assumes
		// they agree.
		for role, db := range map[string]*sql.DB{"secondary": topology.Secondary, "rejoiner": topology.Rejoiner} {
			want := replication.GroupReplication{
				PluginStatus: grPluginOn, GroupName: name,
				TransactionSizeLimit: globalSizeLimit(t, db), TransactionSizeLimitKnown: true,
				SinglePrimaryMode: globalMode(t, db), SinglePrimaryModeKnown: true,
			}
			got, err := replication.NewInspector(db).GroupReplication(t.Context())
			if err != nil {
				t.Fatalf("GroupReplication on the %s: %v", role, err)
			}
			if got != want {
				t.Errorf("GroupReplication on the %s = %#v, want %#v", role, got, want)
			}
		}
	})
}

// TestGroupReplicationSinglePrimaryModeFollowsServerIntegration sets the mode
// OFF and back on the standalone ACTIVE server, which is in no group and
// whose checks are OFF, so the server accepts both values (docs/COMPAT.md
// entry 37), and requires the fact, read by a USAGE-only account, to report
// each. "Known" there means configured, not in force.
func TestGroupReplicationSinglePrimaryModeFollowsServerIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	account := testsupport.CreateMySQLAccount(t, topology.Installed, "grmode")
	usage := testsupport.MySQLDBAsOn(t, topology.InstalledDSN, account)

	original := globalMode(t, topology.Installed)
	t.Cleanup(func() { execOn(t, topology.Installed, "SET GLOBAL group_replication_single_primary_mode = "+original) })

	for _, mode := range []string{grModeOff, grModeOn} {
		execOn(t, topology.Installed, "SET GLOBAL group_replication_single_primary_mode = "+mode)
		if got := globalMode(t, topology.Installed); got != mode {
			t.Fatalf("@@GLOBAL reads %s after setting %s", got, mode)
		}
		got, err := replication.NewInspector(usage).GroupReplication(t.Context())
		if err != nil {
			t.Fatalf("GroupReplication after setting %s: %v", mode, err)
		}
		if got.SinglePrimaryMode != mode || !got.SinglePrimaryModeKnown || got.PluginStatus != grPluginOn {
			t.Errorf("GroupReplication after setting %s = %#v, want the mode %s, known, plugin ACTIVE", mode, got, mode)
		}
	}
}

// TestCompat37GroupModeSwitchIntegration switches the cluster to
// multi-primary and back with the switch functions and requires every ONLINE
// member to report the group's mode after each. Then a member stopped before
// a switch keeps and reports its own value while the others report the
// group's. The mode helper restores single-primary with gr1 primary, every
// member ONLINE and every persisted row as captured, and verifies it.
func TestCompat37GroupModeSwitchIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	change := testsupport.StartGroupModeChange(t, topology)

	members := map[string]*sql.DB{
		"primary": topology.Primary, "secondary": topology.Secondary, "rejoiner": topology.Rejoiner,
	}
	assertModes := func(t *testing.T, want map[string]string) {
		t.Helper()

		for role, mode := range want {
			got, err := replication.NewInspector(members[role]).GroupReplication(t.Context())
			if err != nil {
				t.Fatalf("GroupReplication on the %s: %v", role, err)
			}
			if got.SinglePrimaryMode != mode || !got.SinglePrimaryModeKnown {
				t.Errorf("GroupReplication on the %s = %#v, want the mode %s, known", role, got, mode)
			}
		}
	}

	change.SwitchToMultiPrimary(t)
	assertModes(t, map[string]string{"primary": grModeOff, "secondary": grModeOff, "rejoiner": grModeOff})
	change.SwitchToSinglePrimary(t)
	assertModes(t, map[string]string{"primary": grModeOn, "secondary": grModeOn, "rejoiner": grModeOn})

	execOn(t, topology.Rejoiner, "STOP GROUP_REPLICATION")
	testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberOffline)
	change.SwitchToMultiPrimary(t)
	assertModes(t, map[string]string{"primary": grModeOff, "secondary": grModeOff, "rejoiner": grModeOn})

	change.Restore(t)
	assertModes(t, map[string]string{"primary": grModeOn, "secondary": grModeOn, "rejoiner": grModeOn})
}

// TestCompat30PluginUninstallInstallIntegration is T5: on the standalone
// ACTIVE server, UNINSTALL PLUGIN takes both facts' PluginStatus to "" and
// INSTALL PLUGIN back to ACTIVE, each equal to the direct
// information_schema.PLUGINS read. Other tests share the server, so every
// Group Replication variable's runtime value and the server's persisted rows
// are captured first and restored and verified after.
//
// INSTALL PLUGIN also records the plugin in mysql.plugin, which the server's
// --plugin-load-add already loads; that row is not removed, and the topology
// is torn down with down -v after each run.
func TestCompat30PluginUninstallInstallIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	account := testsupport.CreateMySQLAccount(t, topology.Installed, "grtoggle")
	usage := testsupport.MySQLDBAsOn(t, topology.InstalledDSN, account)

	variables := variableValues(t, topology.Installed, grSQLAllVariables)
	persisted := variableValues(t, topology.Installed, grSQLAllPersisted)
	if len(variables) == 0 {
		t.Fatal("the standalone ACTIVE server lists no group_replication_% variable before the change")
	}
	t.Cleanup(func() {
		if countRows(t, topology.Installed, grSQLPluginStatus) == 0 {
			execOn(t, topology.Installed, grSQLInstallPlugin)
		}
		restoreVariables(t, topology.Installed, variables)
		restorePersisted(t, topology.Installed, persisted)
		verifyRestored(t, topology.Installed, variables, persisted)
	})

	execOn(t, topology.Installed, grSQLUninstallPlugin)
	assertPluginStatusDifferential(t, usage, "")
	assertGroupReplication(t, usage, replication.GroupReplication{})

	execOn(t, topology.Installed, grSQLInstallPlugin)
	assertPluginStatusDifferential(t, usage, grPluginOn)
}

// TestCompat36UninstallRefusedOnSecondaryIntegration is T4's measured premise:
// UNINSTALL PLUGIN must write mysql.plugin first, and a running secondary,
// like a fenced primary, has super_read_only = 1, so the statement fails with
// 1290 and the plugin stays ACTIVE. UNINSTALL is never run on a fenced
// primary; that one is inferred from the same variable (docs/COMPAT.md entry
// 36). If it unexpectedly succeeds, the registered recovery reinstalls the
// plugin, restores the member's Group Replication variables, starts it, waits
// for ONLINE, and the test fails.
func TestCompat36UninstallRefusedOnSecondaryIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	secondary := topology.Secondary

	if readOnly := scalarString(t, secondary, "SELECT @@GLOBAL.super_read_only"); readOnly != "1" {
		t.Fatalf("the secondary's super_read_only is %s, want 1", readOnly)
	}
	variables := variableValues(t, secondary, grSQLAllVariables)
	persisted := variableValues(t, secondary, grSQLAllPersisted)
	t.Cleanup(func() {
		if countRows(t, secondary, grSQLPluginStatus) != 0 {
			return
		}
		t.Error("UNINSTALL PLUGIN succeeded on a running secondary; recovering the member")
		execOn(t, secondary, "SET GLOBAL super_read_only = OFF")
		execOn(t, secondary, grSQLInstallPlugin)
		restoreVariables(t, secondary, variables)
		restorePersisted(t, secondary, persisted)
		execOn(t, secondary, "START GROUP_REPLICATION")
		testsupport.WaitMemberState(t, secondary, testsupport.MemberOnline)
		verifyRestored(t, secondary, variables, persisted)
	})

	assertServerError(t, secondary, grSQLUninstallPlugin, erOptionPreventsStatement)
	if status := scalarString(t, secondary, grSQLPluginStatus); status != grPluginOn {
		t.Errorf("after the refused UNINSTALL the secondary's plugin is %q, want %s", status, grPluginOn)
	}
	assertGroupReplicationStatus(t, secondary, grPluginOn)
}

// TestGroupReplicationTransactionSizeLimitFollowsServerIntegration sets the
// limit on the standalone ACTIVE server, which is in no group, and requires
// the fact, read by a USAGE-only account, to report each value: 0, which the
// manual documents as no limit, 1, and the documented maximum. It never
// changes a cluster member, where a small limit would reject other tests'
// transactions.
func TestGroupReplicationTransactionSizeLimitFollowsServerIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	account := testsupport.CreateMySQLAccount(t, topology.Installed, "grlimit")
	usage := testsupport.MySQLDBAsOn(t, topology.InstalledDSN, account)

	// Registered after the account and before the first change: cleanups run
	// last-in first-out, so the original limit is back before the account is
	// dropped, and a failure below still restores it.
	original := globalSizeLimit(t, topology.Installed)
	t.Cleanup(func() { setSizeLimit(t, topology.Installed, original) })

	for _, limit := range []uint64{0, 1, 2147483647} {
		setSizeLimit(t, topology.Installed, limit)
		if got := globalSizeLimit(t, topology.Installed); got != limit {
			t.Fatalf("@@GLOBAL reads %d after setting %d", got, limit)
		}
		// The plugin is ACTIVE here, so the fact also reports the mode, read
		// by root on the same server.
		assertGroupReplication(t, usage, replication.GroupReplication{
			PluginStatus: grPluginOn, TransactionSizeLimit: limit, TransactionSizeLimitKnown: true,
			SinglePrimaryMode: globalMode(t, topology.Installed), SinglePrimaryModeKnown: true,
		})
	}
}

// TestCompat29ReplicaStatusOnGroupMembersIntegration pins the healthy half of COMPAT entry
// 29: no member's SHOW REPLICA STATUS lists a Group Replication channel,
// although the manual says only the applier channel is excluded, and FOR
// CHANNEL 'group_replication_recovery' returns a row only on a member whose
// recovery channel has a source host, which in this topology is not the member
// that bootstrapped the group.
func TestCompat29ReplicaStatusOnGroupMembersIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	// The expected counts are this topology's history, not a rule about roles:
	// the secondary and the rejoiner joined through distributed recovery, and
	// the primary bootstrapped the group and never rejoined (EnsureGroupOnline
	// never restarts it).
	for _, member := range []struct {
		role             string
		db               *sql.DB
		wantRecoveryRows int
	}{
		{"primary", topology.Primary, 0},
		{"secondary", topology.Secondary, 1},
		{"rejoiner", topology.Rejoiner, 1},
	} {
		assertNoReplicaChannels(t, member.role, member.db)
		assertServerError(t, member.db, grSQLApplierChannelStatus, erReplicaChannelOp)
		assertRecoveryChannelRows(t, member.role, member.db, member.wantRecoveryRows)
	}
}

// TestCompat29And33HeldRecoveryIntegration pins the recovering half of COMPAT
// entry 29 and entry 33. The rejoiner is stopped, the primary writes, and a
// table lock on the rejoiner holds its recovery applier, so the member is
// observably RECOVERING with its recovery connection running.
func TestCompat29And33HeldRecoveryIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	ctx := t.Context()

	schema := "grhold_" + randomSuffix(t)
	execOn(t, topology.Primary, "CREATE DATABASE "+schema)
	t.Cleanup(func() { execOn(t, topology.Primary, "DROP DATABASE IF EXISTS "+schema) })
	execOn(t, topology.Primary, "CREATE TABLE "+schema+".t (id INT PRIMARY KEY)")
	waitExecuted(t, topology.Primary, topology.Rejoiner)

	rejoiner := testsupport.ServerUUID(t, topology.Rejoiner)

	// A registration left by an earlier run lasts as long as its donor's dump
	// thread (COMPAT entry 33). Waiting it out first means the registration
	// asserted below is this run's.
	testsupport.PollUntil(t, "no donor still lists the rejoiner from an earlier run", func() (bool, string) {
		for role, db := range map[string]*sql.DB{"primary": topology.Primary, "secondary": topology.Secondary} {
			if registers(t, db, rejoiner) || rawRegisters(t, db, rejoiner) {
				return false, "the " + role + " lists " + rejoiner
			}
		}

		return true, ""
	})

	// Registered before the stop, so a failure anywhere below still returns
	// the group to all-ONLINE for the next test.
	t.Cleanup(func() { testsupport.EnsureGroupOnline(t, topology) })

	execOn(t, topology.Rejoiner, "STOP GROUP_REPLICATION")
	testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberOffline)
	execOn(t, topology.Primary, "INSERT INTO "+schema+".t VALUES (1)")

	lock, err := topology.Rejoiner.Conn(ctx)
	if err != nil {
		t.Fatalf("pin a connection on the rejoiner: %v", err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, "LOCK TABLES "+schema+".t READ"); err != nil {
		t.Fatalf("lock the table on the rejoiner: %v", err)
	}
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			if _, err := lock.ExecContext(ctx, "UNLOCK TABLES"); err != nil {
				t.Errorf("unlock the rejoiner's table: %v", err)
			}
		}
	}
	defer unlock()

	started := make(chan error, 1)
	go func() {
		_, startErr := topology.Rejoiner.ExecContext(ctx, "START GROUP_REPLICATION")
		started <- startErr
	}()

	// Independent evidence first: the member is RECOVERING and its recovery
	// connection is running. Only then is the absence below meaningful.
	testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberRecovering)
	testsupport.PollUntil(t, "recovery connection ON", func() (bool, string) {
		state := scalarString(t, topology.Rejoiner, grSQLRecoveryConnection)

		return state == "ON", state
	})

	assertNoReplicaChannels(t, "recovering rejoiner", topology.Rejoiner)
	if rows := countRows(t, topology.Rejoiner, grSQLRecoveryChannelStatus); rows != 1 {
		t.Errorf("FOR CHANNEL group_replication_recovery on the recovering rejoiner: %d rows, want 1", rows)
	}

	donor := ""
	var donorDB *sql.DB
	testsupport.PollUntil(t, "a donor registers the rejoiner", func() (bool, string) {
		for role, db := range map[string]*sql.DB{"primary": topology.Primary, "secondary": topology.Secondary} {
			if registers(t, db, rejoiner) {
				donor, donorDB = role, db

				return true, role
			}
		}

		return false, "no donor lists " + rejoiner
	})
	if !rawRegisters(t, donorDB, rejoiner) {
		t.Errorf("the %s's raw SHOW REPLICAS does not list %s, though RegisteredReplicas does", donor, rejoiner)
	}

	unlock()
	testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberOnline)
	if startErr := <-started; startErr != nil {
		t.Fatalf("START GROUP_REPLICATION on the rejoiner: %v", startErr)
	}

	// The donor's row is not asserted after ONLINE: it lasts only as long as
	// the donor's dump thread, whose exit time varies (COMPAT entry 33).
	assertNoReplicaChannels(t, "rejoined member", topology.Rejoiner)
	t.Logf("the %s registered the rejoiner during recovery", donor)
}

// TestReplicaStatusKeepsAsyncChannelOnGroupMemberIntegration proves the async fact still
// reports an ordinary async channel on a Group Replication member: the
// external source replicates into the primary as channel grext.
func TestReplicaStatusKeepsAsyncChannelOnGroupMemberIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	// GroupReplicationTopology has already removed a channel an interrupted
	// earlier run left behind.
	execOn(t, topology.Primary, "CHANGE REPLICATION SOURCE TO SOURCE_HOST = '"+topology.ExtHost+
		"', SOURCE_PORT = 3306, SOURCE_USER = 'repl', SOURCE_PASSWORD = 'repl', "+
		"SOURCE_AUTO_POSITION = 1, GET_SOURCE_PUBLIC_KEY = 1 FOR CHANNEL '"+grExtChannel+"'")
	t.Cleanup(func() {
		execOn(t, topology.Primary, "STOP REPLICA FOR CHANNEL '"+grExtChannel+"'")
		execOn(t, topology.Primary, "RESET REPLICA ALL FOR CHANNEL '"+grExtChannel+"'")
	})
	execOn(t, topology.Primary, "START REPLICA FOR CHANNEL '"+grExtChannel+"'")

	schema := "grext_" + randomSuffix(t)
	execOn(t, topology.Ext, "CREATE DATABASE "+schema)
	// Runs before the channel cleanup above (LIFO), and waits for the drop to
	// reach the primary, so no test data outlives the test.
	t.Cleanup(func() {
		execOn(t, topology.Ext, "DROP DATABASE IF EXISTS "+schema)
		waitExecuted(t, topology.Ext, topology.Primary)
	})
	execOn(t, topology.Ext, "CREATE TABLE "+schema+".t (id INT PRIMARY KEY)")
	execOn(t, topology.Ext, "INSERT INTO "+schema+".t VALUES (1)")

	// The row reaches the secondary through the channel and then the group.
	testsupport.PollUntil(t, "the external row reaches the secondary", func() (bool, string) {
		var n int
		err := topology.Secondary.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+schema+".t").Scan(&n)
		if err != nil {
			return false, err.Error()
		}

		return n == 1, "rows on the secondary"
	})

	channels, err := replication.NewInspector(topology.Primary).ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the primary: %v", err)
	}
	if len(channels) != 1 || channels[0].ChannelName != grExtChannel {
		t.Fatalf("ReplicaStatus on the primary = %+v, want exactly channel %q", channels, grExtChannel)
	}
	if channels[0].IORunning != grThreadYes || channels[0].SQLRunning != grThreadYes {
		t.Errorf("channel %q = IO %q / SQL %q, want Yes / Yes", grExtChannel, channels[0].IORunning, channels[0].SQLRunning)
	}

	assertNoReplicaChannels(t, "secondary", topology.Secondary)
}

// assertPluginStatusDifferential requires GroupReplication's and
// GroupConsistency's PluginStatus, read through db, to equal db's own direct
// information_schema.PLUGINS read, and that read to equal want: "" for no row.
func assertPluginStatusDifferential(t *testing.T, db *sql.DB, want string) {
	t.Helper()

	direct := ""
	if countRows(t, db, grSQLPluginStatus) > 0 {
		direct = scalarString(t, db, grSQLPluginStatus)
	}
	if direct != want {
		t.Fatalf("the direct information_schema.PLUGINS read is %q, want %q", direct, want)
	}
	inspector := replication.NewInspector(db)
	group, err := inspector.GroupReplication(t.Context())
	if err != nil {
		t.Fatalf("GroupReplication: %v", err)
	}
	consistency, err := inspector.GroupConsistency(t.Context())
	if err != nil {
		t.Fatalf("GroupConsistency: %v", err)
	}
	if group.PluginStatus != direct || consistency.PluginStatus != direct {
		t.Errorf("PluginStatus: GroupReplication %q, GroupConsistency %q; the direct read %q",
			group.PluginStatus, consistency.PluginStatus, direct)
	}
}

func assertGroupReplicationStatus(t *testing.T, db *sql.DB, want string) {
	t.Helper()

	got, err := replication.NewInspector(db).GroupReplication(t.Context())
	if err != nil {
		t.Fatalf("GroupReplication: %v", err)
	}
	if got.PluginStatus != want {
		t.Errorf("GroupReplication().PluginStatus = %q, want %q", got.PluginStatus, want)
	}
}

func assertGroupReplication(t *testing.T, db *sql.DB, want replication.GroupReplication) {
	t.Helper()

	got, err := replication.NewInspector(db).GroupReplication(t.Context())
	if err != nil {
		t.Fatalf("GroupReplication() returned error %v, want nil", err)
	}
	if got != want {
		t.Errorf("GroupReplication() = %#v, want %#v", got, want)
	}
}

// assertNoReplicaChannels checks both the shipped method and the raw
// statement, so a library-side filter could not fake the absence.
func assertNoReplicaChannels(t *testing.T, role string, db *sql.DB) {
	t.Helper()

	channels, err := replication.NewInspector(db).ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the %s: %v", role, err)
	}
	if len(channels) != 0 {
		t.Errorf("ReplicaStatus on the %s = %+v, want no channels", role, channels)
	}
	if rows := countRows(t, db, grSQLShowReplicaStatus); rows != 0 {
		t.Errorf("SHOW REPLICA STATUS on the %s: %d rows, want 0", role, rows)
	}
}

// assertRecoveryChannelRows pins what SHOW REPLICA STATUS FOR CHANNEL
// 'group_replication_recovery' returns on a healthy member: one row exactly
// when Performance Schema configures that channel with a non-empty source host,
// otherwise no row and no error. want is the count this member must show, so
// both sides of that rule are exercised.
func assertRecoveryChannelRows(t *testing.T, role string, db *sql.DB, want int) {
	t.Helper()

	hosts := configuredChannelHosts(t, db)
	if _, ok := hosts[grApplierChannel]; !ok {
		t.Errorf("Performance Schema on the %s lists %v, want %s among them", role, hosts, grApplierChannel)
	}
	host, configured := hosts[grRecoveryChannel]
	byHost := 0
	if configured && host != "" {
		byHost = 1
	}
	if byHost != want {
		t.Errorf("the %s's recovery channel: configured %t with host %q; want a configured host: %t",
			role, configured, host, want == 1)
	}
	if want == 1 && host != grRecoveryPlaceholderHost {
		t.Errorf("the %s's recovery channel host = %q, want the placeholder string %q", role, host, grRecoveryPlaceholderHost)
	}
	if rows := countRows(t, db, grSQLRecoveryChannelStatus); rows != byHost {
		t.Errorf("%s on the %s: %d rows, want %d (recovery channel configured %t, host %q)",
			grSQLRecoveryChannelStatus, role, rows, byHost, configured, host)
	}
}

// configuredChannelHosts maps each channel in
// performance_schema.replication_connection_configuration to its HOST, with a
// NULL host read as "".
func configuredChannelHosts(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	rows, err := db.QueryContext(ctx, grSQLConfiguredChannels)
	if err != nil {
		t.Fatalf("%s: %v", grSQLConfiguredChannels, err)
	}
	defer rows.Close()

	hosts := map[string]string{}
	for rows.Next() {
		var channel string
		var host sql.NullString
		if err := rows.Scan(&channel, &host); err != nil {
			t.Fatalf("scan %s: %v", grSQLConfiguredChannels, err)
		}
		hosts[channel] = host.String
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", grSQLConfiguredChannels, err)
	}

	return hosts
}

func assertServerError(t *testing.T, db *sql.DB, query string, number uint16) {
	t.Helper()

	// ExecContext reads and discards any result set, so only the server's
	// refusal is observed.
	ctx, cancel := statementContext(t)
	defer cancel()

	_, err := db.ExecContext(ctx, query)
	if err == nil {
		t.Fatalf("%q succeeded, want server error %d", query, number)
	}

	var serverErr *mysql.MySQLError
	if !errors.As(err, &serverErr) || serverErr.Number != number {
		t.Errorf("%q failed with %v, want server error %d", query, err, number)
	}
}

// countRows runs on statementContext, so it also works from t.Cleanup, where
// t.Context() is already canceled.
func countRows(t *testing.T, db replication.Querier, query string) int {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%q: %v", query, err)
	}

	return n
}

// assertGroupNameRows requires want rows for the group-name variable from both
// performance_schema.global_variables, which the fact reads, and SHOW GLOBAL
// VARIABLES, which COMPAT entry 30 describes: the table answers every plugin
// state as the statement does.
func assertGroupNameRows(t *testing.T, db replication.Querier, want int, state string) {
	t.Helper()

	for _, query := range []string{grSQLTableGroupName, grSQLShowGroupName} {
		if rows := countRows(t, db, query); rows != want {
			t.Errorf("%s: %q returned %d rows, want %d", state, query, rows, want)
		}
	}
}

// assertSizeLimitRows is assertGroupNameRows for
// group_replication_transaction_size_limit: the table the fact reads and SHOW
// GLOBAL VARIABLES must both return want rows. It is what tells a known limit
// of 0 from an unregistered variable, which the fact also reports as 0.
func assertSizeLimitRows(t *testing.T, db replication.Querier, want int, state string) {
	t.Helper()

	for _, query := range []string{grSQLTableSizeLimit, grSQLShowSizeLimit} {
		if rows := countRows(t, db, query); rows != want {
			t.Errorf("%s: %q returned %d rows, want %d", state, query, rows, want)
		}
	}
}

// assertModeRows is assertGroupNameRows for
// group_replication_single_primary_mode.
func assertModeRows(t *testing.T, db replication.Querier, want int, state string) {
	t.Helper()

	for _, query := range []string{grSQLTableMode, grSQLShowMode} {
		if rows := countRows(t, db, query); rows != want {
			t.Errorf("%s: %q returned %d rows, want %d", state, query, rows, want)
		}
	}
}

// globalMode reads group_replication_single_primary_mode with @@GLOBAL, a
// spelling independent of the table the fact reads, which answers 1 or 0, and
// returns the table's spelling, ON or OFF. Call it only on a server where the
// variable is registered.
func globalMode(t *testing.T, db *sql.DB) string {
	t.Helper()

	switch value := scalarString(t, db, grSQLModeVariable); value {
	case "1":
		return grModeOn
	case "0":
		return grModeOff
	default:
		t.Fatalf("%s = %q, want 1 or 0", grSQLModeVariable, value)

		return ""
	}
}

// variableValues maps VARIABLE_NAME to VARIABLE_VALUE for query's rows.
func variableValues(t *testing.T, db *sql.DB, query string) map[string]string {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var name string
		var value sql.NullString
		if err := rows.Scan(&name, &value); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		values[name] = value.String
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", query, err)
	}

	return values
}

// restoreVariables sets, with SET GLOBAL, every variable whose runtime value
// differs from want: a decimal value unquoted, anything else as a string.
// A variable the server will not set fails the test with the server's error.
func restoreVariables(t *testing.T, db *sql.DB, want map[string]string) {
	t.Helper()

	current := variableValues(t, db, grSQLAllVariables)
	names := slices.Sorted(maps.Keys(want))
	for _, name := range names {
		value := want[name]
		if current[name] == value {
			continue
		}
		literal := "'" + strings.ReplaceAll(value, "'", "''") + "'"
		if _, err := strconv.ParseUint(value, 10, 64); err == nil {
			literal = value
		}
		execOn(t, db, "SET GLOBAL "+name+" = "+literal)
	}
}

// restorePersisted takes db's persisted rows back to want, with the mode
// helper's statements.
func restorePersisted(t *testing.T, db *sql.DB, want map[string]string) {
	t.Helper()

	rows := func(values map[string]string) map[string]testsupport.PersistedRow {
		converted := make(map[string]testsupport.PersistedRow, len(values))
		for name, value := range values {
			converted[name] = testsupport.PersistedRow{Present: true, Value: value}
		}

		return converted
	}
	current := variableValues(t, db, grSQLAllPersisted)
	for _, statement := range testsupport.PersistedRestoreStatements(rows(want), rows(current)) {
		execOn(t, db, statement)
	}
}

// verifyRestored reads db's Group Replication variables and persisted rows
// again and requires both to equal what was captured.
func verifyRestored(t *testing.T, db *sql.DB, variables, persisted map[string]string) {
	t.Helper()

	if got := variableValues(t, db, grSQLAllVariables); !maps.Equal(got, variables) {
		t.Errorf("after the restore the Group Replication variables are %v, want %v", got, variables)
	}
	if got := variableValues(t, db, grSQLAllPersisted); !maps.Equal(got, persisted) {
		t.Errorf("after the restore the persisted rows are %v, want %v", got, persisted)
	}
}

// globalSizeLimit reads group_replication_transaction_size_limit with
// @@GLOBAL, a spelling independent of the table the fact reads. Call it only
// on a server where the variable is registered.
func globalSizeLimit(t *testing.T, db *sql.DB) uint64 {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	var limit uint64
	if err := db.QueryRowContext(ctx, grSQLSizeLimitVariable).Scan(&limit); err != nil {
		t.Fatalf("%q: %v", grSQLSizeLimitVariable, err)
	}

	return limit
}

// setSizeLimit changes group_replication_transaction_size_limit with SET
// GLOBAL, never SET PERSIST, so a server restart forgets it.
func setSizeLimit(t *testing.T, db *sql.DB, limit uint64) {
	t.Helper()

	execOn(t, db, grSQLSetSizeLimit+strconv.FormatUint(limit, 10))
}

func registers(t *testing.T, db *sql.DB, replicaUUID string) bool {
	t.Helper()

	replicas, err := replication.NewInspector(db).RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("RegisteredReplicas: %v", err)
	}
	for _, replica := range replicas {
		if replica.ReplicaUUID == replicaUUID {
			return true
		}
	}

	return false
}

// rawRegisters reports whether db's raw SHOW REPLICAS lists replicaUUID,
// reading Replica_UUID by name, independently of the library's decoder.
func rawRegisters(t *testing.T, db *sql.DB, replicaUUID string) bool {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	rows, err := db.QueryContext(ctx, grSQLShowReplicas)
	if err != nil {
		t.Fatalf("%s: %v", grSQLShowReplicas, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("%s columns: %v", grSQLShowReplicas, err)
	}
	uuidAt := slices.Index(columns, "Replica_UUID")
	if uuidAt < 0 {
		t.Fatalf("%s has no Replica_UUID column: %v", grSQLShowReplicas, columns)
	}

	found := false
	values := make([]sql.RawBytes, len(columns))
	targets := make([]any, len(columns))
	for index := range values {
		targets[index] = &values[index]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			t.Fatalf("scan %s: %v", grSQLShowReplicas, err)
		}
		if string(values[uuidAt]) == replicaUUID {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", grSQLShowReplicas, err)
	}

	return found
}

// grStatementTimeout bounds each statement the helpers below issue. They also
// run from t.Cleanup, where t.Context() is already canceled, so they derive
// their context with context.WithoutCancel.
const grStatementTimeout = 90 * time.Second

func statementContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()

	return context.WithTimeout(context.WithoutCancel(t.Context()), grStatementTimeout)
}

func scalarString(t *testing.T, db *sql.DB, query string) string {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	var value string
	if err := db.QueryRowContext(ctx, query).Scan(&value); err != nil {
		t.Fatalf("%q: %v", query, err)
	}

	return value
}

func execOn(t *testing.T, db *sql.DB, statement string) {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	if _, err := db.ExecContext(ctx, statement); err != nil {
		t.Fatalf("%q: %v", statement, err)
	}
}

// waitExecuted blocks until to has executed every transaction from has.
func waitExecuted(t *testing.T, from, to *sql.DB) {
	t.Helper()

	executed := scalarString(t, from, grSQLExecuted)

	ctx, cancel := statementContext(t)
	defer cancel()

	var timedOut int
	if err := to.QueryRowContext(ctx, grSQLWaitFor, executed).Scan(&timedOut); err != nil {
		t.Fatalf("wait for %q: %v", executed, err)
	}
	if timedOut != 0 {
		t.Fatalf("transactions %q did not arrive within 60 s", executed)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate suffix: %v", err)
	}

	return hex.EncodeToString(suffix)
}
