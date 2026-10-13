//go:build integration

package replication_test

import (
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// TestSmokeGroupReplication exercises the GroupReplication fact once against a
// live cluster member: the plugin is ACTIVE and a group name is configured.
func TestSmokeGroupReplication(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	got, err := replication.NewInspector(topology.Primary).GroupReplication(t.Context())
	if err != nil {
		t.Fatalf("GroupReplication on the primary: %v", err)
	}
	if got.PluginStatus != "ACTIVE" || got.GroupName == "" {
		t.Errorf("GroupReplication on the primary = %#v, want ACTIVE with a group name", got)
	}
}
