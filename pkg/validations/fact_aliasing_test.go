package validations

import (
	"database/sql/driver"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// Duplicate requested tables must return independent facts. Columns has always
// cloned; these pin the three paths that did not — InvisibleColumns,
// PrimaryKeys, and the ForeignKeys selector — so the package states one
// convention rather than two. See issue #20.

func TestInvisibleColumnsDoesNotAliasAcrossDuplicateRequests(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "information_schema.COLUMNS",
		Columns: []string{"TABLE_NAME", "COLUMN_NAME"},
		Rows: [][]driver.Value{
			{"alpha", "hidden_one"},
			{"alpha", "hidden_two"},
		},
	})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	got, err := NewInspector(db, "shop").InvisibleColumns(
		t.Context(),
		[]string{"alpha", "alpha"},
	)
	if err != nil {
		t.Fatalf("InvisibleColumns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("InvisibleColumns() returned %d facts, want 2", len(got))
	}

	got[0].Columns[0] = "mutated"
	if got[1].Columns[0] != "hidden_one" {
		t.Errorf(
			"mutating the first fact changed the second: got[1].Columns[0] = %q, want %q",
			got[1].Columns[0],
			"hidden_one",
		)
	}
}

func TestPrimaryKeysDoesNotAliasAcrossDuplicateRequests(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "information_schema.TABLES AS t",
		Columns: []string{"TABLE_NAME", "COLUMN_NAME", "DATA_TYPE", "COLUMN_TYPE"},
		Rows: [][]driver.Value{
			{"alpha", "tenant_id", "int", "int"},
			{"alpha", "seq", "int", "int"},
		},
	})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	got, err := NewInspector(db, "shop").PrimaryKeys(t.Context(), []string{"alpha", "alpha"})
	if err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("PrimaryKeys() returned %d facts, want 2", len(got))
	}

	got[0].Columns[0] = "mutated"
	if got[1].Columns[0] != "tenant_id" {
		t.Errorf(
			"mutating the first fact changed the second: got[1].Columns[0] = %q, want %q",
			got[1].Columns[0],
			"tenant_id",
		)
	}
}

func TestSelectForeignKeysDoesNotAliasColumnSlices(t *testing.T) {
	t.Parallel()

	keys := []ForeignKey{{
		ConstraintName: "fk_orders_customer",
		ChildSchema:    "shop",
		ChildTable:     "orders",
		ChildColumns:   []string{"customer_id"},
		ParentSchema:   "shop",
		ParentTable:    "customers",
		ParentColumns:  []string{"id"},
		Indexed:        true,
	}}

	selected := selectForeignKeys(keys, "shop", OutgoingFrom("orders", "orders"))
	if len(selected) != 2 {
		t.Fatalf("selectForeignKeys() returned %d keys, want 2", len(selected))
	}

	selected[0].ChildColumns[0] = "mutated"
	selected[0].ParentColumns[0] = "mutated"

	if selected[1].ChildColumns[0] != "customer_id" {
		t.Errorf(
			"mutating the first key changed the second: ChildColumns[0] = %q, want %q",
			selected[1].ChildColumns[0],
			"customer_id",
		)
	}
	if selected[1].ParentColumns[0] != "id" {
		t.Errorf(
			"mutating the first key changed the second: ParentColumns[0] = %q, want %q",
			selected[1].ParentColumns[0],
			"id",
		)
	}
	if keys[0].ChildColumns[0] != "customer_id" {
		t.Errorf(
			"mutating a returned key changed the source: keys[0].ChildColumns[0] = %q, want %q",
			keys[0].ChildColumns[0],
			"customer_id",
		)
	}
}
