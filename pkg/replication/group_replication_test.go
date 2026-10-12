package replication

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// The two statements, spelled independently of the library's constants. A pin
// that reused them could not detect the library misspelling one.
const (
	testSQLPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication'"
	testSQLGroupName = "SHOW GLOBAL VARIABLES WHERE Variable_name = 'group_replication_group_name'"

	testMatchPlugins  = "information_schema.PLUGINS"
	testMatchShowVars = "SHOW GLOBAL VARIABLES"
	testGroupUUID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func pluginRows(statuses ...string) testsupport.ScriptedQuery {
	rows := make([][]driver.Value, 0, len(statuses))
	for _, status := range statuses {
		rows = append(rows, []driver.Value{[]byte(status)})
	}

	return testsupport.ScriptedQuery{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: rows}
}

func groupNameRows(values ...driver.Value) testsupport.ScriptedQuery {
	rows := make([][]driver.Value, 0, len(values))
	for _, value := range values {
		rows = append(rows, []driver.Value{[]byte("group_replication_group_name"), value})
	}

	return testsupport.ScriptedQuery{
		Match: testMatchShowVars, Columns: []string{"Variable_name", "Value"}, Rows: rows,
	}
}

// TestGroupReplicationStates pins the four server states the fact reports
// (docs/COMPAT.md entry 30): absence and DISABLED are states, never errors.
func TestGroupReplicationStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   GroupReplication
	}{
		{
			name:   "plugin not installed",
			script: []testsupport.ScriptedQuery{pluginRows(), groupNameRows()},
			want:   GroupReplication{},
		},
		{
			name:   "plugin disabled hides the variable",
			script: []testsupport.ScriptedQuery{pluginRows("DISABLED"), groupNameRows()},
			want:   GroupReplication{PluginStatus: "DISABLED"},
		},
		{
			name:   "plugin active, name unset",
			script: []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows([]byte(""))},
			want:   GroupReplication{PluginStatus: "ACTIVE"},
		},
		{
			name:   "configured member",
			script: []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows([]byte(testGroupUUID))},
			want:   GroupReplication{PluginStatus: "ACTIVE", GroupName: testGroupUUID},
		},
		{
			name:   "unfamiliar status is reported intact",
			script: []testsupport.ScriptedQuery{pluginRows("SOMETHING_NEW"), groupNameRows()},
			want:   GroupReplication{PluginStatus: "SOMETHING_NEW"},
		},
		{
			name: "string driver values",
			script: []testsupport.ScriptedQuery{
				{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{"ACTIVE"}}},
				groupNameRows(testGroupUUID),
			},
			want: GroupReplication{PluginStatus: "ACTIVE", GroupName: testGroupUUID},
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
// that there are exactly two: a consumer auditing a query log sees these and no
// others.
func TestGroupReplicationStatements(t *testing.T) {
	t.Parallel()

	var log []string
	db := testsupport.OpenScriptedDBWithLog(&log, pluginRows("ACTIVE"), groupNameRows([]byte(testGroupUUID)))
	defer db.Close()

	if _, err := NewInspector(db).GroupReplication(t.Context()); err != nil {
		t.Fatalf("GroupReplication() returned error %v, want nil", err)
	}

	want := []string{testSQLPluginStatus, testSQLGroupName}
	if !slices.Equal(log, want) {
		t.Errorf("statements = %q, want %q", log, want)
	}
}

// TestGroupReplicationClosesRowsBeforeSecondRead proves the first statement's
// rows are closed before the second statement is issued. With one connection
// and a result set the driver cannot finish on its own, an unclosed first
// result would hold the connection and the second statement would wait for the
// deadline.
func TestGroupReplicationClosesRowsBeforeSecondRead(t *testing.T) {
	t.Parallel()

	first := pluginRows("ACTIVE")
	first.PendingResultSet = true
	db := testsupport.OpenScriptedDB(first, groupNameRows([]byte(testGroupUUID)))
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if _, err := NewInspector(db).GroupReplication(ctx); err != nil {
		t.Fatalf("GroupReplication() on a single connection returned error %v, want nil", err)
	}
}

func TestGroupReplicationErrors(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")

	cases := []struct {
		name       string
		script     []testsupport.ScriptedQuery
		wantColumn string
		wantCause  error
	}{
		{
			name: "plugin statement fails",
			script: []testsupport.ScriptedQuery{
				{Match: testMatchPlugins, Err: queryFailure}, groupNameRows(),
			},
			wantCause: queryFailure,
		},
		{
			name: "variable statement fails after a successful first read",
			script: []testsupport.ScriptedQuery{
				pluginRows("ACTIVE"), {Match: testMatchShowVars, Err: queryFailure},
			},
			wantCause: queryFailure,
		},
		{
			name:       "two plugin rows",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE", "ACTIVE"), groupNameRows()},
			wantColumn: "PLUGIN_STATUS",
			wantCause:  errMultipleRows,
		},
		{
			name:       "two variable rows",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows([]byte("a"), []byte("b"))},
			wantColumn: "Value",
			wantCause:  errMultipleRows,
		},
		{
			name: "NULL plugin status",
			script: []testsupport.ScriptedQuery{
				{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{nil}}},
				groupNameRows(),
			},
			wantColumn: "PLUGIN_STATUS",
			wantCause:  errUnexpectedNULL,
		},
		{
			name:       "NULL group name",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows(nil)},
			wantColumn: "Value",
			wantCause:  errUnexpectedNULL,
		},
		{
			name:       "undecodable group name",
			script:     []testsupport.ScriptedQuery{pluginRows("ACTIVE"), groupNameRows(int64(7))},
			wantColumn: "Value",
		},
		{
			name: "variable result missing its Value column",
			script: []testsupport.ScriptedQuery{
				pluginRows("ACTIVE"),
				{Match: testMatchShowVars, Columns: []string{"Variable_name"}},
			},
			wantColumn: "Value",
			wantCause:  errMissingColumn,
		},
		{
			name: "iteration fails",
			script: []testsupport.ScriptedQuery{
				{
					Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"},
					Rows: [][]driver.Value{{[]byte("ACTIVE")}}, RowsErr: iterationFailure,
				},
				groupNameRows(),
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
				groupNameRows(),
			},
			wantCause: closeFailure,
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

func TestGroupReplicationJSON(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(GroupReplication{PluginStatus: "ACTIVE", GroupName: testGroupUUID})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	want := `{"plugin_status":"ACTIVE","group_name":"` + testGroupUUID + `"}`
	if string(encoded) != want {
		t.Errorf("json = %s, want %s", encoded, want)
	}
}

// TestGroupReplicationCanceledContext proves a canceled context is an error
// naming the operation, never an empty fact that reads as "plugin absent".
func TestGroupReplicationCanceledContext(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(pluginRows(), groupNameRows())
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
