package replication

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// The three statements, spelled independently of the library's constants. A pin
// that reused them could not detect the library misspelling one.
const (
	testSQLPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication' LIMIT 18446744073709551615"
	testSQLGroupName = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name' LIMIT 18446744073709551615"
	testSQLSizeLimit = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_transaction_size_limit' LIMIT 18446744073709551615"

	// Both variable reads name performance_schema.global_variables, and the
	// scripted driver answers a statement from the first rule whose Match it
	// contains, so each variable rule matches its variable's quoted name.
	testMatchPlugins   = "information_schema.PLUGINS"
	testMatchGroupName = "'group_replication_group_name'"
	testMatchSizeLimit = "'group_replication_transaction_size_limit'"
	testGroupUUID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testDefaultLimit   = "150000000"

	// testSizeLimitCause opens the cause of every failure of the third read.
	testSizeLimitCause = "variable group_replication_transaction_size_limit: "
)

func pluginRows(statuses ...string) testsupport.ScriptedQuery {
	rows := make([][]driver.Value, 0, len(statuses))
	for _, status := range statuses {
		rows = append(rows, []driver.Value{[]byte(status)})
	}

	return testsupport.ScriptedQuery{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: rows}
}

func variableRows(match string, values ...driver.Value) testsupport.ScriptedQuery {
	rows := make([][]driver.Value, 0, len(values))
	for _, value := range values {
		rows = append(rows, []driver.Value{value})
	}

	return testsupport.ScriptedQuery{Match: match, Columns: []string{"VARIABLE_VALUE"}, Rows: rows}
}

func groupNameRows(values ...driver.Value) testsupport.ScriptedQuery {
	return variableRows(testMatchGroupName, values...)
}

func sizeLimitRows(values ...driver.Value) testsupport.ScriptedQuery {
	return variableRows(testMatchSizeLimit, values...)
}

// memberScript is a configured member whose third read returns limit as its
// one row.
func memberScript(limit driver.Value) []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{
		pluginRows("ACTIVE"), groupNameRows([]byte(testGroupUUID)), sizeLimitRows(limit),
	}
}

