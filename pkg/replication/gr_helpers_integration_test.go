//go:build integration

package replication_test

import (
	"database/sql"
	"maps"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// These tests drive the fence harness's and the mode helper's restores
// (internal/testsupport/gr_fence.go and gr_mode.go) from the states a failed
// test would leave behind. They live here, not in internal/testsupport,
// because this is the one package that uses the Group Replication topology:
// go test runs packages in parallel unless told otherwise, and two packages
// switching and fencing the same group at once would race. Each test calls its
// restore twice: the second call must be a no-op, as it is when the registered
// cleanup runs after an explicit restore.

const (
	ghSQLSelfRole      = "SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID = @@server_uuid"
	ghSQLSuperReadOnly = "SELECT @@GLOBAL.super_read_only"
)

// TestGroupModeChangeRestoresFromStoppedMemberIntegration leaves the group
// multi-primary with gr3 stopped before the switch, which is where a failing
// mode test stops, and requires Restore to bring back single-primary with gr1
// primary, every member ONLINE, and every member's runtime values and
// persisted rows as captured.
func TestGroupModeChangeRestoresFromStoppedMemberIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	change := testsupport.StartGroupModeChange(t, topology)

	execOn(t, topology.Rejoiner, "STOP GROUP_REPLICATION")
	testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberOffline)
	change.SwitchToMultiPrimary(t)

	change.Restore(t)
	change.Restore(t)

	assertModeChangeRestored(t, topology, change)
}

// TestGroupModeChangeRestoresStoppedMultiPrimaryMemberIntegration leaves the
// group multi-primary with gr3 stopped after the switch, so gr3 keeps the
// multi-primary mode and checks at runtime and in its persisted rows. Restore
// must then set gr3's checks and mode with SET GLOBAL before gr3 can rejoin,
// and restore its persisted rows on the running member: SET PERSIST_ONLY where
// the baseline has a row, RESET PERSIST IF EXISTS where it has none. The test
// logs which statements that is on this server.
func TestGroupModeChangeRestoresStoppedMultiPrimaryMemberIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	change := testsupport.StartGroupModeChange(t, topology)

	change.SwitchToMultiPrimary(t)
	execOn(t, topology.Rejoiner, "STOP GROUP_REPLICATION")
	testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberOffline)

	// The preconditions of the two repair paths, from the server.
	baseline := change.Baseline(t, topology.Rejoiner)
	stopped := testsupport.ReadMemberModeState(t, topology.Rejoiner)
	for _, name := range []string{testsupport.VarSinglePrimaryMode, testsupport.VarEnforceUpdateChecks} {
		if stopped.Runtime[name] == baseline.Runtime[name] {
			t.Fatalf("the stopped gr3's %s is %q, as captured: the SET GLOBAL repair would not run",
				name, stopped.Runtime[name])
		}
	}
	statements := testsupport.PersistedRestoreStatements(baseline.Persisted, stopped.Persisted)
	if len(statements) != 2 {
		t.Fatalf("gr3's persisted restore is %q, want two statements: the switch persists the mode and the checks",
			statements)
	}
	t.Logf("gr3's persisted restore, issued on the running member: %q", statements)

	change.Restore(t)
	change.Restore(t)

	assertModeChangeRestored(t, topology, change)
}

// TestGroupFenceReleaseRestoresIntegration builds the fence and releases it at
// once, the unwind a failing fence test gets from its cleanup, and requires
// gr1 primary and writable, every member ONLINE, and the harness's schema gone
// from every member.
func TestGroupFenceReleaseRestoresIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	fence := testsupport.BuildGroupFence(t, topology)

	if role := scalarString(t, fence.Primary, ghSQLSelfRole); role != testsupport.MemberPrimary {
		t.Fatalf("the fenced member's role is %q, want %s", role, testsupport.MemberPrimary)
	}
	if readOnly := scalarString(t, fence.Primary, ghSQLSuperReadOnly); readOnly != "1" {
		t.Fatalf("the fenced primary's super_read_only is %s, want 1", readOnly)
	}

	fence.Release(t)
	fence.Release(t)

	if role := scalarString(t, topology.Primary, ghSQLSelfRole); role != testsupport.MemberPrimary {
		t.Errorf("gr1's role after the release is %q, want %s", role, testsupport.MemberPrimary)
	}
	if readOnly := scalarString(t, topology.Primary, ghSQLSuperReadOnly); readOnly != "0" {
		t.Errorf("gr1's super_read_only after the release is %s, want 0", readOnly)
	}
	schemaCount := "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '" + fence.Schema + "'"
	for role, db := range map[string]*sql.DB{
		"gr1": topology.Primary, "gr2": topology.Secondary, "gr3": topology.Rejoiner,
	} {
		if state := testsupport.MemberState(t, db); state != testsupport.MemberOnline {
			t.Errorf("%s is %s after the release, want %s", role, state, testsupport.MemberOnline)
		}
		if schemas := scalarString(t, db, schemaCount); schemas != "0" {
			t.Errorf("the harness schema %s is still on %s after the release", fence.Schema, role)
		}
	}
}

// assertModeChangeRestored requires gr1 primary, every member ONLINE, and each
// member's runtime values and persisted rows equal to what the helper captured.
func assertModeChangeRestored(t *testing.T, topology *testsupport.GRTopology, change *testsupport.GroupModeChange) {
	t.Helper()

	if role := scalarString(t, topology.Primary, ghSQLSelfRole); role != testsupport.MemberPrimary {
		t.Errorf("gr1's role after the restore is %q, want %s", role, testsupport.MemberPrimary)
	}
	for role, db := range map[string]*sql.DB{
		"gr1": topology.Primary, "gr2": topology.Secondary, "gr3": topology.Rejoiner,
	} {
		if state := testsupport.MemberState(t, db); state != testsupport.MemberOnline {
			t.Errorf("%s is %s after the restore, want %s", role, state, testsupport.MemberOnline)
		}
		got, want := testsupport.ReadMemberModeState(t, db), change.Baseline(t, db)
		if !maps.Equal(got.Runtime, want.Runtime) || !maps.Equal(got.Persisted, want.Persisted) {
			t.Errorf("%s after the restore = %+v, want the captured %+v", role, got, want)
		}
	}
}
