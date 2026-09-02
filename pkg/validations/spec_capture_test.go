package validations

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func TestTableSpecRejectsInvalidRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  TableRef
	}{
		{name: "zero value", ref: TableRef{}},
		{name: "empty schema", ref: Ref("", "payment")},
		{name: "empty table", ref: Ref("sakila", "")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			inspector := NewInspector(testsupport.OpenFailingDB(errors.New("unused")), "sakila")
			_, err := inspector.TableSpec(context.Background(), test.ref)
			if !errors.Is(err, ErrInvalidTableRef) {
				t.Errorf("TableSpec with %s returned %v, want ErrInvalidTableRef; "+
					"an unset ref must be rejected before any query runs", test.name, err)
			}
		})
	}
}

func TestTableSpecRejectsNilQuerier(t *testing.T) {
	t.Parallel()

	var inspector *Inspector
	_, err := inspector.TableSpec(context.Background(), Ref("sakila", "payment"))
	if !errors.Is(err, ErrNilQuerier) {
		t.Errorf("TableSpec on a nil Inspector returned %v, want ErrNilQuerier", err)
	}
}

func TestTableSpecWrapsQueryFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection refused")
	inspector := NewInspector(testsupport.OpenFailingDB(cause), "sakila")

	_, err := inspector.TableSpec(context.Background(), Ref("sakila", "payment"))
	if !errors.Is(err, cause) {
		t.Errorf("TableSpec returned %v, which does not wrap the driver cause; "+
			"errors must stay inspectable", err)
	}

	var objErr *ObjectError
	if !errors.As(err, &objErr) {
		t.Fatalf("TableSpec returned %v, want an *ObjectError naming the object", err)
	}
	if objErr.Table != "payment" || objErr.Schema != "sakila" {
		t.Errorf("ObjectError names (%q, %q), want (\"sakila\", \"payment\"); "+
			"an error must name the object it concerns", objErr.Schema, objErr.Table)
	}
}

func TestTableSpecRecordsRequestedSections(t *testing.T) {
	t.Parallel()

	var request specRequest
	for _, option := range []SpecOption{WithIndexes(), WithComment()} {
		option(&request)
	}

	if !request.sections.Has(SectionIndexes) || !request.sections.Has(SectionComment) {
		t.Error("requested sections were not recorded; Captured is what lets DiffSpecs " +
			"tell an empty section from an unasked question")
	}
	if request.sections.Has(SectionConstraints) {
		t.Error("SectionConstraints was recorded without WithConstraints")
	}
}

const tablesQueryMarker = "information_schema.TABLES"

const columnsQueryMarker = "information_schema.COLUMNS"

var tableRowColumns = []string{
	"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "ENGINE",
	"CHARACTER_SET_NAME", "TABLE_COLLATION", "TABLE_COMMENT",
}

func tableRowScript(schema, table string) testsupport.ScriptedQuery {
	return testsupport.ScriptedQuery{
		Match:   tablesQueryMarker,
		Columns: tableRowColumns,
		Rows: [][]driver.Value{
			{schema, table, "BASE TABLE", "InnoDB",
				"utf8mb4", "utf8mb4_0900_ai_ci", ""},
		},
	}
}

func TestTableSpecRejectsAView(t *testing.T) {
	t.Parallel()

	// A view carries NULL engine and collation, and information_schema.COLUMNS
	// still lists its columns — so without this check a view would produce a
	// spec that compares equal to any other view over the same columns.
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   tablesQueryMarker,
		Columns: tableRowColumns,
		Rows: [][]driver.Value{
			{"shop", "v_items", "VIEW", nil, nil, nil, "VIEW"},
		},
	})
	inspector := NewInspector(db, "shop")

	_, err := inspector.TableSpec(context.Background(), Ref("shop", "v_items"))
	if !errors.Is(err, ErrUnsupportedTableType) {
		t.Errorf("TableSpec for a view returned %v, want ErrUnsupportedTableType; "+
			"a view spec would be silently incomparable, not merely incomplete", err)
	}
}

func TestTableSpecRejectsACaseVariantTable(t *testing.T) {
	t.Parallel()

	// A contract test, not a reproduction. On the supported matrix TABLE_NAME
	// is utf8mb3_bin, so a real server would not return this row for a request
	// for "items" — but the guarantee must not rest on a collation, and this
	// pins the Go comparison that makes it independent of one.
	db := testsupport.OpenScriptedDB(tableRowScript("shop", "Items"))
	inspector := NewInspector(db, "shop")

	_, err := inspector.TableSpec(context.Background(), Ref("shop", "items"))
	if !errors.Is(err, ErrTableNotFound) {
		t.Errorf("TableSpec for \"items\" given only a row spelled \"Items\" returned "+
			"%v, want ErrTableNotFound; a differently cased near-match is not the "+
			"requested table", err)
	}
}

