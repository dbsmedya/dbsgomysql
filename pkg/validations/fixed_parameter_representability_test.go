package validations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// unrepresentableSchema carries a character above U+FFFF. Comparing it against
// an information_schema name column raises error 3988,
// ER_IMPOSSIBLE_STRING_CONVERSION, rather than matching nothing — see
// representable and docs/COMPAT.md entry 8.
//
// Every case below binds an Inspector to this schema, backed by
// testsupport.OpenNoStatementDB, and requests an ordinary, non-empty table.
// Until each call site is guarded, the request reaches the database and the
// harness fails the test — that failure, not the assertions after it, is the
// mechanism these cases exist to prove: "absence instead of 3988" is also
// satisfied by code that issues the statement and swallows the error, and
// only a no-statement assertion tells the two apart.
const unrepresentableSchema = "supp_\U00010000"

// unrepresentableTable is unrepresentableSchema's counterpart for TableSpec's
// second fixed parameter. TableSpec.resolveTable compares both ref.schema and
// ref.table, and a guard that checks only the schema half passes every other
// case in this file while leaving the table half unguarded — this is the case
// that half would fail.
const unrepresentableTable = "orders_\U00010000"

func assertTableSpecAbsentError(t *testing.T, err error, schema, table string) {
	t.Helper()

	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("TableSpec: %v, want errors.Is(err, ErrTableNotFound)", err)
	}

	var objErr *ObjectError
	if !errors.As(err, &objErr) {
		t.Fatalf("TableSpec error has type %T, want *ObjectError", err)
	}
	if objErr.Op != opTableSpec {
		t.Errorf("ObjectError.Op = %q, want %q", objErr.Op, opTableSpec)
	}
	if objErr.Schema != schema || objErr.Table != table {
		t.Errorf("ObjectError names (%q, %q), want (%q, %q); an error must name "+
			"the object it concerns", objErr.Schema, objErr.Table, schema, table)
	}

	wantCause := fmt.Errorf("resolve table: %w", ErrTableNotFound)
	if objErr.Err == nil || objErr.Err.Error() != wantCause.Error() {
		t.Errorf("ObjectError.Err = %v, want %q", objErr.Err, wantCause)
	}
	want := newObjectError(opTableSpec, schema, table, wantCause)
	if err.Error() != want.Error() {
		t.Errorf("TableSpec error = %q, want resolved-and-absent error %q", err, want)
	}
}

func TestColumnsIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), unrepresentableSchema)
	got, err := inspector.Columns(t.Context(), []string{"orders"})
	if err != nil {
		t.Fatalf("Columns: %v, want a nil error", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Columns = %#v, want a non-nil empty slice", got)
	}
}

func TestInvisibleColumnsIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), unrepresentableSchema)
	got, err := inspector.InvisibleColumns(t.Context(), []string{"orders"})
	if err != nil {
		t.Fatalf("InvisibleColumns: %v, want a nil error", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("InvisibleColumns = %#v, want a non-nil empty slice", got)
	}
}

func TestTablesIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), unrepresentableSchema)
	got, err := inspector.Tables(t.Context(), []string{"orders"})
	if err != nil {
		t.Fatalf("Tables: %v, want a nil error", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Tables = %#v, want a non-nil empty slice", got)
	}
}

func TestPrimaryKeysIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), unrepresentableSchema)
	got, err := inspector.PrimaryKeys(t.Context(), []string{"orders"})
	if err != nil {
		t.Fatalf("PrimaryKeys: %v, want a nil error", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("PrimaryKeys = %#v, want a non-nil empty slice", got)
	}
}

func TestTriggersIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), unrepresentableSchema)
	got, err := inspector.Triggers(t.Context(), []string{"orders"}, TriggerDelete)
	if err != nil {
		t.Fatalf("Triggers: %v, want a nil error", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Triggers = %#v, want a non-nil empty slice", got)
	}
}

func TestTableSpecIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), unrepresentableSchema)
	_, err := inspector.TableSpec(t.Context(), Ref(unrepresentableSchema, "orders"))
	assertTableSpecAbsentError(t, err, unrepresentableSchema, "orders")
}

