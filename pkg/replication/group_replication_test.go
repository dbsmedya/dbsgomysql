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

// The four statements, spelled independently of the library's constants. A pin
// that reused them could not detect the library misspelling one.
const (
	testSQLPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication' LIMIT 18446744073709551615"
	testSQLGroupName = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name' LIMIT 18446744073709551615"
	testSQLSizeLimit = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_transaction_size_limit' LIMIT 18446744073709551615"
	testSQLSinglePrimaryMode = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_single_primary_mode' LIMIT 18446744073709551615"

	// The variable reads all name performance_schema.global_variables, and
	// the scripted driver answers a statement from the first rule whose Match
	// it contains, so each variable rule matches its variable's quoted name.
	testMatchPlugins   = "information_schema.PLUGINS"
	testMatchGroupName = "'group_replication_group_name'"
	testMatchSizeLimit = "'group_replication_transaction_size_limit'"
	testMatchMode      = "'group_replication_single_primary_mode'"
	testGroupUUID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testDefaultLimit   = "150000000"

	// The mode's two documented values, as performance_schema spells them.
	testModeOn  = "ON"
	testModeOff = "OFF"

	// testSizeLimitCause and testModeCause open the cause of every failure of
	// the limit read and of the mode read.
	testSizeLimitCause = "variable group_replication_transaction_size_limit: "
	testModeCause      = "variable group_replication_single_primary_mode: "
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

func modeRows(values ...driver.Value) testsupport.ScriptedQuery {
	return variableRows(testMatchMode, values...)
}

// memberScript is a configured single-primary member whose limit read returns
// limit as its one row. It scripts no plugin row: with the group name's row
// returned the fact must not read information_schema.PLUGINS, and if it did
// it would read no row and report "" instead of ACTIVE.
func memberScript(limit driver.Value) []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{
		groupNameRows([]byte(testGroupUUID)), sizeLimitRows(limit), modeRows([]byte(testModeOn)),
	}
}

// absentScript is a server without the plugin: no row for any variable, and
// no plugin row.
func absentScript() []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{groupNameRows(), sizeLimitRows(), modeRows(), pluginRows()}
}

