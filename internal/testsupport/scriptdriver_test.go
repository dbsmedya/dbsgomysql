package testsupport

import (
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestScriptedRowsRejectMisalignedRow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		row  []driver.Value
		want string
	}{
		{name: "short row", row: []driver.Value{"row2a"}, want: "row 1 has 1 values, want 2 columns"},
		{name: "long row", row: []driver.Value{"row2a", "row2b", "row2c"}, want: "row 1 has 3 values, want 2 columns"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := OpenScriptedDB(ScriptedQuery{
				Match:   "SELECT",
				Columns: []string{"a", "b"},
				Rows:    [][]driver.Value{{"row1a", "row1b"}, testCase.row},
			})
			defer db.Close()

			rows, err := db.QueryContext(t.Context(), "SELECT a, b FROM t")
			if err != nil {
				t.Fatalf("QueryContext: %v", err)
			}
			defer rows.Close()

			if !rows.Next() {
				t.Fatalf("first row missing: %v", rows.Err())
			}
			if rows.Next() {
				var a, b string
				scanErr := rows.Scan(&a, &b)
				if scanErr != nil {
					t.Fatalf("Scan %s: %v", testCase.name, scanErr)
				}
				t.Fatalf("%s was delivered as (%q, %q), want the query to fail at that row", testCase.name, a, b)
			}
			err = rows.Err()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("rows.Err() = %v, want a length-mismatch error containing %q", err, testCase.want)
			}
		})
	}
}

// TestScriptedDriverConnRecordsWhatTheReaderDid pins the three things the
// driver-connection tests rely on: the open-result count each statement saw,
// the refused methods, and the context and arguments of each call.
func TestScriptedDriverConnRecordsWhatTheReaderDid(t *testing.T) {
	t.Parallel()

	conn := &ScriptedDriverConn{Script: []ScriptedQuery{
		{Match: "one", Columns: []string{"v"}, Rows: [][]driver.Value{{[]byte("1")}}},
	}}

	first, err := conn.QueryContext(t.Context(), "SELECT one", nil)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	second, err := conn.QueryContext(t.Context(), "SELECT two", nil)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	if open := conn.OpenResults(); open != 2 {
		t.Errorf("OpenResults = %d with two results open, want 2", open)
	}
	_ = first.Close()
	_ = first.Close()
	_ = second.Close()
	if open := conn.OpenResults(); open != 0 {
		t.Errorf("OpenResults = %d after closing both, one of them twice, want 0", open)
	}

	calls := conn.Calls()
	if len(calls) != 2 || calls[0].OpenResults != 0 || calls[1].OpenResults != 1 {
		t.Errorf("Calls = %+v, want two calls that saw 0 and 1 open results", calls)
	}
	if calls[0].Ctx != t.Context() {
		t.Error("the recorded context is not the one passed")
	}

	if _, err := conn.Prepare("SELECT 1"); !errors.Is(err, errForbiddenDriverCall) {
		t.Errorf("Prepare = %v, want the refusal", err)
	}
	if err := conn.Close(); !errors.Is(err, errForbiddenDriverCall) {
		t.Errorf("Close = %v, want the refusal", err)
	}
	if got := conn.Forbidden(); !slices.Equal(got, []string{"Prepare", "Close"}) {
		t.Errorf("Forbidden = %q, want [Prepare Close]", got)
	}
}

// TestScriptedDriverConnReusesBuffers pins that with ReuseBuffers a kept
// []byte is overwritten by the next Next, as a driver reusing its buffer
// would overwrite it, so a reader that decodes late is caught.
func TestScriptedDriverConnReusesBuffers(t *testing.T) {
	t.Parallel()

	conn := &ScriptedDriverConn{ReuseBuffers: true, Script: []ScriptedQuery{
		{Match: "SELECT", Columns: []string{"v"}, Rows: [][]driver.Value{{[]byte("first")}, {[]byte("other")}}},
	}}
	rows, err := conn.QueryContext(t.Context(), "SELECT v", nil)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()

	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil {
		t.Fatalf("Next: %v", err)
	}
	kept, ok := dest[0].([]byte)
	if !ok || string(kept) != "first" {
		t.Fatalf("first row = %v, want first", dest[0])
	}
	decoded := string(kept)
	if err := rows.Next(dest); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if string(kept) == "first" {
		t.Error("the kept slice still reads first after the next row: the buffer was not reused")
	}
	if decoded != "first" {
		t.Errorf("the decoded copy = %q, want first", decoded)
	}
	if err := rows.Next(dest); !errors.Is(err, io.EOF) {
		t.Errorf("Next after the last row = %v, want io.EOF", err)
	}
}