func TestTableSpecResolvesTheExactCandidate(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   tablesQueryMarker,
		Columns: tableRowColumns,
		Rows: [][]driver.Value{
			{"shop", "Items", "BASE TABLE", "MyISAM",
				"latin1", "latin1_swedish_ci", "wrong"},
			{"shop", "items", "BASE TABLE", "InnoDB",
				"utf8mb4", "utf8mb4_0900_ai_ci", "right"},
		},
	})
	inspector := NewInspector(db, "shop")

	spec, err := inspector.TableSpec(
		context.Background(), Ref("shop", "items"), WithComment())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if spec.Table != "items" || spec.Engine != "InnoDB" || spec.Comment != "right" {
		t.Errorf("resolved (%q, %q, %q), want (\"items\", \"InnoDB\", \"right\"); "+
			"the exact-case candidate must win", spec.Table, spec.Engine, spec.Comment)
	}
	if spec.Schema != "shop" || spec.Table != "items" {
		t.Errorf("resolved identity (%q, %q) differs from the request; on success "+
			"they are equal by construction", spec.Schema, spec.Table)
	}
}

func TestTableSpecDiscardsColumnsOfACaseVariantTable(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(
		tableRowScript("shop", "items"),
		testsupport.ScriptedQuery{
			Match: columnsQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION",
				"COLUMN_TYPE", "IS_NULLABLE", "CHARACTER_SET_NAME", "COLLATION_NAME",
				"COLUMN_DEFAULT", "EXTRA", "GENERATION_EXPRESSION",
			},
			Rows: [][]driver.Value{
				{"shop", "items", "id", int64(1), "int", "NO", nil, nil, nil, "", ""},
				{"shop", "Items", "wrong", int64(2), "int", "YES", nil, nil, nil, "", ""},
			},
		},
	)
	inspector := NewInspector(db, "shop")

	spec, err := inspector.TableSpec(context.Background(), Ref("shop", "items"))
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if len(spec.Columns) != 1 || spec.Columns[0].Name != "id" {
		t.Errorf("columns = %+v, want only \"id\"; the scripted server returned "+
			"a defensive near-match, which must not be merged into the resolved "+
			"table", spec.Columns)
	}
}

const statisticsQueryMarker = "information_schema.STATISTICS"

func TestTableSpecIndexQueryFailureIsWrapped(t *testing.T) {
	t.Parallel()

	cause := errors.New("index metadata unavailable")
	db := testsupport.OpenScriptedDB(
		tableRowScript("sakila", "payment"),
		testsupport.ScriptedQuery{Match: statisticsQueryMarker, Err: cause},
	)
	inspector := NewInspector(db, "sakila")

	_, err := inspector.TableSpec(
		context.Background(), Ref("sakila", "payment"), WithIndexes())
	if !errors.Is(err, cause) {
		t.Errorf("TableSpec with WithIndexes returned %v, which does not wrap the "+
			"index-query cause; the table and column queries succeeded, so only the "+
			"index query can have failed", err)
	}
}

func TestTableSpecWithoutIndexesIssuesNoIndexQuery(t *testing.T) {
	t.Parallel()

	cause := errors.New("index metadata unavailable")
	db := testsupport.OpenScriptedDB(
		tableRowScript("sakila", "payment"),
		testsupport.ScriptedQuery{Match: statisticsQueryMarker, Err: cause},
	)
	inspector := NewInspector(db, "sakila")

	if _, err := inspector.TableSpec(
		context.Background(), Ref("sakila", "payment")); err != nil {
		t.Errorf("TableSpec without WithIndexes returned %v; the index query is "+
			"opt-in and must not run", err)
	}
}

