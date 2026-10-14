//go:build integration

package replication_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// These tests pin docs/COMPAT.md entries 29, 30, and 33 and the async facts'
// behavior on Group Replication members, against the topology of
// tests/docker/compose_group_replication.yaml. None calls t.Parallel: the
// cluster is shared and two of them change its membership or its channels.

// Statements spelled independently of the library's constants.
const (
	grSQLShowReplicaStatus     = "SHOW REPLICA STATUS"
	grSQLRecoveryChannelStatus = "SHOW REPLICA STATUS FOR CHANNEL 'group_replication_recovery'"
	grSQLApplierChannelStatus  = "SHOW REPLICA STATUS FOR CHANNEL 'group_replication_applier'"
	grSQLGroupNameVariable     = "SELECT @@GLOBAL.group_replication_group_name"
	grSQLShowGroupName         = "SHOW GLOBAL VARIABLES WHERE Variable_name = 'group_replication_group_name'"
	grSQLTableGroupName        = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name'"
	grSQLRecoveryConnection = "SELECT SERVICE_STATE FROM performance_schema.replication_connection_status " +
		"WHERE CHANNEL_NAME = 'group_replication_recovery'"
	grSQLExecuted     = "SELECT @@GLOBAL.gtid_executed"
	grSQLWaitFor      = "SELECT WAIT_FOR_EXECUTED_GTID_SET(?, 60)"
	grSQLShowReplicas = "SHOW REPLICAS"

	// Server error numbers these pins assert, from the MySQL error reference.
	erUnknownSystemVariable = 1193 // ER_UNKNOWN_SYSTEM_VARIABLE
	erReplicaChannelOp      = 3139 // SHOW REPLICA STATUS refused for group_replication_applier

	grExtChannel = testsupport.ExtChannel
	grPluginOn   = "ACTIVE"
	grPluginOff  = "DISABLED"
	grThreadYes  = "Yes"
)

// TestCompat30GroupReplicationPluginStatesIntegration pins COMPAT entry 30 and the
// GroupReplication fact on all four server states, each read by a USAGE-only
// account.
func TestCompat30GroupReplicationPluginStatesIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	t.Run("not installed", func(t *testing.T) {
		admin, _ := testsupport.GroupReplicationStandalone(t, "grabsent")
		account := testsupport.CreateMySQLAccount(t, admin, "grabsent")
		usage := testsupport.MySQLDBAs(t, account)

		assertGroupReplication(t, usage, replication.GroupReplication{})
		assertGroupNameRows(t, usage, 0, "an absent plugin's variable")
		assertServerError(t, usage, grSQLGroupNameVariable, erUnknownSystemVariable)
	})

	t.Run("disabled", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, topology.Disabled, "grdisabled")
		usage := testsupport.MySQLDBAsOn(t, topology.DisabledDSN, account)

		assertGroupReplication(t, usage, replication.GroupReplication{PluginStatus: grPluginOff})
		assertGroupNameRows(t, usage, 0, "a DISABLED plugin's variable (it hides the variable)")
		assertServerError(t, usage, grSQLGroupNameVariable, erUnknownSystemVariable)
	})

	t.Run("installed, never configured", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, topology.Installed, "grinstalled")
		usage := testsupport.MySQLDBAsOn(t, topology.InstalledDSN, account)

		assertGroupReplication(t, usage, replication.GroupReplication{PluginStatus: grPluginOn})
		// The fact reads "" from one row with an empty value here, and from
		// zero rows on the two states above; only the raw count tells them
		// apart.
		assertGroupNameRows(t, usage, 1, "an ACTIVE, unnamed plugin's variable (registered, empty)")
	})

	t.Run("cluster member", func(t *testing.T) {
		var name string
		if err := topology.Primary.QueryRowContext(t.Context(), grSQLGroupNameVariable).Scan(&name); err != nil {
			t.Fatalf("read the group name as root: %v", err)
		}
		if name == "" {
			t.Fatal("the cluster's group name is empty")
		}

		account := testsupport.CreateMySQLAccount(t, topology.Primary, "grmember")
		usage := testsupport.MySQLDBAsOn(t, topology.PrimaryDSN, account)
		assertGroupReplication(t, usage, replication.GroupReplication{PluginStatus: grPluginOn, GroupName: name})

		for role, db := range map[string]*sql.DB{"secondary": topology.Secondary, "rejoiner": topology.Rejoiner} {
			got, err := replication.NewInspector(db).GroupReplication(t.Context())
			if err != nil {
				t.Fatalf("GroupReplication on the %s: %v", role, err)
			}
			if got.PluginStatus != grPluginOn || got.GroupName != name {
				t.Errorf("GroupReplication on the %s = %#v, want ACTIVE with %q", role, got, name)
			}
		}
	})
}

// TestCompat29ReplicaStatusOnGroupMembersIntegration pins the healthy half of COMPAT entry
// 29: no member's SHOW REPLICA STATUS lists a Group Replication channel,
// although the manual says only the applier channel is excluded.
func TestCompat29ReplicaStatusOnGroupMembersIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	for role, db := range map[string]*sql.DB{
		"primary": topology.Primary, "secondary": topology.Secondary, "rejoiner": topology.Rejoiner,
	} {
		assertNoReplicaChannels(t, role, db)
		assertServerError(t, db, grSQLApplierChannelStatus, erReplicaChannelOp)
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

func countRows(t *testing.T, db replication.Querier, query string) int {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), query)
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
