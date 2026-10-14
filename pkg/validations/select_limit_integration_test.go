//go:build integration

package validations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
	"github.com/dbsmedya/dbsgomysql/pkg/validations"
)

// These tests pin docs/COMPAT.md entry 35 for pkg/validations: an inherited or
// session sql_select_limit caps every SELECT without its own LIMIT, and every
// fact still returns its complete answer. Each limit below is smaller than
// the fixture's row count for the facts compared (assertBaselineExceeds), so
// a truncated read cannot equal the baseline.
var selectLimits = []int{0, 1, 2}

// largestSelectLimit is the largest of selectLimits.
const largestSelectLimit = 2

// specTable has four columns, four index parts and a two-column foreign key:
// every TableSpec section exceeds largestSelectLimit.
const specTable = "fk_composite_child"

// foreignKeyAnswer is a ForeignKeyResult with its PrimaryError rendered as
// text, so two answers compare with reflect.DeepEqual: errors do not.
type foreignKeyAnswer struct {
	Keys            []validations.ForeignKey
	Visibility      validations.MetadataVisibility
	DowngradeReason validations.ForeignKeyDowngradeReason
	PrimaryError    string
}

func answerOf(result *validations.ForeignKeyResult) foreignKeyAnswer {
	answer := foreignKeyAnswer{
		Keys:            result.Keys,
		Visibility:      result.Visibility,
		DowngradeReason: result.DowngradeReason,
	}
	if result.PrimaryError != nil {
		answer.PrimaryError = result.PrimaryError.Error()
	}

	return answer
}

type catalogFacts struct {
	Tables      []validations.TableInfo
	Columns     []validations.TableColumns
	Invisible   []validations.InvisibleColumns
	PrimaryKeys []validations.PKInfo
	Triggers    []validations.TriggerInfo
	ForeignKeys foreignKeyAnswer
	Spec        validations.TableSpec
	Grants      validations.Grants
}

func readCatalogFacts(t *testing.T, q validations.Querier, schema string, tables []string) catalogFacts {
	t.Helper()

	ctx := t.Context()
	inspector := validations.NewInspector(q, schema)
	var facts catalogFacts
	var err error
	if facts.Tables, err = inspector.Tables(ctx, tables); err != nil {
		t.Fatalf("Tables: %v", err)
	}
	if facts.Columns, err = inspector.Columns(ctx, tables); err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if facts.Invisible, err = inspector.InvisibleColumns(ctx, tables); err != nil {
		t.Fatalf("InvisibleColumns: %v", err)
	}
	if facts.PrimaryKeys, err = inspector.PrimaryKeys(ctx, tables); err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	if facts.Triggers, err = inspector.Triggers(ctx, tables, validations.TriggerDelete); err != nil {
		t.Fatalf("Triggers: %v", err)
	}
	foreignKeys, err := inspector.ForeignKeys(ctx, validations.Within(tables...))
	if err != nil {
		t.Fatalf("ForeignKeys: %v", err)
	}
	facts.ForeignKeys = answerOf(&foreignKeys)
	if facts.Spec, err = inspector.TableSpec(ctx, validations.Ref(schema, specTable),
		validations.WithIndexes(), validations.WithConstraints(), validations.WithComment()); err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if facts.Grants, err = inspector.Grants(ctx); err != nil {
		t.Fatalf("Grants: %v", err)
	}

	return facts
}

