package validations

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

const innoDBSource = "INNODB_FOREIGN AS f"

func innoDBColumns() []string {
	return []string{
		"ID", "FOR_NAME", "REF_NAME", "N_COLS", "TYPE",
		"FOR_COL_NAME", "REF_COL_NAME", "POS",
	}
}

// innoDBRow is one complete single-column constraint. TYPE 9 is the flag
// combination the existing source tests use for CASCADE / SET NULL.
func innoDBRow(constraint, child, parent string) []driver.Value {
	return []driver.Value{
		constraint, child, parent, int64(1), int64(9), "pid", "id", int64(1),
	}
}

func TestForeignKeysInnoDBDeduplicatesItsComposedPredicate(t *testing.T) {
	t.Parallel()

	// Two distinct tables in three requested positions must bind two
	// parameters. IN (?,?) is not a substring of IN (?,?,?), so this match
	// fires only on the deduplicated statement.
	script := &queryScript{steps: []queryStep{{
		contains: "IN (?,?)",
		columns:  innoDBColumns(),
		rows: [][]driver.Value{
			innoDBRow("shop/fk_a", "shop/items", "shop/orders"),
		},
	}}}

	result, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
		t.Context(),
		OutgoingFrom("items", "carts", "items"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys: %v", err)
	}
	script.assertDone(t)

	// The predicate deduplicates; the result does not. "items" was requested
	// twice, so its constraint appears twice.
	if len(result.Keys) != 2 {
		t.Errorf("returned %d keys, want the items constraint at both requested positions",
			len(result.Keys))
	}
	if result.Visibility != VisibilityComplete {
		t.Errorf("visibility = %s, want complete", result.Visibility)
	}
}

func TestForeignKeysInnoDBGuardsTheSchemaInsideItsComposedParameters(t *testing.T) {
	t.Parallel()

	// This site binds schema+"/"+table, not the table name. An implementation
	// that guards sel.tables passes every other guard test in this package,
	// because every other one uses an unrepresentable *table*. Only a clean
	// table under an unrepresentable schema tells the two apart.
	//
	// Measured 2026-08-08 on 8.0.46, 8.4.9 and 9.7.1: a supplementary
	// character in the bound parameter raises error 3988 rather than matching
	// nothing. See docs/COMPAT.md entry 8.
	for _, testCase := range []struct{ name, schema string }{
		{"supplementary schema", "supp_\U00010000"},
		{"invalid utf-8 schema", "bad_\xff"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			script := &queryScript{steps: []queryStep{{
				contains: innoDBSource,
				lacks:    "IN (",
				columns:  innoDBColumns(),
			}}}

			result, err := NewInspector(
				openScriptedDB(t, script), testCase.schema,
			).ForeignKeys(t.Context(), OutgoingFrom("ordinary_table"))
			if err != nil {
				t.Fatalf("ForeignKeys: %v", err)
			}
			script.assertDone(t)

			if len(result.Keys) != 0 {
				t.Errorf("Keys = %#v, want empty", result.Keys)
			}
			if result.Visibility != VisibilityComplete {
				t.Errorf("visibility = %s, want complete", result.Visibility)
			}
		})
	}
}

func TestForeignKeysInnoDBUnnarrowedResultDiscardsOtherSchemas(t *testing.T) {
	t.Parallel()

	// The unnarrowed InnoDB query has no schema predicate to keep — the schema
	// is embedded in FOR_NAME — so it reads every foreign key on the server.
	// Its correctness rests entirely on the Go filter, and this is what proves
	// the filter carries it.
	script := &queryScript{steps: []queryStep{{
		contains: innoDBSource,
		lacks:    "IN (",
		columns:  innoDBColumns(),
		rows: [][]driver.Value{
			innoDBRow("shop/fk_ours", "shop/items", "shop/orders"),
			innoDBRow("elsewhere/fk_theirs", "elsewhere/items", "elsewhere/orders"),
		},
	}}}

	result, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
		t.Context(),
		OutgoingFrom("items", "supp_\U00010000"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys: %v", err)
	}
	script.assertDone(t)

	got := make([]string, 0, len(result.Keys))
	for _, key := range result.Keys {
		got = append(got, key.ChildSchema+"/"+key.ConstraintName)
	}
	if want := []string{"shop/fk_ours"}; !reflect.DeepEqual(got, want) {
		t.Errorf("returned %#v, want %#v — the other schema was not discarded", got, want)
	}
}

