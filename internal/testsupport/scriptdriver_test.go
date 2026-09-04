package testsupport

import (
	"database/sql/driver"
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
