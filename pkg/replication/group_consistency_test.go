package replication

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// GroupConsistency's own statements, spelled independently of the library's
// constants. The group-name and plugin statements are the testSQL constants
// of group_replication_test.go.
const (
	testSQLSessionConsistency = "SELECT VARIABLE_VALUE FROM performance_schema.session_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_consistency' LIMIT 18446744073709551615"
	testSQLGlobalConsistency = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_consistency' LIMIT 18446744073709551615"

	// Both consistency reads name the same variable, so their rules match on
	// the table, and the global rule must come after the group-name rule,
	// which also names global_variables.
	testMatchSessionConsistency = "session_variables WHERE VARIABLE_NAME = 'group_replication_consistency'"
	testMatchGlobalConsistency  = "global_variables WHERE VARIABLE_NAME = 'group_replication_consistency'"

	testGroupNameCause = "variable group_replication_group_name: "
	testSessionCause   = "session variable group_replication_consistency: "
	testGlobalCause    = "global variable group_replication_consistency: "
)

func sessionRows(values ...driver.Value) testsupport.ScriptedQuery {
	return variableRows(testMatchSessionConsistency, values...)
}

func globalRows(values ...driver.Value) testsupport.ScriptedQuery {
	return variableRows(testMatchGlobalConsistency, values...)
}

// consistencyMember is a member whose session runs session and whose global
// value is global. No plugin row is scripted: with the group name's row
// returned the plugin table must not be read.
func consistencyMember(session, global string) []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{
		groupNameRows([]byte(testGroupUUID)), sessionRows([]byte(session)), globalRows([]byte(global)),
	}
}

// consistencyAbsent is a server without the plugin: the variable is the
// server's, so both rows are there; no group-name row, no plugin row.
func consistencyAbsent() []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{
		groupNameRows(), sessionRows([]byte("EVENTUAL")), globalRows([]byte("EVENTUAL")), pluginRows(),
	}
}

// consistencyStates are the states both entry points must report alike.
func consistencyStates() []struct {
	name   string
	script []testsupport.ScriptedQuery
	want   GroupConsistency
} {
	bopf := "BEFORE_ON_PRIMARY_FAILOVER"

	return []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   GroupConsistency
	}{
		{
			name:   "member, fresh session",
			script: consistencyMember(bopf, bopf),
			want: GroupConsistency{
				PluginStatus: "ACTIVE", SessionConsistency: bopf, SessionConsistencyKnown: true,
				GlobalConsistency: bopf, GlobalConsistencyKnown: true,
			},
		},
		{
			name:   "member, session set",
			script: consistencyMember("AFTER", bopf),
			want: GroupConsistency{
				PluginStatus: "ACTIVE", SessionConsistency: "AFTER", SessionConsistencyKnown: true,
				GlobalConsistency: bopf, GlobalConsistencyKnown: true,
			},
		},
		{
			name:   "plugin not installed",
			script: consistencyAbsent(),
			want: GroupConsistency{
				SessionConsistency: "EVENTUAL", SessionConsistencyKnown: true,
				GlobalConsistency: "EVENTUAL", GlobalConsistencyKnown: true,
			},
		},
		{
			name: "plugin disabled",
			script: []testsupport.ScriptedQuery{
				groupNameRows(), sessionRows([]byte("EVENTUAL")), globalRows([]byte("EVENTUAL")), pluginRows("DISABLED"),
			},
			want: GroupConsistency{
				PluginStatus: "DISABLED", SessionConsistency: "EVENTUAL", SessionConsistencyKnown: true,
				GlobalConsistency: "EVENTUAL", GlobalConsistencyKnown: true,
			},
		},
		{
			name: "no session row is a value not known",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte(testGroupUUID)), sessionRows(), globalRows([]byte("EVENTUAL")),
			},
			want: GroupConsistency{
				PluginStatus: "ACTIVE", GlobalConsistency: "EVENTUAL", GlobalConsistencyKnown: true,
			},
		},
		{
			name: "no global row is a value not known",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte(testGroupUUID)), sessionRows([]byte("EVENTUAL")), globalRows(),
			},
			want: GroupConsistency{
				PluginStatus: "ACTIVE", SessionConsistency: "EVENTUAL", SessionConsistencyKnown: true,
			},
		},
		{
			name: "string driver values and an unfamiliar spelling",
			script: []testsupport.ScriptedQuery{
				groupNameRows(testGroupUUID), sessionRows("SOMETHING_NEW"), globalRows("EVENTUAL"),
			},
			want: GroupConsistency{
				PluginStatus: "ACTIVE", SessionConsistency: "SOMETHING_NEW", SessionConsistencyKnown: true,
				GlobalConsistency: "EVENTUAL", GlobalConsistencyKnown: true,
			},
		},
	}
}

