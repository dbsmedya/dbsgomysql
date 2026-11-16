//go:build integration

package replication_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// These tests pin docs/COMPAT.md entry 36 (A3): on a newly elected primary
// under a fence, every replication fact returns, while a table read and the
// v1.5.0 information_schema.PLUGINS read are held. The fence is the harness's
// (testsupport.BuildGroupFence), restored by its cleanup.

const (
	fenceValue           = "BEFORE_ON_PRIMARY_FAILOVER"
	fenceSessionTimeout  = 5                // wait_timeout of a fenced session, in seconds
	fenceFactDeadline    = 20 * time.Second // a fact that is not held answers in well under this
	fenceHeldDeadline    = 1 * time.Second  // contract (c)'s caller deadline
	fenceHeldMargin      = 2 * time.Second
	erGRHoldWaitTimeout  = 3781 // ER_GR_HOLD_WAIT_TIMEOUT
	fenceSQLSessionValue = "SELECT @@SESSION.group_replication_consistency"
)

// TestCompat36FencedPrimaryIntegration builds the fence once and runs every
// case against it: every fact with the fencing value set on the session and
// inherited from the global (T3 and A3), and contract (c)'s held read.
func TestCompat36FencedPrimaryIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	fence := testsupport.BuildGroupFence(t, topology)

	t.Run("session value set", func(t *testing.T) {
		assertFencedFacts(t, fence, true)
	})

	t.Run("session value inherited from the global", func(t *testing.T) {
		original := scalarString(t, fence.Primary, gcSQLGlobalConsistency)
		if original != fenceValue {
			// 8.0's default is EVENTUAL. SET GLOBAL is permitted under the
			// fence and is not persisted.
			t.Cleanup(func() {
				execOn(t, fence.Primary, "SET GLOBAL group_replication_consistency = '"+original+"'")
			})
			execOn(t, fence.Primary, "SET GLOBAL group_replication_consistency = '"+fenceValue+"'")
		}
		assertFencedFacts(t, fence, false)
	})

	t.Run("contract (c): a held read ends at the caller's deadline", func(t *testing.T) {
		conn := fencedSession(t, fence, true, 30)

		var elapsed time.Duration
		var readErr error
		if err := conn.Raw(func(driverConn any) error {
			queryer, ok := driverConn.(driver.QueryerContext)
			if !ok {
				return fmt.Errorf("driver connection %T has no QueryContext", driverConn)
			}
			ctx, cancel := context.WithTimeout(t.Context(), fenceHeldDeadline)
			defer cancel()
			started := time.Now()
			_, _, readErr = replication.DriverReadValueForTest(
				ctx, queryer, "SELECT COUNT(*) FROM "+fence.HeldTable+" LIMIT 18446744073709551615", "COUNT(*)",
			)
			elapsed = time.Since(started)

			return nil
		}); err != nil {
			t.Fatalf("conn.Raw: %v", err)
		}
		// The driver has invalidated the connection; closing it is the
		// caller's, as the function's contract says.
		closeConn(t, conn)

		if !errors.Is(readErr, context.DeadlineExceeded) {
			t.Fatalf("the held read returned %v after %s, want context.DeadlineExceeded", readErr, elapsed)
		}
		var opErr *replication.OpError
		if !errors.As(readErr, &opErr) || opErr.Op != "group_consistency" {
			t.Errorf("error %v, want *OpError with Op group_consistency", readErr)
		}
		// Held, then ended by the deadline: not refused at once, and not
		// waited out to the server's own timeout.
		if elapsed < fenceHeldDeadline*9/10 || elapsed > fenceHeldDeadline+fenceHeldMargin {
			t.Errorf("the held read returned after %s, want about %s", elapsed, fenceHeldDeadline)
		}
	})
}

