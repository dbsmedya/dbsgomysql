package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Environment contract for the source-replica topology started by
// tests/docker/compose_replication.yaml.
const (
	envSourceDSN          = "DBSGOMYSQL_TEST_SOURCE_DSN"
	envReplicaDSN         = "DBSGOMYSQL_TEST_REPLICA_DSN"
	envSilentReplicaDSN   = "DBSGOMYSQL_TEST_SILENT_REPLICA_DSN"
	envReplSourceHost     = "DBSGOMYSQL_TEST_REPL_SOURCE_HOST"
	envRequireReplication = "DBSGOMYSQL_TEST_REQUIRE_REPLICATION"
)

const (
	// ReplicationWaitDeadline bounds every poll in this file. Callers may pass
	// their own bound to WaitChannelsRunning; a non-positive one means this.
	ReplicationWaitDeadline = 90 * time.Second

	// replicationPollInterval is how often the topology is re-observed. There
	// are no fixed sleeps anywhere here: every wait polls a real observation
	// and every wait is bounded.
	replicationPollInterval = 250 * time.Millisecond

	replicationPingTimeout = 20 * time.Second
)

// SHOW REPLICA STATUS columns this file observes. The library decodes its own
// copy of these names; the helper repeats them rather than importing
// pkg/replication, which would make the test scaffolding depend on the code
// under test for the definition of "running".
const (
	colChannelName         = "Channel_Name"
	colReplicaIORunning    = "Replica_IO_Running"
	colReplicaSQLRunning   = "Replica_SQL_Running"
	colSecondsBehindSource = "Seconds_Behind_Source"
	colLastIOError         = "Last_IO_Error"
	colLastSQLError        = "Last_SQL_Error"

	threadRunning = "Yes"
	threadStopped = "No"
)

const (
	sqlShowReplicaStatus = "SHOW REPLICA STATUS"
	sqlStartReplica      = "START REPLICA"
	sqlStartApplier      = "START REPLICA SQL_THREAD"
	sqlStopApplier       = "STOP REPLICA SQL_THREAD"
	sqlSourceExecuted    = "SELECT @@GLOBAL.gtid_executed"
	sqlWaitExecuted      = "SELECT WAIT_FOR_EXECUTED_GTID_SET(?, 60)"

	// replicationHostExpr is the shape a source hostname must have before it
	// is spliced into CHANGE REPLICATION SOURCE TO, which takes no parameter
	// markers. Compose service names are the only values it ever receives.
	replicationHostExpr = `^[A-Za-z0-9._-]+$`
)

// ReplTopology holds the three servers of one replication trio: a source, a
// replica that reports itself to the source, and a replica that does not.
// SourceHost is the source's hostname as the replicas reach it (compose DNS),
// which is not the host in any of the DSNs — those are host-mapped ports.
//
// The zero value is unusable; build one with ReplicationTopology.
type ReplTopology struct {
	Source     *sql.DB
	Replica    *sql.DB
	Silent     *sql.DB
	SourceHost string
}

// ReplicationTopology opens the trio described by the five
// DBSGOMYSQL_TEST_* replication variables, pings all three servers, and
// registers their Close.
//
// A missing variable skips the test — unless
// DBSGOMYSQL_TEST_REQUIRE_REPLICATION is "1", where it fails instead. CI sets
// that: a skipped replication test is not evidence, and a silent skip is
// indistinguishable from a pass in a summary.
func ReplicationTopology(t *testing.T) *ReplTopology {
	t.Helper()

	sourceDSN := requiredReplicationEnv(t, envSourceDSN)
	replicaDSN := requiredReplicationEnv(t, envReplicaDSN)
	silentDSN := requiredReplicationEnv(t, envSilentReplicaDSN)
	sourceHost := requiredReplicationEnv(t, envReplSourceHost)

	return &ReplTopology{
		Source:     openReplicationDB(t, "source", sourceDSN),
		Replica:    openReplicationDB(t, "replica", replicaDSN),
		Silent:     openReplicationDB(t, "silent replica", silentDSN),
		SourceHost: sourceHost,
	}
}

