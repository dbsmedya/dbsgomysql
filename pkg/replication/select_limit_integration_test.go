//go:build integration

package replication_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// These tests pin docs/COMPAT.md entry 35 for pkg/replication. The SELECT
// facts end every statement with the maximum LIMIT, so a session
// sql_select_limit cannot truncate them. The SHOW facts take no LIMIT; these
// tests are the measurement of whether the variable affects them, on populated
// fixtures with more rows than the limit. A failure in a SHOW case means the
// server caps that statement: stop and take it to the operator (spec §3.3).
var selectLimits = []int{0, 1}

const sqlShowVersionVariable = "SHOW GLOBAL VARIABLES WHERE Variable_name = 'version'"

func setSessionSelectLimit(t *testing.T, conn *sql.Conn, limit int) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(ctx, "SET SESSION sql_select_limit = DEFAULT"); err != nil {
			t.Errorf("restore sql_select_limit: %v", err)
		}
	})
	if _, err := conn.ExecContext(t.Context(), fmt.Sprintf("SET SESSION sql_select_limit = %d", limit)); err != nil {
		t.Fatalf("set sql_select_limit = %d: %v", limit, err)
	}
}

func pinnedConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()

	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("pin a connection: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close pinned connection: %v", err)
		}
	})

	return conn
}

// TestCompat35VariableFactsIgnoreSessionSelectLimitIntegration: at limit 0 a
// single-row SELECT without its own LIMIT returns no row, which the variable
// facts would report as an error.
func TestCompat35VariableFactsIgnoreSessionSelectLimitIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	conn := pinnedConn(t, topology.Source)
	inspector := replication.NewInspector(conn)

	read := func(t *testing.T) (bool, replication.GTIDStatus, replication.Config) {
		t.Helper()

		logBin, err := inspector.BinaryLogEnabled(t.Context())
		if err != nil {
			t.Fatalf("BinaryLogEnabled: %v", err)
		}
		gtid, err := inspector.GTIDStatus(t.Context())
		if err != nil {
			t.Fatalf("GTIDStatus: %v", err)
		}
		config, err := inspector.ReplicationConfig(t.Context())
		if err != nil {
			t.Fatalf("ReplicationConfig: %v", err)
		}

		return logBin, gtid, config
	}

	wantLogBin, wantGTID, wantConfig := read(t)
	setSessionSelectLimit(t, conn, 0)
	logBin, gtid, config := read(t)
	if logBin != wantLogBin || gtid != wantGTID || config != wantConfig {
		t.Errorf("variable facts under sql_select_limit = 0 = (%v, %+v, %+v), want (%v, %+v, %+v)",
			logBin, gtid, config, wantLogBin, wantGTID, wantConfig)
	}

	// The trio's source has no Group Replication plugin: the lookup's answer
	// is empty with or without the cap, so only a nil error distinguishes.
	group, err := inspector.GroupReplication(t.Context())
	if err != nil {
		t.Fatalf("GroupReplication on a server without the plugin under sql_select_limit = 0: %v", err)
	}
	if group != (replication.GroupReplication{}) {
		t.Errorf("GroupReplication on a server without the plugin = %+v, want the zero value", group)
	}
}

