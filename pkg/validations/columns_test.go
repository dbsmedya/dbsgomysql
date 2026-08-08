package validations

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// columnsScript answers the narrowed and unnarrowed forms of the Columns query
// with different column names, so a result says which statement was issued.
//
// The scripted driver matches on a substring of the statement and takes the
// first rule that matches, so these must run most-specific first. Note the
// fallback rule cannot be keyed on a fixture name: "narrow_alpha" would also
// have to avoid being a substring of the narrowed statement, and a bare "a" is
// a substring of "information_schema". Key it on the table instead, which only
// the statement that dropped its IN clause reaches without matching an earlier
// rule.
func columnsScript(narrowedPlaceholders string) []testsupport.ScriptedQuery {
	row := func(table, column string) []driver.Value {
		return []driver.Value{table, column, int64(1), "int", "int", ""}
	}
	columns := []string{
		"TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE", "COLUMN_TYPE", "EXTRA",
	}

	script := make([]testsupport.ScriptedQuery, 0, 3)
	if narrowedPlaceholders != "" {
		script = append(script, testsupport.ScriptedQuery{
			Match:   "TABLE_NAME IN (" + narrowedPlaceholders + ")",
			Columns: columns,
			Rows: [][]driver.Value{
				row("narrow_alpha", "exact_count_col"),
				row("narrow_beta", "exact_count_col"),
				row("t0", "exact_count_col"),
			},
		})
	}

	return append(script,
		testsupport.ScriptedQuery{
			Match:   "TABLE_NAME IN (",
			Columns: columns,
			Rows: [][]driver.Value{
				row("narrow_alpha", "narrowed_col"),
				row("narrow_beta", "narrowed_col"),
				row("t0", "narrowed_col"),
			},
		},
		testsupport.ScriptedQuery{
			Match:   "information_schema.COLUMNS",
			Columns: columns,
			Rows: [][]driver.Value{
				row("narrow_alpha", "fallback_col"),
				row("narrow_beta", "fallback_col"),
				row("t0", "fallback_col"),
			},
		},
	)
}

func columnsInspector(t *testing.T, script ...testsupport.ScriptedQuery) *Inspector {
	t.Helper()

	db := testsupport.OpenScriptedDB(script...)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	return NewInspector(db, "shop")
}

// firstColumnName reports which scripted rule answered.
func firstColumnName(t *testing.T, got []TableColumns) string {
	t.Helper()

	if len(got) == 0 || len(got[0].Columns) == 0 {
		t.Fatalf("no columns returned: %#v", got)
	}

	return got[0].Columns[0].Name
}

func TestColumnsDeduplicatesPredicateAndKeepsRequestedMultiplicity(t *testing.T) {
	t.Parallel()

	// Two distinct names in three requested positions must emit exactly two
	// placeholders — IN (?,?) is not a substring of IN (?,?,?), so this rule
	// fires only on the deduplicated statement — while the result still
	// repeats narrow_alpha at both positions it was asked for.
	got, err := columnsInspector(t, columnsScript("?,?")...).Columns(
		t.Context(),
		[]string{"narrow_alpha", "narrow_beta", "narrow_alpha"},
	)
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if name := firstColumnName(t, got); name != "exact_count_col" {
		t.Errorf("answered by %q, want the two-placeholder statement", name)
	}

	tables := make([]string, 0, len(got))
	for _, table := range got {
		tables = append(tables, table.Table)
	}
	want := []string{"narrow_alpha", "narrow_beta", "narrow_alpha"}
	if !reflect.DeepEqual(tables, want) {
		t.Errorf("returned tables = %#v, want %#v", tables, want)
	}
}

func TestColumnsFallsBackWhenAnyRequestedNameIsUnrepresentable(t *testing.T) {
	t.Parallel()

	// One unrepresentable name makes the whole call unnarrowed; the ordinary
	// names in the same request still come back. Measured 2026-08-08: without
	// this, MySQL raises error 3988 rather than reporting absence.
	for _, testCase := range []struct{ name, bad string }{
		{"supplementary", "supp_\U00010000"},
		{"invalid utf-8", "bad_\xff"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := columnsInspector(t, columnsScript("")...).Columns(
				t.Context(),
				[]string{"narrow_alpha", testCase.bad},
			)
			if err != nil {
				t.Fatalf("Columns: %v", err)
			}
			if name := firstColumnName(t, got); name != "fallback_col" {
				t.Errorf("answered by %q, want the unnarrowed statement", name)
			}
			if len(got) != 1 || got[0].Table != "narrow_alpha" {
				t.Errorf("Columns = %#v, want narrow_alpha alone", got)
			}
		})
	}
}

func TestColumnsBoundAccountsForItsSchemaParameter(t *testing.T) {
	t.Parallel()

	// Columns binds TABLE_SCHEMA = ? as well, so its budget is one below the
	// ceiling. A site declaring the wrong fixed-argument count flips narrowing
	// and fallback at exactly this boundary, and nothing in the results shows
	// it — everywhere except here the two agree.
	for _, testCase := range []struct {
		name   string
		count  int
		answer string
	}{
		{"at the bound", maxStatementParameters - 1, "narrowed_col"},
		{"above the bound", maxStatementParameters, "fallback_col"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := columnsInspector(t, columnsScript("")...).Columns(
				t.Context(),
				distinctNames(testCase.count),
			)
			if err != nil {
				t.Fatalf("Columns: %v", err)
			}
			if name := firstColumnName(t, got); name != testCase.answer {
				t.Errorf("%d names answered by %q, want %q",
					testCase.count, name, testCase.answer)
			}
		})
	}
}

func TestColumnsReturnsEveryColumnInRequestedObjectOrder(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match: "information_schema.COLUMNS",
		Columns: []string{
			"TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE", "COLUMN_TYPE", "EXTRA",
		},
		Rows: [][]driver.Value{
			{"Alpha", "wrong_case_table", int64(1), "int", "int", ""},
			{"alpha", "id", int64(1), "bigint", "bigint unsigned", "auto_increment"},
			{"alpha", "hidden", int64(2), "varchar", "varchar(20)", "INVISIBLE"},
			{"alpha", "virtual_value", int64(3), "int", "int", "VIRTUAL GENERATED"},
			{
				"alpha", "stored_hidden", int64(4), "int", "int(10) unsigned zerofill",
				"STORED GENERATED INVISIBLE",
			},
			{
				"alpha", "expression_default", int64(5), "datetime", "datetime",
				"DEFAULT_GENERATED",
			},
			{"beta", "code", int64(1), "char", "char(4)", ""},
		},
	})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	got, err := NewInspector(db, "shop").Columns(
		t.Context(),
		[]string{"beta", "missing", "alpha", "alpha"},
	)
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}

	alphaColumns := []ColumnInfo{
		{Name: "id", Ordinal: 1, DataType: "bigint", Unsigned: true},
		{Name: "hidden", Ordinal: 2, DataType: "varchar", Invisible: true},
		{Name: "virtual_value", Ordinal: 3, DataType: "int", Generated: true},
		{
			Name: "stored_hidden", Ordinal: 4, DataType: "int",
			Unsigned: true, Invisible: true, Generated: true,
		},
		{Name: "expression_default", Ordinal: 5, DataType: "datetime"},
	}
	want := []TableColumns{
		{Table: "beta", Columns: []ColumnInfo{{Name: "code", Ordinal: 1, DataType: "char"}}},
		{Table: "alpha", Columns: alphaColumns},
		{Table: "alpha", Columns: alphaColumns},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Columns() =\n%#v\nwant\n%#v", got, want)
	}
}