func requiredReplicationEnv(t *testing.T, name string) string {
	t.Helper()

	value := os.Getenv(name)
	if value != "" {
		return value
	}

	if os.Getenv(envRequireReplication) == "1" {
		t.Fatalf("%s is unset while %s=1: a skipped replication test is not evidence", name, envRequireReplication)
	}
	t.Skipf("%s is unset", name)

	return ""
}

func openReplicationDB(t *testing.T, role, dsn string) *sql.DB {
	t.Helper()

	db, openErr := sql.Open("mysql", dsn)
	if openErr != nil {
		t.Fatalf("open the %s: %v", role, openErr)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close the %s: %v", role, closeErr)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), replicationPingTimeout)
	defer cancel()
	if pingErr := db.PingContext(ctx); pingErr != nil {
		t.Fatalf("ping the %s: %v", role, pingErr)
	}

	return db
}

// BootstrapReplication brings both replicas to running replication against the
// source, and proves it before returning.
//
// It is convergent on every call and deliberately not guarded by a sync.Once:
// a t.Fatal inside a once-guard would consume the only attempt for the whole
// run, and a replica left configured-but-stopped by an earlier test has to be
// repaired rather than waited on. Every path ends in WaitChannelsRunning, so
// the postcondition is either proven running state or a failed test.
func BootstrapReplication(t *testing.T, topology *ReplTopology) {
	t.Helper()

	bootstrapReplica(t, topology.Replica, topology.SourceHost, "replica")
	bootstrapReplica(t, topology.Silent, topology.SourceHost, "silent replica")
}

func bootstrapReplica(t *testing.T, db *sql.DB, sourceHost, role string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), ReplicationWaitDeadline)
	defer cancel()

	rows, statusErr := replicaStatusRows(ctx, db)
	if statusErr != nil {
		t.Fatalf("read the %s's replica status: %v", role, statusErr)
	}

	switch {
	case len(rows) == 0:
		configureReplica(t, db, sourceHost, role)
	case channelsRunning(rows):
		// Already converged; issuing anything here would only add a state
		// transition the topology does not need.
	default:
		restartReplica(t, db, role)
	}

	WaitChannelsRunning(t, db, ReplicationWaitDeadline)
}

// configureReplica points a never-configured replica at the source. The
// statement takes no parameter markers, so the hostname is validated against
// replicationHostExpr before it is spliced in.
func configureReplica(t *testing.T, db *sql.DB, sourceHost, role string) {
	t.Helper()

	if !regexp.MustCompile(replicationHostExpr).MatchString(sourceHost) {
		t.Fatalf("source host %q does not match %s; refusing to splice it into a statement", sourceHost, replicationHostExpr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), ReplicationWaitDeadline)
	defer cancel()

	change := fmt.Sprintf(
		"CHANGE REPLICATION SOURCE TO SOURCE_HOST='%s', SOURCE_PORT=3306, "+
			"SOURCE_USER='root', SOURCE_PASSWORD='root', "+
			"SOURCE_AUTO_POSITION=1, GET_SOURCE_PUBLIC_KEY=1",
		sourceHost,
	)
	if _, changeErr := db.ExecContext(ctx, change); changeErr != nil {
		t.Fatalf("point the %s at %q: %v", role, sourceHost, changeErr)
	}
	if _, startErr := db.ExecContext(ctx, sqlStartReplica); startErr != nil {
		t.Fatalf("start the %s: %v", role, startErr)
	}
}

// restartReplica repairs a configured replica whose threads are not both
// running. A statement error is tolerated here and only here: what the server
// does with START REPLICA on a partially running replica is not this helper's
// claim to make. The caller's WaitChannelsRunning delivers the verdict —
// converge, then verify.
func restartReplica(t *testing.T, db *sql.DB, role string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), ReplicationWaitDeadline)
	defer cancel()

	if _, startErr := db.ExecContext(ctx, sqlStartReplica); startErr != nil {
		t.Logf("%s on the %s reported %v; waiting for convergence anyway", sqlStartReplica, role, startErr)
	}
}

