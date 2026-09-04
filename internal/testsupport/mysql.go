package testsupport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
)

// MySQLDatabase opens the configured integration database, creates an isolated
// schema, and registers cleanup. The calling test package must register its
// MySQL driver.
func MySQLDatabase(t *testing.T, prefix string) (db *sql.DB, schema string) {
	t.Helper()

	dsn := os.Getenv("DBSGOMYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("DBSGOMYSQL_TEST_DSN is unset")
	}

	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL test database: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping MySQL test database: %v", err)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		_ = db.Close()
		t.Fatalf("generate schema suffix: %v", err)
	}
	schema = prefix + "_" + hex.EncodeToString(suffix)
	if err := sqlutil.ValidateIdentifier(schema); err != nil {
		_ = db.Close()
		t.Fatalf("generated schema name %q: %v", schema, err)
	}

	if _, err := db.ExecContext(
		ctx,
		"CREATE DATABASE "+sqlutil.QuoteIdentifier(schema)+" CHARACTER SET utf8mb4",
	); err != nil {
		_ = db.Close()
		t.Fatalf("create schema %q: %v", schema, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(
			cleanupCtx,
			"DROP DATABASE "+sqlutil.QuoteIdentifier(schema),
		); err != nil {
			t.Errorf("drop schema %q: %v", schema, err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("close MySQL test database: %v", err)
		}
	})

	return db, schema
}

// LoadSQLFixture executes a semicolon-delimited SQL fixture. The literal
// {{schema}} is replaced by the safely quoted schema identifier.
//
// Statements are split on every ";", so a fixture must not contain one inside
// a string literal, a comment, or a routine body; the fixtures under
// tests/fixtures/ contain none. A literal-aware splitter is deliberately
// absent until a fixture needs one.
func LoadSQLFixture(t *testing.T, db *sql.DB, schema, path string) {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SQL fixture %q: %v", path, err)
	}
	rendered := strings.ReplaceAll(
		string(content),
		"{{schema}}",
		sqlutil.QuoteIdentifier(schema),
	)
	for _, raw := range strings.Split(rendered, ";") {
		statement := strings.TrimSpace(raw)
		if statement == "" {
			continue
		}
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("execute fixture statement %q: %v", statement, err)
		}
	}
}

// ExecSQL executes statement or fails the test.
func ExecSQL(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatalf("execute SQL: %v", err)
	}
}
