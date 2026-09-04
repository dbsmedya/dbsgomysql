//go:build integration

package replication_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	// Registers the "mysql" driver the topology is opened with.
	_ "github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
)

// These tests pin docs/COMPAT.md entries 6 and 20-23 against a live
// source-replica trio, once per supported MySQL version. None of them calls
// t.Parallel: the trio is shared and two of them mutate its state.

const (
	envMySQLVersion   = "DBSGOMYSQL_TEST_MYSQL_VERSION"
	envReplSourceHost = "DBSGOMYSQL_TEST_REPL_SOURCE_HOST"

	// The trio's service names differ per topology — repl80-source on the
	// Oracle stack, percona-repl80-source on the Percona mirror — but the
	// suffixes that distinguish the three servers do not. That is what makes
	// the source-to-replica swap below sound rather than a guess.
	sourceHostSuffix  = "-source"
	replicaHostSuffix = "-replica"

	mysqlVersion80 = "8.0"
	mysqlVersion97 = "9.7"
)

// The two source-status statements, spelled independently of the library's own
// constants. A pin that reused them could not detect the library misspelling
// one.
const (
	sqlShowBinaryLogStatus = "SHOW BINARY LOG STATUS"
	sqlShowMasterStatus    = "SHOW MASTER STATUS"

	sqlGTIDNextAutomatic = "SET SESSION gtid_next = 'AUTOMATIC'"
)

const (
	compatWaitTimeout = 30 * time.Second

	// gtidTagExpr is the shape of a generated tag, checked before it is
	// spliced into SET gtid_next, which takes no parameter markers.
	gtidTagExpr = `^[a-z][a-z0-9]{7}$`
)

// compatMySQLVersion reports the version under test. The library never
// branches on a version; these tests must, because the behavior they pin is
// what differs between versions.
func compatMySQLVersion(t *testing.T) string {
	t.Helper()

	version := os.Getenv(envMySQLVersion)
	if version == "" {
		t.Fatalf("%s is unset: the compat pins choose their assertions by version", envMySQLVersion)
	}

	return version
}

// replicaReportedHost is the hostname the reporting replica of the trio under
// test registers with.
//
// It derives that name from the trio's own environment contract rather than
// from the MySQL version, which keeps the pin portable across topologies: the
// Oracle trio's replica reports repl80-replica and the Percona mirror's
// reports percona-repl80-replica, and neither service name is written into
// this file. The <prefix>-source / <prefix>-replica pair is structural in both
// compose files, so swapping the suffix names the replica of whichever trio
// the environment points at.
func replicaReportedHost(t *testing.T) string {
	t.Helper()

	sourceHost := os.Getenv(envReplSourceHost)
	if sourceHost == "" {
		t.Fatalf(
			"%s is unset: the compat pins derive the reporting replica's hostname from the trio's source",
			envReplSourceHost,
		)
	}
	if !strings.HasSuffix(sourceHost, sourceHostSuffix) {
		t.Fatalf(
			"%s = %q, which does not name the trio's source as %s: the compat pins derive the reporting replica's hostname by swapping that suffix for %s",
			envReplSourceHost, sourceHost, "<prefix>"+sourceHostSuffix, replicaHostSuffix,
		)
	}

	return strings.TrimSuffix(sourceHost, sourceHostSuffix) + replicaHostSuffix
}

// probeStatement runs statement for its acceptance only and returns the error
// the server gave, or nil when the server accepted it.
func probeStatement(t *testing.T, db *sql.DB, statement string) error {
	t.Helper()

	rows, queryErr := db.QueryContext(t.Context(), statement)
	if queryErr != nil {
		return queryErr
	}
	defer rows.Close()

	return rows.Err()
}

