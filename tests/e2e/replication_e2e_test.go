//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

const (
	replicationRunningGolden = "testdata/replication_running.json"
	replicationStoppedGolden = "testdata/replication_sql_stopped.json"

	// e2eMaxSecondsBehind is a deliberately wide bound. The scenario proves
	// what a stopped applier does to the gate, not how fast an idle topology
	// replicates, so the healthy snapshot must not depend on timing.
	e2eMaxSecondsBehind = 300

	// Placeholders for the parts of a channel snapshot that cannot be a
	// golden: GTID sets carry server UUIDs and a transaction count that grows
	// with every run, and the source coordinates carry the compose service
	// name and container port of whichever version's trio is under test.
	gtidSetPlaceholder    = "{{gtid_set}}"
	sourceHostPlaceholder = "{{source_host}}"
)

// TestReplicationScenarioE2E walks one replication incident end to end: a
// healthy replica passes the gate, a stopped applier produces exactly the two
// findings that describe it, and the cleanup restores the topology and proves
// it running again.
//
// It never calls t.Parallel: it stops a thread on the shared replica.
func TestReplicationScenarioE2E(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	testsupport.BootstrapReplication(t, topology)

	inspector := replication.NewInspector(topology.Replica)

	assertGoldenReplicationFindings(
		t,
		replicationGateFindings(t, inspector),
		replicationRunningGolden,
	)

	// StopReplicaSQLThread registers the restart cleanup before it stops
	// anything, and returns only once one single snapshot showed the applier
	// stopped and Seconds_Behind_Source NULL together. Taking the snapshot
	// after it returns is what keeps the golden comparison from racing a
	// statement the server executes without blocking.
	testsupport.StopReplicaSQLThread(t, topology.Replica)

	stopped := replicationGateFindings(t, inspector)
	if len(stopped) != 2 {
		t.Fatalf(
			"a stopped applier produced %d findings, want 2 (channels running, seconds behind): %+v",
			len(stopped),
			stopped,
		)
	}
	assertGoldenReplicationFindings(t, stopped, replicationStoppedGolden)

	// Restoration is proven rather than assumed, and it is proven by the
	// helper: its cleanup issues START REPLICA SQL_THREAD and then polls the
	// channel back to both-threads-running, failing this test if it cannot.
}

// replicationGateFindings runs the three-check replication gate over one
// snapshot. One snapshot feeds all three checks, so the findings cannot
// disagree about the state they describe.
func replicationGateFindings(
	t *testing.T,
	inspector *replication.Inspector,
) []replication.Finding {
	t.Helper()

	channels, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus on the replica: %v", err)
	}

	return slices.Concat(
		replication.CheckReplicationConfigured(channels),
		replication.CheckReplicationChannelsRunning(channels),
		replication.CheckSecondsBehindSourceWithin(channels, e2eMaxSecondsBehind),
	)
}

type projectedReplicationFinding struct {
	Check    string   `json:"check"`
	Channels []string `json:"channels"`
	Facts    any      `json:"facts"`
}

func assertGoldenReplicationFindings(
	t *testing.T,
	findings []replication.Finding,
	path string,
) {
	t.Helper()

	// Message is deliberately not projected: the spec states its wording is
	// not contractual, and a golden over it would freeze prose.
	projected := make([]projectedReplicationFinding, 0, len(findings))
	for index := range findings {
		projected = append(projected, projectedReplicationFinding{
			Check:    findings[index].Check,
			Channels: findings[index].Channels,
			Facts:    normalizeReplicationFact(findings[index].Facts),
		})
	}

	actualJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected replication findings: %v", err)
	}
	expectedJSON, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v", path, err)
	}

	var actual, expected any
	if err := json.Unmarshal(actualJSON, &actual); err != nil {
		t.Fatalf("parse actual replication findings JSON: %v", err)
	}
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatalf("parse replication golden JSON: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("findings differ from %s:\n got %s\nwant %s", path, actualJSON, expectedJSON)
	}
}

// normalizeReplicationFact replaces the fields of a channel snapshot that
// differ between runs and between versions. Everything else — the thread
// states, the last-error pair, and the lag estimate's validity — is exactly
// what the golden exists to pin.
func normalizeReplicationFact(fact any) any {
	channel, ok := fact.(replication.ChannelStatus)
	if !ok {
		return fact
	}

	channel.RetrievedGTIDSet = gtidSetPlaceholder
	channel.ExecutedGTIDSet = gtidSetPlaceholder
	channel.SourceHost = sourceHostPlaceholder
	channel.SourcePort = 0

	return channel
}