func TestTableSpecCapturesIndexParts(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(
		tableRowScript("shop", "items"),
		testsupport.ScriptedQuery{
			Match: statisticsQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "SEQ_IN_INDEX",
				"COLUMN_NAME", "SUB_PART", "COLLATION", "EXPRESSION",
				"NON_UNIQUE", "INDEX_TYPE", "IS_VISIBLE",
			},
			// Shapes taken from the probe: a functional part reports
			// COLUMN_NAME NULL, SUB_PART NULL, and COLLATION 'A'.
			Rows: [][]driver.Value{
				{"shop", "items", "idx_func", int64(1), nil, nil, "A", "(`amount` * 2)",
					int64(1), "BTREE", "YES"},
				{"shop", "items", "idx_name", int64(1), "name", int64(10), "A", nil,
					int64(1), "BTREE", "YES"},
				{"shop", "items", "idx_name", int64(2), "sku", nil, "D", nil,
					int64(1), "BTREE", "YES"},
				{"shop", "Items", "idx_wrong", int64(1), "x", nil, "A", nil,
					int64(1), "BTREE", "YES"},
			},
		},
	)
	inspector := NewInspector(db, "shop")

	spec, err := inspector.TableSpec(
		context.Background(), Ref("shop", "items"), WithIndexes())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}

	if len(spec.Indexes) != 2 {
		t.Fatalf("indexes = %+v, want exactly two; the idx_wrong row belongs to a "+
			"case-variant table and must be discarded", spec.Indexes)
	}

	functional := spec.Indexes[0]
	if functional.Name != "idx_func" || len(functional.Parts) != 1 {
		t.Fatalf("functional index = %+v, want idx_func with one part", functional)
	}
	if functional.Parts[0].Expression != "(`amount` * 2)" {
		t.Errorf("functional part Expression = %q, want \"(`amount` * 2)\"; without "+
			"it two different functional indexes compare equal",
			functional.Parts[0].Expression)
	}
	if functional.Parts[0].Column != "" {
		t.Errorf("functional part Column = %q, want empty; COLUMN_NAME is NULL for a "+
			"functional key part", functional.Parts[0].Column)
	}
	if functional.Parts[0].Descending {
		t.Error("functional part reported descending; the probe shows COLLATION 'A' " +
			"for a functional part, and only 'D' means descending")
	}

	mixed := spec.Indexes[1]
	if len(mixed.Parts) != 2 {
		t.Fatalf("idx_name parts = %+v, want 2", mixed.Parts)
	}
	if mixed.Parts[0].Column != "name" {
		t.Errorf("part 0 column = %q, want \"name\"", mixed.Parts[0].Column)
	}
	if mixed.Parts[0].SubPart != 10 {
		t.Errorf("part 0 SubPart = %d, want 10; INDEX(name(10)) is not INDEX(name) "+
			"and must not compare equal to it", mixed.Parts[0].SubPart)
	}
	if mixed.Parts[0].Descending {
		t.Error("part 0 reported descending; COLLATION 'A' is ascending")
	}
	if mixed.Parts[1].SubPart != 0 {
		t.Errorf("part 1 SubPart = %d, want 0; a NULL SUB_PART indexes the whole "+
			"value", mixed.Parts[1].SubPart)
	}
	if !mixed.Parts[1].Descending {
		t.Error("part 1 did not report descending; COLLATION 'D' is a descending " +
			"key part and a real schema difference")
	}
}

const (
	checkConstraintQueryMarker = "information_schema.CHECK_CONSTRAINTS"
	foreignKeyQueryMarker      = "information_schema.REFERENTIAL_CONSTRAINTS"
)

func TestTableSpecCheckConstraintQueryFailureIsWrapped(t *testing.T) {
	t.Parallel()

	cause := errors.New("check-constraint metadata unavailable")
	db := testsupport.OpenScriptedDB(
		tableRowScript("sakila", "payment"),
		testsupport.ScriptedQuery{Match: checkConstraintQueryMarker, Err: cause},
	)
	inspector := NewInspector(db, "sakila")

	_, err := inspector.TableSpec(
		context.Background(), Ref("sakila", "payment"), WithConstraints())
	if !errors.Is(err, cause) {
		t.Errorf("TableSpec with WithConstraints returned %v, which does not wrap "+
			"the check-constraint cause", err)
	}
}

func TestTableSpecForeignKeyQueryFailureIsWrapped(t *testing.T) {
	t.Parallel()

	// The CHECK query is scripted to succeed so only the foreign-key query can
	// be the source of the failure. Two statements, two separate assertions.
	cause := errors.New("foreign-key metadata unavailable")
	db := testsupport.OpenScriptedDB(
		tableRowScript("sakila", "payment"),
		testsupport.ScriptedQuery{
			Match: checkConstraintQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "CHECK_CLAUSE",
				"ENFORCED",
			},
		},
		testsupport.ScriptedQuery{Match: foreignKeyQueryMarker, Err: cause},
	)
	inspector := NewInspector(db, "sakila")

	_, err := inspector.TableSpec(
		context.Background(), Ref("sakila", "payment"), WithConstraints())
	if !errors.Is(err, cause) {
		t.Errorf("TableSpec with WithConstraints returned %v, which does not wrap "+
			"the foreign-key cause", err)
	}
}