const standardSource = "KEY_COLUMN_USAGE AS kcu"

func standardColumns() []string {
	return []string{
		"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME",
		"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
		"REFERENCED_COLUMN_NAME", "DELETE_RULE", "UPDATE_RULE",
		"ORDINAL_POSITION",
	}
}

// primaryDenied is the first step of every standard-path script. Calling
// ForeignKeys on a healthy script never reaches the standard source at all —
// foreignKeysInnoDB returns first, and an empty result is a success — so the
// primary query has to be failed deliberately.
func primaryDenied() queryStep {
	return queryStep{contains: innoDBSource, err: errors.New("PROCESS denied")}
}

func TestForeignKeysStandardDeduplicatesItsPredicate(t *testing.T) {
	t.Parallel()

	script := &queryScript{steps: []queryStep{
		primaryDenied(),
		{
			contains: "IN (?,?)",
			columns:  standardColumns(),
			rows: [][]driver.Value{
				{"shop", "items", "fk_a", "pid", "shop", "orders", "id", "RESTRICT", "NO ACTION", int64(1)},
			},
		},
		{
			contains: "information_schema.STATISTICS",
			columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "COLUMN_NAME", "SEQ_IN_INDEX",
			},
		},
	}}

	result, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
		t.Context(),
		OutgoingFrom("items", "carts", "items"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys: %v", err)
	}
	script.assertDone(t)

	if len(result.Keys) != 2 {
		t.Errorf("returned %d keys, want the items constraint at both requested positions",
			len(result.Keys))
	}
	if result.Visibility != VisibilityUnconfirmed {
		t.Errorf("visibility = %s, want unconfirmed", result.Visibility)
	}
}

func TestForeignKeysStandardFallsBackKeepingItsSchemaPredicate(t *testing.T) {
	t.Parallel()

	// The unnarrowed form drops the table list and keeps the schema equality,
	// so unlike the InnoDB source this one stays inside the schema.
	for _, testCase := range []struct{ name, bad string }{
		{"supplementary table", "supp_\U00010000"},
		{"invalid utf-8 table", "bad_\xff"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			script := &queryScript{steps: []queryStep{
				primaryDenied(),
				{
					contains: "kcu.TABLE_SCHEMA = ?",
					lacks:    "IN (",
					columns:  standardColumns(),
				},
			}}

			result, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
				t.Context(),
				OutgoingFrom("ordinary_table", testCase.bad),
			)
			if err != nil {
				t.Fatalf("ForeignKeys: %v", err)
			}
			script.assertDone(t)

			if len(result.Keys) != 0 {
				t.Errorf("Keys = %#v, want empty", result.Keys)
			}
			if result.Visibility != VisibilityUnconfirmed {
				t.Errorf("visibility = %s, want unconfirmed", result.Visibility)
			}
		})
	}
}

func TestForeignKeysStandardBoundAccountsForItsSchemaParameter(t *testing.T) {
	t.Parallel()

	// This site binds the schema as well, so its budget is one below the
	// ceiling — the opposite shape from the InnoDB source in the same
	// function. Declaring the InnoDB site's fixed-argument count here would
	// build a statement of 65536 parameters, which is the failure this whole
	// change exists to prevent.
	for _, testCase := range []struct {
		name  string
		count int
		lacks string
	}{
		{"at the bound", maxStatementParameters - 1, ""},
		{"above the bound", maxStatementParameters, "IN ("},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			step := queryStep{
				contains: standardSource,
				lacks:    testCase.lacks,
				columns:  standardColumns(),
			}
			if testCase.lacks == "" {
				step.contains = "IN ("
			}
			script := &queryScript{steps: []queryStep{primaryDenied(), step}}

			if _, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
				t.Context(),
				OutgoingFrom(distinctNames(testCase.count)...),
			); err != nil {
				t.Fatalf("ForeignKeys: %v", err)
			}
			script.assertDone(t)
		})
	}
}

// fallbackIndexPairsPerBatch is what populateFallbackIndexes can fit in one
// statement: it binds two parameters per distinct child table.
const fallbackIndexPairsPerBatch = maxStatementParameters / 2

