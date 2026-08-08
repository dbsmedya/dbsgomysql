package validations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestForeignKeysPrimarySuccessIsComplete(t *testing.T) {
	t.Parallel()

	script := &queryScript{steps: []queryStep{{
		contains: "INNODB_FOREIGN AS f",
		columns: []string{
			"ID", "FOR_NAME", "REF_NAME", "N_COLS", "TYPE",
			"FOR_COL_NAME", "REF_COL_NAME", "POS",
		},
		rows: [][]driver.Value{
			{"shop/fk_items_orders", "shop/items", "shop/orders", int64(2), int64(9), "tenant_id", "tenant_id", int64(1)},
			{"shop/fk_items_orders", "shop/items", "shop/orders", int64(2), int64(9), "order_id", "id", int64(2)},
		},
	}}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys primary: %v", err)
	}
	want := ForeignKeyResult{
		Keys: []ForeignKey{{
			ConstraintName: "fk_items_orders",
			ChildSchema:    "shop",
			ChildTable:     "items",
			ChildColumns:   []string{"tenant_id", "order_id"},
			ParentSchema:   "shop",
			ParentTable:    "orders",
			ParentColumns:  []string{"tenant_id", "id"},
			OnDelete:       "CASCADE",
			OnUpdate:       "SET NULL",
			Indexed:        true,
		}},
		Visibility: VisibilityComplete,
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ForeignKeys primary = %#v, want %#v", result, want)
	}
	script.assertDone(t)
}

func TestForeignKeysPrimaryEmptyDoesNotFallThrough(t *testing.T) {
	t.Parallel()

	script := &queryScript{steps: []queryStep{{
		contains: "INNODB_FOREIGN AS f",
		columns: []string{
			"ID", "FOR_NAME", "REF_NAME", "N_COLS", "TYPE",
			"FOR_COL_NAME", "REF_COL_NAME", "POS",
		},
	}}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys empty primary: %v", err)
	}
	want := ForeignKeyResult{Visibility: VisibilityComplete}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ForeignKeys empty primary = %#v, want %#v", result, want)
	}
	script.assertDone(t)
}

func TestForeignKeysFallbackIsUnconfirmedAndMatchesIndexes(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("PROCESS denied")
	script := &queryScript{steps: []queryStep{
		{contains: "INNODB_FOREIGN AS f", err: primaryErr},
		{
			contains: "KEY_COLUMN_USAGE AS kcu",
			columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME", "DELETE_RULE", "UPDATE_RULE",
				"ORDINAL_POSITION",
			},
			rows: [][]driver.Value{
				{"shop", "items", "fk_items_orders", "tenant_id", "shop", "orders", "tenant_id", "RESTRICT", "NO ACTION", int64(1)},
				{"shop", "items", "fk_items_orders", "order_id", "shop", "orders", "id", "RESTRICT", "NO ACTION", int64(2)},
			},
		},
		{
			contains: "information_schema.STATISTICS",
			columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "COLUMN_NAME", "SEQ_IN_INDEX",
			},
			rows: [][]driver.Value{
				{"shop", "items", "idx_fk", "tenant_id", int64(1)},
				{"shop", "items", "idx_fk", "order_id", int64(2)},
			},
		},
	}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys fallback: %v", err)
	}
	if result.Visibility != VisibilityUnconfirmed {
		t.Errorf("fallback visibility = %s, want unconfirmed", result.Visibility)
	}
	if len(result.Keys) != 1 || !result.Keys[0].Indexed {
		t.Errorf("fallback keys = %#v, want one indexed composite", result.Keys)
	}
	script.assertDone(t)
}

// fallbackScriptWithIndexes builds the three steps every functional-key-part
// case shares: the PROCESS-gated primary source fails, the standard source
// returns one composite constraint on shop.items, and information_schema.
// STATISTICS returns the caller's index rows. Only those rows differ between
// cases, which is what keeps each case's subject visible.
func fallbackScriptWithIndexes(statistics [][]driver.Value) *queryScript {
	return &queryScript{steps: []queryStep{
		{contains: "INNODB_FOREIGN AS f", err: errors.New("PROCESS denied")},
		{
			contains: "KEY_COLUMN_USAGE AS kcu",
			columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME", "DELETE_RULE", "UPDATE_RULE",
				"ORDINAL_POSITION",
			},
			rows: [][]driver.Value{
				{"shop", "items", "fk_items_orders", "tenant_id", "shop", "orders", "tenant_id", "RESTRICT", "NO ACTION", int64(1)},
				{"shop", "items", "fk_items_orders", "order_id", "shop", "orders", "id", "RESTRICT", "NO ACTION", int64(2)},
			},
		},
		{
			contains: "information_schema.STATISTICS",
			columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "COLUMN_NAME", "SEQ_IN_INDEX",
			},
			rows: statistics,
		},
	}}
}

