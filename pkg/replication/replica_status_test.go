package replication

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// replicaStatusColumns returns the promised columns in the order the server
// reports them, plus any extras the caller wants appended.
func replicaStatusColumns(extra ...string) []string {
	columns := []string{
		"Channel_Name",
		"Replica_IO_Running",
		"Replica_SQL_Running",
		"Seconds_Behind_Source",
		"Last_IO_Errno",
		"Last_IO_Error",
		"Last_SQL_Errno",
		"Last_SQL_Error",
		"Retrieved_Gtid_Set",
		"Executed_Gtid_Set",
		"Source_Host",
		"Source_Port",
	}

	return append(columns, extra...)
}

// replicaStatusRow builds one row aligned with replicaStatusColumns, mixing the
// integer and text deliveries a driver may produce.
func replicaStatusRow(channel string, seconds, port driver.Value, extra ...driver.Value) []driver.Value {
	row := []driver.Value{
		[]byte(channel),
		[]byte("Yes"),
		[]byte("Yes"),
		seconds,
		int64(0),
		[]byte(""),
		int64(0),
		[]byte(""),
		[]byte("uuid:1-5"),
		[]byte("uuid:1-5"),
		[]byte("db1"),
		port,
	}

	return append(row, extra...)
}

func scriptReplicaStatus(t *testing.T, columns []string, rows [][]driver.Value) *sql.DB {
	t.Helper()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "SHOW REPLICA STATUS",
		Columns: columns,
		Rows:    rows,
	})
	t.Cleanup(func() { db.Close() })

	return db
}

func TestReplicaStatusTwoChannels(t *testing.T) {
	t.Parallel()

	db := scriptReplicaStatus(t, replicaStatusColumns(), [][]driver.Value{
		replicaStatusRow("ch1", int64(5), int64(3306)),
		replicaStatusRow("ch2", nil, []byte("3307")),
	})

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus() returned error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReplicaStatus() returned %d channels, want 2", len(got))
	}

	want := ChannelStatus{
		ChannelName:         "ch1",
		IORunning:           "Yes",
		SQLRunning:          "Yes",
		SecondsBehindSource: sql.NullInt64{Int64: 5, Valid: true},
		LastIOErrno:         0,
		LastIOError:         "",
		LastSQLErrno:        0,
		LastSQLError:        "",
		RetrievedGTIDSet:    "uuid:1-5",
		ExecutedGTIDSet:     "uuid:1-5",
		SourceHost:          "db1",
		SourcePort:          3306,
	}
	if got[0] != want {
		t.Errorf("ReplicaStatus()[0] = %#v, want %#v", got[0], want)
	}

	// Server row order is preserved, not sorted.
	if got[1].ChannelName != "ch2" {
		t.Errorf("ReplicaStatus()[1].ChannelName = %q, want %q", got[1].ChannelName, "ch2")
	}
	if got[1].SourcePort != 3307 {
		t.Errorf("ReplicaStatus()[1].SourcePort = %d, want 3307", got[1].SourcePort)
	}

	// A NULL Seconds_Behind_Source is the one promised column that may be NULL.
	if got[1].SecondsBehindSource.Valid {
		t.Errorf("ReplicaStatus()[1].SecondsBehindSource = %#v, want Valid false",
			got[1].SecondsBehindSource)
	}
	if !got[0].SecondsBehindSource.Valid {
		t.Errorf("ReplicaStatus()[0].SecondsBehindSource = %#v, want Valid true",
			got[0].SecondsBehindSource)
	}
}

func TestReplicaStatusEmpty(t *testing.T) {
	t.Parallel()

	db := scriptReplicaStatus(t, replicaStatusColumns(), nil)

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus() returned error %v, want nil", err)
	}
	if got == nil {
		t.Fatal("ReplicaStatus() = nil, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("ReplicaStatus() returned %d channels, want 0", len(got))
	}
}

func TestReplicaStatusMissingColumn(t *testing.T) {
	t.Parallel()

	columns := replicaStatusColumns()
	rows := [][]driver.Value{replicaStatusRow("ch1", int64(0), int64(3306))}

	// Drop Retrieved_Gtid_Set from both the column list and every row.
	const dropped = "Retrieved_Gtid_Set"
	position := -1
	for index, name := range columns {
		if name == dropped {
			position = index

			break
		}
	}
	if position < 0 {
		t.Fatalf("%s missing from the promised column list", dropped)
	}
	columns = append(columns[:position], columns[position+1:]...)
	for index, row := range rows {
		rows[index] = append(row[:position], row[position+1:]...)
	}

	db := scriptReplicaStatus(t, columns, rows)

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err == nil {
		t.Fatalf("ReplicaStatus() = %#v, nil; want a missing-column error", got)
	}
	if !errors.Is(err, errMissingColumn) {
		t.Errorf("errors.Is(%v, errMissingColumn) = false, want true", err)
	}
	assertOpError(t, err, opReplicaStatus, "", dropped)
}