// TestGroupReplicationStates pins the server states the fact reports
// (docs/COMPAT.md entry 30): absence and DISABLED are states, never errors;
// PluginStatus is ACTIVE exactly when the group name's row is returned and
// otherwise the plugin table's status; the limit and the mode are known
// exactly when the server returned their rows.
func TestGroupReplicationStates(t *testing.T) {
	t.Parallel()

	member := func(limit uint64) GroupReplication {
		return GroupReplication{
			PluginStatus: "ACTIVE", GroupName: testGroupUUID,
			TransactionSizeLimit: limit, TransactionSizeLimitKnown: true,
			SinglePrimaryMode: testModeOn, SinglePrimaryModeKnown: true,
		}
	}

	cases := []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   GroupReplication
	}{
		{
			name:   "plugin not installed",
			script: absentScript(),
			want:   GroupReplication{},
		},
		{
			name:   "plugin disabled hides every variable",
			script: []testsupport.ScriptedQuery{groupNameRows(), sizeLimitRows(), modeRows(), pluginRows("DISABLED")},
			want:   GroupReplication{PluginStatus: "DISABLED"},
		},
		{
			name: "plugin active, name unset",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte("")), sizeLimitRows([]byte(testDefaultLimit)), modeRows([]byte(testModeOn)),
			},
			want: GroupReplication{
				PluginStatus: "ACTIVE", TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true,
				SinglePrimaryMode: testModeOn, SinglePrimaryModeKnown: true,
			},
		},
		{
			name:   "configured member",
			script: memberScript([]byte(testDefaultLimit)),
			want:   member(150000000),
		},
		{
			// Option D: the group name's row is what reports ACTIVE. A
			// plugin row saying otherwise is never read.
			name: "the group name's row reports ACTIVE without reading the plugin table",
			script: append(memberScript([]byte(testDefaultLimit)),
				pluginRows("DISABLED")),
			want: member(150000000),
		},
		{
			name: "multi-primary member",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte(testGroupUUID)), sizeLimitRows([]byte(testDefaultLimit)), modeRows([]byte(testModeOff)),
			},
			want: GroupReplication{
				PluginStatus: "ACTIVE", GroupName: testGroupUUID,
				TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true,
				SinglePrimaryMode: testModeOff, SinglePrimaryModeKnown: true,
			},
		},
		{
			name: "an unfamiliar mode spelling is reported as sent",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte(testGroupUUID)), sizeLimitRows([]byte(testDefaultLimit)), modeRows([]byte("1")),
			},
			want: GroupReplication{
				PluginStatus: "ACTIVE", GroupName: testGroupUUID,
				TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true,
				SinglePrimaryMode: "1", SinglePrimaryModeKnown: true,
			},
		},
		{
			// The pair the two limit fields exist for: the text "0" is a
			// known limit of none, and zero rows (the first two cases) is an
			// unknown limit. Both carry the number 0.
			name:   "a zero limit is known, not absent",
			script: memberScript([]byte("0")),
			want:   member(0),
		},
		{
			name:   "unfamiliar status is reported intact",
			script: []testsupport.ScriptedQuery{groupNameRows(), sizeLimitRows(), modeRows(), pluginRows("SOMETHING_NEW")},
			want:   GroupReplication{PluginStatus: "SOMETHING_NEW"},
		},
		{
			name: "string driver values",
			script: []testsupport.ScriptedQuery{
				groupNameRows(testGroupUUID), sizeLimitRows("2147483647"), modeRows(testModeOn),
			},
			want: member(2147483647),
		},
		{
			name: "string plugin status without a group-name row",
			script: []testsupport.ScriptedQuery{
				groupNameRows(), sizeLimitRows(), modeRows(),
				{Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{"DISABLED"}}},
			},
			want: GroupReplication{PluginStatus: "DISABLED"},
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
			// The reads are not one snapshot, and the fact never reconciles
			// them: no group-name row sends it to the plugin table, which
			// has no row either.
			name: "a limit row without a group-name or plugin row is reported as read",
			script: []testsupport.ScriptedQuery{
				groupNameRows(), sizeLimitRows([]byte(testDefaultLimit)), modeRows(), pluginRows(),
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

// TestGroupReplicationStatements pins the exact statements and their order:
// three when the group-name read returns a row, an empty value included, and
// four, information_schema.PLUGINS last, when it returns none (T6). A
// consumer auditing a query log sees these and no others.
func TestGroupReplicationStatements(t *testing.T) {
	t.Parallel()

	withRow := []string{testSQLGroupName, testSQLSizeLimit, testSQLSinglePrimaryMode}
	withoutRow := []string{testSQLGroupName, testSQLSizeLimit, testSQLSinglePrimaryMode, testSQLPluginStatus}

	cases := []struct {
		name   string
		script []testsupport.ScriptedQuery
		want   []string
	}{
		{name: "configured member", script: memberScript([]byte(testDefaultLimit)), want: withRow},
		{
			name: "plugin active, name unset",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte("")), sizeLimitRows([]byte(testDefaultLimit)), modeRows([]byte(testModeOn)),
			},
			want: withRow,
		},
		{name: "plugin not installed", script: absentScript(), want: withoutRow},
		{
			name:   "plugin disabled",
			script: []testsupport.ScriptedQuery{groupNameRows(), sizeLimitRows(), modeRows(), pluginRows("DISABLED")},
			want:   withoutRow,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, testCase.script...)
			defer db.Close()

			if _, err := NewInspector(db).GroupReplication(t.Context()); err != nil {
				t.Fatalf("GroupReplication() returned error %v, want nil", err)
			}
			if !slices.Equal(log, testCase.want) {
				t.Errorf("statements = %q, want %q", log, testCase.want)
			}
		})
	}
}

