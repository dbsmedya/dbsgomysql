package replication

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// The statement, spelled independently of the library's constant.
const (
	testSQLGroupMemberStats = "SELECT CHANNEL_NAME, VIEW_ID, MEMBER_ID, " +
		"COUNT_TRANSACTIONS_IN_QUEUE, COUNT_TRANSACTIONS_CHECKED, COUNT_CONFLICTS_DETECTED, " +
		"COUNT_TRANSACTIONS_ROWS_VALIDATING, COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE, " +
		"COUNT_TRANSACTIONS_REMOTE_APPLIED, MEMBER_ID = @@server_uuid AS IS_SELF " +
		"FROM performance_schema.replication_group_member_stats LIMIT 18446744073709551615"

	testMatchGroupMemberStats = "performance_schema.replication_group_member_stats"
	testViewID                = "17288841234567890:3"
)

func groupMemberStatsColumns() []string {
	return []string{
		"CHANNEL_NAME", "VIEW_ID", "MEMBER_ID",
		"COUNT_TRANSACTIONS_IN_QUEUE", "COUNT_TRANSACTIONS_CHECKED", "COUNT_CONFLICTS_DETECTED",
		"COUNT_TRANSACTIONS_ROWS_VALIDATING", "COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE",
		"COUNT_TRANSACTIONS_REMOTE_APPLIED", "IS_SELF",
	}
}

// groupMemberStatsRow builds one row aligned with groupMemberStatsColumns; the
// six counters are 1 to 6 in column order, delivered as text and integers.
func groupMemberStatsRow(view, id string, self driver.Value) []driver.Value {
	return []driver.Value{
		[]byte(testApplierChannel), []byte(view), []byte(id),
		[]byte("1"), uint64(2), int64(3), []byte("4"), uint64(5), []byte("6"), self,
	}
}

func scriptGroupMemberStats(t *testing.T, columns []string, rows [][]driver.Value) *sql.DB {
	t.Helper()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match: testMatchGroupMemberStats, Columns: columns, Rows: rows,
	})
	t.Cleanup(func() { db.Close() })

	return db
}