// standardRowsForDistinctChildren returns count constraints, each on its own
// child table, so populateFallbackIndexes sees count distinct pairs.
func standardRowsForDistinctChildren(count int) [][]driver.Value {
	rows := make([][]driver.Value, 0, count)
	for index := range count {
		table := "t" + strconv.Itoa(index)
		rows = append(rows, []driver.Value{
			"shop", table, "fk_" + strconv.Itoa(index), "pid",
			"shop", "orders", "id", "RESTRICT", "NO ACTION", int64(1),
		})
	}

	return rows
}

func statisticsStep(rows [][]driver.Value) queryStep {
	return queryStep{
		contains: "information_schema.STATISTICS",
		columns: []string{
			"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "COLUMN_NAME", "SEQ_IN_INDEX",
		},
		rows: rows,
	}
}

func TestFallbackIndexLookupBatchesWithoutSplittingAPair(t *testing.T) {
	t.Parallel()

	// populateFallbackIndexes builds its predicate by hand rather than through
	// sqlPlaceholders, so it is invisible to a grep for that function — and it
	// runs on the standard path, which is this change's own fallback. Two
	// parameters per pair means it reaches the ceiling at half as many items.
	//
	// It batches rather than dropping its WHERE clause: the scanner
	// density-checks every row it reads before anything is selected, so an
	// unnarrowed read would validate the whole server's index metadata and can
	// fail on a table this call never named.
	for _, testCase := range []struct {
		name    string
		pairs   int
		queries int
	}{
		{"one batch at the limit", fallbackIndexPairsPerBatch, 1},
		{"two batches one pair over", fallbackIndexPairsPerBatch + 1, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			last := "t" + strconv.Itoa(testCase.pairs-1)
			firstIndex := []driver.Value{"shop", "t0", "idx_pid", "pid", int64(1)}
			lastIndex := []driver.Value{"shop", last, "idx_pid", "pid", int64(1)}

			steps := []queryStep{
				primaryDenied(),
				{
					contains: standardSource,
					columns:  standardColumns(),
					rows:     standardRowsForDistinctChildren(testCase.pairs),
				},
			}
			// Both t0 and the last pair must come back indexed. When one
			// statement suffices they arrive together; when the pair count
			// crosses the limit they arrive in different statements, and only
			// an accumulator spanning both batches still reports both.
			if testCase.queries == 1 {
				steps = append(steps, statisticsStep([][]driver.Value{firstIndex, lastIndex}))
			} else {
				steps = append(steps,
					statisticsStep([][]driver.Value{firstIndex}),
					statisticsStep([][]driver.Value{lastIndex}),
				)
			}
			script := &queryScript{steps: steps}

			result, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
				t.Context(),
				OutgoingFrom("t0", last),
			)
			if err != nil {
				t.Fatalf("ForeignKeys: %v", err)
			}
			// assertDone is the query count: a third STATISTICS query would
			// have found no step and failed, and a missing one leaves a step
			// unconsumed.
			script.assertDone(t)

			if len(result.Keys) != 2 {
				t.Fatalf("returned %d keys, want t0 and %s", len(result.Keys), last)
			}
			for _, key := range result.Keys {
				if !key.Indexed {
					t.Errorf("%s reported unindexed; its batch's rows were lost",
						key.ChildTable)
				}
			}
		})
	}
}

func TestForeignKeysInnoDBBoundHasNoFixedParameter(t *testing.T) {
	t.Parallel()

	// Unlike Columns, this statement binds nothing but its IN list, so its
	// budget is the whole ceiling. A site declaring the wrong fixed-argument
	// count flips narrowing and fallback at exactly this boundary and nowhere
	// else.
	for _, testCase := range []struct {
		name  string
		count int
		lacks string
	}{
		{"at the bound", maxStatementParameters, ""},
		{"above the bound", maxStatementParameters + 1, "IN ("},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			step := queryStep{
				contains: innoDBSource,
				lacks:    testCase.lacks,
				columns:  innoDBColumns(),
			}
			if testCase.lacks == "" {
				step.contains = "IN ("
			}
			script := &queryScript{steps: []queryStep{step}}

			if _, err := NewInspector(openScriptedDB(t, script), "shop").ForeignKeys(
				t.Context(),
				OutgoingFrom(distinctNames(testCase.count)...),
			); err != nil {
				t.Fatalf("ForeignKeys: %v", err)
			}
			script.assertDone(t)
		})
	}
}