// WaitChannelsRunning polls SHOW REPLICA STATUS until every channel reports
// both threads running, failing the test on timeout with the last row it saw —
// which is the only evidence of why the topology did not converge. A
// non-positive deadline means ReplicationWaitDeadline.
//
// The poll is rooted at context.Background rather than t.Context because
// StopReplicaSQLThread's cleanup calls this helper, and by then t.Context is
// already canceled.
func WaitChannelsRunning(t *testing.T, db *sql.DB, deadline time.Duration) {
	t.Helper()

	pollReplicaStatus(t, db, deadline, channelsRunning, "every channel running")
}

// WaitReplicaCaughtUp blocks until the replica has executed everything the
// source had executed when this call started. The wait is server-side and
// bounded; a non-zero return means the replica did not catch up in time.
func WaitReplicaCaughtUp(t *testing.T, topology *ReplTopology) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), ReplicationWaitDeadline)
	defer cancel()

	var executed string
	if readErr := topology.Source.QueryRowContext(ctx, sqlSourceExecuted).Scan(&executed); readErr != nil {
		t.Fatalf("read the source's gtid_executed: %v", readErr)
	}

	var result sql.NullInt64
	if waitErr := topology.Replica.QueryRowContext(ctx, sqlWaitExecuted, executed).Scan(&result); waitErr != nil {
		t.Fatalf("wait for the replica to execute %q: %v", executed, waitErr)
	}
	if !result.Valid {
		t.Fatalf("WAIT_FOR_EXECUTED_GTID_SET(%q, 60) returned NULL, want 0", executed)
	}
	if result.Int64 != 0 {
		t.Fatalf(
			"WAIT_FOR_EXECUTED_GTID_SET(%q, 60) returned %d, want 0: the replica did not catch up",
			executed,
			result.Int64,
		)
	}
}

// StopReplicaSQLThread stops the applier on db and registers its restart.
//
// The cleanup is registered before the thread is stopped, so nothing between
// the two can leave the shared replica stopped for every later test. The
// postcondition is one single snapshot showing Replica_SQL_Running "No" and a
// NULL Seconds_Behind_Source together: STOP REPLICA is nonblocking and
// SHOW REPLICA STATUS may return a stale snapshot while it runs, so reading
// the stopped thread from one query and the NULL estimate from a later one
// would be a race rather than a proof.
func StopReplicaSQLThread(t *testing.T, db *sql.DB) {
	t.Helper()

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), ReplicationWaitDeadline)
		defer cleanupCancel()

		if startErr := execReplication(cleanupCtx, db, sqlStartApplier); startErr != nil {
			t.Errorf("restart the replica applier: %v", startErr)
		}
		WaitChannelsRunning(t, db, ReplicationWaitDeadline)
	})

	ctx, cancel := context.WithTimeout(context.Background(), ReplicationWaitDeadline)
	defer cancel()

	if stopErr := execReplication(ctx, db, sqlStopApplier); stopErr != nil {
		t.Fatalf("stop the replica applier: %v", stopErr)
	}

	pollReplicaStatus(
		t,
		db,
		ReplicationWaitDeadline,
		applierStopped,
		"the applier stopped with a NULL lag estimate in one snapshot",
	)
}

func execReplication(ctx context.Context, db *sql.DB, statement string) error {
	if _, execErr := db.ExecContext(ctx, statement); execErr != nil {
		return fmt.Errorf("execute %s: %w", statement, execErr)
	}

	return nil
}