// TestCompat35GroupFactsIgnoreSessionSelectLimitIntegration: at limit 0 the
// zero-or-one-row PLUGINS lookup would return no row, which GroupReplication
// reads as "plugin absent" — a false answer, not an error — on the group
// primary and on the standalone servers with the plugin ACTIVE and DISABLED.
// The group name and the transaction size limit are read from
// performance_schema.global_variables, not SHOW VARIABLES, which the variable
// caps (docs/COMPAT.md entry 35), so at limit 0 they must still be the
// configured values. The members and stats tables hold three rows each, more
// than either limit.
func TestCompat35GroupFactsIgnoreSessionSelectLimitIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	conn := pinnedConn(t, topology.Primary)
	inspector := replication.NewInspector(conn)

	wantGroup, err := inspector.GroupReplication(t.Context())
	if err != nil {
		t.Fatalf("GroupReplication: %v", err)
	}
	if wantGroup.PluginStatus != "ACTIVE" || wantGroup.GroupName == "" || !wantGroup.TransactionSizeLimitKnown {
		t.Fatalf("baseline GroupReplication = %+v, want ACTIVE with a group name and a known limit", wantGroup)
	}
	// The standalone servers carry the plugin ACTIVE and DISABLED with no
	// group: a lookup that lost its row would read DISABLED as absent, and a
	// limit read that lost its row would read the ACTIVE server's limit as
	// unknown.
	standalone := []struct {
		name     string
		conn     *sql.Conn
		want     string
		known    bool
		baseline replication.GroupReplication
	}{
		{name: "plugin-installed server", conn: pinnedConn(t, topology.Installed), want: "ACTIVE", known: true},
		{name: "plugin-disabled server", conn: pinnedConn(t, topology.Disabled), want: "DISABLED", known: false},
	}
	for index := range standalone {
		server := &standalone[index]
		group, err := replication.NewInspector(server.conn).GroupReplication(t.Context())
		if err != nil || group.PluginStatus != server.want || group.TransactionSizeLimitKnown != server.known {
			t.Fatalf("baseline GroupReplication on the %s = %+v, %v; want %s with the limit known: %t",
				server.name, group, err, server.want, server.known)
		}
		server.baseline = group
	}
	wantMembers := groupMemberIDs(t, inspector)
	wantStats := groupStatsMemberIDs(t, inspector)
	if len(wantMembers) != 3 || len(wantStats) != 3 {
		t.Fatalf("baseline has %d members and %d stats rows, want 3 of each", len(wantMembers), len(wantStats))
	}

	for _, limit := range selectLimits {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			setSessionSelectLimit(t, conn, limit)

			group, err := inspector.GroupReplication(t.Context())
			if err != nil {
				t.Fatalf("GroupReplication: %v", err)
			}
			if group != wantGroup {
				t.Errorf("GroupReplication under sql_select_limit = %d = %+v, want %+v", limit, group, wantGroup)
			}
			// Named on their own: these are the reads SHOW VARIABLES would lose.
			if group.GroupName != wantGroup.GroupName {
				t.Errorf("GroupName under sql_select_limit = %d = %q, want %q", limit, group.GroupName, wantGroup.GroupName)
			}
			if group.TransactionSizeLimit != wantGroup.TransactionSizeLimit ||
				group.TransactionSizeLimitKnown != wantGroup.TransactionSizeLimitKnown {
				t.Errorf("transaction size limit under sql_select_limit = %d = %d (known %t), want %d (known %t)",
					limit, group.TransactionSizeLimit, group.TransactionSizeLimitKnown,
					wantGroup.TransactionSizeLimit, wantGroup.TransactionSizeLimitKnown)
			}
			if got := groupMemberIDs(t, inspector); !slices.Equal(got, wantMembers) {
				t.Errorf("GroupMembers under sql_select_limit = %d = %q, want %q", limit, got, wantMembers)
			}
			if got := groupStatsMemberIDs(t, inspector); !slices.Equal(got, wantStats) {
				t.Errorf("GroupMemberStats under sql_select_limit = %d = %q, want %q", limit, got, wantStats)
			}
			for _, server := range standalone {
				setSessionSelectLimit(t, server.conn, limit)
				group, err := replication.NewInspector(server.conn).GroupReplication(t.Context())
				if err != nil {
					t.Fatalf("GroupReplication on the %s: %v", server.name, err)
				}
				if group != server.baseline {
					t.Errorf("GroupReplication on the %s under sql_select_limit = %d = %+v, want %+v",
						server.name, limit, group, server.baseline)
				}
			}
		})
	}
}

// groupMemberIDs and groupStatsMemberIDs compare identities, sorted: counters
// and states move between reads, membership does not.
func groupMemberIDs(t *testing.T, inspector *replication.Inspector) []string {
	t.Helper()

	members, err := inspector.GroupMembers(t.Context())
	if err != nil {
		t.Fatalf("GroupMembers: %v", err)
	}
	ids := make([]string, 0, len(members))
	for index := range members {
		ids = append(ids, members[index].MemberID)
	}
	slices.Sort(ids)

	return ids
}

