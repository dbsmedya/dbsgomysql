//go:build integration

package replication_test

import (
	"slices"
	"testing"

	// Registers the "mysql" driver that internal/testsupport opens the
	// topology with; the library itself never imports a driver.
	_ "github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// smokeMaxSecondsBehind bounds the replica's reported lag generously. The
// smoke topology carries no workload, so the estimate is 0 in practice; a wide
// bound exercises the check's passing arm without turning the smoke run into a
// timing assertion.
const smokeMaxSecondsBehind = 300

const (
	smokeThreadRunning = "Yes"
	smokeGTIDModeOn    = "ON"
)

// TestSmokeReplication exercises every fact and every check once against a
// live source-replica pair: the replica answers the channel-scoped facts and
// passes the three-check gate, the source answers the server-scoped facts and
// fails REPLICATION_CONFIGURED on its own empty channel list.
//
// It never calls t.Parallel: the topology is shared and other replication
// tests mutate its threads.
func TestSmokeReplication(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	testsupport.BootstrapReplication(t, topology)

	smokeReplicaFacts(t, topology)
	smokeSourceFacts(t, topology)
}

func smokeReplicaFacts(t *testing.T, topology *testsupport.ReplTopology) {
	t.Helper()

	inspector := replication.NewInspector(topology.Replica)

	channels, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the replica: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("ReplicaStatus returned %d channels, want 1: %+v", len(channels), channels)
	}

	channel := channels[0]
	if channel.IORunning != smokeThreadRunning || channel.SQLRunning != smokeThreadRunning {
		t.Errorf(
			"channel %q threads = IO %q / SQL %q, want %q / %q",
			channel.ChannelName,
			channel.IORunning,
			channel.SQLRunning,
			smokeThreadRunning,
			smokeThreadRunning,
		)
	}
	if !channel.SecondsBehindSource.Valid {
		t.Errorf(
			"channel %q Seconds_Behind_Source is NULL while both threads run",
			channel.ChannelName,
		)
	}

	findings := slices.Concat(
		replication.CheckReplicationConfigured(channels),
		replication.CheckReplicationChannelsRunning(channels),
		replication.CheckSecondsBehindSourceWithin(channels, smokeMaxSecondsBehind),
	)
	if len(findings) != 0 {
		t.Errorf("replication gate on a healthy replica returned findings: %+v", findings)
	}

	config, err := inspector.ReplicationConfig(t.Context())
	if err != nil {
		t.Fatalf("ReplicationConfig on the replica: %v", err)
	}
	if !config.ReadOnly {
		t.Error("replica ReadOnly = false, want true — compose starts it with --read-only=ON")
	}

	gtid, err := inspector.GTIDStatus(t.Context())
	if err != nil {
		t.Fatalf("GTIDStatus on the replica: %v", err)
	}
	if gtid.Mode != smokeGTIDModeOn {
		t.Errorf("replica GTID mode = %q, want %q", gtid.Mode, smokeGTIDModeOn)
	}
}

func smokeSourceFacts(t *testing.T, topology *testsupport.ReplTopology) {
	t.Helper()

	inspector := replication.NewInspector(topology.Source)

	enabled, err := inspector.BinaryLogEnabled(t.Context())
	if err != nil {
		t.Fatalf("BinaryLogEnabled on the source: %v", err)
	}
	if !enabled {
		t.Error("source BinaryLogEnabled = false, want true")
	}
	if findings := replication.CheckBinaryLogEnabled(enabled); len(findings) != 0 {
		t.Errorf("CheckBinaryLogEnabled on a logging source returned findings: %+v", findings)
	}

	status, err := inspector.BinaryLogStatus(t.Context())
	if err != nil {
		t.Fatalf("BinaryLogStatus on the source: %v", err)
	}
	if status == nil {
		t.Fatal("BinaryLogStatus on a logging source returned nil")
	}
	if status.File == "" {
		t.Error("BinaryLogStatus reported an empty File on a logging source")
	}

	replicas, err := inspector.RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("RegisteredReplicas on the source: %v", err)
	}
	if len(replicas) == 0 {
		t.Error("RegisteredReplicas returned no rows; the reporting replica should have registered")
	}

	gtid, err := inspector.GTIDStatus(t.Context())
	if err != nil {
		t.Fatalf("GTIDStatus on the source: %v", err)
	}
	if findings := replication.CheckGTIDModeOn(gtid); len(findings) != 0 {
		t.Errorf("CheckGTIDModeOn on a GTID source returned findings: %+v", findings)
	}

	channels, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the source: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("the source reports %d replication channels, want 0: %+v", len(channels), channels)
	}

	findings := replication.CheckReplicationConfigured(channels)
	if len(findings) != 1 {
		t.Fatalf(
			"CheckReplicationConfigured on the source returned %d findings, want 1: %+v",
			len(findings),
			findings,
		)
	}
	if findings[0].Check != replication.IDReplicationConfigured {
		t.Errorf(
			"finding check = %q, want %q",
			findings[0].Check,
			replication.IDReplicationConfigured,
		)
	}
}