// assertFencedFacts reads every fact in one fresh fenced session, each inside
// fenceFactDeadline, between two held controls in sessions of their own, so
// the facts cannot pass with the fence down. It also holds T3's second
// control, the v1.5.0 plugin read, which shows that D's plugin read must stay
// conditional.
func assertFencedFacts(t *testing.T, fence *testsupport.GroupFence, setSession bool) {
	t.Helper()

	assertHeld(t, fence, setSession, "SELECT COUNT(*) FROM "+fence.HeldTable)

	conn := fencedSession(t, fence, setSession, fenceSessionTimeout)
	if value := connScalar(t, conn, fenceSQLSessionValue); value != fenceValue {
		t.Fatalf("the fenced session's group_replication_consistency is %s, want %s", value, fenceValue)
	}
	inspector := replication.NewInspector(conn)

	fact := func(name string, read func(ctx context.Context) error) {
		t.Helper()

		ctx, cancel := context.WithTimeout(t.Context(), fenceFactDeadline)
		defer cancel()
		started := time.Now()
		if err := read(ctx); err != nil {
			t.Errorf("%s on the fenced primary after %s: %v", name, time.Since(started), err)
		}
	}

	fact("ReplicaStatus", func(ctx context.Context) error {
		_, err := inspector.ReplicaStatus(ctx)

		return err
	})
	fact("BinaryLogEnabled", func(ctx context.Context) error {
		enabled, err := inspector.BinaryLogEnabled(ctx)
		if err == nil && !enabled {
			err = errors.New("binary logging reported off on a group member")
		}

		return err
	})
	fact("BinaryLogStatus", func(ctx context.Context) error {
		status, err := inspector.BinaryLogStatus(ctx)
		if err == nil && status == nil {
			err = errors.New("no binary log status on a group member")
		}

		return err
	})
	fact("GTIDStatus", func(ctx context.Context) error {
		_, err := inspector.GTIDStatus(ctx)

		return err
	})
	fact("ReplicationConfig", func(ctx context.Context) error {
		config, err := inspector.ReplicationConfig(ctx)
		if err == nil && !config.SuperReadOnly {
			err = errors.New("super_read_only reported off on the fenced primary")
		}

		return err
	})
	fact("RegisteredReplicas", func(ctx context.Context) error {
		_, err := inspector.RegisteredReplicas(ctx)

		return err
	})
	fact("GroupMembers", func(ctx context.Context) error {
		members, err := inspector.GroupMembers(ctx)
		if err == nil && len(members) != 3 {
			err = fmt.Errorf("%d members, want 3", len(members))
		}

		return err
	})
	fact("GroupMemberStats", func(ctx context.Context) error {
		_, err := inspector.GroupMemberStats(ctx)

		return err
	})
	fact("GroupReplication", func(ctx context.Context) error {
		group, err := inspector.GroupReplication(ctx)
		if err == nil && (group.PluginStatus != grPluginOn || group.GroupName == "" ||
			group.SinglePrimaryMode != grModeOn || !group.SinglePrimaryModeKnown || !group.TransactionSizeLimitKnown) {
			err = fmt.Errorf("= %#v, want ACTIVE with a group name, a known limit and the mode ON", group)
		}

		return err
	})
	wantConsistency := func(consistency replication.GroupConsistency) error {
		if consistency.PluginStatus != grPluginOn || consistency.SessionConsistency != fenceValue ||
			!consistency.SessionConsistencyKnown || !consistency.GlobalConsistencyKnown {
			return fmt.Errorf("= %#v, want ACTIVE with the session's %s", consistency, fenceValue)
		}

		return nil
	}
	fact("GroupConsistency", func(ctx context.Context) error {
		consistency, err := inspector.GroupConsistency(ctx)
		if err != nil {
			return err
		}

		return wantConsistency(consistency)
	})
	fact("GroupConsistencyOnDriverConn", func(ctx context.Context) error {
		consistency, err := viaDriver(t, ctx, conn)
		if err != nil {
			return err
		}

		return wantConsistency(consistency)
	})
	closeConn(t, conn)

	assertHeld(t, fence, setSession, "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS "+
		"WHERE PLUGIN_NAME = 'group_replication' LIMIT 18446744073709551615")
	assertHeld(t, fence, setSession, "SELECT COUNT(*) FROM "+fence.HeldTable)
}

// assertHeld requires query, in a fresh fenced session of its own, to end with
// ER_GR_HOLD_WAIT_TIMEOUT once the session's wait_timeout passes.
func assertHeld(t *testing.T, fence *testsupport.GroupFence, setSession bool, query string) {
	t.Helper()

	conn := fencedSession(t, fence, setSession, fenceSessionTimeout)
	defer closeConn(t, conn)

	started := time.Now()
	err := heldQuery(t, conn, query)
	if number := serverErrorNumber(err); number != erGRHoldWaitTimeout {
		t.Errorf("%q on the fenced primary after %s: %v, want server error %d: the fence is not in force",
			query, time.Since(started), err, erGRHoldWaitTimeout)
	}
}

// heldQuery runs query on conn inside fenceFactDeadline and reads it to the
// end. A query that answers is an error here: under the fence it must not.
func heldQuery(t *testing.T, conn *sql.Conn, query string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), fenceFactDeadline)
	defer cancel()

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	read := 0
	for rows.Next() {
		read++
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return fmt.Errorf("answered with %d rows", read)
}

// fencedSession opens a fresh root session on the fenced primary with
// wait_timeout seconds and, when setSession is true, the fencing value set
// on the session; otherwise it inherits the server's global value. Each call
// is a new server session: the pool keeps no idle connection.
func fencedSession(t *testing.T, fence *testsupport.GroupFence, setSession bool, waitTimeout int) *sql.Conn {
	t.Helper()

	db, err := sql.Open("mysql", fence.PrimaryDSN)
	if err != nil {
		t.Fatalf("open the fenced primary: %v", err)
	}
	db.SetMaxIdleConns(0)
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := statementContext(t)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("connect to the fenced primary: %v", err)
	}
	statements := []string{fmt.Sprintf("SET SESSION wait_timeout = %d", waitTimeout)}
	if setSession {
		statements = append(statements, "SET SESSION group_replication_consistency = '"+fenceValue+"'")
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			_ = conn.Close()
			t.Fatalf("%s on the fenced primary: %v", statement, err)
		}
	}

	return conn
}

func closeConn(t *testing.T, conn *sql.Conn) {
	t.Helper()

	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		t.Errorf("close the fenced session: %v", err)
	}
}