// assertBaselineExceeds proves the fixture can expose truncation: each
// compared fact has more rows than the limit it is read under. Invisible
// columns number two, so they discriminate limits 0 and 1 only.
func assertBaselineExceeds(t *testing.T, admin *sql.DB, facts *catalogFacts) {
	t.Helper()

	columns := 0
	for _, table := range facts.Columns {
		columns += len(table.Columns)
	}
	invisible := 0
	for _, table := range facts.Invisible {
		invisible += len(table.Columns)
	}
	keyColumns := 0
	for index := range facts.ForeignKeys.Keys {
		keyColumns += len(facts.ForeignKeys.Keys[index].ChildColumns)
	}
	var userPrivileges int
	if err := admin.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES "+
			"WHERE GRANTEE = CONCAT(\"'\", SUBSTRING_INDEX(CURRENT_USER(), '@', 1), \"'@'\", "+
			"SUBSTRING_INDEX(CURRENT_USER(), '@', -1), \"'\")",
	).Scan(&userPrivileges); err != nil {
		t.Fatalf("count the account's global privilege rows: %v", err)
	}

	for _, count := range []struct {
		what  string
		got   int
		above int
	}{
		{"tables", len(facts.Tables), largestSelectLimit},
		{"columns", columns, largestSelectLimit},
		{"invisible columns", invisible, 1},
		{"primary-key rows", len(facts.PrimaryKeys), largestSelectLimit},
		{"DELETE triggers", len(facts.Triggers), largestSelectLimit},
		{"foreign-key columns", keyColumns, largestSelectLimit},
		{"spec columns", len(facts.Spec.Columns), largestSelectLimit},
		{"global privilege rows", userPrivileges, largestSelectLimit},
	} {
		if count.got <= count.above {
			t.Fatalf("fixture has %d %s, want more than %d: the comparison could not detect truncation",
				count.got, count.what, count.above)
		}
	}
}

func fixtureTables(t *testing.T, admin *sql.DB, schema string) []string {
	t.Helper()

	rows, err := admin.QueryContext(t.Context(),
		"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", schema)
	if err != nil {
		t.Fatalf("list fixture tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan fixture table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list fixture tables: %v", err)
	}

	return tables
}

// setSessionSelectLimit sets the session's sql_select_limit on conn, after
// registering the restore.
func setSessionSelectLimit(t *testing.T, conn *sql.Conn, limit int) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(ctx, "SET SESSION sql_select_limit = DEFAULT"); err != nil {
			t.Errorf("restore sql_select_limit: %v", err)
		}
	})
	if _, err := conn.ExecContext(t.Context(), fmt.Sprintf("SET SESSION sql_select_limit = %d", limit)); err != nil {
		t.Fatalf("set sql_select_limit = %d: %v", limit, err)
	}
}

// sessionSelectLimit reads the session value with its own LIMIT, so the cap
// it reports cannot hide the read.
func sessionSelectLimit(t *testing.T, q validations.Querier) uint64 {
	t.Helper()

	var limit uint64
	if err := q.QueryRowContext(t.Context(),
		"SELECT @@SESSION.sql_select_limit LIMIT 18446744073709551615").Scan(&limit); err != nil {
		t.Fatalf("read sql_select_limit: %v", err)
	}

	return limit
}

func pinnedConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()

	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("pin a connection: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close pinned connection: %v", err)
		}
	})

	return conn
}

// pastPointLookupTables extends tables past the 2048 names a narrowed
// statement binds, with names the fixture does not have, so the object facts
// take their unnarrowed schema scan and still answer for the real tables only.
func pastPointLookupTables(tables []string) []string {
	extended := slices.Clone(tables)
	for index := range 2049 {
		extended = append(extended, fmt.Sprintf("absent_%04d", index))
	}

	return extended
}

// TestCompat35FactsIgnoreSessionSelectLimitIntegration reads every fact on a
// pinned connection, then again under each session limit — directly and on a
// transaction — and requires the identical answer, for the narrowed statements
// and for the unnarrowed fallback. The library must also leave the session's
// limit as it found it.
func TestCompat35FactsIgnoreSessionSelectLimitIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	tables := fixtureTables(t, db, schema)
	conn := pinnedConn(t, db)

	for _, shape := range []struct {
		name   string
		tables []string
	}{
		{"narrowed", tables},
		{"unnarrowed", pastPointLookupTables(tables)},
	} {
		t.Run(shape.name, func(t *testing.T) {
			baseline := readCatalogFacts(t, conn, schema, shape.tables)
			assertBaselineExceeds(t, db, &baseline)

			for _, limit := range selectLimits {
				t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
					setSessionSelectLimit(t, conn, limit)

					got := readCatalogFacts(t, conn, schema, shape.tables)
					if !reflect.DeepEqual(got, baseline) {
						t.Errorf("facts under sql_select_limit = %d differ from the unlimited read:\ngot  %+v\nwant %+v",
							limit, got, baseline)
					}
					if after := sessionSelectLimit(t, conn); after != uint64(limit) {
						t.Errorf("session sql_select_limit after the facts = %d, want %d unchanged", after, limit)
					}

					// A *sql.Tx on the same session carries the same limit.
					tx, err := conn.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatalf("begin a transaction: %v", err)
					}
					defer func() {
						if err := tx.Rollback(); err != nil {
							t.Errorf("roll back: %v", err)
						}
					}()
					if got := readCatalogFacts(t, tx, schema, shape.tables); !reflect.DeepEqual(got, baseline) {
						t.Errorf("facts on a transaction under sql_select_limit = %d differ from the unlimited read:\ngot  %+v\nwant %+v",
							limit, got, baseline)
					}
				})
			}
		})
	}
}

