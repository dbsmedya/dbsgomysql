package testsupport

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

func TestPollReplicaStatusUntilKeepsLastSuccessfulObservation(t *testing.T) {
	t.Parallel()

	const snapshot = "Channel_Name=\"\" Replica_IO_Running=\"Connecting\" " +
		"Replica_SQL_Running=\"Yes\" Seconds_Behind_Source=\"0\" " +
		"Last_IO_Error=\"connection refused\" Last_SQL_Error=\"\""
	const expired = "query SHOW REPLICA STATUS: context deadline exceeded"

	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()

	calls := 0
	accepted, observed := pollReplicaStatusUntil(ctx, ticks, func(context.Context) (bool, string, bool) {
		calls++
		if calls == 1 {
			return false, snapshot, true
		}

		cancel()

		return false, expired, false
	})
	if accepted {
		t.Fatal("pollReplicaStatusUntil() accepted an unsatisfied observation")
	}
	if observed != snapshot {
		t.Errorf("pollReplicaStatusUntil() observed %q, want last successful snapshot %q", observed, snapshot)
	}
}

func TestPollReplicaStatusUntilReportsReadErrorWithoutSnapshot(t *testing.T) {
	t.Parallel()

	const expired = "query SHOW REPLICA STATUS: context deadline exceeded"

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	accepted, observed := pollReplicaStatusUntil(ctx, nil, func(context.Context) (bool, string, bool) {
		return false, expired, false
	})
	if accepted {
		t.Fatal("pollReplicaStatusUntil() accepted a failed read")
	}
	if observed != expired {
		t.Errorf("pollReplicaStatusUntil() observed %q, want read error %q", observed, expired)
	}
}

func TestReplicaStatusRowsRequiresObservedColumns(t *testing.T) {
	t.Parallel()

	required := []struct {
		name  string
		value driver.Value
	}{
		{name: colChannelName, value: []byte("")},
		{name: colReplicaIORunning, value: []byte(threadRunning)},
		{name: colReplicaSQLRunning, value: []byte(threadRunning)},
		{name: colSecondsBehindSource, value: []byte("0")},
		{name: colLastIOError, value: []byte("")},
		{name: colLastSQLError, value: []byte("")},
	}

	for _, omitted := range required {
		t.Run(omitted.name, func(t *testing.T) {
			t.Parallel()

			columns := make([]string, 0, len(required)-1)
			row := make([]driver.Value, 0, len(required)-1)
			for _, field := range required {
				if field.name == omitted.name {
					continue
				}
				columns = append(columns, field.name)
				row = append(row, field.value)
			}

			db := OpenScriptedDB(ScriptedQuery{
				Match:   sqlShowReplicaStatus,
				Columns: columns,
				Rows:    [][]driver.Value{row},
			})
			t.Cleanup(func() { db.Close() })

			got, err := replicaStatusRows(t.Context(), db)
			if err == nil {
				t.Fatalf("replicaStatusRows() = %#v, nil; want missing-column error for %s", got, omitted.name)
			}
			if !strings.Contains(err.Error(), omitted.name) {
				t.Errorf("replicaStatusRows() error %q does not name missing column %s", err, omitted.name)
			}
		})
	}
}