// TestGroupReplicationClosesRowsBeforeNextRead proves each statement's rows
// are closed before the next statement is issued and before the fact returns.
// With one connection and a result set the driver cannot finish on its own,
// an unclosed result would hold the connection, and the next statement, or
// the second call, would wait for the deadline.
func TestGroupReplicationClosesRowsBeforeNextRead(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		name   string
		script func() []testsupport.ScriptedQuery
		index  int
	}{
		{"group-name read", func() []testsupport.ScriptedQuery { return memberScript([]byte(testDefaultLimit)) }, 0},
		{"limit read", func() []testsupport.ScriptedQuery { return memberScript([]byte(testDefaultLimit)) }, 1},
		{"mode read", func() []testsupport.ScriptedQuery { return memberScript([]byte(testDefaultLimit)) }, 2},
		{"plugin read", func() []testsupport.ScriptedQuery {
			return []testsupport.ScriptedQuery{groupNameRows(), sizeLimitRows(), modeRows(), pluginRows("DISABLED")}
		}, 3},
	} {
		t.Run(held.name, func(t *testing.T) {
			t.Parallel()

			script := held.script()
			script[held.index].PendingResultSet = true
			db := testsupport.OpenScriptedDB(script...)
			defer db.Close()
			db.SetMaxOpenConns(1)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			for call := 1; call <= 2; call++ {
				got, err := NewInspector(db).GroupReplication(ctx)
				if err != nil {
					t.Fatalf("GroupReplication() call %d on a single connection returned error %v, want nil", call, err)
				}
				if got.PluginStatus == "" {
					t.Errorf("GroupReplication() call %d = %#v, want a status", call, got)
				}
			}
		})
	}
}