func TestReplicaStatusUndecodableSeconds(t *testing.T) {
	t.Parallel()

	db := scriptReplicaStatus(t, replicaStatusColumns(), [][]driver.Value{
		replicaStatusRow("ch1", int64(0), int64(3306)),
		replicaStatusRow("ch2", []byte("soon"), int64(3307)),
	})

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err == nil {
		t.Fatalf("ReplicaStatus() = %#v, nil; want a decode error", got)
	}
	assertOpError(t, err, opReplicaStatus, "ch2", "Seconds_Behind_Source")
}

func TestReplicaStatusOverflowAttribution(t *testing.T) {
	t.Parallel()

	db := scriptReplicaStatus(t, replicaStatusColumns(), [][]driver.Value{
		replicaStatusRow("ch1", int64(0), int64(70000)),
	})

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err == nil {
		t.Fatalf("ReplicaStatus() = %#v, nil; want an overflow error", got)
	}
	if !errors.Is(err, errValueOutOfRange) {
		t.Errorf("errors.Is(%v, errValueOutOfRange) = false, want true", err)
	}
	assertOpError(t, err, opReplicaStatus, "ch1", "Source_Port")
}

func TestReplicaStatusUndecodableChannelName(t *testing.T) {
	t.Parallel()

	row := replicaStatusRow("ch1", int64(0), int64(3306))
	row[0] = nil // Channel_Name is never NULL; the row is unattributable.

	db := scriptReplicaStatus(t, replicaStatusColumns(), [][]driver.Value{row})

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err == nil {
		t.Fatalf("ReplicaStatus() = %#v, nil; want a decode error", got)
	}
	if !errors.Is(err, errUnexpectedNULL) {
		t.Errorf("errors.Is(%v, errUnexpectedNULL) = false, want true", err)
	}
	// Channel stays empty rather than being guessed from another column.
	assertOpError(t, err, opReplicaStatus, "", "Channel_Name")
}

func TestReplicaStatusIgnoresUnknownColumn(t *testing.T) {
	t.Parallel()

	// User and Password appear under --show-replica-auth-info; future server
	// versions may add more. Unknown columns are ignored, never an error.
	db := scriptReplicaStatus(t,
		replicaStatusColumns("User", "Password"),
		[][]driver.Value{
			replicaStatusRow("ch1", int64(1), int64(3306), []byte("repl"), []byte("secret")),
		},
	)

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("ReplicaStatus() returned error %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("ReplicaStatus() returned %d channels, want 1", len(got))
	}
	if got[0].ChannelName != "ch1" {
		t.Errorf("ReplicaStatus()[0].ChannelName = %q, want %q", got[0].ChannelName, "ch1")
	}
}

func TestReplicaStatusRowsErr(t *testing.T) {
	t.Parallel()

	cause := errors.New("iteration failed")
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "SHOW REPLICA STATUS",
		Columns: replicaStatusColumns(),
		Rows:    [][]driver.Value{replicaStatusRow("ch1", int64(0), int64(3306))},
		RowsErr: cause,
	})
	defer db.Close()

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err == nil {
		t.Fatalf("ReplicaStatus() = %#v, nil; want the scripted iteration failure", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false, want true", err)
	}
	assertOpError(t, err, opReplicaStatus, "", "")
}

func TestReplicaStatusCloseErr(t *testing.T) {
	t.Parallel()

	cause := errors.New("close failed")
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:    "SHOW REPLICA STATUS",
		Columns:  replicaStatusColumns(),
		Rows:     [][]driver.Value{replicaStatusRow("ch1", int64(0), int64(3306))},
		CloseErr: cause,
	})
	defer db.Close()

	got, err := NewInspector(db).ReplicaStatus(t.Context())
	if err == nil {
		t.Fatalf("ReplicaStatus() = %#v, nil; want the scripted close failure", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false, want true", err)
	}
	assertOpError(t, err, opReplicaStatus, "", "")
}

