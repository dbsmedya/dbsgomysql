//go:build integration

package sqlutil_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
)

// Server error numbers these tests pin. Asserting the number rather than
// "some error occurred" is what makes the docs/COMPAT.md entries evidence: a
// statement that failed for an unrelated reason would otherwise satisfy the
// assertion and silently unpin the behavior.
const (
	erWrongDBName     = 1102 // ER_WRONG_DB_NAME
	erTooLongIdent    = 1059 // ER_TOO_LONG_IDENT
	erWrongTableName  = 1103 // ER_WRONG_TABLE_NAME
	erWrongColumnName = 1166 // ER_WRONG_COLUMN_NAME
	erCannotConvert   = 3988 // ER_CANNOT_CONVERT_STRING
)

func TestQuoteIdentifierRoundTripIntegration(t *testing.T) {
	t.Parallel()

	db, schema := integrationDatabase(t)
	table := "wei`rd"

	assertIntegrationTableRoundTrip(t, db, schema, table, 42)
}

func TestIdentifierLengthBoundaryIntegration(t *testing.T) {
	t.Parallel()

	db, schema := integrationDatabase(t)
	name64 := strings.Repeat("a", 64)
	name65 := strings.Repeat("b", 65)

	execIntegrationSQL(t, db, "CREATE TABLE "+sqlutil.QuoteQualified(schema, name64)+" (id INT)")
	assertIntegrationSQLRejected(
		t,
		db,
		erTooLongIdent,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, name65)+" (id INT)",
	)
}

func TestIdentifierCharacterSetIntegration(t *testing.T) {
	t.Parallel()

	db, schema := integrationDatabase(t)
	supplementaryName := "supplementary_\U00010000"

	validNames := []struct {
		name string
		id   int
	}{
		{name: "表_é", id: 7},
		{name: "trailing_nbsp\u00A0", id: 8},
		{name: "trailing_ideographic_space\u3000", id: 9},
	}
	for _, valid := range validNames {
		assertIntegrationTableRoundTrip(t, db, schema, valid.name, valid.id)
	}

	assertSupplementaryIdentifierReplacement(t, db, schema, supplementaryName)
	assertSupplementaryDatabaseIdentifierReplacement(t, db)

	// MySQL rejects each of these six characters in the final position of a
	// database, table, or column name but accepts every one of them in the
	// leading position. That asymmetry is why ValidateIdentifier inspects only
	// the last rune, so both halves are pinned against the matrix.
	spaceCharacters := []struct {
		name      string
		character string
	}{
		{name: "tab", character: "\t"},
		{name: "lf", character: "\n"},
		{name: "vt", character: "\v"},
		{name: "ff", character: "\f"},
		{name: "cr", character: "\r"},
		{name: "space", character: " "},
	}
	for i, leading := range spaceCharacters {
		assertIntegrationTableRoundTrip(t, db, schema, leading.character+"leading_"+leading.name, 20+i)
	}

	for _, trailing := range spaceCharacters {
		t.Run(trailing.name, func(t *testing.T) {
			assertIntegrationDatabaseNameRejected(
				t,
				db,
				erWrongDBName,
				schema+"_database_"+trailing.name+trailing.character,
			)
			assertIntegrationSQLRejected(
				t,
				db,
				erWrongTableName,
				"CREATE TABLE "+sqlutil.QuoteQualified(schema, "table"+trailing.character)+" (id INT)",
			)
			assertIntegrationSQLRejected(
				t,
				db,
				erWrongColumnName,
				"CREATE TABLE "+sqlutil.QuoteQualified(schema, "column_probe_"+trailing.name)+
					" ("+sqlutil.QuoteIdentifier("column"+trailing.character)+" INT)",
			)
		})
	}
}