func TestGroupConsistencyStates(t *testing.T) {
	t.Parallel()

	for _, testCase := range consistencyStates() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.script...)
			defer db.Close()

			got, err := NewInspector(db).GroupConsistency(t.Context())
			if err != nil {
				t.Fatalf("GroupConsistency() returned error %v, want nil", err)
			}
			if got != testCase.want {
				t.Errorf("GroupConsistency() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestGroupConsistencyStatements pins the statements and their order on the
// Inspector path: three with the group name's row, four without, the plugin
// read last (T6).
func TestGroupConsistencyStatements(t *testing.T) {
	t.Parallel()

	withRow := []string{testSQLGroupName, testSQLSessionConsistency, testSQLGlobalConsistency}
	withoutRow := append(slices.Clone(withRow), testSQLPluginStatus)

	for _, testCase := range []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   []string
	}{
		{name: "member", script: consistencyMember("EVENTUAL", "EVENTUAL"), want: withRow},
		{name: "plugin not installed", script: consistencyAbsent(), want: withoutRow},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, testCase.script...)
			defer db.Close()

			if _, err := NewInspector(db).GroupConsistency(t.Context()); err != nil {
				t.Fatalf("GroupConsistency() returned error %v, want nil", err)
			}
			if !slices.Equal(log, testCase.want) {
				t.Errorf("statements = %q, want %q", log, testCase.want)
			}
		})
	}
}

// consistencyFailures are the failures both entry points must report alike:
// the Op, the Column, the wrapped cause and the variable each one names.
func consistencyFailures() []struct {
	name       string
	script     []testsupport.ScriptedQuery
	wantColumn string
	wantCause  error
	named      string
} {
	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")

	afterName := func(rules ...testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return append([]testsupport.ScriptedQuery{groupNameRows([]byte(testGroupUUID))}, rules...)
	}
	afterSession := func(rule testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return afterName(sessionRows([]byte("EVENTUAL")), rule)
	}
	lastRead := func(rule testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return []testsupport.ScriptedQuery{
			groupNameRows(), sessionRows([]byte("EVENTUAL")), globalRows([]byte("EVENTUAL")), rule,
		}
	}

	return []struct {
		name       string
		script     []testsupport.ScriptedQuery
		wantColumn string
		wantCause  error
		named      string
	}{
		{
			name:      "group-name statement fails",
			script:    []testsupport.ScriptedQuery{{Match: testMatchGroupName, Err: queryFailure}},
			wantCause: queryFailure, named: testGroupNameCause,
		},
		{
			name:       "two group-name rows",
			script:     []testsupport.ScriptedQuery{groupNameRows([]byte("a"), []byte("b"))},
			wantColumn: "VARIABLE_VALUE", wantCause: errMultipleRows, named: testGroupNameCause,
		},
		{
			name:      "session statement fails",
			script:    afterName(testsupport.ScriptedQuery{Match: testMatchSessionConsistency, Err: queryFailure}),
			wantCause: queryFailure, named: testSessionCause,
		},
		{
			name:       "NULL session value",
			script:     afterName(sessionRows(nil)),
			wantColumn: "VARIABLE_VALUE", wantCause: errUnexpectedNULL, named: testSessionCause,
		},
		{
			name:       "two session rows",
			script:     afterName(sessionRows([]byte("EVENTUAL"), []byte("AFTER"))),
			wantColumn: "VARIABLE_VALUE", wantCause: errMultipleRows, named: testSessionCause,
		},
		{
			name:       "undecodable session value",
			script:     afterName(sessionRows(int64(1))),
			wantColumn: "VARIABLE_VALUE", named: testSessionCause,
		},
		{
			name: "session result missing its VARIABLE_VALUE column",
			script: afterName(testsupport.ScriptedQuery{
				Match: testMatchSessionConsistency, Columns: []string{"VARIABLE_NAME"},
			}),
			wantColumn: "VARIABLE_VALUE", wantCause: errMissingColumn, named: testSessionCause,
		},
		{
			name: "session iteration fails",
			script: afterName(testsupport.ScriptedQuery{
				Match: testMatchSessionConsistency, Columns: []string{"VARIABLE_VALUE"},
				Rows: [][]driver.Value{{[]byte("EVENTUAL")}}, RowsErr: iterationFailure,
			}),
			wantCause: iterationFailure, named: testSessionCause,
		},
		{
			name: "session close fails",
			script: afterName(testsupport.ScriptedQuery{
				Match: testMatchSessionConsistency, Columns: []string{"VARIABLE_VALUE"},
				Rows:     [][]driver.Value{{[]byte("EVENTUAL")}},
				CloseErr: closeFailure, PendingResultSet: true,
			}),
			wantCause: closeFailure, named: testSessionCause,
		},
		{
			name:      "global statement fails",
			script:    afterSession(testsupport.ScriptedQuery{Match: testMatchGlobalConsistency, Err: queryFailure}),
			wantCause: queryFailure, named: testGlobalCause,
		},
		{
			name:       "NULL global value",
			script:     afterSession(globalRows(nil)),
			wantColumn: "VARIABLE_VALUE", wantCause: errUnexpectedNULL, named: testGlobalCause,
		},
		{
			name:       "two global rows",
			script:     afterSession(globalRows([]byte("EVENTUAL"), []byte("AFTER"))),
			wantColumn: "VARIABLE_VALUE", wantCause: errMultipleRows, named: testGlobalCause,
		},
		{
			name:      "plugin statement fails",
			script:    lastRead(testsupport.ScriptedQuery{Match: testMatchPlugins, Err: queryFailure}),
			wantCause: queryFailure,
		},
		{
			name: "NULL plugin status",
			script: lastRead(testsupport.ScriptedQuery{
				Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{nil}},
			}),
			wantColumn: "PLUGIN_STATUS", wantCause: errUnexpectedNULL,
		},
		{
			name:       "two plugin rows",
			script:     lastRead(pluginRows("DISABLED", "DISABLED")),
			wantColumn: "PLUGIN_STATUS", wantCause: errMultipleRows,
		},
	}
}