func TestTableSpecForeignKeyQueryPinsBothSidesToTheResolvedTable(t *testing.T) {
	t.Parallel()

	// These equalities are redundant for results but not for MySQL's plan. On
	// the supported matrix they keep the data-dictionary join independent of
	// the number of foreign keys elsewhere in the schema.
	db := testsupport.OpenScriptedDB(
		tableRowScript("shop", "items"),
		testsupport.ScriptedQuery{
			Match: checkConstraintQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "CHECK_CLAUSE",
				"ENFORCED",
			},
		},
		testsupport.ScriptedQuery{
			Match: "AND kcu.TABLE_NAME = rc.TABLE_NAME",
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME",
				"UPDATE_RULE", "DELETE_RULE", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME",
			},
			Rows: [][]driver.Value{
				{"shop", "items", "fk_category", "RESTRICT", "CASCADE", "category_id",
					"shop", "categories", "id"},
			},
		},
		testsupport.ScriptedQuery{
			Match:   foreignKeyQueryMarker,
			Columns: []string{"unused"},
		},
	)

	spec, err := NewInspector(db, "shop").TableSpec(
		t.Context(), Ref("shop", "items"), WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if len(spec.Constraints) != 1 || spec.Constraints[0].Name != "fk_category" {
		t.Errorf("constraints = %#v, want the row from the fully pinned query", spec.Constraints)
	}
}

func TestTableSpecCheckQueryFiltersConstraintType(t *testing.T) {
	t.Parallel()

	var queries []string
	db := testsupport.OpenScriptedDBWithLog(
		&queries,
		tableRowScript("shop", "items"),
		emptyCheckConstraintScript(),
		emptyForeignKeyConstraintScript(),
	)

	_, err := NewInspector(db, "shop").TableSpec(
		t.Context(), Ref("shop", "items"), WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if !queryLogContains(queries, "tc.CONSTRAINT_TYPE = 'CHECK'") {
		t.Errorf("CHECK query does not filter TABLE_CONSTRAINTS to CHECK rows: %q", queries)
	}
}

func TestTableSpecForeignKeyQueryExcludesNonReferencingRows(t *testing.T) {
	t.Parallel()

	var queries []string
	db := testsupport.OpenScriptedDBWithLog(
		&queries,
		tableRowScript("shop", "items"),
		emptyCheckConstraintScript(),
		emptyForeignKeyConstraintScript(),
	)

	_, err := NewInspector(db, "shop").TableSpec(
		t.Context(), Ref("shop", "items"), WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if !queryLogContains(queries, "kcu.REFERENCED_TABLE_NAME IS NOT NULL") {
		t.Errorf("foreign-key query does not exclude nonreferencing key rows: %q", queries)
	}
}

func TestSortConstraintsOrdersByNameThenKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		constraints []ConstraintSpec
	}{
		{
			name: "foreign key first",
			constraints: []ConstraintSpec{
				{Name: "k", Kind: ConstraintForeignKey},
				{Name: "k", Kind: ConstraintCheck},
				{Name: "a", Kind: ConstraintCheck},
			},
		},
		{
			name: "check first",
			constraints: []ConstraintSpec{
				{Name: "k", Kind: ConstraintCheck},
				{Name: "k", Kind: ConstraintForeignKey},
				{Name: "a", Kind: ConstraintCheck},
			},
		},
	}
	want := []ConstraintSpec{
		{Name: "a", Kind: ConstraintCheck},
		{Name: "k", Kind: ConstraintCheck},
		{Name: "k", Kind: ConstraintForeignKey},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			sortConstraints(test.constraints)
			for index := range want {
				if test.constraints[index].Name != want[index].Name ||
					test.constraints[index].Kind != want[index].Kind {
					t.Fatalf("constraints = %#v, want %#v", test.constraints, want)
				}
			}
		})
	}
}

func TestTableSpecCapturesSameNamedCheckAndForeignKey(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(
		tableRowScript("shop", "items"),
		testsupport.ScriptedQuery{
			Match: checkConstraintQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "CHECK_CLAUSE",
				"ENFORCED",
			},
			Rows: [][]driver.Value{{"shop", "items", "k", "(`parent_id` >= 0)", "YES"}},
		},
		testsupport.ScriptedQuery{
			Match: foreignKeyQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME",
				"UPDATE_RULE", "DELETE_RULE", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME",
			},
			Rows: [][]driver.Value{{
				"shop", "items", "k", "RESTRICT", "CASCADE", "parent_id",
				"shop", "parents", "id",
			}},
		},
	)

	spec, err := NewInspector(db, "shop").TableSpec(
		t.Context(), Ref("shop", "items"), WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if len(spec.Constraints) != 2 {
		t.Fatalf("constraints = %#v, want same-named CHECK and FOREIGN KEY", spec.Constraints)
	}
	if spec.Constraints[0].Name != "k" || spec.Constraints[0].Kind != ConstraintCheck ||
		spec.Constraints[1].Name != "k" ||
		spec.Constraints[1].Kind != ConstraintForeignKey {
		t.Errorf("constraints = %#v, want k/CHECK then k/FOREIGN KEY", spec.Constraints)
	}
}

