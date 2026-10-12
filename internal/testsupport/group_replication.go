package testsupport

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"testing"
	"time"
)

// Environment contract for the Group Replication topology started by
// tests/docker/compose_group_replication.yaml. docs/testing.md publishes it.
const (
	envGRPrimaryDSN   = "DBSGOMYSQL_TEST_GR_PRIMARY_DSN"
	envGRSecondaryDSN = "DBSGOMYSQL_TEST_GR_SECONDARY_DSN"
	envGRRejoinerDSN  = "DBSGOMYSQL_TEST_GR_REJOINER_DSN"
	envGRInstalledDSN = "DBSGOMYSQL_TEST_GR_INSTALLED_DSN"
	envGRDisabledDSN  = "DBSGOMYSQL_TEST_GR_DISABLED_DSN"
	envGRExtDSN       = "DBSGOMYSQL_TEST_GR_EXT_DSN"
	envGRExtHost      = "DBSGOMYSQL_TEST_GR_EXT_HOST"
	envRequireGR      = "DBSGOMYSQL_TEST_REQUIRE_GROUP_REPLICATION"
)

const (
	// GroupReplicationWaitDeadline bounds every Group Replication poll here.
	GroupReplicationWaitDeadline = 120 * time.Second

	// MemberOnline and its siblings are MEMBER_STATE spellings this file
	// waits for, repeated rather than imported from pkg/replication for the
	// same reason replication.go repeats its column names.
	MemberOnline     = "ONLINE"
	MemberOffline    = "OFFLINE"
	MemberRecovering = "RECOVERING"

	sqlSelfMemberState = "SELECT MEMBER_STATE FROM performance_schema.replication_group_members " +
		"WHERE MEMBER_ID = @@server_uuid"
	sqlStartGroupReplication = "START GROUP_REPLICATION"
	sqlServerUUID            = "SELECT @@server_uuid"

	// grStatementTimeout bounds each statement these helpers issue. They run
	// from t.Cleanup too, where t.Context() is already canceled, so every
	// statement derives from context.WithoutCancel(t.Context()) instead.
	grStatementTimeout = 30 * time.Second
)

// cleanupSafeContext is t.Context() without its cancellation, bounded by
// timeout: usable from a t.Cleanup function, where t.Context() is canceled.
func cleanupSafeContext(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()

	return context.WithTimeout(context.WithoutCancel(t.Context()), timeout)
}

// GRTopology holds the servers of one Group Replication topology. Primary,
// Secondary, and Rejoiner are the three cluster members gr1, gr2, gr3;
// Installed and Disabled are standalone servers with the plugin ACTIVE and
// DISABLED; Ext is a standalone GTID source, and ExtHost is its hostname as
// the members reach it. The DSN fields are what account connections derive
// from.
//
// The zero value is unusable; build one with GroupReplicationTopology.
type GRTopology struct {
	Primary, Secondary, Rejoiner, Installed, Disabled, Ext *sql.DB

	PrimaryDSN, InstalledDSN, DisabledDSN string
	ExtHost                               string
}

// GroupReplicationTopology opens every server described by the
// DBSGOMYSQL_TEST_GR_* variables, pings them, registers their Close, and
// brings the cluster to all-ONLINE before returning (EnsureGroupOnline).
//
// A missing variable skips the test — unless
// DBSGOMYSQL_TEST_REQUIRE_GROUP_REPLICATION is "1", where it fails instead.
func GroupReplicationTopology(t *testing.T) *GRTopology {
	t.Helper()

	topology := &GRTopology{
		PrimaryDSN:   requiredGREnv(t, envGRPrimaryDSN),
		InstalledDSN: requiredGREnv(t, envGRInstalledDSN),
		DisabledDSN:  requiredGREnv(t, envGRDisabledDSN),
		ExtHost:      requiredGREnv(t, envGRExtHost),
	}
	secondaryDSN := requiredGREnv(t, envGRSecondaryDSN)
	rejoinerDSN := requiredGREnv(t, envGRRejoinerDSN)
	extDSN := requiredGREnv(t, envGRExtDSN)

	if !regexp.MustCompile(replicationHostExpr).MatchString(topology.ExtHost) {
		t.Fatalf("%s=%q is not a plain hostname", envGRExtHost, topology.ExtHost)
	}

	topology.Primary = openReplicationDB(t, "group primary", topology.PrimaryDSN)
	topology.Secondary = openReplicationDB(t, "group secondary", secondaryDSN)
	topology.Rejoiner = openReplicationDB(t, "group rejoiner", rejoinerDSN)
	topology.Installed = openReplicationDB(t, "plugin-installed server", topology.InstalledDSN)
	topology.Disabled = openReplicationDB(t, "plugin-disabled server", topology.DisabledDSN)
	topology.Ext = openReplicationDB(t, "external source", extDSN)

	EnsureGroupOnline(t, topology)

	return topology
}