func assertConsistencyFailure(
	t *testing.T, got GroupConsistency, err error, wantColumn string, wantCause error, named string,
) {
	t.Helper()

	if err == nil {
		t.Fatalf("GroupConsistency = %#v, nil; want an error", got)
	}
	if got != (GroupConsistency{}) {
		t.Errorf("GroupConsistency returned %#v with its error, want the zero value", got)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("error %v is %T, want *OpError", err, err)
	}
	if opErr.Op != "group_consistency" {
		t.Errorf("Op = %q, want group_consistency", opErr.Op)
	}
	if opErr.Column != wantColumn {
		t.Errorf("Column = %q, want %q", opErr.Column, wantColumn)
	}
	if wantCause != nil && !errors.Is(err, wantCause) {
		t.Errorf("error %v does not wrap %v", err, wantCause)
	}
	cause := opErr.Err.Error()
	for _, prefix := range []string{testGroupNameCause, testSessionCause, testGlobalCause} {
		if starts := strings.HasPrefix(cause, prefix); starts != (prefix == named) {
			t.Errorf("cause %q starts with %q: %t, want %t", cause, prefix, starts, prefix == named)
		}
	}
}

func TestGroupConsistencyErrors(t *testing.T) {
	t.Parallel()

	for _, testCase := range consistencyFailures() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.script...)
			defer db.Close()

			got, err := NewInspector(db).GroupConsistency(t.Context())
			assertConsistencyFailure(t, got, err, testCase.wantColumn, testCase.wantCause, testCase.named)
		})
	}
}

