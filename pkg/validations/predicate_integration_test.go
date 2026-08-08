//go:build integration

package validations_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
	"github.com/dbsmedya/dbsgomysql/pkg/validations"
)

// supplementaryName carries a character above U+FFFF. Comparing it against an
// information_schema name column raises error 3988 rather than matching
// nothing, so before the representability guard every call below returned an
// error where its own documentation promises absence. Measured on 8.0.46,
// 8.4.9 and 9.7.1; see docs/COMPAT.md entry 8.
const supplementaryName = "supp_\U00010000"

// Only the supplementary case is covered here, deliberately. An invalid-UTF-8
// name already returns absence and a nil error on all three servers — measured
// alongside the above — so an integration test for it would pass without the
// guard and prove nothing. Its half of the guard is pinned by the unit tests,
// which can observe the decision instead of the outcome.

// noProcessConn returns a connection for an account holding SELECT on one
// table and nothing else, so information_schema.INNODB_FOREIGN is unreadable
// and ForeignKeys must answer from the standard source.
func noProcessConn(t *testing.T, admin *sql.DB, schema string) *sql.Conn {
	t.Helper()

	account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_predicate")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON "+sqlutil.QuoteQualified(schema, "fk_internal_child")+
			" TO "+testsupport.GrantAccountSQL(account),
	)

	return testsupport.MySQLConnAs(t, account)
}

func TestPredicateGuardReportsAbsenceIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)

	columns, err := inspector.Columns(t.Context(), []string{supplementaryName})
	if err != nil {
		t.Errorf("Columns: %v, want a nil error — absence is not a failure", err)
	}
	if len(columns) != 0 {
		t.Errorf("Columns = %#v, want empty", columns)
	}

	// Both foreign-key sources must be reached, and Visibility is the only
	// observable that says which one answered. A PROCESS-holding connection
	// returns from the InnoDB source before the standard source is touched,
	// so one call cannot exercise both.
	complete, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom(supplementaryName),
	)
	if err != nil {
		t.Errorf("ForeignKeys with PROCESS: %v, want a nil error", err)
	}
	if len(complete.Keys) != 0 {
		t.Errorf("ForeignKeys with PROCESS = %#v, want empty", complete.Keys)
	}
	if complete.Visibility != validations.VisibilityComplete {
		t.Errorf(
			"visibility = %s, want complete — the InnoDB source did not answer, "+
				"so this case did not measure INNODB_FOREIGN.FOR_NAME",
			complete.Visibility,
		)
	}

	unconfirmed, err := validations.NewInspector(
		noProcessConn(t, db, schema), schema,
	).ForeignKeys(t.Context(), validations.OutgoingFrom(supplementaryName))
	if err != nil {
		t.Errorf("ForeignKeys without PROCESS: %v, want a nil error", err)
	}
	if len(unconfirmed.Keys) != 0 {
		t.Errorf("ForeignKeys without PROCESS = %#v, want empty", unconfirmed.Keys)
	}
	if unconfirmed.Visibility != validations.VisibilityUnconfirmed {
		t.Errorf(
			"visibility = %s, want unconfirmed — the standard source did not answer, "+
				"so this case did not measure KEY_COLUMN_USAGE.TABLE_NAME",
			unconfirmed.Visibility,
		)
	}
}