// TestGroupReplicationStates pins the server states the fact reports
// (docs/COMPAT.md entry 30): absence and DISABLED are states, never errors,
// and the limit is known exactly when the server returned its row.
func TestGroupReplicationStates(t *testing.T) {
	t.Parallel()

	member := func(limit uint64) GroupReplication {
		return GroupReplication{
			PluginStatus: "ACTIVE", GroupName: testGroupUUID,
			TransactionSizeLimit: limit, TransactionSizeLimitKnown: true,
		}
	}

	cases := []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   GroupReplication
	}{
		{
			name:   "plugin not installed",
			script: []testsupport.ScriptedQuery{pluginRows(), groupNameRows(), sizeLimitRows()},
			want:   GroupReplication{},
		},
		{
			name:   "plugin disabled hides both variables",
			script: []testsupport.ScriptedQuery{pluginRows("DISABLED"), groupNameRows(), sizeLimitRows()},
			want:   GroupReplication{PluginStatus: "DISABLED"},
		},
		{
			name: "plugin active, name unset",
			script: []testsupport.ScriptedQuery{
				pluginRows("ACTIVE"), groupNameRows([]byte("")), sizeLimitRows([]byte(testDefaultLimit)),
			},
			want: GroupReplication{
				PluginStatus: "ACTIVE", TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true,
			},
		},
		{
			name:   "configured member",
			script: memberScript([]byte(testDefaultLimit)),
			want:   member(150000000),
		},
		{
			// The pair the two fields exist for: the text "0" is a known
			// limit of none, and zero rows (the first two cases) is an
			// unknown limit. Both carry the number 0.
			name:   "a zero limit is known, not absent",
			script: memberScript([]byte("0")),
			want:   member(0),
		},
		{
			name:   "unfamiliar status is reported intact",
			script: []testsupport.ScriptedQuery{pluginRows("SOMETHING_NEW"), groupNameRows(), sizeLimitRows()},
			want:   GroupReplication{PluginStatus: "SOMETHING_NEW"},
		},
		{
			name: "string driver values",
			script: []testsupport.ScriptedQuery{
				{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{"ACTIVE"}}},
				groupNameRows(testGroupUUID),
				sizeLimitRows("2147483647"),
			},
			want: member(2147483647),
		},
		{name: "signed integer driver value", script: memberScript(int64(1)), want: member(1)},
		{name: "unsigned integer driver value", script: memberScript(uint64(2147483647)), want: member(2147483647)},
		{
			// decodeUint64 accepts an integral float, as for every unsigned
			// column in this package.
			name:   "integral float driver value",
			script: memberScript(float64(150000000)),
			want:   member(150000000),
		},
		{
			// The documented maximum is the manual's bound, not the fact's.
			name:   "a value above the documented maximum is reported as sent",
			script: memberScript([]byte("4294967296")),
			want:   member(4294967296),
		},
		{
			name:   "the largest uint64 is reported as sent",
			script: memberScript([]byte("18446744073709551615")),
			want:   member(math.MaxUint64),
		},
		{
			// The three reads are not one snapshot, and the fact never
			// reconciles them.
			name: "a limit row without a plugin row is reported as read",
			script: []testsupport.ScriptedQuery{
				pluginRows(), groupNameRows(), sizeLimitRows([]byte(testDefaultLimit)),
			},
			want: GroupReplication{TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.script...)
			defer db.Close()

			got, err := NewInspector(db).GroupReplication(t.Context())
			if err != nil {
				t.Fatalf("GroupReplication() returned error %v, want nil", err)
			}
			if got != testCase.want {
				t.Errorf("GroupReplication() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestGroupReplicationStatements pins the exact statements, their order, and
// that there are exactly three whatever the plugin row says: a consumer
// auditing a query log sees these and no others.
func TestGroupReplicationStatements(t *testing.T) {
	t.Parallel()

	scripts := map[string][]testsupport.ScriptedQuery{
		"configured member":    memberScript([]byte(testDefaultLimit)),
		"plugin not installed": {pluginRows(), groupNameRows(), sizeLimitRows()},
	}

	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, script...)
			defer db.Close()

			if _, err := NewInspector(db).GroupReplication(t.Context()); err != nil {
				t.Fatalf("GroupReplication() returned error %v, want nil", err)
			}

			want := []string{testSQLPluginStatus, testSQLGroupName, testSQLSizeLimit}
			if !slices.Equal(log, want) {
				t.Errorf("statements = %q, want %q", log, want)
			}
		})
	}
}

// TestGroupReplicationClosesRowsBeforeNextRead proves each statement's rows
// are closed before the next statement is issued. With one connection and a
// result set the driver cannot finish on its own, an unclosed result would
// hold the connection and the next statement would wait for the deadline.
func TestGroupReplicationClosesRowsBeforeNextRead(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		name  string
		index int
	}{
		{"plugin read", 0},
		{"group-name read", 1},
	} {
		t.Run(held.name, func(t *testing.T) {
			t.Parallel()

			script := memberScript([]byte(testDefaultLimit))
			script[held.index].PendingResultSet = true
			db := testsupport.OpenScriptedDB(script...)
			defer db.Close()
			db.SetMaxOpenConns(1)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			got, err := NewInspector(db).GroupReplication(ctx)
			if err != nil {
				t.Fatalf("GroupReplication() on a single connection returned error %v, want nil", err)
			}
			if !got.TransactionSizeLimitKnown {
				t.Errorf("GroupReplication() = %#v, want the third read's limit", got)
			}
		})
	}
}