// TestTableSpecIssuesNoStatementForUnrepresentableTable is the companion the
// schema-only case above cannot stand in for. resolveTable checks
// !representable(ref.schema) || !representable(ref.table); a schema that is
// always unrepresentable in every case never exercises the table half of that
// OR, so an implementation missing "|| !representable(ref.table)" would pass
// every other case in this file while leaving the table half unguarded and
// untested.
func TestTableSpecIssuesNoStatementForUnrepresentableTable(t *testing.T) {
	t.Parallel()

	const schema = "sakila"

	inspector := NewInspector(testsupport.OpenNoStatementDB(t), schema)
	_, err := inspector.TableSpec(t.Context(), Ref(schema, unrepresentableTable))
	assertTableSpecAbsentError(t, err, schema, unrepresentableTable)
}

// TestForeignKeysFallbackIssuesNoStatementForUnrepresentableSchema is
// ForeignKeys' counterpart to the seven cases above, and cannot share their
// harness. ForeignKeys must issue the InnoDB query regardless of
// representability because VisibilityComplete means "the
// PROCESS-gated InnoDB query succeeded," and a skipped query could not
// honestly claim that. testsupport.OpenNoStatementDB forbids every statement,
// so it cannot express "the first query must run and the second must not."
//
// The queryScript harness in foreign_keys_source_test.go can: scripted with
// exactly one step — the failing InnoDB query — a second (standard-source)
// query has no step left to answer, and queryScript.query returns its own
// "unexpected query" error rather than silently succeeding. That error
// propagates through foreignKeysStandard and back out of ForeignKeys as part
// of the joined primary+fallback error, which is what fails this test today.
//
// This is deliberately not a result assertion. An empty fallback result is
// exactly what "the fallback ran and matched nothing" looks like, so checking
// only Keys and Visibility would pass against an unguarded implementation
// whose fallback query happened to return no rows. Only "no second query was
// issued" — proven here by the script running out of steps — separates the
// guard from that coincidence.
func TestForeignKeysFallbackIssuesNoStatementForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("PROCESS denied")
	script := &queryScript{steps: []queryStep{
		{contains: "INNODB_FOREIGN AS f", err: primaryErr},
	}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, unrepresentableSchema).ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys: %v, want a nil error", err)
	}
	if result.Keys != nil {
		t.Errorf("ForeignKeys Keys = %#v, want nil", result.Keys)
	}
	if result.Visibility != VisibilityUnconfirmed {
		t.Errorf("ForeignKeys Visibility = %s, want VisibilityUnconfirmed", result.Visibility)
	}
	assertPrimaryQueryDowngrade(t, result, primaryErr)
	script.assertDone(t)
}

// TestForeignKeysInnoDBSuccessIsCompleteDespiteUnrepresentableSchema is a
// regression guard, not test-first evidence: it already passes, before any
// guard exists, because ForeignKeys returns on primary success without
// attempting the fallback regardless of schema representability. It stays
// here so that step 4b's guard at foreignKeysStandard — which this case never
// reaches — cannot be hoisted somewhere that also suppresses a successful
// InnoDB read. That read must keep running unconditionally: a skipped query
// could not honestly claim VisibilityComplete, and foreignKeysStandard is the
// only source short-circuited for an unrepresentable schema.
func TestForeignKeysInnoDBSuccessIsCompleteDespiteUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	script := &queryScript{steps: []queryStep{{
		contains: "INNODB_FOREIGN AS f",
		columns: []string{
			"ID", "FOR_NAME", "REF_NAME", "N_COLS", "TYPE",
			"FOR_COL_NAME", "REF_COL_NAME", "POS",
		},
	}}}
	db := openScriptedDB(t, script)

	result, err := NewInspector(db, unrepresentableSchema).ForeignKeys(
		t.Context(),
		IncomingTo("orders"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys: %v", err)
	}
	if result.Keys != nil {
		t.Errorf("ForeignKeys Keys = %#v, want nil", result.Keys)
	}
	if result.Visibility != VisibilityComplete {
		t.Errorf("ForeignKeys Visibility = %s, want complete", result.Visibility)
	}
	assertNoForeignKeyDowngrade(t, result)
	script.assertDone(t)
}