// TestGroupMemberStatsStates pins the shapes COMPAT entry 32 records, decoded
// through the fact.
func TestGroupMemberStatsStates(t *testing.T) {
	t.Parallel()

	zeroRow := func(id string, self driver.Value) []driver.Value {
		return []driver.Value{
			[]byte(testApplierChannel), []byte(""), []byte(id),
			uint64(0), uint64(0), uint64(0), uint64(0), uint64(0), uint64(0), self,
		}
	}

	cases := []struct {
		name string
		rows [][]driver.Value
		want []GroupMemberStats
	}{
		{
			name: "no active plugin: no rows",
			want: []GroupMemberStats{},
		},
		{
			name: "never started, every version: one anonymous row of zeros",
			rows: [][]driver.Value{zeroRow("", int64(0))},
			want: []GroupMemberStats{{ChannelName: testApplierChannel}},
		},
		{
			name: "stopped after participating: its own row of zeros",
			rows: [][]driver.Value{zeroRow(testMemberA, int64(1))},
			want: []GroupMemberStats{{ChannelName: testApplierChannel, MemberID: testMemberA, Self: true}},
		},
		{
			name: "formed group: every counter decoded into its own field",
			rows: [][]driver.Value{
				groupMemberStatsRow(testViewID, testMemberB, int64(0)),
				groupMemberStatsRow(testViewID, testMemberA, []byte("1")),
			},
			want: []GroupMemberStats{
				{
					ChannelName: testApplierChannel, ViewID: testViewID, MemberID: testMemberB,
					TransactionsInQueue: 1, TransactionsChecked: 2, ConflictsDetected: 3,
					TransactionsRowsValidating: 4, TransactionsRemoteInApplierQueue: 5,
					TransactionsRemoteApplied: 6,
				},
				{
					ChannelName: testApplierChannel, ViewID: testViewID, MemberID: testMemberA,
					TransactionsInQueue: 1, TransactionsChecked: 2, ConflictsDetected: 3,
					TransactionsRowsValidating: 4, TransactionsRemoteInApplierQueue: 5,
					TransactionsRemoteApplied: 6, Self: true,
				},
			},
		},
		{
			name: "a counter above MaxInt64 decodes exactly",
			rows: [][]driver.Value{{
				[]byte(testApplierChannel), []byte(testViewID), []byte(testMemberA),
				[]byte(strconv.FormatUint(math.MaxUint64, 10)), uint64(0), uint64(0), uint64(0),
				uint64(math.MaxInt64) + 1, uint64(0), int64(1),
			}},
			want: []GroupMemberStats{{
				ChannelName: testApplierChannel, ViewID: testViewID, MemberID: testMemberA,
				TransactionsInQueue: math.MaxUint64, TransactionsRemoteInApplierQueue: math.MaxInt64 + 1,
				Self: true,
			}},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := scriptGroupMemberStats(t, groupMemberStatsColumns(), testCase.rows)
			got, err := NewInspector(db).GroupMemberStats(t.Context())
			if err != nil {
				t.Fatalf("GroupMemberStats() returned error %v, want nil", err)
			}
			if got == nil {
				t.Fatal("GroupMemberStats() = nil, want a non-nil slice")
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("GroupMemberStats() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestGroupMemberStatsReadsColumnsByName reverses the promised columns and
// interleaves the unread ones the table also has: a positional reader, or one
// that swapped two counters, would fail here.
func TestGroupMemberStatsReadsColumnsByName(t *testing.T) {
	t.Parallel()

	columns := []string{
		"IS_SELF", "COUNT_TRANSACTIONS_REMOTE_APPLIED", "COUNT_TRANSACTIONS_LOCAL_PROPOSED",
		"COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE", "TRANSACTIONS_COMMITTED_ALL_MEMBERS",
		"COUNT_TRANSACTIONS_ROWS_VALIDATING", "COUNT_CONFLICTS_DETECTED", "COUNT_TRANSACTIONS_CHECKED",
		"COUNT_TRANSACTIONS_IN_QUEUE", "MEMBER_ID", "VIEW_ID", "CHANNEL_NAME",
	}
	row := []driver.Value{
		int64(1), uint64(60), uint64(99), uint64(50), []byte("uuid:1-9"),
		uint64(40), uint64(30), uint64(20), uint64(10), []byte(testMemberA), []byte(testViewID),
		[]byte(testApplierChannel),
	}

	db := scriptGroupMemberStats(t, columns, [][]driver.Value{row})
	got, err := NewInspector(db).GroupMemberStats(t.Context())
	if err != nil {
		t.Fatalf("GroupMemberStats() returned error %v, want nil", err)
	}

	want := []GroupMemberStats{{
		ChannelName: testApplierChannel, ViewID: testViewID, MemberID: testMemberA,
		TransactionsInQueue: 10, TransactionsChecked: 20, ConflictsDetected: 30,
		TransactionsRowsValidating: 40, TransactionsRemoteInApplierQueue: 50,
		TransactionsRemoteApplied: 60, Self: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GroupMemberStats() = %#v, want %#v", got, want)
	}
}

func TestGroupMemberStatsStatement(t *testing.T) {
	t.Parallel()

	var log []string
	db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
		Match: testMatchGroupMemberStats, Columns: groupMemberStatsColumns(),
	})
	defer db.Close()

	if _, err := NewInspector(db).GroupMemberStats(t.Context()); err != nil {
		t.Fatalf("GroupMemberStats() returned error %v, want nil", err)
	}
	if want := []string{testSQLGroupMemberStats}; !slices.Equal(log, want) {
		t.Errorf("statements = %q, want %q", log, want)
	}
}

func TestGroupMemberStatsErrors(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")
	valid := groupMemberStatsRow(testViewID, testMemberA, int64(1))

	withValue := func(position int, value driver.Value) []driver.Value {
		row := slices.Clone(valid)
		row[position] = value

		return row
	}
	rowsQuery := func(row []driver.Value) testsupport.ScriptedQuery {
		return testsupport.ScriptedQuery{
			Match: testMatchGroupMemberStats, Columns: groupMemberStatsColumns(),
			Rows: [][]driver.Value{row},
		}
	}

	cases := []struct {
		name        string
		query       testsupport.ScriptedQuery
		wantChannel string
		wantColumn  string
		wantCause   error
	}{
		{
			name:      "statement fails",
			query:     testsupport.ScriptedQuery{Match: testMatchGroupMemberStats, Err: queryFailure},
			wantCause: queryFailure,
		},
		{
			name: "promised column missing",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMemberStats, Columns: groupMemberStatsColumns()[1:],
			},
			wantColumn: "CHANNEL_NAME",
			wantCause:  errMissingColumn,
		},
		{
			name:        "NULL view id",
			query:       rowsQuery(withValue(1, nil)),
			wantChannel: testApplierChannel,
			wantColumn:  "VIEW_ID",
			wantCause:   errUnexpectedNULL,
		},
		{
			name:        "NULL counter",
			query:       rowsQuery(withValue(7, nil)),
			wantChannel: testApplierChannel,
			wantColumn:  "COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE",
			wantCause:   errUnexpectedNULL,
		},
		{
			name:        "negative counter is a range failure, not a wraparound",
			query:       rowsQuery(withValue(3, int64(-1))),
			wantChannel: testApplierChannel,
			wantColumn:  "COUNT_TRANSACTIONS_IN_QUEUE",
			wantCause:   errValueOutOfRange,
		},
		{
			name:        "malformed counter text",
			query:       rowsQuery(withValue(8, []byte("lots"))),
			wantChannel: testApplierChannel,
			wantColumn:  "COUNT_TRANSACTIONS_REMOTE_APPLIED",
		},
		{
			name:        "IS_SELF outside 0 and 1",
			query:       rowsQuery(withValue(9, []byte("2"))),
			wantChannel: testApplierChannel,
			wantColumn:  "IS_SELF",
			wantCause:   errUnrecognizedValue,
		},
		{
			name: "iteration fails",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMemberStats, Columns: groupMemberStatsColumns(),
				Rows: [][]driver.Value{valid}, RowsErr: iterationFailure,
			},
			wantCause: iterationFailure,
		},
		{
			name: "close fails",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMemberStats, Columns: groupMemberStatsColumns(),
				Rows: [][]driver.Value{valid}, CloseErr: closeFailure, PendingResultSet: true,
			},
			wantCause: closeFailure,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.query)
			defer db.Close()

			got, err := NewInspector(db).GroupMemberStats(t.Context())
			if err == nil {
				t.Fatalf("GroupMemberStats() = %#v, nil; want an error", got)
			}
			if got != nil {
				t.Errorf("GroupMemberStats() returned %#v with its error, want nil", got)
			}
			assertOpError(t, err, "group_member_stats", testCase.wantChannel, testCase.wantColumn)
			if testCase.wantCause != nil && !errors.Is(err, testCase.wantCause) {
				t.Errorf("error %v does not wrap %v", err, testCase.wantCause)
			}
		})
	}
}

