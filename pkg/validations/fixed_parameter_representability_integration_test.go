//go:build integration

package validations_test

import (
	"errors"
	"testing"

	"github.com/dbsmedya/dbsgomysql/pkg/validations"
)

// These cases measure the fixed-parameter guard's public-API behavior against
// a real server, complementing the unit-level mechanism and precedence cases
// in pkg/validations' own fixed_parameter_representability_test.go, which a
// scripted or no-statement Querier cannot: they prove the guard survives a
// real driver and connection, not only a scripted one, and — before the
// guard — they fail against the real error the guard exists to avoid.
//
// supplementaryName and noProcessConn are declared in
// predicate_integration_test.go, in this same package, and are reused as-is:
// both already carry exactly the shape these cases need.
//
// Only the supplementary case is covered here, deliberately. An invalid-UTF-8
// name already returns absence and a nil error on all three servers without
// this guard, so an integration test for it would pass on main and prove
// nothing; that half is pinned at the unit level, which can observe the
// decision instead of the outcome.

// TestFactsReportAbsenceForUnrepresentableSchemaIntegration pins the first
// row: an Inspector bound to a schema MySQL cannot store returns absence, not
// an error, for each of the five facts. Fails on main with 3988
// (ER_IMPOSSIBLE_STRING_CONVERSION) — measured on 8.0.46, 8.4.9 and 9.7.1; see
// docs/COMPAT.md entry 8.
func TestFactsReportAbsenceForUnrepresentableSchemaIntegration(t *testing.T) {
	db, _ := validationDatabase(t)
	inspector := validations.NewInspector(db, supplementaryName)
	requested := []string{"clean_table"}

	columns, err := inspector.Columns(t.Context(), requested)
	if err != nil {
		t.Errorf("Columns: %v, want a nil error — absence is not a failure", err)
	}
	if len(columns) != 0 {
		t.Errorf("Columns = %#v, want empty", columns)
	}

	invisible, err := inspector.InvisibleColumns(t.Context(), requested)
	if err != nil {
		t.Errorf("InvisibleColumns: %v, want a nil error", err)
	}
	if len(invisible) != 0 {
		t.Errorf("InvisibleColumns = %#v, want empty", invisible)
	}

	tables, err := inspector.Tables(t.Context(), requested)
	if err != nil {
		t.Errorf("Tables: %v, want a nil error", err)
	}
	if len(tables) != 0 {
		t.Errorf("Tables = %#v, want empty", tables)
	}

	pks, err := inspector.PrimaryKeys(t.Context(), requested)
	if err != nil {
		t.Errorf("PrimaryKeys: %v, want a nil error", err)
	}
	if len(pks) != 0 {
		t.Errorf("PrimaryKeys = %#v, want empty", pks)
	}

	triggers, err := inspector.Triggers(t.Context(), requested, validations.TriggerDelete)
	if err != nil {
		t.Errorf("Triggers: %v, want a nil error", err)
	}
	if len(triggers) != 0 {
		t.Errorf("Triggers = %#v, want empty", triggers)
	}
}

// TestTableSpecReportsTableNotFoundForUnrepresentableRefIntegration pins the
// second row: a supplementary schema, and separately a supplementary table,
// both resolve to ErrTableNotFound rather than a server error. Both fail on
// main with 3988 — TableSpec's resolveTable compares both ref.schema and
// ref.table in Go, and both were measured raising it.
func TestTableSpecReportsTableNotFoundForUnrepresentableRefIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)

	_, err := inspector.TableSpec(t.Context(), validations.Ref(supplementaryName, "clean_table"))
	if !errors.Is(err, validations.ErrTableNotFound) {
		t.Errorf("TableSpec for a supplementary schema returned %v, want ErrTableNotFound", err)
	}

	_, err = inspector.TableSpec(t.Context(), validations.Ref(schema, supplementaryName))
	if !errors.Is(err, validations.ErrTableNotFound) {
		t.Errorf("TableSpec for a supplementary table returned %v, want ErrTableNotFound", err)
	}
}

// TestForeignKeysStandardFallbackReportsUnconfirmedForUnrepresentableSchemaIntegration
// pins the third and fourth rows together, because both describe the same
// request answered by different sources. With PROCESS, the InnoDB source is
// unguarded (see foreign_keys.go's own comment on why) and still reports
// VisibilityComplete for a supplementary schema — narrowNames rejects the
// composed schema/table value, the predicate drops, and the unnarrowed
// whole-server read is filtered in Go, so nothing about this guard leaks
// upward into that source. Without PROCESS, the standard source is what the
// guard protects, and answers with an honest VisibilityUnconfirmed instead of
// what main returns today: a joined 1227 (PROCESS denied) and 3988
// (ER_IMPOSSIBLE_STRING_CONVERSION) error.
func TestForeignKeysStandardFallbackReportsUnconfirmedForUnrepresentableSchemaIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	complete, err := validations.NewInspector(db, supplementaryName).ForeignKeys(
		t.Context(),
		validations.IncomingTo("clean_table"),
	)
	if err != nil {
		t.Errorf("ForeignKeys with PROCESS: %v, want a nil error", err)
	}
	if complete.Keys != nil {
		t.Errorf("ForeignKeys with PROCESS Keys = %#v, want nil", complete.Keys)
	}
	if complete.Visibility != validations.VisibilityComplete {
		t.Errorf(
			"visibility = %s, want complete — the InnoDB source must not be "+
				"suppressed by the standard-source guard",
			complete.Visibility,
		)
	}

	unconfirmed, err := validations.NewInspector(
		noProcessConn(t, db, schema), supplementaryName,
	).ForeignKeys(t.Context(), validations.IncomingTo("clean_table"))
	if err != nil {
		t.Errorf("ForeignKeys without PROCESS: %v, want a nil error", err)
	}
	if unconfirmed.Keys != nil {
		t.Errorf("ForeignKeys without PROCESS Keys = %#v, want nil", unconfirmed.Keys)
	}
	if unconfirmed.Visibility != validations.VisibilityUnconfirmed {
		t.Errorf(
			"visibility = %s, want unconfirmed — the standard-source guard must "+
				"still report that the fallback ran",
			unconfirmed.Visibility,
		)
	}
}