// The cases below are precedence guards, written after every guard above was
// already wired — they are regression guards, not test-first evidence. Each
// combines an unrepresentable schema with a condition that must still win, and
// asserts that the other outcome still happens: the guard must never become
// the first thing a caller hits.

// TestTriggersInvalidEventWinsOverUnrepresentableSchema pins precedence:
// checks_triggers.go checks the event before the guard, so an invalid event
// must still report ErrInvalidTriggerEvent even when the schema is one the
// guard would otherwise skip querying for. A guard hoisted above that check
// would instead return an empty result and swallow the invalid event
// silently.
func TestTriggersInvalidEventWinsOverUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, unrepresentableSchema)
	_, err := inspector.Triggers(t.Context(), []string{"orders"}, TriggerEvent(99))
	assertObjectErrorCause(t, err, ErrInvalidTriggerEvent, opTriggers, unrepresentableSchema)
}

// TestTableSpecInvalidRefWinsOverUnrepresentableSchema pins precedence: a
// TableRef that is invalid for its own reason (here, an empty table) must
// still report ErrInvalidTableRef even when its schema half is also one the
// guard would otherwise treat as absent. TableSpec checks ref.valid() before
// resolveTable — where the guard lives — ever runs, so a guard hoisted above
// that check would instead report ErrTableNotFound: a different, misleading
// cause for what is actually an invalid argument.
func TestTableSpecInvalidRefWinsOverUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, "shop")
	_, err := inspector.TableSpec(t.Context(), Ref(unrepresentableSchema, ""))
	if !errors.Is(err, ErrInvalidTableRef) {
		t.Fatalf("TableSpec: %v, want errors.Is(err, ErrInvalidTableRef)", err)
	}
	if errors.Is(err, ErrTableNotFound) {
		t.Errorf("TableSpec: %v, also matches ErrTableNotFound — the guard must not "+
			"produce the resolved-and-absent cause in place of the ref-validation one", err)
	}
}

// TestForeignKeysInvalidSelectorWinsOverUnrepresentableSchema pins
// precedence: ForeignKeys checks the selector before either source runs, so
// an invalid selector must still report ErrInvalidFKSelector even when the
// Inspector's schema is one the standard-source guard would otherwise skip
// querying for.
func TestForeignKeysInvalidSelectorWinsOverUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, unrepresentableSchema)
	_, err := inspector.ForeignKeys(t.Context(), FKSelector{})
	assertObjectErrorCause(t, err, ErrInvalidFKSelector, opForeignKeys, unrepresentableSchema)
}

// TestFactsEmptyInputReturnsNilForUnrepresentableSchema pins precedence for
// the empty-input early return each of the five facts has ahead of its guard.
// An empty request against an unrepresentable schema must still produce the
// existing nil slice, nil error — not an empty slice.
//
// Checking only err == nil is not enough to tell the two apart: a guard
// hoisted above the empty-input return would still run the assembly loop over
// an accumulator that never got populated, producing a non-nil empty slice
// with no error attached to reveal the difference. Only asserting the slice
// itself is nil separates the two.
func TestFactsEmptyInputReturnsNilForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, unrepresentableSchema)
	calls := []struct {
		name string
		call func(t *testing.T)
	}{
		{
			name: "columns",
			call: func(t *testing.T) {
				got, err := inspector.Columns(t.Context(), nil)
				if err != nil {
					t.Fatalf("Columns: %v, want a nil error", err)
				}
				if got != nil {
					t.Errorf("Columns = %#v, want nil, not merely an empty slice", got)
				}
			},
		},
		{
			name: "invisible columns",
			call: func(t *testing.T) {
				got, err := inspector.InvisibleColumns(t.Context(), nil)
				if err != nil {
					t.Fatalf("InvisibleColumns: %v, want a nil error", err)
				}
				if got != nil {
					t.Errorf("InvisibleColumns = %#v, want nil, not merely an empty slice", got)
				}
			},
		},
		{
			name: "tables",
			call: func(t *testing.T) {
				got, err := inspector.Tables(t.Context(), nil)
				if err != nil {
					t.Fatalf("Tables: %v, want a nil error", err)
				}
				if got != nil {
					t.Errorf("Tables = %#v, want nil, not merely an empty slice", got)
				}
			},
		},
		{
			name: "primary keys",
			call: func(t *testing.T) {
				got, err := inspector.PrimaryKeys(t.Context(), nil)
				if err != nil {
					t.Fatalf("PrimaryKeys: %v, want a nil error", err)
				}
				if got != nil {
					t.Errorf("PrimaryKeys = %#v, want nil, not merely an empty slice", got)
				}
			},
		},
		{
			name: "triggers",
			call: func(t *testing.T) {
				got, err := inspector.Triggers(t.Context(), nil, TriggerDelete)
				if err != nil {
					t.Fatalf("Triggers: %v, want a nil error", err)
				}
				if got != nil {
					t.Errorf("Triggers = %#v, want nil, not merely an empty slice", got)
				}
			},
		},
	}

	for _, call := range calls {
		t.Run(call.name, call.call)
	}
}