func requiredGREnv(t *testing.T, name string) string {
	t.Helper()

	value := os.Getenv(name)
	if value != "" {
		return value
	}

	if os.Getenv(envRequireGR) == "1" {
		t.Fatalf("%s is unset while %s=1: a skipped group replication test is not evidence", name, envRequireGR)
	}
	t.Skipf("%s is unset", name)

	return ""
}

// EnsureGroupOnline is convergent: a secondary or rejoiner left OFFLINE by an
// earlier test is started again, and every path ends by proving all three
// members ONLINE. The primary is never restarted — re-forming a group is the
// bootstrap's job — so a primary that is not ONLINE fails the test with the
// teardown instruction.
func EnsureGroupOnline(t *testing.T, topology *GRTopology) {
	t.Helper()

	if state := MemberState(t, topology.Primary); state != MemberOnline {
		t.Fatalf("group primary is %q, not ONLINE: run `docker compose ... down -v` and start the topology again", state)
	}

	for _, member := range []struct {
		role string
		db   *sql.DB
	}{{"group secondary", topology.Secondary}, {"group rejoiner", topology.Rejoiner}} {
		if MemberState(t, member.db) == MemberOffline {
			ctx, cancel := cleanupSafeContext(t, GroupReplicationWaitDeadline)
			_, err := member.db.ExecContext(ctx, sqlStartGroupReplication)
			cancel()
			if err != nil {
				t.Fatalf("restart Group Replication on the %s: %v", member.role, err)
			}
		}
		WaitMemberState(t, member.db, MemberOnline)
	}
}

// MemberState reads this server's own MEMBER_STATE, or "" when it has no row
// of its own.
func MemberState(t *testing.T, db *sql.DB) string {
	t.Helper()

	ctx, cancel := cleanupSafeContext(t, grStatementTimeout)
	defer cancel()

	var state string
	err := db.QueryRowContext(ctx, sqlSelfMemberState).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read own member state: %v", err)
	}

	return state
}

// WaitMemberState polls until this server's own MEMBER_STATE is want, failing
// with the last state seen once GroupReplicationWaitDeadline passes.
func WaitMemberState(t *testing.T, db *sql.DB, want string) {
	t.Helper()

	ctx, cancel := cleanupSafeContext(t, GroupReplicationWaitDeadline)
	defer cancel()

	ticker := time.NewTicker(replicationPollInterval)
	defer ticker.Stop()

	last := ""
	for {
		last = MemberState(t, db)
		if last == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("member state is %q after %s, want %q", last, GroupReplicationWaitDeadline, want)
		case <-ticker.C:
		}
	}
}

// ServerUUID reads @@server_uuid.
func ServerUUID(t *testing.T, db *sql.DB) string {
	t.Helper()

	ctx, cancel := cleanupSafeContext(t, grStatementTimeout)
	defer cancel()

	var uuid string
	if err := db.QueryRowContext(ctx, sqlServerUUID).Scan(&uuid); err != nil {
		t.Fatalf("read server_uuid: %v", err)
	}

	return uuid
}

// PollUntil re-runs observe until it reports done, failing with what observe
// last described once GroupReplicationWaitDeadline passes. It is the one
// bounded wait the Group Replication tests build on; there are no fixed
// sleeps.
func PollUntil(t *testing.T, what string, observe func() (done bool, last string)) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), GroupReplicationWaitDeadline)
	defer cancel()

	ticker := time.NewTicker(replicationPollInterval)
	defer ticker.Stop()

	for {
		done, last := observe()
		if done {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: not reached after %s; last observation: %s", what, GroupReplicationWaitDeadline, last)
		case <-ticker.C:
		}
	}
}