func TestGroupConsistencyNilQuerier(t *testing.T) {
	t.Parallel()

	_, err := NewInspector(nil).GroupConsistency(t.Context())
	if !errors.Is(err, ErrNilQuerier) {
		t.Fatalf("GroupConsistency() on a nil Querier returned %v, want ErrNilQuerier", err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Op != "group_consistency" {
		t.Errorf("error %v, want *OpError with Op group_consistency", err)
	}
}

func TestGroupConsistencyJSON(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(GroupConsistency{
		PluginStatus: "ACTIVE", SessionConsistency: "AFTER", SessionConsistencyKnown: true,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"plugin_status":"ACTIVE","session_consistency":"AFTER","session_consistency_known":true,` +
		`"global_consistency":"","global_consistency_known":false}`
	if string(encoded) != want {
		t.Errorf("json = %s, want %s", encoded, want)
	}
}

func TestGroupConsistencyCanceledContext(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(consistencyAbsent()...)
	defer db.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := NewInspector(db).GroupConsistency(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GroupConsistency() on a canceled context = %#v, %v; want context.Canceled", got, err)
	}
}

// TestGroupConsistencyOnDriverConnMatchesInspector pins that the two entry
// points issue the same statements in the same order and report the same
// fact, in every state (T6 on the driver path).
func TestGroupConsistencyOnDriverConnMatchesInspector(t *testing.T) {
	t.Parallel()

	for _, testCase := range consistencyStates() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, testCase.script...)
			defer db.Close()
			viaInspector, err := NewInspector(db).GroupConsistency(t.Context())
			if err != nil {
				t.Fatalf("GroupConsistency() returned error %v, want nil", err)
			}

			conn := &testsupport.ScriptedDriverConn{Script: testCase.script}
			viaDriver, err := GroupConsistencyOnDriverConn(t.Context(), conn)
			if err != nil {
				t.Fatalf("GroupConsistencyOnDriverConn() returned error %v, want nil", err)
			}

			if viaDriver != testCase.want || viaInspector != testCase.want {
				t.Errorf("driver path = %#v, Inspector path = %#v, want %#v", viaDriver, viaInspector, testCase.want)
			}
			var issued []string
			for _, call := range conn.Calls() {
				issued = append(issued, call.Query)
			}
			if !slices.Equal(issued, log) {
				t.Errorf("driver statements = %q, Inspector statements = %q, want the same", issued, log)
			}
		})
	}
}

type ctxMarker struct{}

// TestGroupConsistencyOnDriverConnHandsOverContext is the unit half of
// contract (c): every QueryContext receives the caller's context itself, not
// a derived one, and nil arguments.
func TestGroupConsistencyOnDriverConnHandsOverContext(t *testing.T) {
	t.Parallel()

	for _, script := range [][]testsupport.ScriptedQuery{consistencyMember("AFTER", "EVENTUAL"), consistencyAbsent()} {
		ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ctxMarker{}, "caller"))
		conn := &testsupport.ScriptedDriverConn{Script: script}

		if _, err := GroupConsistencyOnDriverConn(ctx, conn); err != nil {
			cancel()
			t.Fatalf("GroupConsistencyOnDriverConn() returned error %v, want nil", err)
		}
		cancel()

		calls := conn.Calls()
		if len(calls) < 3 {
			t.Fatalf("%d statements issued, want at least 3", len(calls))
		}
		for index, call := range calls {
			if call.Ctx != ctx {
				t.Errorf("statement %d received a context other than the caller's", index+1)
			}
			if call.Ctx.Value(ctxMarker{}) != "caller" {
				t.Errorf("statement %d's context lost the caller's value", index+1)
			}
			if call.Args != nil {
				t.Errorf("statement %d received arguments %v, want nil", index+1, call.Args)
			}
		}
	}
}

// TestGroupConsistencyOnDriverConnLeavesConnAsFound is the unit half of
// contract (b): every result is closed before the next statement and before
// the function returns, on success and on a failure at each step, and none of
// the methods a reader must never call is called.
func TestGroupConsistencyOnDriverConnLeavesConnAsFound(t *testing.T) {
	t.Parallel()

	type outcome struct {
		name    string
		script  []testsupport.ScriptedQuery
		wantErr bool
	}
	outcomes := []outcome{
		{name: "success with the group name's row", script: consistencyMember("AFTER", "EVENTUAL")},
		{name: "success without it", script: consistencyAbsent()},
	}
	for _, failure := range consistencyFailures() {
		outcomes = append(outcomes, outcome{name: failure.name, script: failure.script, wantErr: true})
	}

	for _, testCase := range outcomes {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			conn := &testsupport.ScriptedDriverConn{Script: testCase.script}
			if _, err := GroupConsistencyOnDriverConn(t.Context(), conn); (err != nil) != testCase.wantErr {
				t.Fatalf("GroupConsistencyOnDriverConn() error = %v, want an error: %t", err, testCase.wantErr)
			}

			for index, call := range conn.Calls() {
				if call.OpenResults != 0 {
					t.Errorf("statement %d was issued with %d results still open", index+1, call.OpenResults)
				}
			}
			if open := conn.OpenResults(); open != 0 {
				t.Errorf("%d results still open after return", open)
			}
			if forbidden := conn.Forbidden(); len(forbidden) != 0 {
				t.Errorf("called %v, which a reader must never call", forbidden)
			}
		})
	}
}

// TestGroupConsistencyOnDriverConnErrors pins that a failure on the driver
// path is the same *OpError the Inspector path returns.
func TestGroupConsistencyOnDriverConnErrors(t *testing.T) {
	t.Parallel()

	for _, testCase := range consistencyFailures() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			conn := &testsupport.ScriptedDriverConn{Script: testCase.script}
			got, err := GroupConsistencyOnDriverConn(t.Context(), conn)
			assertConsistencyFailure(t, got, err, testCase.wantColumn, testCase.wantCause, testCase.named)
		})
	}
}

// TestGroupConsistencyOnDriverConnDecodesBeforeNextRow pins that a value is
// decoded before the driver may reuse its buffer: the scripted connection
// overwrites every []byte it delivered at the next Next and at Close.
func TestGroupConsistencyOnDriverConnDecodesBeforeNextRow(t *testing.T) {
	t.Parallel()

	conn := &testsupport.ScriptedDriverConn{Script: consistencyMember("AFTER", "BEFORE"), ReuseBuffers: true}
	got, err := GroupConsistencyOnDriverConn(t.Context(), conn)
	if err != nil {
		t.Fatalf("GroupConsistencyOnDriverConn() returned error %v, want nil", err)
	}
	want := GroupConsistency{
		PluginStatus: "ACTIVE", SessionConsistency: "AFTER", SessionConsistencyKnown: true,
		GlobalConsistency: "BEFORE", GlobalConsistencyKnown: true,
	}
	if got != want {
		t.Errorf("GroupConsistencyOnDriverConn() = %#v, want %#v", got, want)
	}
}

// TestGroupConsistencyOnDriverConnWrapsErrSkip pins that driver.ErrSkip never
// comes back bare: database/sql compares it with ==, and a bare one returned
// through a caller's own driver method would read as "fall back".
func TestGroupConsistencyOnDriverConnWrapsErrSkip(t *testing.T) {
	t.Parallel()

	conn := &testsupport.ScriptedDriverConn{Script: []testsupport.ScriptedQuery{
		{Match: testMatchGroupName, Err: driver.ErrSkip},
	}}
	_, err := GroupConsistencyOnDriverConn(t.Context(), conn)
	if err == driver.ErrSkip { //nolint:errorlint // the identity is what is pinned
		t.Fatal("GroupConsistencyOnDriverConn() returned driver.ErrSkip bare")
	}
	if !errors.Is(err, driver.ErrSkip) {
		t.Errorf("error %v does not wrap driver.ErrSkip", err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Op != "group_consistency" {
		t.Errorf("error %v, want *OpError with Op group_consistency", err)
	}
}

func TestGroupConsistencyOnDriverConnNilConn(t *testing.T) {
	t.Parallel()

	for name, conn := range map[string]driver.QueryerContext{
		"nil":       nil,
		"typed nil": (*testsupport.ScriptedDriverConn)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := GroupConsistencyOnDriverConn(t.Context(), conn)
			if !errors.Is(err, ErrNilConn) {
				t.Fatalf("GroupConsistencyOnDriverConn(%s) = %#v, %v; want ErrNilConn", name, got, err)
			}

			var opErr *OpError
			if !errors.As(err, &opErr) || opErr.Op != "group_consistency" {
				t.Errorf("error %v, want *OpError with Op group_consistency", err)
			}
		})
	}
}

func TestGroupConsistencyOnDriverConnCanceledContext(t *testing.T) {
	t.Parallel()

	conn := &testsupport.ScriptedDriverConn{Script: consistencyAbsent()}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := GroupConsistencyOnDriverConn(ctx, conn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GroupConsistencyOnDriverConn() on a canceled context returned %v, want context.Canceled", err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Op != "group_consistency" {
		t.Errorf("error %v, want *OpError with Op group_consistency", err)
	}
	if calls := conn.Calls(); len(calls) != 1 {
		t.Errorf("%d statements reached the connection, want 1: the first fails and ends the call", len(calls))
	}
}