// TestCompat35StandardForeignKeysIgnoreSessionSelectLimitIntegration covers
// the source the PROCESS-holding connection above never reaches: an account
// without PROCESS reads foreign keys from the standard source and their
// supporting indexes from STATISTICS.
func TestCompat35StandardForeignKeysIgnoreSessionSelectLimitIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	tables := fixtureTables(t, db, schema)

	account := testsupport.CreateMySQLAccount(t, db, "dbsgomysql_selectlimit")
	testsupport.ExecSQL(t, db,
		"GRANT SELECT ON "+sqlutil.QuoteIdentifier(schema)+".* TO "+testsupport.GrantAccountSQL(account))
	conn := testsupport.MySQLConnAs(t, account)
	inspector := validations.NewInspector(conn, schema)

	result, err := inspector.ForeignKeys(t.Context(), validations.Within(tables...))
	if err != nil {
		t.Fatalf("ForeignKeys: %v", err)
	}
	baseline := answerOf(&result)
	if baseline.Visibility != validations.VisibilityUnconfirmed {
		t.Fatalf("visibility = %s, want unconfirmed: the standard source did not answer", baseline.Visibility)
	}
	if len(baseline.Keys) <= largestSelectLimit {
		t.Fatalf("standard source returned %d keys, want more than %d", len(baseline.Keys), largestSelectLimit)
	}

	for _, limit := range selectLimits {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			setSessionSelectLimit(t, conn, limit)

			result, err := inspector.ForeignKeys(t.Context(), validations.Within(tables...))
			if err != nil {
				t.Fatalf("ForeignKeys under sql_select_limit = %d: %v", limit, err)
			}
			if got := answerOf(&result); !reflect.DeepEqual(got, baseline) {
				t.Errorf("standard foreign keys under sql_select_limit = %d differ:\ngot  %+v\nwant %+v",
					limit, got, baseline)
			}
		})
	}
}

// TestCompat35FactsIgnoreInheritedGlobalSelectLimitIntegration sets the
// global limit, opens a new connection — which inherits it — and restores the
// global value before reading, so the window in which other connections could
// inherit it is the connect alone. The new session's own value proves the
// inheritance; the facts must still match the unlimited read.
func TestCompat35FactsIgnoreInheritedGlobalSelectLimitIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	tables := fixtureTables(t, db, schema)
	baseline := readCatalogFacts(t, pinnedConn(t, db), schema, tables)

	fresh, err := sql.Open("mysql", os.Getenv("DBSGOMYSQL_TEST_DSN"))
	if err != nil {
		t.Fatalf("open a second pool: %v", err)
	}
	t.Cleanup(func() {
		if err := fresh.Close(); err != nil {
			t.Errorf("close the second pool: %v", err)
		}
	})

	restoreGlobal := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "SET GLOBAL sql_select_limit = DEFAULT"); err != nil {
			t.Errorf("restore the global sql_select_limit: %v", err)
		}
	}
	t.Cleanup(restoreGlobal)
	testsupport.ExecSQL(t, db, fmt.Sprintf("SET GLOBAL sql_select_limit = %d", largestSelectLimit))
	conn := pinnedConn(t, fresh)
	restoreGlobal()

	if inherited := sessionSelectLimit(t, conn); inherited != largestSelectLimit {
		t.Fatalf("new session's sql_select_limit = %d, want %d inherited from the global value",
			inherited, largestSelectLimit)
	}
	if got := readCatalogFacts(t, conn, schema, tables); !reflect.DeepEqual(got, baseline) {
		t.Errorf("facts under an inherited sql_select_limit = %d differ from the unlimited read:\ngot  %+v\nwant %+v",
			largestSelectLimit, got, baseline)
	}
}