// TestCompat20BinaryLogStatusIntegration pins entry 20: the source-status
// statement differs across the supported range, and the fact bridges it.
//
// Success alone is the proof, because on each version the *other* statement
// cannot succeed — 8.0 has no SHOW BINARY LOG STATUS, and 8.4 and later
// removed SHOW MASTER STATUS. The test asserts that rejection too, so the
// inference stays valid if a future server gains both spellings.
func TestCompat20BinaryLogStatusIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	version := compatMySQLVersion(t)

	status, err := replication.NewInspector(topology.Source).BinaryLogStatus(t.Context())
	if err != nil {
		t.Fatalf("BinaryLogStatus on the MySQL %s source: %v", version, err)
	}
	if status == nil {
		t.Fatal("BinaryLogStatus returned nil on a source with binary logging enabled")
	}
	if status.File == "" {
		t.Error("BinaryLogStatus reported an empty File on a source with binary logging enabled")
	}

	// Which statement the fact must have used is settled by which one this
	// server rejects.
	unavailable := sqlShowMasterStatus
	took := "the primary statement"
	if version == mysqlVersion80 {
		unavailable = sqlShowBinaryLogStatus
		took = "the fallback statement"
	}

	if probeErr := probeStatement(t, topology.Source, unavailable); probeErr == nil {
		t.Errorf(
			"%q was accepted by MySQL %s, so the fact's success no longer proves it took %s",
			unavailable,
			version,
			took,
		)
	}
}

// TestCompat6SecondsBehindIntegration pins entry 6: Seconds_Behind_Source is
// NULL exactly when the server says so, and the fact reports that as an
// invalid sql.NullInt64 rather than a fabricated zero.
func TestCompat6SecondsBehindIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	version := compatMySQLVersion(t)
	testsupport.BootstrapReplication(t, topology)

	inspector := replication.NewInspector(topology.Replica)

	running, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the running MySQL %s replica: %v", version, err)
	}
	if len(running) != 1 {
		t.Fatalf("the replica reports %d channels, want 1: %+v", len(running), running)
	}
	if !running[0].SecondsBehindSource.Valid {
		t.Error("Seconds_Behind_Source is NULL while both threads run, want a reported estimate")
	}
	if running[0].SecondsBehindSource.Int64 < 0 {
		t.Errorf(
			"Seconds_Behind_Source = %d on a running replica, want a non-negative estimate",
			running[0].SecondsBehindSource.Int64,
		)
	}

	// The helper returns only once one snapshot showed the applier stopped and
	// the estimate NULL together, so the snapshot below cannot be a stale read
	// taken while STOP REPLICA was still in flight.
	testsupport.StopReplicaSQLThread(t, topology.Replica)

	stopped, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the stopped MySQL %s replica: %v", version, err)
	}
	if len(stopped) != 1 {
		t.Fatalf("the stopped replica reports %d channels, want 1: %+v", len(stopped), stopped)
	}
	if stopped[0].SecondsBehindSource.Valid {
		t.Errorf(
			"Seconds_Behind_Source = %d with the applier stopped, want an invalid NullInt64",
			stopped[0].SecondsBehindSource.Int64,
		)
	}
}

// TestCompat21TaggedGTIDIntegration pins entry 21: a GTID set may carry
// UUID:TAG:NUMBER from 8.4 on, and the facts return both sets as opaque
// strings that survive it. Nothing here parses a GTID set; the assertions are
// substring containment of this run's own tag.
func TestCompat21TaggedGTIDIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	version := compatMySQLVersion(t)
	testsupport.BootstrapReplication(t, topology)

	sourceGTID, err := replication.NewInspector(topology.Source).GTIDStatus(t.Context())
	if err != nil {
		t.Fatalf("GTIDStatus on the MySQL %s source: %v", version, err)
	}
	if sourceGTID.Mode != "ON" {
		t.Fatalf("source GTID mode = %q, want ON", sourceGTID.Mode)
	}

	if version == mysqlVersion80 {
		// Tagged GTIDs do not exist before 8.4, so this version creates no
		// tagged transaction. Both sets are still read and returned whole.
		replicaGTID, replicaErr := replication.NewInspector(topology.Replica).GTIDStatus(t.Context())
		if replicaErr != nil {
			t.Fatalf("GTIDStatus on the MySQL %s replica: %v", version, replicaErr)
		}
		if replicaGTID.Mode != "ON" {
			t.Errorf("replica GTID mode = %q, want ON", replicaGTID.Mode)
		}
		t.Logf(
			"MySQL %s: no tagged transaction created; source sets executed=%q purged=%q",
			version,
			sourceGTID.Executed,
			sourceGTID.Purged,
		)

		return
	}

	tag := compat21CreateTaggedTransaction(t, topology)

	tagged, err := replication.NewInspector(topology.Source).GTIDStatus(t.Context())
	if err != nil {
		t.Fatalf("GTIDStatus on the source after the tagged transaction: %v", err)
	}
	marker := ":" + tag + ":"
	if !strings.Contains(tagged.Executed, marker) {
		t.Errorf(
			"source gtid_executed %q does not contain this run's tag %s",
			tagged.Executed,
			marker,
		)
	}

	testsupport.WaitReplicaCaughtUp(t, topology)

	channels, err := replication.NewInspector(topology.Replica).ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the replica after the tagged transaction: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("the replica reports %d channels, want 1: %+v", len(channels), channels)
	}
	if !strings.Contains(channels[0].RetrievedGTIDSet, marker) {
		t.Errorf(
			"replica Retrieved_Gtid_Set %q does not contain this run's tag %s",
			channels[0].RetrievedGTIDSet,
			marker,
		)
	}
}

