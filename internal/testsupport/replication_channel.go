package testsupport

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// SilentChannel names the second channel AddSilentReplicaChannel configures on
// the trio's replica.
const SilentChannel = "silentprobe"

const sqlSilentChannelConfigured = "SELECT COUNT(*) FROM performance_schema.replication_connection_configuration " +
	"WHERE CHANNEL_NAME = '" + SilentChannel + "'"

// AddSilentReplicaChannel gives the trio's replica a second running channel,
// replicating from the silent replica, so SHOW REPLICA STATUS on the replica
// returns two rows. A test that needs a multi-row answer from that statement
// has no other fixture: the trio gives each replica one channel, and a Group
// Replication member lists neither group channel (docs/COMPAT.md entry 29).
//
// The silent replica's hostname is the source's with "-silent" in place of
// "-source", the naming every trio compose file uses. GTID auto-positioning
// skips every transaction the replica already applied from the source, so the
// channel applies nothing new. Removal is registered before the channel
// exists; a channel a killed run left behind is removed by
// BootstrapReplication, which every trio test that reads the replica's
// channels calls first, so the next run's one-channel assertions never see it.
func AddSilentReplicaChannel(t *testing.T, topology *ReplTopology) {
	t.Helper()

	base, ok := strings.CutSuffix(topology.SourceHost, "-source")
	if !ok {
		t.Fatalf("source host %q does not end in -source; cannot derive the silent replica's host", topology.SourceHost)
	}
	silentHost := base + "-silent"
	if !regexp.MustCompile(replicationHostExpr).MatchString(silentHost) {
		t.Fatalf("silent host %q does not match %s; refusing to splice it into a statement", silentHost, replicationHostExpr)
	}

	removeSilentChannel(t, topology.Replica)
	t.Cleanup(func() { removeSilentChannel(t, topology.Replica) })

	ctx, cancel := cleanupSafeContext(t, ReplicationWaitDeadline)
	defer cancel()

	change := fmt.Sprintf(
		"CHANGE REPLICATION SOURCE TO SOURCE_HOST='%s', SOURCE_PORT=3306, "+
			"SOURCE_USER='root', SOURCE_PASSWORD='root', "+
			"SOURCE_AUTO_POSITION=1, GET_SOURCE_PUBLIC_KEY=1 FOR CHANNEL '%s'",
		silentHost, SilentChannel,
	)
	for _, statement := range []string{change, sqlStartReplica + " FOR CHANNEL '" + SilentChannel + "'"} {
		if _, err := topology.Replica.ExecContext(ctx, statement); err != nil {
			t.Fatalf("add the %s channel to the replica: %s: %v", SilentChannel, statement, err)
		}
	}

	WaitChannelsRunning(t, topology.Replica, ReplicationWaitDeadline)
}

func removeSilentChannel(t *testing.T, db *sql.DB) {
	t.Helper()

	ctx, cancel := cleanupSafeContext(t, grStatementTimeout)
	defer cancel()

	var configured int
	if err := db.QueryRowContext(ctx, sqlSilentChannelConfigured).Scan(&configured); err != nil {
		t.Errorf("look for the %s channel on the replica: %v", SilentChannel, err)
		return
	}
	if configured == 0 {
		return
	}
	for _, statement := range []string{
		"STOP REPLICA FOR CHANNEL '" + SilentChannel + "'",
		"RESET REPLICA ALL FOR CHANNEL '" + SilentChannel + "'",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Errorf("remove the %s channel from the replica: %s: %v", SilentChannel, statement, err)
			return
		}
	}
}