func integrationDatabase(t *testing.T) (db *sql.DB, schema string) {
	t.Helper()

	dsn := os.Getenv("DBSGOMYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("DBSGOMYSQL_TEST_DSN is unset")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping integration database: %v", err)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		_ = db.Close()
		t.Fatalf("generate integration schema suffix: %v", err)
	}
	schema = "dbsgomysql_sqlutil_" + hex.EncodeToString(suffix)
	if err := sqlutil.ValidateIdentifier(schema); err != nil {
		_ = db.Close()
		t.Fatalf("generated integration schema name %q: %v", schema, err)
	}

	if _, err := db.ExecContext(
		ctx,
		"CREATE DATABASE "+sqlutil.QuoteIdentifier(schema)+" CHARACTER SET utf8mb4",
	); err != nil {
		_ = db.Close()
		t.Fatalf("create integration schema %q: %v", schema, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()

		if _, err := db.ExecContext(
			cleanupCtx,
			"DROP DATABASE "+sqlutil.QuoteIdentifier(schema),
		); err != nil {
			t.Errorf("drop integration schema %q: %v", schema, err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("close integration database: %v", err)
		}
	})

	return db, schema
}

func execIntegrationSQL(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatalf("execute integration statement: %v", err)
	}
}

func assertIntegrationTableRoundTrip(t *testing.T, db *sql.DB, schema, table string, id int) {
	t.Helper()

	qualifiedTable := sqlutil.QuoteQualified(schema, table)
	execIntegrationSQL(t, db, "CREATE TABLE "+qualifiedTable+" (id INT NOT NULL) CHARACTER SET utf8mb4")
	execIntegrationSQL(t, db, "INSERT INTO "+qualifiedTable+" (id) VALUES (?)", id)

	var selectedID int
	err := db.QueryRowContext(t.Context(), "SELECT id FROM "+qualifiedTable+" WHERE id = ?", id).Scan(&selectedID)
	if err != nil {
		t.Fatalf("select from table %q: %v", table, err)
	}
	if selectedID != id {
		t.Errorf("selected id = %d, want %d", selectedID, id)
	}

	assertIntegrationStoredTableName(t, db, schema, table)
}

func assertSupplementaryIdentifierReplacement(t *testing.T, db *sql.DB, schema, table string) {
	t.Helper()

	execIntegrationSQL(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, table)+" (id INT) CHARACTER SET utf8mb4",
	)

	wantStoredName := strings.ReplaceAll(table, "\U00010000", "?")
	assertIntegrationStoredTableName(t, db, schema, wantStoredName)

	// Looking the original name up in metadata does not merely fail to match:
	// the utf8mb4 parameter cannot be converted into the utf8mb3 collation of
	// the metadata column, so the server raises an error instead of reporting
	// no rows. Callers must not read that error as "the table does not exist".
	var storedName string
	err := db.QueryRowContext(
		t.Context(),
		"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?",
		schema,
		table,
	).Scan(&storedName)
	if err == nil {
		t.Errorf("metadata lookup of the original name %q unexpectedly succeeded", table)

		return
	}
	assertIntegrationServerErrorNumber(
		t,
		err,
		erCannotConvert,
		"metadata lookup of original name "+strconv.Quote(table),
	)
}