func TestGroupReplicationErrors(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")

	// third is the limit read's rule, behind two successful reads.
	third := func(rule testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows([]byte(testGroupUUID)), rule}
	}
	limitText := func(text string) []testsupport.ScriptedQuery {
		return third(sizeLimitRows([]byte(text)))
	}

	cases := []struct {
		name       string
		script     []testsupport.ScriptedQuery
		wantColumn string
		wantCause  error
		// limitRead marks a failure of the third read, whose cause must name
		// the variable; no other failure's cause may.
		limitRead bool
	}{
		{
			name: "plugin statement fails",
			script: []testsupport.ScriptedQuery{
				{Match: testMatchPlugins, Err: queryFailure}, groupNameRows(), sizeLimitRows(),
			},
			wantCause: queryFailure,
		},
		{
			name: "group-name statement fails after a successful first read",
			script: []testsupport.ScriptedQuery{
				pluginRows("ACTIVE"), {Match: testMatchGroupName, Err: queryFailure}, sizeLimitRows(),
			},
			wantCause: queryFailure,
		},
		{
			name:       "two plugin rows",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE", "ACTIVE"), groupNameRows(), sizeLimitRows()},
			wantColumn: "PLUGIN_STATUS",
			wantCause:  errMultipleRows,
		},
		{
			name: "two group-name rows",
			script: []testsupport.ScriptedQuery{
				pluginRows("ACTIVE"), groupNameRows([]byte("a"), []byte("b")), sizeLimitRows(),
			},
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMultipleRows,
		},
		{
			name: "NULL plugin status",
			script: []testsupport.ScriptedQuery{
				{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{nil}}},
				groupNameRows(), sizeLimitRows(),
			},
			wantColumn: "PLUGIN_STATUS",
			wantCause:  errUnexpectedNULL,
		},
		{
			name:       "NULL group name",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows(nil), sizeLimitRows()},
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnexpectedNULL,
		},
		{
			name:       "undecodable group name",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows(int64(7)), sizeLimitRows()},
			wantColumn: "VARIABLE_VALUE",
		},
		{
			name: "group-name result missing its VARIABLE_VALUE column",
			script: []testsupport.ScriptedQuery{
				pluginRows("ACTIVE"),
				{Match: testMatchGroupName, Columns: []string{"VARIABLE_NAME"}},
				sizeLimitRows(),
			},
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMissingColumn,
		},
		{
			name: "iteration fails",
			script: []testsupport.ScriptedQuery{
				{
					Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"},
					Rows: [][]driver.Value{{[]byte("ACTIVE")}}, RowsErr: iterationFailure,
				},
				groupNameRows(), sizeLimitRows(),
			},
			wantCause: iterationFailure,
		},
		{
			name: "close fails",
			script: []testsupport.ScriptedQuery{
				{
					Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"},
					Rows:     [][]driver.Value{{[]byte("ACTIVE")}},
					CloseErr: closeFailure, PendingResultSet: true,
				},
				groupNameRows(), sizeLimitRows(),
			},
			wantCause: closeFailure,
		},
		{
			name:      "limit statement fails after two successful reads",
			script:    third(testsupport.ScriptedQuery{Match: testMatchSizeLimit, Err: queryFailure}),
			wantCause: queryFailure,
			limitRead: true,
		},
		{
			name:       "two limit rows",
			script:     third(sizeLimitRows([]byte("1"), []byte("2"))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMultipleRows,
			limitRead:  true,
		},
		{
			name:       "NULL limit",
			script:     third(sizeLimitRows(nil)),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnexpectedNULL,
			limitRead:  true,
		},
		{
			name:       "empty limit text",
			script:     limitText(""),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			limitRead:  true,
		},
		{
			name:       "non-numeric limit text",
			script:     limitText("unlimited"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			limitRead:  true,
		},
		{
			name:       "negative limit text",
			script:     limitText("-1"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			limitRead:  true,
		},
		{
			name:       "fractional limit text",
			script:     limitText("1.5"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			limitRead:  true,
		},
		{
			name:       "limit text beyond uint64",
			script:     limitText("18446744073709551616"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrRange,
			limitRead:  true,
		},
		{
			name:       "negative integer limit",
			script:     third(sizeLimitRows(int64(-1))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errValueOutOfRange,
			limitRead:  true,
		},
		{
			name:       "fractional float limit",
			script:     third(sizeLimitRows(float64(1.5))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnrecognizedValue,
			limitRead:  true,
		},
		{
			name:       "float limit beyond uint64",
			script:     third(sizeLimitRows(float64(1 << 64))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errValueOutOfRange,
			limitRead:  true,
		},
		{
			name:       "undecodable limit driver type",
			script:     third(sizeLimitRows(true)),
			wantColumn: "VARIABLE_VALUE",
			limitRead:  true,
		},
		{
			name:       "limit result missing its VARIABLE_VALUE column",
			script:     third(testsupport.ScriptedQuery{Match: testMatchSizeLimit, Columns: []string{"VARIABLE_NAME"}}),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMissingColumn,
			limitRead:  true,
		},
		{
			name: "limit iteration fails",
			script: third(testsupport.ScriptedQuery{
				Match: testMatchSizeLimit, Columns: []string{"VARIABLE_VALUE"},
				Rows: [][]driver.Value{{[]byte("1")}}, RowsErr: iterationFailure,
			}),
			wantCause: iterationFailure,
			limitRead: true,
		},
		{
			name: "limit close fails",
			script: third(testsupport.ScriptedQuery{
				Match: testMatchSizeLimit, Columns: []string{"VARIABLE_VALUE"},
				Rows:     [][]driver.Value{{[]byte("1")}},
				CloseErr: closeFailure, PendingResultSet: true,
			}),
			wantCause: closeFailure,
			limitRead: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.script...)
			defer db.Close()

			got, err := NewInspector(db).GroupReplication(t.Context())
			if err == nil {
				t.Fatalf("GroupReplication() = %#v, nil; want an error", got)
			}
			if got != (GroupReplication{}) {
				t.Errorf("GroupReplication() returned %#v with its error, want the zero value", got)
			}

			var opErr *OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("error %v is %T, want *OpError", err, err)
			}
			if opErr.Op != "group_replication" {
				t.Errorf("Op = %q, want group_replication", opErr.Op)
			}
			if opErr.Column != testCase.wantColumn {
				t.Errorf("Column = %q, want %q", opErr.Column, testCase.wantColumn)
			}
			if opErr.Channel != "" {
				t.Errorf("Channel = %q, want empty", opErr.Channel)
			}
			if testCase.wantCause != nil && !errors.Is(err, testCase.wantCause) {
				t.Errorf("error %v does not wrap %v", err, testCase.wantCause)
			}
			if named := strings.HasPrefix(opErr.Err.Error(), testSizeLimitCause); named != testCase.limitRead {
				t.Errorf("cause %q starts with %q: %t, want %t", opErr.Err, testSizeLimitCause, named, testCase.limitRead)
			}
		})
	}
}

// TestGroupReplicationErrorText pins the rendered text of a failed limit read,
// which names the variable, next to the group-name read's, which is unchanged.
func TestGroupReplicationErrorText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   string
	}{
		{
			name:   "NULL limit",
			script: memberScript(nil),
			want: "replication: group_replication column VARIABLE_VALUE: " +
				"variable group_replication_transaction_size_limit: unexpected NULL",
		},
		{
			name:   "NULL group name",
			script: []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows(nil), sizeLimitRows()},
			want:   "replication: group_replication column VARIABLE_VALUE: unexpected NULL",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.script...)
			defer db.Close()

			_, err := NewInspector(db).GroupReplication(t.Context())
			if err == nil || err.Error() != testCase.want {
				t.Errorf("GroupReplication() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestGroupReplicationNilQuerier(t *testing.T) {
	t.Parallel()

	_, err := NewInspector(nil).GroupReplication(t.Context())
	if !errors.Is(err, ErrNilQuerier) {
		t.Fatalf("GroupReplication() on a nil Querier returned %v, want ErrNilQuerier", err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Op != "group_replication" {
		t.Errorf("error %v, want *OpError with Op group_replication", err)
	}
}

// TestGroupReplicationJSON pins the encoding, including both limit keys: a
// known limit of zero and an unknown limit encode the same number and differ
// only in transaction_size_limit_known.
func TestGroupReplicationJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fact GroupReplication
		want string
	}{
		{
			name: "member with a known limit of zero",
			fact: GroupReplication{PluginStatus: "ACTIVE", GroupName: testGroupUUID, TransactionSizeLimitKnown: true},
			want: `{"plugin_status":"ACTIVE","group_name":"` + testGroupUUID +
				`","transaction_size_limit":0,"transaction_size_limit_known":true}`,
		},
		{
			name: "member with the default limit",
			fact: GroupReplication{
				PluginStatus: "ACTIVE", GroupName: testGroupUUID,
				TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true,
			},
			want: `{"plugin_status":"ACTIVE","group_name":"` + testGroupUUID +
				`","transaction_size_limit":150000000,"transaction_size_limit_known":true}`,
		},
		{
			name: "limit unknown",
			fact: GroupReplication{PluginStatus: "DISABLED"},
			want: `{"plugin_status":"DISABLED","group_name":"",` +
				`"transaction_size_limit":0,"transaction_size_limit_known":false}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(testCase.fact)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if string(encoded) != testCase.want {
				t.Errorf("json = %s, want %s", encoded, testCase.want)
			}
		})
	}
}

// TestGroupReplicationCanceledContext proves a canceled context is an error
// naming the operation, never an empty fact that reads as "plugin absent".
func TestGroupReplicationCanceledContext(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(pluginRows(), groupNameRows(), sizeLimitRows())
	defer db.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := NewInspector(db).GroupReplication(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GroupReplication() on a canceled context = %#v, %v; want context.Canceled", got, err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Op != "group_replication" {
		t.Errorf("error %v, want *OpError with Op group_replication", err)
	}
}

// cancelBeforeQuery is a Querier that cancels the caller's context when
// statement number at arrives, after every earlier statement's rows were read
// and closed: the cancellation falls strictly between two of the fact's reads.
type cancelBeforeQuery struct {
	db      *sql.DB
	cancel  context.CancelFunc
	at      int
	queries int
}

func (q *cancelBeforeQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.queries++
	if q.queries == q.at {
		q.cancel()
	}

	return q.db.QueryContext(ctx, query, args...)
}

func (q *cancelBeforeQuery) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return q.db.QueryRowContext(ctx, query, args...)
}

// TestGroupReplicationCanceledBetweenStatements pins that a context canceled
// after one read and before the next fails the whole fact: all three reads
// must succeed, never a half-filled value. Canceled before the third read,
// the error names the limit variable.
func TestGroupReplicationCanceledBetweenStatements(t *testing.T) {
	t.Parallel()

	for _, at := range []int{2, 3} {
		t.Run("before statement "+strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(memberScript([]byte(testDefaultLimit))...)
			defer db.Close()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			querier := &cancelBeforeQuery{db: db, cancel: cancel, at: at}

			got, err := NewInspector(querier).GroupReplication(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("GroupReplication() canceled before statement %d = %#v, %v; want context.Canceled", at, got, err)
			}
			if got != (GroupReplication{}) {
				t.Errorf("GroupReplication() = %#v alongside its error, want the zero value", got)
			}
			if querier.queries != at {
				t.Errorf("issued %d statements, want %d: the cancellation must reach statement %d", querier.queries, at, at)
			}

			var opErr *OpError
			if !errors.As(err, &opErr) || opErr.Op != "group_replication" {
				t.Fatalf("error %v, want *OpError with Op group_replication", err)
			}
			if named := strings.HasPrefix(opErr.Err.Error(), testSizeLimitCause); named != (at == 3) {
				t.Errorf("cause %q starts with %q: %t, want %t", opErr.Err, testSizeLimitCause, named, at == 3)
			}
		})
	}
}