func TestPredicateFallbackMatchesNarrowedResultIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)

	// One unrepresentable name unnarrows the whole call. What the other
	// requested names return must not change, which is what makes the
	// fallback a widening of the read rather than of the answer.
	narrowed, err := inspector.Columns(
		t.Context(),
		[]string{"clean_table", "composite_pk"},
	)
	if err != nil {
		t.Fatalf("Columns narrowed: %v", err)
	}
	if len(narrowed) != 2 {
		t.Fatalf("Columns narrowed returned %d tables, want 2", len(narrowed))
	}

	fallback, err := inspector.Columns(
		t.Context(),
		[]string{"clean_table", supplementaryName, "composite_pk"},
	)
	if err != nil {
		t.Fatalf("Columns unnarrowed: %v", err)
	}
	if !reflect.DeepEqual(fallback, narrowed) {
		t.Errorf("unnarrowed Columns = %#v, want %#v", fallback, narrowed)
	}

	narrowedKeys, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys narrowed: %v", err)
	}
	if len(narrowedKeys.Keys) == 0 {
		t.Fatal("ForeignKeys narrowed returned nothing; the comparison would be vacuous")
	}

	fallbackKeys, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child", supplementaryName),
	)
	if err != nil {
		t.Fatalf("ForeignKeys unnarrowed: %v", err)
	}
	if !reflect.DeepEqual(fallbackKeys.Keys, narrowedKeys.Keys) {
		t.Errorf("unnarrowed ForeignKeys keys = %#v, want %#v", fallbackKeys.Keys, narrowedKeys.Keys)
	}
	if fallbackKeys.Visibility != narrowedKeys.Visibility {
		t.Errorf(
			"unnarrowed ForeignKeys visibility = %s, want %s",
			fallbackKeys.Visibility,
			narrowedKeys.Visibility,
		)
	}
	assertNoForeignKeyDowngradeIntegration(t, narrowedKeys)
	assertNoForeignKeyDowngradeIntegration(t, fallbackKeys)
}

func TestPredicateDeduplicationKeepsRequestedMultiplicityIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)

	// The predicate now binds one parameter for a name requested three times.
	// Requested order and multiplicity are reconstructed from the caller's
	// slice rather than from the row set, so the result must not change.
	columns, err := inspector.Columns(
		t.Context(),
		[]string{"clean_table", "clean_table", "clean_table"},
	)
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(columns) != 3 {
		t.Fatalf("Columns returned %d tables, want 3", len(columns))
	}
	for index, table := range columns {
		if !reflect.DeepEqual(table, columns[0]) {
			t.Errorf("Columns[%d] = %#v, want a copy of Columns[0] %#v",
				index, table, columns[0])
		}
	}

	once, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys once: %v", err)
	}
	twice, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child", "fk_internal_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys twice: %v", err)
	}
	if len(once.Keys) == 0 {
		t.Fatal("ForeignKeys returned no keys; the comparison would be vacuous")
	}
	if len(twice.Keys) != 2*len(once.Keys) {
		t.Errorf("duplicate request returned %d keys, want %d",
			len(twice.Keys), 2*len(once.Keys))
	}
}

func TestInnoDBFallbackDiscardsOtherSchemasIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	// A second schema carrying the same fixture, so it holds a foreign key on
	// a table with the *same name* as the one requested below. The unnarrowed
	// InnoDB query has no schema predicate — the schema is embedded in
	// FOR_NAME — so it reads this schema's constraints too, and only the Go
	// comparison in selectForeignKeys keeps them out of the answer.
	other, otherSchema := testsupport.MySQLDatabase(t, "dbsgomysql_predicate_other")
	testsupport.LoadSQLFixture(t, other, otherSchema, fixturePath)
	if otherSchema == schema {
		t.Fatal("both schemas resolved to the same name; the test proves nothing")
	}

	inspector := validations.NewInspector(db, schema)
	narrowed, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys narrowed: %v", err)
	}
	if len(narrowed.Keys) == 0 {
		t.Fatal("ForeignKeys returned no keys; the comparison would be vacuous")
	}

	fallback, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child", supplementaryName),
	)
	if err != nil {
		t.Fatalf("ForeignKeys unnarrowed: %v", err)
	}
	if fallback.Visibility != validations.VisibilityComplete {
		t.Fatalf(
			"visibility = %s, want complete — the InnoDB source did not answer, "+
				"so the whole-server read was never performed",
			fallback.Visibility,
		)
	}
	for _, key := range fallback.Keys {
		if key.ChildSchema != schema {
			t.Errorf("unnarrowed result carries %s.%s from another schema",
				key.ChildSchema, key.ChildTable)
		}
	}
	if !reflect.DeepEqual(fallback.Keys, narrowed.Keys) {
		t.Errorf("unnarrowed ForeignKeys keys = %#v, want %#v", fallback.Keys, narrowed.Keys)
	}
	if fallback.Visibility != narrowed.Visibility {
		t.Errorf(
			"unnarrowed ForeignKeys visibility = %s, want %s",
			fallback.Visibility,
			narrowed.Visibility,
		)
	}
	assertNoForeignKeyDowngradeIntegration(t, narrowed)
	assertNoForeignKeyDowngradeIntegration(t, fallback)
}
