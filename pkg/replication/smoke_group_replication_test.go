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

// TestSmokeGroupMembers exercises GroupMembers and GroupMemberStats once
// against a live cluster member: the primary sees itself ONLINE among three
// members, and holds a stats row of its own.
func TestSmokeGroupMembers(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	inspector := replication.NewInspector(topology.Primary)

	primary := testsupport.ServerUUID(t, topology.Primary)
	waitForFullView(t, "primary", topology.Primary, map[string]string{
		primary: "primary",
		testsupport.ServerUUID(t, topology.Secondary): "secondary",
		testsupport.ServerUUID(t, topology.Rejoiner):  "rejoiner",
	}, primary)

	members, err := inspector.GroupMembers(t.Context())
	if err != nil {
		t.Fatalf("GroupMembers on the primary: %v", err)
	}
	if len(members) != 3 {
		t.Errorf("GroupMembers on the primary = %+v, want three members", members)
	}
	if findings := replication.CheckGroupReplicationLocalMemberOnline(members); len(findings) != 0 {
		t.Errorf("LOCAL_MEMBER_ONLINE on the primary = %+v, want none", findings)
	}

	stats, err := inspector.GroupMemberStats(t.Context())
	if err != nil {
		t.Fatalf("GroupMemberStats on the primary: %v", err)
	}
	own := 0
	for _, row := range stats {
		if row.Self {
			own++
		}
	}
	if own != 1 {
		t.Errorf("GroupMemberStats on the primary = %+v, want exactly one row of its own", stats)
	}
}