func groupStatsMemberIDs(t *testing.T, inspector *replication.Inspector) []string {
	t.Helper()

	stats, err := inspector.GroupMemberStats(t.Context())
	if err != nil {
		t.Fatalf("GroupMemberStats: %v", err)
	}
	ids := make([]string, 0, len(stats))
	for _, row := range stats {
		ids = append(ids, row.MemberID)
	}
	slices.Sort(ids)

	return ids
}

// TestCompat35ShowFactsUnderSessionSelectLimitIntegration measures the SHOW
// statements the library cannot give a LIMIT, each on a fixture with more rows
// than the limit where the statement can return several: SHOW REPLICA STATUS
// with two channels, SHOW REPLICAS with two registered replicas, and SHOW
// BINARY LOG STATUS (SHOW MASTER STATUS before 8.2.0) on a live source. It
// also pins the capped side: SHOW GLOBAL VARIABLES is answered from
// Performance Schema and returns no row at limit 0, which is why
// GroupReplication reads performance_schema.global_variables instead.
func TestCompat35ShowFactsUnderSessionSelectLimitIntegration(t *testing.T) {
	topology := testsupport.ReplicationTopology(t)
	testsupport.BootstrapReplication(t, topology)
	testsupport.AddSilentReplicaChannel(t, topology)

	replicaConn := pinnedConn(t, topology.Replica)
	sourceConn := pinnedConn(t, topology.Source)
	replicaInspector := replication.NewInspector(replicaConn)
	sourceInspector := replication.NewInspector(sourceConn)

	wantChannels := channelNames(t, replicaInspector)
	if len(wantChannels) != 2 {
		t.Fatalf("replica lists channels %q, want two", wantChannels)
	}
	wantReplicas := registeredServerIDs(t, sourceInspector)
	if len(wantReplicas) < 2 {
		t.Fatalf("source lists registered replicas %v, want at least two", wantReplicas)
	}
	wantStatus, err := sourceInspector.BinaryLogStatus(t.Context())
	if err != nil || wantStatus == nil {
		t.Fatalf("BinaryLogStatus = %+v, %v; want a row from a source with binary logging", wantStatus, err)
	}

	for _, limit := range selectLimits {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			setSessionSelectLimit(t, replicaConn, limit)
			setSessionSelectLimit(t, sourceConn, limit)

			if got := channelNames(t, replicaInspector); !slices.Equal(got, wantChannels) {
				t.Errorf("SHOW REPLICA STATUS under sql_select_limit = %d lists %q, want %q", limit, got, wantChannels)
			}
			if got := registeredServerIDs(t, sourceInspector); !slices.Equal(got, wantReplicas) {
				t.Errorf("SHOW REPLICAS under sql_select_limit = %d lists %v, want %v", limit, got, wantReplicas)
			}
			status, err := sourceInspector.BinaryLogStatus(t.Context())
			if err != nil {
				t.Fatalf("BinaryLogStatus under sql_select_limit = %d: %v", limit, err)
			}
			if status == nil || status.File != wantStatus.File {
				t.Errorf("BinaryLogStatus under sql_select_limit = %d = %+v, want file %q", limit, status, wantStatus.File)
			}
			// One row matches, so the cap shows only at limit 0.
			if got, want := countRows(t, sourceConn, sqlShowVersionVariable), min(limit, 1); got != want {
				t.Errorf("SHOW GLOBAL VARIABLES under sql_select_limit = %d returned %d rows, want %d", limit, got, want)
			}
		})
	}
}

func channelNames(t *testing.T, inspector *replication.Inspector) []string {
	t.Helper()

	channels, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus: %v", err)
	}
	names := make([]string, 0, len(channels))
	for index := range channels {
		names = append(names, channels[index].ChannelName)
	}
	slices.Sort(names)

	return names
}

func registeredServerIDs(t *testing.T, inspector *replication.Inspector) []uint32 {
	t.Helper()

	replicas, err := inspector.RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("RegisteredReplicas: %v", err)
	}
	ids := make([]uint32, 0, len(replicas))
	for _, replica := range replicas {
		ids = append(ids, replica.ServerID)
	}
	slices.Sort(ids)

	return ids
}