// fallbackKeyOnItems is the constraint fallbackScriptWithIndexes describes.
// Only Indexed varies between cases, so comparing the whole value guards the
// grouping and the identity rather than just the flag under test.
func fallbackKeyOnItems(indexed bool) ForeignKey {
	return ForeignKey{
		ConstraintName: "fk_items_orders",
		ChildSchema:    "shop",
		ChildTable:     "items",
		ChildColumns:   []string{"tenant_id", "order_id"},
		ParentSchema:   "shop",
		ParentTable:    "orders",
		ParentColumns:  []string{"tenant_id", "id"},
		OnDelete:       fkRuleRestrict,
		OnUpdate:       fkRuleNoAction,
		Indexed:        indexed,
	}
}

func assertFallbackKey(t *testing.T, result ForeignKeyResult, err error, indexed bool) {
	t.Helper()

	if err != nil {
		t.Fatalf("ForeignKeys fallback: %v", err)
	}
	want := ForeignKeyResult{
		Keys:       []ForeignKey{fallbackKeyOnItems(indexed)},
		Visibility: VisibilityUnconfirmed,
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ForeignKeys fallback = %#v, want %#v", result, want)
	}
}

// A functional key part reports STATISTICS.COLUMN_NAME as NULL. Reading it into
// a plain string aborts the whole fallback, so one functional index anywhere on
// a child table used to turn a legitimate no-PROCESS inspection into a hard
// failure. See docs/COMPAT.md entry 18.
func TestForeignKeysFallbackToleratesFunctionalIndexPart(t *testing.T) {
	t.Parallel()

	// idx_fk sorts before idx_func under the query's ORDER BY INDEX_NAME, which
	// is the order a real server returns these rows in.
	script := fallbackScriptWithIndexes([][]driver.Value{
		{"shop", "items", "idx_fk", "tenant_id", int64(1)},
		{"shop", "items", "idx_fk", "order_id", int64(2)},
		{"shop", "items", "idx_func", nil, int64(1)},
	})
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(t.Context(), IncomingTo("orders"))
	assertFallbackKey(t, result, err, true)
	script.assertDone(t)
}

// A functional part in the leading slot means the foreign key's columns are not
// the index's first columns, so the index cannot support the constraint.
func TestForeignKeysFallbackFunctionalLeadingPartIsNotSupporting(t *testing.T) {
	t.Parallel()

	script := fallbackScriptWithIndexes([][]driver.Value{
		{"shop", "items", "idx_mixed", nil, int64(1)},
		{"shop", "items", "idx_mixed", "tenant_id", int64(2)},
		{"shop", "items", "idx_mixed", "order_id", int64(3)},
	})
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(t.Context(), IncomingTo("orders"))
	assertFallbackKey(t, result, err, false)
	script.assertDone(t)
}

// The sharpest case for recording a NULL part as "" rather than dropping it.
// Here both foreign-key columns are present and in order, separated only by an
// expression, so dropping the NULL row would yield an exact match instead of a
// mere prefix — the strongest false "supported" this code could report.
func TestForeignKeysFallbackInteriorFunctionalPartBreaksThePrefix(t *testing.T) {
	t.Parallel()

	script := fallbackScriptWithIndexes([][]driver.Value{
		{"shop", "items", "idx_interior", "tenant_id", int64(1)},
		{"shop", "items", "idx_interior", nil, int64(2)},
		{"shop", "items", "idx_interior", "order_id", int64(3)},
	})
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(t.Context(), IncomingTo("orders"))
	assertFallbackKey(t, result, err, false)
	script.assertDone(t)
}

func TestForeignKeysPrimaryDecodeErrorFallsBack(t *testing.T) {
	t.Parallel()

	script := &queryScript{steps: []queryStep{
		{
			contains: "INNODB_FOREIGN AS f",
			columns: []string{
				"ID", "FOR_NAME", "REF_NAME", "N_COLS", "TYPE",
				"FOR_COL_NAME", "REF_COL_NAME", "POS",
			},
			rows: [][]driver.Value{
				{"shop/fk_bad", "shop/items", "shop/orders", int64(1), int64(64), "order_id", "id", int64(1)},
			},
		},
		{
			contains: "KEY_COLUMN_USAGE AS kcu",
			columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME", "DELETE_RULE", "UPDATE_RULE",
				"ORDINAL_POSITION",
			},
		},
	}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys decode fallback: %v", err)
	}
	if result.Visibility != VisibilityUnconfirmed || result.Keys != nil {
		t.Errorf("decode fallback result = %#v, want empty unconfirmed", result)
	}
	script.assertDone(t)
}