func TestReplicaStatusFreshSlices(t *testing.T) {
	t.Parallel()

	db := scriptReplicaStatus(t, replicaStatusColumns(), [][]driver.Value{
		replicaStatusRow("ch1", int64(5), int64(3306)),
	})
	inspector := NewInspector(db)

	first, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("first ReplicaStatus() returned error %v, want nil", err)
	}

	first[0].ChannelName = "tampered"
	first[0].RetrievedGTIDSet = "tampered"
	first[0].SecondsBehindSource = sql.NullInt64{Int64: 999, Valid: true}

	second, err := inspector.ReplicaStatus(t.Context())
	if err != nil {
		t.Fatalf("second ReplicaStatus() returned error %v, want nil", err)
	}
	if second[0].ChannelName != "ch1" {
		t.Errorf("second ReplicaStatus()[0].ChannelName = %q, want %q", second[0].ChannelName, "ch1")
	}
	if second[0].RetrievedGTIDSet != "uuid:1-5" {
		t.Errorf("second ReplicaStatus()[0].RetrievedGTIDSet = %q, want %q",
			second[0].RetrievedGTIDSet, "uuid:1-5")
	}
	if second[0].SecondsBehindSource != (sql.NullInt64{Int64: 5, Valid: true}) {
		t.Errorf("second ReplicaStatus()[0].SecondsBehindSource = %#v, want {5 true}",
			second[0].SecondsBehindSource)
	}
	if &first[0] == &second[0] {
		t.Error("both calls returned the same backing array; each call must build a fresh slice")
	}
}

func TestReplicaStatusSQLAndQueryCount(t *testing.T) {
	t.Parallel()

	var log []string
	db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
		Match:   "SHOW REPLICA STATUS",
		Columns: replicaStatusColumns(),
		Rows:    [][]driver.Value{replicaStatusRow("ch1", int64(0), int64(3306))},
	})
	defer db.Close()

	if _, err := NewInspector(db).ReplicaStatus(t.Context()); err != nil {
		t.Fatalf("ReplicaStatus() returned error %v, want nil", err)
	}

	if len(log) != 1 {
		t.Fatalf("ReplicaStatus() issued %d statements (%q), want exactly 1", len(log), log)
	}
	if log[0] != "SHOW REPLICA STATUS" {
		t.Errorf("statement = %q, want %q", log[0], "SHOW REPLICA STATUS")
	}
}

func TestChannelStatusJSONContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		channel ChannelStatus
		want    string
	}{
		{
			name: "seconds present",
			channel: ChannelStatus{
				ChannelName:         "ch1",
				IORunning:           "Yes",
				SQLRunning:          "Yes",
				SecondsBehindSource: sql.NullInt64{Int64: 5, Valid: true},
				LastIOErrno:         0,
				LastIOError:         "",
				LastSQLErrno:        0,
				LastSQLError:        "",
				RetrievedGTIDSet:    "uuid:1-5",
				ExecutedGTIDSet:     "uuid:1-5",
				SourceHost:          "db1",
				SourcePort:          3306,
			},
			want: `{"channel_name":"ch1","io_running":"Yes","sql_running":"Yes",` +
				`"seconds_behind_source":{"Int64":5,"Valid":true},` +
				`"last_io_errno":0,"last_io_error":"","last_sql_errno":0,"last_sql_error":"",` +
				`"retrieved_gtid_set":"uuid:1-5","executed_gtid_set":"uuid:1-5",` +
				`"source_host":"db1","source_port":3306}`,
		},
		{
			name: "seconds NULL",
			channel: ChannelStatus{
				ChannelName:         "",
				IORunning:           "Connecting",
				SQLRunning:          "Yes",
				SecondsBehindSource: sql.NullInt64{},
				LastIOErrno:         2003,
				LastIOError:         "error connecting to source",
				RetrievedGTIDSet:    "",
				ExecutedGTIDSet:     "",
				SourceHost:          "db1",
				SourcePort:          3306,
			},
			want: `{"channel_name":"","io_running":"Connecting","sql_running":"Yes",` +
				`"seconds_behind_source":{"Int64":0,"Valid":false},` +
				`"last_io_errno":2003,"last_io_error":"error connecting to source",` +
				`"last_sql_errno":0,"last_sql_error":"",` +
				`"retrieved_gtid_set":"","executed_gtid_set":"",` +
				`"source_host":"db1","source_port":3306}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(testCase.channel)
			if err != nil {
				t.Fatalf("json.Marshal returned error %v, want nil", err)
			}
			if string(encoded) != testCase.want {
				t.Errorf("json.Marshal = %s, want %s", encoded, testCase.want)
			}
		})
	}
}

func assertOpError(t *testing.T, err error, op, channel, column string) {
	t.Helper()

	var opErr *OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("errors.As(%v, *OpError) = false, want true", err)
	}
	if opErr.Op != op {
		t.Errorf("OpError.Op = %q, want %q", opErr.Op, op)
	}
	if opErr.Channel != channel {
		t.Errorf("OpError.Channel = %q, want %q", opErr.Channel, channel)
	}
	if opErr.Column != column {
		t.Errorf("OpError.Column = %q, want %q", opErr.Column, column)
	}
}