// compat21CreateTaggedTransaction commits one tagged transaction on the source
// over a single pinned connection and returns the tag it used.
//
// The tag is fresh per run because gtid_executed is cumulative for the
// container's lifetime: a fixed tag from an earlier run would satisfy the
// assertion even if this run created nothing.
func compat21CreateTaggedTransaction(t *testing.T, topology *testsupport.ReplTopology) string {
	t.Helper()

	tag := freshGTIDTag(t)
	probeSchema := "tag_probe_" + tag[1:]
	if err := sqlutil.ValidateIdentifier(probeSchema); err != nil {
		t.Fatalf("generated schema name %q: %v", probeSchema, err)
	}

	conn, connErr := topology.Source.Conn(t.Context())
	if connErr != nil {
		t.Fatalf("pin a connection to the source: %v", connErr)
	}
	t.Cleanup(func() { conn.Close() })

	if _, setErr := conn.ExecContext(
		t.Context(), "SET SESSION gtid_next = 'AUTOMATIC:"+tag+"'",
	); setErr != nil {
		t.Fatalf("set gtid_next to the tagged form: %v", setErr)
	}

	// The backstop is registered the moment the SET succeeds. It is only a
	// backstop: the restoration below is an in-sequence statement, because a
	// deferred one would run after the DDL and too late to bound the
	// contamination. A session that cannot be restored must never go back to
	// the pool.
	restored := false
	defer func() {
		if restored {
			return
		}
		compat21RestoreGTIDNext(t, conn)
	}()

	if _, createErr := conn.ExecContext(
		t.Context(), "CREATE DATABASE "+sqlutil.QuoteIdentifier(probeSchema),
	); createErr != nil {
		t.Fatalf("create the tagged probe schema %q: %v", probeSchema, createErr)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), compatWaitTimeout)
		defer cancel()

		if _, dropErr := topology.Source.ExecContext(
			ctx, "DROP DATABASE IF EXISTS "+sqlutil.QuoteIdentifier(probeSchema),
		); dropErr != nil {
			t.Errorf("drop the tagged probe schema %q: %v", probeSchema, dropErr)
		}
	})

	if _, restoreErr := conn.ExecContext(t.Context(), sqlGTIDNextAutomatic); restoreErr != nil {
		t.Fatalf("restore gtid_next in sequence: %v", restoreErr)
	}
	restored = true

	return tag
}

// compat21RestoreGTIDNext is the backstop for a session whose gtid_next was
// never restored in sequence. A session that cannot be restored is discarded
// rather than returned to the pool.
func compat21RestoreGTIDNext(t *testing.T, conn *sql.Conn) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), compatWaitTimeout)
	defer cancel()

	_, restoreErr := conn.ExecContext(ctx, sqlGTIDNextAutomatic)
	if restoreErr == nil {
		return
	}
	t.Errorf("backstop restore of gtid_next failed: %v", restoreErr)

	rawErr := conn.Raw(func(any) error { return driver.ErrBadConn })
	if rawErr != nil && !errors.Is(rawErr, driver.ErrBadConn) {
		t.Errorf("discard the contaminated connection: %v", rawErr)
	}
}