func TestForeignKeysBothSourcesFailPreservesCauses(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("primary failed")
	fallbackErr := errors.New("fallback failed")
	script := &queryScript{steps: []queryStep{
		{contains: "INNODB_FOREIGN AS f", err: primaryErr},
		{contains: "KEY_COLUMN_USAGE AS kcu", err: fallbackErr},
	}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, "shop").ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if !reflect.DeepEqual(result, ForeignKeyResult{}) {
		t.Errorf("failed result = %#v, want zero", result)
	}
	if !errors.Is(err, primaryErr) || !errors.Is(err, fallbackErr) {
		t.Errorf("joined error %v does not preserve both causes", err)
	}
	var objectErr *ObjectError
	if !errors.As(err, &objectErr) || objectErr.Op != opForeignKeys {
		t.Errorf("error = %#v, want foreign_keys ObjectError", err)
	}
	script.assertDone(t)
}

func TestSelectorOrderingDuplicatesAndCompositeGrouping(t *testing.T) {
	t.Parallel()

	keys := []ForeignKey{
		{
			ConstraintName: "z", ChildSchema: "cross", ChildTable: "items",
			ParentSchema: "shop", ParentTable: "orders",
			ChildColumns: []string{"tenant_id", "order_id"},
		},
		{
			ConstraintName: "a", ChildSchema: "shop", ChildTable: "audit",
			ParentSchema: "shop", ParentTable: "orders",
		},
	}
	got := selectForeignKeys(keys, "shop", IncomingTo("orders", "orders"))
	if len(got) != 4 {
		t.Fatalf("selected duplicates = %d, want 4: %#v", len(got), got)
	}
	wantNames := []string{"z", "a", "z", "a"}
	names := make([]string, 0, len(got))
	for _, key := range got {
		names = append(names, key.ConstraintName)
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("selected order = %v, want %v", names, wantNames)
	}
	if len(got[0].ChildColumns) != 2 {
		t.Errorf("composite constraint split into columns: %#v", got)
	}
}

type queryStep struct {
	contains string
	// lacks, when set, must not appear in the statement. Narrowed and
	// unnarrowed forms of a query differ by a removed clause, so the
	// unnarrowed one has no fragment of its own to match positively.
	lacks   string
	columns []string
	rows    [][]driver.Value
	err     error
}

type queryScript struct {
	mu    sync.Mutex
	steps []queryStep
	next  int
}

func (s *queryScript) query(query string) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.next >= len(s.steps) {
		return nil, errors.New("unexpected query: " + query)
	}
	step := s.steps[s.next]
	s.next++
	if !strings.Contains(query, step.contains) {
		return nil, errors.New("query does not contain " + step.contains + ": " + query)
	}
	if step.lacks != "" && strings.Contains(query, step.lacks) {
		return nil, errors.New("query unexpectedly contains " + step.lacks + ": " + query)
	}
	if step.err != nil {
		return nil, step.err
	}

	return &scriptedRows{columns: step.columns, rows: step.rows}, nil
}

func (s *queryScript) assertDone(t *testing.T) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next != len(s.steps) {
		t.Errorf("script consumed %d of %d query steps", s.next, len(s.steps))
	}
}

type scriptedConnector struct {
	script *queryScript
}

func (c scriptedConnector) Connect(context.Context) (driver.Conn, error) {
	return scriptedConn(c), nil
}

func (c scriptedConnector) Driver() driver.Driver {
	return scriptedDriver(c)
}

type scriptedDriver struct {
	script *queryScript
}

func (d scriptedDriver) Open(string) (driver.Conn, error) {
	return scriptedConn(d), nil
}

type scriptedConn struct {
	script *queryScript
}

func (c scriptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare is not supported")
}

func (c scriptedConn) Close() error {
	return nil
}

func (c scriptedConn) Begin() (driver.Tx, error) {
	return nil, errors.New("Begin is not supported")
}

func (c scriptedConn) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	return c.script.query(query)
}

type scriptedRows struct {
	columns []string
	rows    [][]driver.Value
	next    int
}

func (r *scriptedRows) Columns() []string {
	return r.columns
}

func (r *scriptedRows) Close() error {
	return nil
}

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++

	return nil
}

func openScriptedDB(t *testing.T, script *queryScript) *sql.DB {
	t.Helper()

	db := sql.OpenDB(scriptedConnector{script: script})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	return db
}
