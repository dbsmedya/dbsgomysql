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

	"github.com/go-sql-driver/mysql"
)

// MySQLAccount holds credentials for one temporary integration-test account.
// It is a plain value and is safe for concurrent use.
type MySQLAccount struct {
	User     string
	Host     string
	Password string
}

// CreateMySQLAccount creates a namespaced account and registers its removal.
func CreateMySQLAccount(
	t *testing.T,
	admin *sql.DB,
	prefix string,
) MySQLAccount {
	t.Helper()

	return CreateNamedMySQLAccount(t, admin, generatedAccountName(t, prefix))
}

// CreateNamedMySQLAccount creates user@% and registers its removal.
func CreateNamedMySQLAccount(
	t *testing.T,
	admin *sql.DB,
	user string,
) MySQLAccount {
	t.Helper()

	passwordBytes := make([]byte, 16)
	if _, err := rand.Read(passwordBytes); err != nil {
		t.Fatalf("generate account password: %v", err)
	}
	account := MySQLAccount{
		User: user, Host: "%", Password: hex.EncodeToString(passwordBytes),
	}
	statement := "CREATE USER " + quoteAccount(account.User, account.Host) +
		" IDENTIFIED BY " + quoteSQLString(account.Password)
	if _, err := admin.ExecContext(t.Context(), statement); err != nil {
		t.Fatalf("create account %s: %v", quoteAccount(account.User, account.Host), err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(
			ctx,
			"DROP USER IF EXISTS "+quoteAccount(account.User, account.Host),
		); err != nil {
			t.Errorf("drop account %s: %v", quoteAccount(account.User, account.Host), err)
		}
	})

	return account
}

// CreateMySQLRole creates a namespaced role and registers its removal.
func CreateMySQLRole(t *testing.T, admin *sql.DB, prefix string) MySQLAccount {
	t.Helper()

	role := MySQLAccount{User: generatedAccountName(t, prefix), Host: "%"}
	if _, err := admin.ExecContext(
		t.Context(),
		"CREATE ROLE "+quoteAccount(role.User, role.Host),
	); err != nil {
		t.Fatalf("create role %s: %v", quoteAccount(role.User, role.Host), err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(
			ctx,
			"DROP ROLE IF EXISTS "+quoteAccount(role.User, role.Host),
		); err != nil {
			t.Errorf("drop role %s: %v", quoteAccount(role.User, role.Host), err)
		}
	})

	return role
}

// MySQLDBAs opens and registers a pool authenticated as account.
func MySQLDBAs(t *testing.T, account MySQLAccount) *sql.DB {
	t.Helper()

	cfg := accountDSN(t, account)
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open database as %s: %v", quoteAccount(account.User, account.Host), err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		_ = db.Close()
		t.Fatalf("ping database as %s: %v", quoteAccount(account.User, account.Host), err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database as %s: %v", quoteAccount(account.User, account.Host), err)
		}
	})

	return db
}

// MySQLConnAs opens and registers a pinned connection authenticated as account.
func MySQLConnAs(t *testing.T, account MySQLAccount) *sql.Conn {
	t.Helper()

	db := MySQLDBAs(t, account)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("pin connection as %s: %v", quoteAccount(account.User, account.Host), err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close pinned connection as %s: %v", quoteAccount(account.User, account.Host), err)
		}
	})

	return conn
}

// GrantAccountSQL returns account as a safely quoted MySQL account expression.
func GrantAccountSQL(account MySQLAccount) string {
	return quoteAccount(account.User, account.Host)
}

func accountDSN(t *testing.T, account MySQLAccount) *mysql.Config {
	t.Helper()

	cfg, err := mysql.ParseDSN(os.Getenv("DBSGOMYSQL_TEST_DSN"))
	if err != nil {
		t.Fatalf("parse MySQL test DSN: %v", err)
	}
	cfg.User = account.User
	cfg.Passwd = account.Password
	cfg.DBName = ""

	return cfg
}

func generatedAccountName(t *testing.T, prefix string) string {
	t.Helper()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate account suffix: %v", err)
	}
	name := prefix + "_" + hex.EncodeToString(suffix)
	if len(name) > 32 {
		name = name[:32]
	}

	return name
}

func quoteAccount(user, host string) string {
	return quoteSQLString(user) + "@" + quoteSQLString(host)
}

// quoteSQLString renders value as a MySQL string literal for the default
// sql_mode the compose files run, where backslash is the escape character
// and a quote is doubled. NO_BACKSLASH_ESCAPES is not supported by this
// harness: under it the backslash doubling below would be wrong.
func quoteSQLString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)

	return "'" + strings.ReplaceAll(escaped, "'", "''") + "'"
}