// pollReplicaStatus re-observes SHOW REPLICA STATUS every
// replicationPollInterval until satisfied accepts the snapshot, or fails the
// test at the deadline reporting want and the last observation.
func pollReplicaStatus(
	t *testing.T,
	db *sql.DB,
	deadline time.Duration,
	satisfied func([]map[string]sql.NullString) bool,
	want string,
) {
	t.Helper()

	if deadline <= 0 {
		deadline = ReplicationWaitDeadline
	}

	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	ticker := time.NewTicker(replicationPollInterval)
	defer ticker.Stop()

	for {
		accepted, observed := observeReplicaStatus(ctx, db, satisfied)
		if accepted {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("waited %s for %s; last observed: %s", deadline, want, observed)
		case <-ticker.C:
		}
	}
}

// observeReplicaStatus takes one snapshot and reports whether satisfied
// accepts it. When it does not — including when the snapshot could not be read
// at all — observed describes what was actually seen, which is what a timeout
// has to print to be worth anything.
func observeReplicaStatus(
	ctx context.Context,
	db *sql.DB,
	satisfied func([]map[string]sql.NullString) bool,
) (accepted bool, observed string) {
	rows, statusErr := replicaStatusRows(ctx, db)
	switch {
	case statusErr != nil:
		return false, statusErr.Error()
	case len(rows) == 0:
		return false, sqlShowReplicaStatus + " returned no rows"
	case satisfied(rows):
		return true, ""
	default:
		return false, formatReplicaStatus(rows)
	}
}

func channelsRunning(rows []map[string]sql.NullString) bool {
	if len(rows) == 0 {
		return false
	}

	for i := range rows {
		if rows[i][colReplicaIORunning].String != threadRunning {
			return false
		}
		if rows[i][colReplicaSQLRunning].String != threadRunning {
			return false
		}
	}

	return true
}

func applierStopped(rows []map[string]sql.NullString) bool {
	if len(rows) == 0 {
		return false
	}

	for i := range rows {
		if rows[i][colReplicaSQLRunning].String != threadStopped {
			return false
		}
		seconds, present := rows[i][colSecondsBehindSource]
		if !present || seconds.Valid {
			return false
		}
	}

	return true
}

// replicaStatusRows reads SHOW REPLICA STATUS as one column-name map per
// channel row. Every value is read as a nullable string: the helper decides
// nothing about types, which keeps it independent of the decoding the library
// under test performs.
func replicaStatusRows(ctx context.Context, db *sql.DB) ([]map[string]sql.NullString, error) {
	rows, queryErr := db.QueryContext(ctx, sqlShowReplicaStatus)
	if queryErr != nil {
		return nil, fmt.Errorf("query %s: %w", sqlShowReplicaStatus, queryErr)
	}
	defer rows.Close()

	columns, columnsErr := rows.Columns()
	if columnsErr != nil {
		return nil, fmt.Errorf("read %s columns: %w", sqlShowReplicaStatus, columnsErr)
	}

	values := make([]sql.NullString, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}

	var status []map[string]sql.NullString
	for rows.Next() {
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return nil, fmt.Errorf("scan %s: %w", sqlShowReplicaStatus, scanErr)
		}

		row := make(map[string]sql.NullString, len(columns))
		for i, name := range columns {
			row[name] = values[i]
		}
		status = append(status, row)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterate %s: %w", sqlShowReplicaStatus, iterErr)
	}

	return status, nil
}

// formatReplicaStatus renders the columns that explain a stalled topology.
// Printing all sixty would bury them.
func formatReplicaStatus(rows []map[string]sql.NullString) string {
	reported := []string{
		colChannelName,
		colReplicaIORunning,
		colReplicaSQLRunning,
		colSecondsBehindSource,
		colLastIOError,
		colLastSQLError,
	}

	summaries := make([]string, 0, len(rows))
	for i := range rows {
		fields := make([]string, 0, len(reported))
		for _, name := range reported {
			fields = append(fields, name+"="+nullText(rows[i][name]))
		}
		summaries = append(summaries, strings.Join(fields, " "))
	}

	return strings.Join(summaries, " | ")
}

func nullText(value sql.NullString) string {
	if !value.Valid {
		return "NULL"
	}

	return fmt.Sprintf("%q", value.String)
}
