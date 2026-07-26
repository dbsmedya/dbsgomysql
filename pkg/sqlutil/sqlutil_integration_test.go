//go:build integration

package sqlutil_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
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
				schema+"_database_"+trailing.name+trailing.character,
			)
			assertIntegrationSQLRejected(
				t,
				db,
				"CREATE TABLE "+sqlutil.QuoteQualified(schema, "table"+trailing.character)+" (id INT)",
			)
			assertIntegrationSQLRejected(
				t,
				db,
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

func assertIntegrationDatabaseNameRejected(t *testing.T, db *sql.DB, database string) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), "CREATE DATABASE "+sqlutil.QuoteIdentifier(database)); err != nil {
		return
	}

	if _, err := db.ExecContext(t.Context(), "DROP DATABASE "+sqlutil.QuoteIdentifier(database)); err != nil {
		t.Fatalf("drop unexpectedly accepted database %q: %v", database, err)
	}
	t.Errorf("database name unexpectedly succeeded: %q", database)
}

func assertIntegrationSQLRejected(t *testing.T, db *sql.DB, statement string) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), statement); err == nil {
		t.Errorf("statement unexpectedly succeeded: %s", statement)
	}
}
