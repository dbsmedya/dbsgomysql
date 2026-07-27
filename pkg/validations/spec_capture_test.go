package validations

import (
	"context"
	"database/sql/driver"
	"errors"
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