// assertSupplementaryDatabaseIdentifierReplacement extends
// assertSupplementaryIdentifierReplacement's pin from a table name to a
// schema name. It measures that CREATE DATABASE silently stores a different
// spelling too. pkg/validations' fixed-parameter guard needs only the requested
// supplementary spelling to be impossible as an exact stored identifier;
// replacement is the server behavior this test pins, not a necessary premise
// of the guard.
func assertSupplementaryDatabaseIdentifierReplacement(t *testing.T, db *sql.DB) {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate supplementary database suffix: %v", err)
	}
	requested := "dbsgomysql_supp_db_" + hex.EncodeToString(suffix) + "_\U00010000"

	execIntegrationSQL(t, db, "CREATE DATABASE "+sqlutil.QuoteIdentifier(requested)+" CHARACTER SET utf8mb4")

	wantStoredName := strings.ReplaceAll(requested, "\U00010000", "?")

	// Clean up the stored spelling, not the requested one: the two spellings
	// differ on the server, so DROP DATABASE with the original supplementary
	// name would either fail or address a different object — the exact
	// substitution this pin exists to measure.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(
			cleanupCtx,
			"DROP DATABASE "+sqlutil.QuoteIdentifier(wantStoredName),
		); err != nil {
			t.Errorf("drop supplementary database %q: %v", wantStoredName, err)
		}
	})

	var storedName string
	err := db.QueryRowContext(
		t.Context(),
		"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?",
		wantStoredName,
	).Scan(&storedName)
	if err != nil {
		t.Fatalf("look up stored database name %q: %v", wantStoredName, err)
	}
	if storedName != wantStoredName {
		t.Errorf("stored database name = %q, want %q", storedName, wantStoredName)
	}

	// Looking the original name up in metadata does not merely fail to match:
	// the utf8mb4 parameter cannot be converted into the utf8mb3 collation of
	// SCHEMATA.SCHEMA_NAME, so the server raises an error instead of reporting
	// no rows — the same failure mode entry 8 already documents for a table
	// name, now measured for a schema name too.
	var lookupName string
	lookupErr := db.QueryRowContext(
		t.Context(),
		"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?",
		requested,
	).Scan(&lookupName)
	if lookupErr == nil {
		t.Errorf("metadata lookup of the original database name %q unexpectedly succeeded", requested)

		return
	}
	assertIntegrationServerErrorNumber(
		t,
		lookupErr,
		erCannotConvert,
		"metadata lookup of original database name "+strconv.Quote(requested),
	)
}

func assertIntegrationStoredTableName(t *testing.T, db *sql.DB, schema, want string) {
	t.Helper()

	rows, err := db.QueryContext(
		t.Context(),
		"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ?",
		schema,
	)
	if err != nil {
		t.Fatalf("list table metadata for schema %q: %v", schema, err)
	}
	defer rows.Close()

	var storedNames []string
	found := false
	for rows.Next() {
		var storedName string
		if err := rows.Scan(&storedName); err != nil {
			t.Fatalf("scan table metadata for schema %q: %v", schema, err)
		}
		storedNames = append(storedNames, storedName)
		if storedName == want {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table metadata for schema %q: %v", schema, err)
	}
	if found {
		return
	}

	t.Errorf("stored table names in schema %q = %q, want exact name %q", schema, storedNames, want)
}

func assertIntegrationDatabaseNameRejected(t *testing.T, db *sql.DB, wantNumber uint16, database string) {
	t.Helper()

	_, err := db.ExecContext(t.Context(), "CREATE DATABASE "+sqlutil.QuoteIdentifier(database))
	if err != nil {
		assertIntegrationServerErrorNumber(t, err, wantNumber, "create database "+strconv.Quote(database))

		return
	}

	if _, err := db.ExecContext(t.Context(), "DROP DATABASE "+sqlutil.QuoteIdentifier(database)); err != nil {
		t.Fatalf("drop unexpectedly accepted database %q: %v", database, err)
	}
	t.Errorf("database name unexpectedly succeeded: %q", database)
}

func assertIntegrationSQLRejected(t *testing.T, db *sql.DB, wantNumber uint16, statement string) {
	t.Helper()

	_, err := db.ExecContext(t.Context(), statement)
	if err == nil {
		t.Errorf("statement unexpectedly succeeded: %s", statement)

		return
	}

	assertIntegrationServerErrorNumber(t, err, wantNumber, statement)
}

// assertIntegrationServerErrorNumber requires err to be a MySQL server error
// carrying wantNumber. A driver-level or connection error, or a server error
// raised for some other reason, fails the assertion instead of passing it.
func assertIntegrationServerErrorNumber(t *testing.T, err error, wantNumber uint16, operation string) {
	t.Helper()

	var serverErr *mysql.MySQLError
	if !errors.As(err, &serverErr) {
		t.Errorf("%s: error %v is not a MySQL server error, want number %d", operation, err, wantNumber)

		return
	}
	if serverErr.Number != wantNumber {
		t.Errorf(
			"%s: server error %d (%s), want number %d",
			operation, serverErr.Number, serverErr.Message, wantNumber,
		)
	}
}