func TestGroupMemberStatsNilQuerier(t *testing.T) {
	t.Parallel()

	_, err := NewInspector(nil).GroupMemberStats(t.Context())
	if !errors.Is(err, ErrNilQuerier) {
		t.Fatalf("GroupMemberStats() on a nil Querier returned %v, want ErrNilQuerier", err)
	}
	assertOpError(t, err, "group_member_stats", "", "")
}

// TestGroupMemberStatsCanceledContext proves a canceled context is an error,
// never an empty slice that a queue check would pass.
func TestGroupMemberStatsCanceledContext(t *testing.T) {
	t.Parallel()

	db := scriptGroupMemberStats(t, groupMemberStatsColumns(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := NewInspector(db).GroupMemberStats(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GroupMemberStats() on a canceled context = %#v, %v; want context.Canceled", got, err)
	}
	assertOpError(t, err, "group_member_stats", "", "")
}

func TestGroupMemberStatsFreshSlices(t *testing.T) {
	t.Parallel()

	db := scriptGroupMemberStats(t, groupMemberStatsColumns(), [][]driver.Value{
		groupMemberStatsRow(testViewID, testMemberA, int64(1)),
	})
	inspector := NewInspector(db)

	first, err := inspector.GroupMemberStats(t.Context())
	if err != nil {
		t.Fatalf("first GroupMemberStats() returned error %v", err)
	}
	first[0].TransactionsInQueue = 999

	second, err := inspector.GroupMemberStats(t.Context())
	if err != nil {
		t.Fatalf("second GroupMemberStats() returned error %v", err)
	}
	if second[0].TransactionsInQueue != 1 {
		t.Errorf("second GroupMemberStats()[0].TransactionsInQueue = %d, want 1", second[0].TransactionsInQueue)
	}
}

func TestGroupMemberStatsJSONContract(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(GroupMemberStats{
		ChannelName: testApplierChannel, ViewID: testViewID, MemberID: testMemberA,
		TransactionsInQueue: 1, TransactionsChecked: 2, ConflictsDetected: 3,
		TransactionsRowsValidating: 4, TransactionsRemoteInApplierQueue: 5,
		TransactionsRemoteApplied: math.MaxUint64, Self: true,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error %v, want nil", err)
	}

	want := `{"channel_name":"group_replication_applier","view_id":"` + testViewID + `",` +
		`"member_id":"` + testMemberA + `","transactions_in_queue":1,"transactions_checked":2,` +
		`"conflicts_detected":3,"transactions_rows_validating":4,` +
		`"transactions_remote_in_applier_queue":5,` +
		`"transactions_remote_applied":18446744073709551615,"self":true}`
	if string(encoded) != want {
		t.Errorf("json.Marshal = %s, want %s", encoded, want)
	}
}

// TestGroupMemberStatsErrorNamesMember proves a counter failure names the
// member whose row it is, keeping the cause reachable through errors.Is.
func TestGroupMemberStatsErrorNamesMember(t *testing.T) {
	t.Parallel()

	row := groupMemberStatsRow(testViewID, testMemberB, int64(0))
	row[7] = int64(-5)
	db := scriptGroupMemberStats(t, groupMemberStatsColumns(), [][]driver.Value{row})

	_, err := NewInspector(db).GroupMemberStats(t.Context())
	assertOpError(t, err, "group_member_stats", testApplierChannel, "COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE")
	if !strings.Contains(err.Error(), "member "+testMemberB+": ") {
		t.Errorf("error %q does not name member %s", err, testMemberB)
	}
	if !errors.Is(err, errValueOutOfRange) {
		t.Errorf("error %v does not wrap errValueOutOfRange", err)
	}
}
