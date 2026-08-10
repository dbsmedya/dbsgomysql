package validations

import (
	"database/sql/driver"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func assertUsesRequestedObjects(
	t *testing.T,
	columns []string,
	rows [][]driver.Value,
	call func(*Inspector) (int, error),
) {
	t.Helper()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   ") AS requested",
		Columns: columns,
		Rows:    rows,
	})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	count, err := call(NewInspector(db, "shop"))
	if err != nil {
		t.Fatalf("fact query: %v", err)
	}
	if count != 1 {
		t.Errorf("fact count = %d, want 1 from requested-object query", count)
	}
}

func TestIssue19FactsUseRequestedObjectLookups(t *testing.T) {
	t.Parallel()

	t.Run("tables", func(t *testing.T) {
		t.Parallel()
		assertUsesRequestedObjects(t,
			[]string{"TABLE_NAME", "TABLE_TYPE", "ENGINE"},
			[][]driver.Value{{"orders", "BASE TABLE", "InnoDB"}},
			func(inspector *Inspector) (int, error) {
				facts, err := inspector.Tables(t.Context(), []string{"orders"})
				return len(facts), err
			},
		)
	})

	t.Run("columns", func(t *testing.T) {
		t.Parallel()
		assertUsesRequestedObjects(t,
			[]string{
				"TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION",
				"DATA_TYPE", "COLUMN_TYPE", "EXTRA",
			},
			[][]driver.Value{{"orders", "id", int64(1), "bigint", "bigint unsigned", ""}},
			func(inspector *Inspector) (int, error) {
				facts, err := inspector.Columns(t.Context(), []string{"orders"})
				return len(facts), err
			},
		)
	})

	t.Run("invisible columns", func(t *testing.T) {
		t.Parallel()
		assertUsesRequestedObjects(t,
			[]string{"TABLE_NAME", "COLUMN_NAME"},
			[][]driver.Value{{"orders", "hidden"}},
			func(inspector *Inspector) (int, error) {
				facts, err := inspector.InvisibleColumns(t.Context(), []string{"orders"})
				return len(facts), err
			},
		)
	})

	t.Run("primary keys", func(t *testing.T) {
		t.Parallel()
		assertUsesRequestedObjects(t,
			[]string{"TABLE_NAME", "COLUMN_NAME", "DATA_TYPE", "COLUMN_TYPE"},
			[][]driver.Value{{"orders", "id", "bigint", "bigint unsigned"}},
			func(inspector *Inspector) (int, error) {
				facts, err := inspector.PrimaryKeys(t.Context(), []string{"orders"})
				return len(facts), err
			},
		)
	})

	t.Run("triggers", func(t *testing.T) {
		t.Parallel()
		assertUsesRequestedObjects(t,
			[]string{"EVENT_OBJECT_TABLE", "TRIGGER_NAME", "EVENT_MANIPULATION", "ACTION_TIMING"},
			[][]driver.Value{{"orders", "before_delete", "DELETE", "BEFORE"}},
			func(inspector *Inspector) (int, error) {
				facts, err := inspector.Triggers(t.Context(), []string{"orders"}, TriggerDelete)
				return len(facts), err
			},
		)
	})
}