// TestCompat35GrantsWithRolesIgnoreSessionSelectLimitIntegration gives an
// account three active roles and three rows in each of SCHEMA_PRIVILEGES,
// TABLE_PRIVILEGES and COLUMN_PRIVILEGES, so every Grants read but the global
// one returns more rows than any limit; root, read above, has rows in
// USER_PRIVILEGES only. A truncated role or grant set could change an answer
// the trigger-visibility proof depends on (spec §3.1).
func TestCompat35GrantsWithRolesIgnoreSessionSelectLimitIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	account := testsupport.CreateMySQLAccount(t, db, "dbsgomysql_selectlimit_roles")
	grantee := testsupport.GrantAccountSQL(account)
	for range 3 {
		role := testsupport.CreateMySQLRole(t, db, "dbsgomysql_selectlimit_role")
		testsupport.ExecSQL(t, db, "GRANT "+testsupport.GrantAccountSQL(role)+" TO "+grantee)
	}
	testsupport.ExecSQL(t, db, "GRANT SELECT, INSERT, UPDATE ON "+sqlutil.QuoteIdentifier(schema)+".* TO "+grantee)
	for _, table := range []string{"clean_table", "big_pk", "composite_pk"} {
		testsupport.ExecSQL(t, db, "GRANT DELETE ON "+sqlutil.QuoteQualified(schema, table)+" TO "+grantee)
	}
	testsupport.ExecSQL(t, db, "GRANT SELECT (`id`, `z`, `tenant_id`) ON "+
		sqlutil.QuoteQualified(schema, specTable)+" TO "+grantee)

	conn := testsupport.MySQLConnAs(t, account)
	if _, err := conn.ExecContext(t.Context(), "SET ROLE ALL"); err != nil {
		t.Fatalf("activate the account's roles: %v", err)
	}

	var enabledRoles int
	if err := conn.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM information_schema.ENABLED_ROLES LIMIT 18446744073709551615",
	).Scan(&enabledRoles); err != nil {
		t.Fatalf("count enabled roles: %v", err)
	}
	counts := []struct {
		what string
		got  int
	}{{"enabled roles", enabledRoles}}
	for _, relation := range []string{"SCHEMA_PRIVILEGES", "TABLE_PRIVILEGES", "COLUMN_PRIVILEGES"} {
		var rows int
		if err := db.QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM information_schema."+relation+" WHERE GRANTEE = ?", grantee,
		).Scan(&rows); err != nil {
			t.Fatalf("count %s rows: %v", relation, err)
		}
		counts = append(counts, struct {
			what string
			got  int
		}{relation + " rows", rows})
	}
	for _, count := range counts {
		if count.got <= largestSelectLimit {
			t.Fatalf("account has %d %s, want more than %d: the comparison could not detect truncation",
				count.got, count.what, largestSelectLimit)
		}
	}

	inspector := validations.NewInspector(conn, schema)
	baseline, err := inspector.Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	// One resolved answer, so the comparison is not two empty facts: a direct
	// table grant on a pinned session is present even with roles active.
	if state := baseline.Table(schema, "composite_pk", validations.PrivilegeDelete); state != validations.GrantPresent {
		t.Fatalf("baseline DELETE on composite_pk = %s, want present", state)
	}

	for _, limit := range selectLimits {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			setSessionSelectLimit(t, conn, limit)

			got, err := inspector.Grants(t.Context())
			if err != nil {
				t.Fatalf("Grants under sql_select_limit = %d: %v", limit, err)
			}
			if !reflect.DeepEqual(got, baseline) {
				t.Errorf("Grants under sql_select_limit = %d differ from the unlimited read:\ngot  %+v\nwant %+v",
					limit, got, baseline)
			}
		})
	}
}