func emptyCheckConstraintScript() testsupport.ScriptedQuery {
	return testsupport.ScriptedQuery{
		Match: checkConstraintQueryMarker,
		Columns: []string{
			"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "CHECK_CLAUSE",
			"ENFORCED",
		},
	}
}

func emptyForeignKeyConstraintScript() testsupport.ScriptedQuery {
	return testsupport.ScriptedQuery{
		Match: foreignKeyQueryMarker,
		Columns: []string{
			"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME",
			"UPDATE_RULE", "DELETE_RULE", "COLUMN_NAME",
			"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
			"REFERENCED_COLUMN_NAME",
		},
	}
}

func queryLogContains(queries []string, fragment string) bool {
	for _, query := range queries {
		if strings.Contains(query, fragment) {
			return true
		}
	}

	return false
}

func TestTableSpecDiscardsConstraintsOfACaseVariantTable(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(
		tableRowScript("shop", "items"),
		testsupport.ScriptedQuery{
			Match: checkConstraintQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "CHECK_CLAUSE",
				"ENFORCED",
			},
			Rows: [][]driver.Value{
				{"shop", "items", "chk_right", "(`a` > 0)", "YES"},
				{"shop", "Items", "chk_wrong", "(`b` > 0)", "YES"},
			},
		},
		testsupport.ScriptedQuery{
			Match: foreignKeyQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME",
				"UPDATE_RULE", "DELETE_RULE", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME",
			},
			Rows: [][]driver.Value{
				{"shop", "items", "fk_right", "CASCADE", "SET NULL", "cat_id",
					"shop", "cats", "id"},
				{"shop", "Items", "fk_wrong", "CASCADE", "CASCADE", "x",
					"shop", "other", "id"},
			},
		},
	)
	inspector := NewInspector(db, "shop")

	spec, err := inspector.TableSpec(
		context.Background(), Ref("shop", "items"), WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}

	var names []string
	for _, constraint := range spec.Constraints {
		names = append(names, constraint.Name)
	}
	want := []string{"chk_right", "fk_right"}
	if !slices.Equal(names, want) {
		t.Errorf("constraints = %v, want %v; rows belonging to a case-variant table "+
			"must be discarded from every subordinate query, not just columns and "+
			"indexes", names, want)
	}
}

func TestTableSpecCapturesCheckEnforcement(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(
		tableRowScript("shop", "items"),
		testsupport.ScriptedQuery{
			Match: checkConstraintQueryMarker,
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "CHECK_CLAUSE",
				"ENFORCED",
			},
			Rows: [][]driver.Value{
				{"shop", "items", "chk_off", "(`b` > 0)", "NO"},
				{"shop", "items", "chk_on", "(`a` > 0)", "YES"},
			},
		},
	)
	inspector := NewInspector(db, "shop")

	spec, err := inspector.TableSpec(
		context.Background(), Ref("shop", "items"), WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if len(spec.Constraints) != 2 {
		t.Fatalf("constraints = %+v, want 2", spec.Constraints)
	}
	if spec.Constraints[0].Enforced {
		t.Error("chk_off reported enforced; ENFORCED='NO' means the server never " +
			"evaluates the constraint, which is a different table than one that does")
	}
	if !spec.Constraints[1].Enforced {
		t.Error("chk_on reported unenforced despite ENFORCED='YES'")
	}
}

func TestTableSpecWithoutConstraintsIssuesNoConstraintQuery(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(
		tableRowScript("sakila", "payment"),
		testsupport.ScriptedQuery{
			Match: checkConstraintQueryMarker,
			Err:   errors.New("check-constraint metadata unavailable"),
		},
		testsupport.ScriptedQuery{
			Match: foreignKeyQueryMarker,
			Err:   errors.New("foreign-key metadata unavailable"),
		},
	)
	inspector := NewInspector(db, "sakila")

	if _, err := inspector.TableSpec(
		context.Background(), Ref("sakila", "payment")); err != nil {
		t.Errorf("TableSpec without WithConstraints returned %v; both constraint "+
			"queries are opt-in and must not run", err)
	}
}