// TestFactsNilQuerierWinsOverUnrepresentableSchema pins precedence for the
// first rung of the Inspector.validate ladder: a nil Querier must still
// report ErrNilQuerier even when the Inspector's schema is one the guard
// would otherwise treat as absent. i.validate runs before the guard at every
// site sharing this shape, so a guard hoisted above it would instead return
// success silently, without the caller ever being told there is no
// connection to query.
//
// This intentionally has no "empty schema" sibling here or in the case below.
// representable treats an empty name as representable — there is no rune to
// reject — so a schema can never be simultaneously empty and unrepresentable.
// The two conditions this precedence table combines cannot occur together for
// that rung, and the empty-schema rung's position in the ladder is therefore
// unaffected by where this guard sits; it is already covered, without the
// guard in play at all, by TestArgumentValidation in inspector_test.go.
func TestFactsNilQuerierWinsOverUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(nil, unrepresentableSchema)
	calls := []struct {
		name string
		call func(t *testing.T)
	}{
		{
			name: "columns",
			call: func(t *testing.T) {
				_, err := inspector.Columns(t.Context(), []string{"orders"})
				assertObjectErrorCause(t, err, ErrNilQuerier, opColumns, unrepresentableSchema)
			},
		},
		{
			name: "invisible columns",
			call: func(t *testing.T) {
				_, err := inspector.InvisibleColumns(t.Context(), []string{"orders"})
				assertObjectErrorCause(t, err, ErrNilQuerier, opInvisibleColumns, unrepresentableSchema)
			},
		},
		{
			name: "tables",
			call: func(t *testing.T) {
				_, err := inspector.Tables(t.Context(), []string{"orders"})
				assertObjectErrorCause(t, err, ErrNilQuerier, opTables, unrepresentableSchema)
			},
		},
		{
			name: "primary keys",
			call: func(t *testing.T) {
				_, err := inspector.PrimaryKeys(t.Context(), []string{"orders"})
				assertObjectErrorCause(t, err, ErrNilQuerier, opPrimaryKeys, unrepresentableSchema)
			},
		},
		{
			name: "triggers",
			call: func(t *testing.T) {
				_, err := inspector.Triggers(t.Context(), []string{"orders"}, TriggerDelete)
				assertObjectErrorCause(t, err, ErrNilQuerier, opTriggers, unrepresentableSchema)
			},
		},
	}

	for _, call := range calls {
		t.Run(call.name, call.call)
	}
}

