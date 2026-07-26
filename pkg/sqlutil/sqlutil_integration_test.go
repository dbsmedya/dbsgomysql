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
	bmpName := "表_é"
	supplementaryName := "supplementary_\U00010000"

	assertIntegrationTableRoundTrip(t, db, schema, bmpName, 7)
	assertSupplementaryIdentifierReplacement(t, db, schema, supplementaryName)
	assertIntegrationSQLRejected(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "trailing ")+" (id INT)",
	)
}

func integrationDatabase(t *testing.T) (*sql.DB, string) {
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
	schema := "dbsgomysql_sqlutil_" + hex.EncodeToString(suffix)
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

	var storedName string
	err = db.QueryRowContext(
		t.Context(),
		"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?",
		schema,
		table,
	).Scan(&storedName)
	if err != nil {
		t.Fatalf("read metadata for table %q: %v", table, err)
	}
	if storedName != table {
		t.Errorf("stored table name = %q, want exact round-trip %q", storedName, table)
	}
}

func assertSupplementaryIdentifierReplacement(t *testing.T, db *sql.DB, schema, table string) {
	t.Helper()

	execIntegrationSQL(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, table)+" (id INT) CHARACTER SET utf8mb4",
	)

	wantStoredName := strings.ReplaceAll(table, "\U00010000", "?")
	var storedName string
	err := db.QueryRowContext(
		t.Context(),
		"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?",
		schema,
		wantStoredName,
	).Scan(&storedName)
	if err != nil {
		t.Fatalf("read metadata for supplementary table name %q: %v", table, err)
	}
	if storedName != wantStoredName {
		t.Errorf("stored supplementary table name = %q, want replacement %q", storedName, wantStoredName)
	}
}

func assertIntegrationSQLRejected(t *testing.T, db *sql.DB, statement string) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), statement); err == nil {
		t.Errorf("statement unexpectedly succeeded: %s", statement)
	}
}