func TestGroupReplicationErrors(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")

	// afterName is the limit read's rule, behind a group-name row.
	afterName := func(rule testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return []testsupport.ScriptedQuery{groupNameRows([]byte(testGroupUUID)), rule}
	}
	limitText := func(text string) []testsupport.ScriptedQuery {
		return afterName(sizeLimitRows([]byte(text)))
	}
	// afterLimit is the mode read's rule, behind a group-name and a limit row.
	afterLimit := func(rule testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return []testsupport.ScriptedQuery{groupNameRows([]byte(testGroupUUID)), sizeLimitRows([]byte("1")), rule}
	}
	// lastRead is the plugin read's rule, behind three reads without a
	// group-name row.
	lastRead := func(rule testsupport.ScriptedQuery) []testsupport.ScriptedQuery {
		return []testsupport.ScriptedQuery{groupNameRows(), sizeLimitRows(), modeRows(), rule}
	}

	cases := []struct {
		name       string
		script     []testsupport.ScriptedQuery
		wantColumn string
		wantCause  error
		// named is the prefix the cause must start with: the limit read's
		// and the mode read's failures name their variable, no other's does.
		named string
	}{
		{
			name:      "group-name statement fails",
			script:    []testsupport.ScriptedQuery{{Match: testMatchGroupName, Err: queryFailure}},
			wantCause: queryFailure,
		},
		{
			name:       "two group-name rows",
			script:     []testsupport.ScriptedQuery{groupNameRows([]byte("a"), []byte("b"))},
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMultipleRows,
		},
		{
			name:       "NULL group name",
			script:     []testsupport.ScriptedQuery{groupNameRows(nil)},
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnexpectedNULL,
		},
		{
			name:       "undecodable group name",
			script:     []testsupport.ScriptedQuery{groupNameRows(int64(7))},
			wantColumn: "VARIABLE_VALUE",
		},
		{
			name:       "group-name result missing its VARIABLE_VALUE column",
			script:     []testsupport.ScriptedQuery{{Match: testMatchGroupName, Columns: []string{"VARIABLE_NAME"}}},
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMissingColumn,
		},
		{
			name: "group-name iteration fails",
			script: []testsupport.ScriptedQuery{{
				Match: testMatchGroupName, Columns: []string{"VARIABLE_VALUE"},
				Rows: [][]driver.Value{{[]byte(testGroupUUID)}}, RowsErr: iterationFailure,
			}},
			wantCause: iterationFailure,
		},
		{
			name: "group-name close fails",
			script: []testsupport.ScriptedQuery{{
				Match: testMatchGroupName, Columns: []string{"VARIABLE_VALUE"},
				Rows:     [][]driver.Value{{[]byte(testGroupUUID)}},
				CloseErr: closeFailure, PendingResultSet: true,
			}},
			wantCause: closeFailure,
		},
		{
			name:      "limit statement fails after a successful first read",
			script:    afterName(testsupport.ScriptedQuery{Match: testMatchSizeLimit, Err: queryFailure}),
			wantCause: queryFailure,
			named:     testSizeLimitCause,
		},
		{
			name:       "two limit rows",
			script:     afterName(sizeLimitRows([]byte("1"), []byte("2"))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMultipleRows,
			named:      testSizeLimitCause,
		},
		{
			name:       "NULL limit",
			script:     afterName(sizeLimitRows(nil)),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnexpectedNULL,
			named:      testSizeLimitCause,
		},
		{
			name:       "empty limit text",
			script:     limitText(""),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			named:      testSizeLimitCause,
		},
		{
			name:       "non-numeric limit text",
			script:     limitText("unlimited"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			named:      testSizeLimitCause,
		},
		{
			name:       "negative limit text",
			script:     limitText("-1"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			named:      testSizeLimitCause,
		},
		{
			name:       "fractional limit text",
			script:     limitText("1.5"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrSyntax,
			named:      testSizeLimitCause,
		},
		{
			name:       "limit text beyond uint64",
			script:     limitText("18446744073709551616"),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  strconv.ErrRange,
			named:      testSizeLimitCause,
		},
		{
			name:       "negative integer limit",
			script:     afterName(sizeLimitRows(int64(-1))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errValueOutOfRange,
			named:      testSizeLimitCause,
		},
		{
			name:       "fractional float limit",
			script:     afterName(sizeLimitRows(float64(1.5))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnrecognizedValue,
			named:      testSizeLimitCause,
		},
		{
			name:       "float limit beyond uint64",
			script:     afterName(sizeLimitRows(float64(1 << 64))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errValueOutOfRange,
			named:      testSizeLimitCause,
		},
		{
			name:       "undecodable limit driver type",
			script:     afterName(sizeLimitRows(true)),
			wantColumn: "VARIABLE_VALUE",
			named:      testSizeLimitCause,
		},
		{
			name:       "limit result missing its VARIABLE_VALUE column",
			script:     afterName(testsupport.ScriptedQuery{Match: testMatchSizeLimit, Columns: []string{"VARIABLE_NAME"}}),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMissingColumn,
			named:      testSizeLimitCause,
		},
		{
			name: "limit iteration fails",
			script: afterName(testsupport.ScriptedQuery{
				Match: testMatchSizeLimit, Columns: []string{"VARIABLE_VALUE"},
				Rows: [][]driver.Value{{[]byte("1")}}, RowsErr: iterationFailure,
			}),
			wantCause: iterationFailure,
			named:     testSizeLimitCause,
		},
		{
			name: "limit close fails",
			script: afterName(testsupport.ScriptedQuery{
				Match: testMatchSizeLimit, Columns: []string{"VARIABLE_VALUE"},
				Rows:     [][]driver.Value{{[]byte("1")}},
				CloseErr: closeFailure, PendingResultSet: true,
			}),
			wantCause: closeFailure,
			named:     testSizeLimitCause,
		},
		{
			name:      "mode statement fails after two successful reads",
			script:    afterLimit(testsupport.ScriptedQuery{Match: testMatchMode, Err: queryFailure}),
			wantCause: queryFailure,
			named:     testModeCause,
		},
		{
			name:       "two mode rows",
			script:     afterLimit(modeRows([]byte(testModeOn), []byte(testModeOff))),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMultipleRows,
			named:      testModeCause,
		},
		{
			name:       "NULL mode",
			script:     afterLimit(modeRows(nil)),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errUnexpectedNULL,
			named:      testModeCause,
		},
		{
			name:       "undecodable mode driver type",
			script:     afterLimit(modeRows(int64(1))),
			wantColumn: "VARIABLE_VALUE",
			named:      testModeCause,
		},
		{
			name:       "mode result missing its VARIABLE_VALUE column",
			script:     afterLimit(testsupport.ScriptedQuery{Match: testMatchMode, Columns: []string{"VARIABLE_NAME"}}),
			wantColumn: "VARIABLE_VALUE",
			wantCause:  errMissingColumn,
			named:      testModeCause,
		},
		{
			name: "mode iteration fails",
			script: afterLimit(testsupport.ScriptedQuery{
				Match: testMatchMode, Columns: []string{"VARIABLE_VALUE"},
				Rows: [][]driver.Value{{[]byte(testModeOn)}}, RowsErr: iterationFailure,
			}),
			wantCause: iterationFailure,
			named:     testModeCause,
		},
		{
			name: "mode close fails",
			script: afterLimit(testsupport.ScriptedQuery{
				Match: testMatchMode, Columns: []string{"VARIABLE_VALUE"},
				Rows:     [][]driver.Value{{[]byte(testModeOn)}},
				CloseErr: closeFailure, PendingResultSet: true,
			}),
			wantCause: closeFailure,
			named:     testModeCause,
		},
		{
			name:      "plugin statement fails after three reads",
			script:    lastRead(testsupport.ScriptedQuery{Match: testMatchPlugins, Err: queryFailure}),
			wantCause: queryFailure,
		},
		{
			name:       "two plugin rows",
			script:     lastRead(pluginRows("ACTIVE", "ACTIVE")),
			wantColumn: "PLUGIN_STATUS",
			wantCause:  errMultipleRows,
		},
		{
			name: "NULL plugin status",
			script: lastRead(testsupport.ScriptedQuery{
				Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"}, Rows: [][]driver.Value{{nil}},
			}),
			wantColumn: "PLUGIN_STATUS",
			wantCause:  errUnexpectedNULL,
		},
		{
			name: "plugin iteration fails",
			script: lastRead(testsupport.ScriptedQuery{
				Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"},
				Rows: [][]driver.Value{{[]byte("DISABLED")}}, RowsErr: iterationFailure,
			}),
			wantCause: iterationFailure,
		},
		{
			name: "plugin close fails",
			script: lastRead(testsupport.ScriptedQuery{
				Match: testMatchPlugins, Columns: []string{"PLUGIN_STATUS"},
				Rows:     [][]driver.Value{{[]byte("DISABLED")}},
				CloseErr: closeFailure, PendingResultSet: true,
			}),
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
			cause := opErr.Err.Error()
			for _, prefix := range []string{testSizeLimitCause, testModeCause} {
				if named := strings.HasPrefix(cause, prefix); named != (prefix == testCase.named) {
					t.Errorf("cause %q starts with %q: %t, want %t", cause, prefix, named, prefix == testCase.named)
				}
			}
		})
	}
}

// TestGroupReplicationErrorText pins the rendered text of a failed limit read
// and of a failed mode read, which name their variable, next to the
// group-name read's, which is unchanged.
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
			name: "NULL mode",
			script: []testsupport.ScriptedQuery{
				groupNameRows([]byte(testGroupUUID)), sizeLimitRows([]byte(testDefaultLimit)), modeRows(nil),
			},
			want: "replication: group_replication column VARIABLE_VALUE: " +
				"variable group_replication_single_primary_mode: unexpected NULL",
		},
		{
			name:   "NULL group name",
			script: []testsupport.ScriptedQuery{groupNameRows(nil)},
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

// TestGroupReplicationJSON pins the encoding, including both limit keys and
// both mode keys: a known limit of zero and an unknown limit encode the same
// number and differ only in transaction_size_limit_known.
func TestGroupReplicationJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fact GroupReplication
		want string
	}{
		{
			name: "member with a known limit of zero",
			fact: GroupReplication{
				PluginStatus: "ACTIVE", GroupName: testGroupUUID, TransactionSizeLimitKnown: true,
				SinglePrimaryMode: testModeOn, SinglePrimaryModeKnown: true,
			},
			want: `{"plugin_status":"ACTIVE","group_name":"` + testGroupUUID +
				`","transaction_size_limit":0,"transaction_size_limit_known":true,` +
				`"single_primary_mode":"ON","single_primary_mode_known":true}`,
		},
		{
			name: "member with the default limit",
			fact: GroupReplication{
				PluginStatus: "ACTIVE", GroupName: testGroupUUID,
				TransactionSizeLimit: 150000000, TransactionSizeLimitKnown: true,
				SinglePrimaryMode: testModeOff, SinglePrimaryModeKnown: true,
			},
			want: `{"plugin_status":"ACTIVE","group_name":"` + testGroupUUID +
				`","transaction_size_limit":150000000,"transaction_size_limit_known":true,` +
				`"single_primary_mode":"OFF","single_primary_mode_known":true}`,
		},
		{
			name: "limit and mode unknown",
			fact: GroupReplication{PluginStatus: "DISABLED"},
			want: `{"plugin_status":"DISABLED","group_name":"",` +
				`"transaction_size_limit":0,"transaction_size_limit_known":false,` +
				`"single_primary_mode":"","single_primary_mode_known":false}`,
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

	db := testsupport.OpenScriptedDB(absentScript()...)
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
// after one read and before the next fails the whole fact: every read must
// succeed, never a half-filled value. Canceled before the limit or the mode
// read, the error names that variable; before the plugin read, none.
func TestGroupReplicationCanceledBetweenStatements(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		at     int
		script []testsupport.ScriptedQuery
		named  string
	}{
		{at: 2, script: memberScript([]byte(testDefaultLimit)), named: testSizeLimitCause},
		{at: 3, script: memberScript([]byte(testDefaultLimit)), named: testModeCause},
		{at: 4, script: absentScript()},
	} {
		t.Run("before statement "+strconv.Itoa(testCase.at), func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.script...)
			defer db.Close()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			querier := &cancelBeforeQuery{db: db, cancel: cancel, at: testCase.at}

			got, err := NewInspector(querier).GroupReplication(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("GroupReplication() canceled before statement %d = %#v, %v; want context.Canceled",
					testCase.at, got, err)
			}
			if got != (GroupReplication{}) {
				t.Errorf("GroupReplication() = %#v alongside its error, want the zero value", got)
			}
			if querier.queries != testCase.at {
				t.Errorf("issued %d statements, want %d: the cancellation must reach statement %d",
					querier.queries, testCase.at, testCase.at)
			}

			var opErr *OpError
			if !errors.As(err, &opErr) || opErr.Op != "group_replication" {
				t.Fatalf("error %v, want *OpError with Op group_replication", err)
			}
			cause := opErr.Err.Error()
			for _, prefix := range []string{testSizeLimitCause, testModeCause} {
				if named := strings.HasPrefix(cause, prefix); named != (prefix == testCase.named) {
					t.Errorf("cause %q starts with %q: %t, want %t", cause, prefix, named, prefix == testCase.named)
				}
			}
		})
	}
}