// TestFactsEmptyTableNameWinsOverUnrepresentableSchema pins precedence for the
// last rung of the Inspector.validate ladder: an empty table name inside an
// otherwise non-empty request must still report ErrEmptyTableName even when
// the Inspector's schema is one the guard would otherwise treat as absent.
// i.validate's table-name loop runs before the guard at every site sharing
// this shape, so a guard hoisted above it would instead return success
// silently, treating the empty name as simply absent rather than invalid.
func TestFactsEmptyTableNameWinsOverUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, unrepresentableSchema)
	calls := []struct {
		name string
		call func(t *testing.T)
	}{
		{
			name: "columns",
			call: func(t *testing.T) {
				_, err := inspector.Columns(t.Context(), []string{"orders", ""})
				assertObjectErrorCause(t, err, ErrEmptyTableName, opColumns, unrepresentableSchema)
			},
		},
		{
			name: "invisible columns",
			call: func(t *testing.T) {
				_, err := inspector.InvisibleColumns(t.Context(), []string{"orders", ""})
				assertObjectErrorCause(t, err, ErrEmptyTableName, opInvisibleColumns, unrepresentableSchema)
			},
		},
		{
			name: "tables",
			call: func(t *testing.T) {
				_, err := inspector.Tables(t.Context(), []string{"orders", ""})
				assertObjectErrorCause(t, err, ErrEmptyTableName, opTables, unrepresentableSchema)
			},
		},
		{
			name: "primary keys",
			call: func(t *testing.T) {
				_, err := inspector.PrimaryKeys(t.Context(), []string{"orders", ""})
				assertObjectErrorCause(t, err, ErrEmptyTableName, opPrimaryKeys, unrepresentableSchema)
			},
		},
		{
			name: "triggers",
			call: func(t *testing.T) {
				_, err := inspector.Triggers(t.Context(), []string{"orders", ""}, TriggerDelete)
				assertObjectErrorCause(t, err, ErrEmptyTableName, opTriggers, unrepresentableSchema)
			},
		},
	}

	for _, call := range calls {
		t.Run(call.name, call.call)
	}
}

// TestColumnsCanceledContextIsNotSurfacedForUnrepresentableSchema pins a
// decision, not a defect. Skipping the statement also skips the only place
// this package ever observes context cancellation: inside the QueryContext
// call the guard bypasses. An already-canceled context passed to a call the
// guard short-circuits is therefore never consulted, and the call succeeds
// exactly as it would with a live one. Columns stands in for every site
// sharing this shape — the guard sits in the same position, immediately
// before the only ctx-consuming statement, at each of them.
func TestColumnsCanceledContextIsNotSurfacedForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	inspector := NewInspector(panicQuerier{}, unrepresentableSchema)
	got, err := inspector.Columns(ctx, []string{"orders"})
	if err != nil {
		t.Fatalf("Columns: %v, want a nil error — the guard short-circuits "+
			"before ctx is ever consulted", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Columns = %#v, want a non-nil empty slice", got)
	}
}

// failingQuerier is a caller's own Querier implementation that fails every
// call, standing in for a caller who wraps Querier to inject failures or
// count statements. Unlike testsupport.OpenNoStatementDB, it does not fail
// the test when called — it returns an ordinary error — because the case
// below asserts that the error is never seen, not merely that a harness would
// catch the attempt.
type failingQuerier struct{ err error }

func (f failingQuerier) QueryContext(
	context.Context, string, ...any,
) (*sql.Rows, error) {
	return nil, f.err
}

func (f failingQuerier) QueryRowContext(
	context.Context, string, ...any,
) *sql.Row {
	panic("QueryRowContext: no guarded call site in this file uses it")
}

// TestColumnsFailingQuerierIsNotSurfacedForUnrepresentableSchema pins a
// decision, not a defect. Skipping the statement also skips whatever a
// caller's own Querier would have done with it, including fail: a Querier
// configured to fail every call never gets the chance to, because the guard
// returns before QueryContext is invoked.
func TestColumnsFailingQuerierIsNotSurfacedForUnrepresentableSchema(t *testing.T) {
	t.Parallel()

	querier := failingQuerier{err: errors.New("querier configured to fail")}
	inspector := NewInspector(querier, unrepresentableSchema)
	got, err := inspector.Columns(t.Context(), []string{"orders"})
	if err != nil {
		t.Fatalf("Columns: %v, want a nil error — the guard short-circuits "+
			"before the querier is ever consulted", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Columns = %#v, want a non-nil empty slice", got)
	}
}