// freshGTIDTag generates one tag per run: "t" plus seven hex characters. Eight
// characters, letter-first, is valid under both of the conflicting tag-length
// limits the manual states (docs/COMPAT.md entry 21).
func freshGTIDTag(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 4)
	if _, randErr := rand.Read(raw); randErr != nil {
		t.Fatalf("generate a GTID tag: %v", randErr)
	}

	tag := "t" + hex.EncodeToString(raw)[:7]
	if !regexp.MustCompile(gtidTagExpr).MatchString(tag) {
		t.Fatalf("generated tag %q does not match %s", tag, gtidTagExpr)
	}

	return tag
}

// TestCompat22RegisteredReplicasIntegration pins entry 22 as the server
// actually behaves: SHOW REPLICAS lists a replica that reports itself *and*
// one that does not, the latter with an empty Host and its real listening
// port. An implementation that treated an empty Host as a row to discard would
// report a smaller topology than the source described.
func TestCompat22RegisteredReplicasIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	version := compatMySQLVersion(t)
	testsupport.BootstrapReplication(t, topology)

	replicas, err := replication.NewInspector(topology.Source).RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("RegisteredReplicas on the MySQL %s source: %v", version, err)
	}

	reporting, hasReporting := findRegisteredReplica(replicas, 2)
	if !hasReporting {
		t.Fatalf("no replica with ServerID 2 in %+v", replicas)
	}
	wantHost := replicaReportedHost(t)
	if reporting.Host != wantHost {
		t.Errorf("ServerID 2 Host = %q, want %q", reporting.Host, wantHost)
	}

	silent, hasSilent := findRegisteredReplica(replicas, 3)
	if !hasSilent {
		t.Fatalf(
			"no replica with ServerID 3 in %+v; a replica without report_host registers all the same",
			replicas,
		)
	}
	if silent.Host != "" {
		t.Errorf("ServerID 3 Host = %q, want the empty host it reported", silent.Host)
	}
	if silent.Port != 3306 {
		t.Errorf(
			"ServerID 3 Port = %d, want 3306 — an unset report_port reports the real listening port",
			silent.Port,
		)
	}
}

func findRegisteredReplica(
	replicas []replication.RegisteredReplica,
	serverID uint32,
) (replication.RegisteredReplica, bool) {
	for i := range replicas {
		if replicas[i].ServerID == serverID {
			return replicas[i], true
		}
	}

	return replication.RegisteredReplica{}, false
}

// TestCompat23ReplicationConfigIntegration pins entry 23: every variable the
// fact reads exists under one spelling on every supported version, so the fact
// reads all three servers without a version branch.
func TestCompat23ReplicationConfigIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	version := compatMySQLVersion(t)

	source, err := replication.NewInspector(topology.Source).ReplicationConfig(t.Context())
	if err != nil {
		t.Fatalf("ReplicationConfig on the MySQL %s source: %v", version, err)
	}
	if source.ReadOnly {
		t.Error("source ReadOnly = true, want false")
	}

	replica, err := replication.NewInspector(topology.Replica).ReplicationConfig(t.Context())
	if err != nil {
		t.Fatalf("ReplicationConfig on the MySQL %s replica: %v", version, err)
	}
	if !replica.ReadOnly {
		t.Error("replica ReadOnly = false, want true — compose starts it with --read-only=ON")
	}

	silent, err := replication.NewInspector(topology.Silent).ReplicationConfig(t.Context())
	if err != nil {
		t.Fatalf("ReplicationConfig on the MySQL %s silent replica: %v", version, err)
	}
	if !silent.ReadOnly {
		t.Error("silent replica ReadOnly = false, want true")
	}

	var configured int // Config.ReplicaParallelWorkers is an int
	if err := topology.Replica.QueryRowContext(
		t.Context(), "SELECT @@GLOBAL.replica_parallel_workers",
	).Scan(&configured); err != nil {
		t.Fatalf("read replica_parallel_workers on the MySQL %s replica: %v", version, err)
	}
	if replica.ReplicaParallelWorkers != configured {
		t.Errorf(
			"ReplicaParallelWorkers = %d on MySQL %s, want the server's %d",
			replica.ReplicaParallelWorkers, version, configured,
		)
	}
	if version == mysqlVersion97 && replica.ReplicaParallelWorkers < 1 {
		// 9.x prunings left replica_parallel_workers in place with a
		// non-zero default; zero is reachable only on 8.x.
		t.Errorf("replica_parallel_workers = %d on MySQL %s, want at least 1",
			replica.ReplicaParallelWorkers, version)
	}
}
