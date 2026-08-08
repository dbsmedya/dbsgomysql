package testsupport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

// OpenNoStatementDB returns a database that fails t the moment any statement
// reaches it, naming that statement in the failure.
//
// It complements OpenFailingDB. OpenFailingDB fails a statement once one is
// issued, so it proves only that the statement did not succeed — it cannot
// tell "no statement ran" apart from "a statement ran and failed". This draws
// exactly that line, which is what a guard meant to skip a query entirely
// needs a test to prove: that the query was never issued, not merely that it
// did not return a usable answer.
func OpenNoStatementDB(t *testing.T) *sql.DB {
	t.Helper()

	db := sql.OpenDB(noStatementConnector{t: t})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close no-statement database: %v", err)
		}
	})

	return db
}

type noStatementConnector struct{ t *testing.T }

func (c noStatementConnector) Connect(context.Context) (driver.Conn, error) {
	return noStatementConn(c), nil
}

func (c noStatementConnector) Driver() driver.Driver { return noStatementDriver(c) }

type noStatementDriver struct{ t *testing.T }

func (d noStatementDriver) Open(string) (driver.Conn, error) {
	return noStatementConn(d), nil
}

type noStatementConn struct{ t *testing.T }

// QueryContext satisfies driver.QueryerContext, which database/sql prefers over
// Prepare, so a statement issued through the ordinary Query path never reaches
// the unimplemented Prepare below — the same reasoning as scriptedConn's.
//
// Calling t.Fatalf here is safe: database/sql invokes the driver synchronously
// on the caller's own goroutine — the test's — so this satisfies the
// requirement that FailNow run only on the goroutine executing the test.
//
// Deliberately not t.Helper(): the caller is database/sql's own internal query
// path, not the test, so marking this a helper would attribute the failure to
// an unhelpful stdlib frame instead of to this line, which already names the
// statement.
func (c noStatementConn) QueryContext(
	_ context.Context, query string, _ []driver.NamedValue,
) (driver.Rows, error) {
	c.t.Fatalf("testsupport: unexpected statement reached the no-statement database: %s", query)

	return nil, errors.New("unreachable")
}

func (c noStatementConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("testsupport: no-statement database does not support Prepare")
}

func (c noStatementConn) Close() error { return nil }

func (c noStatementConn) Begin() (driver.Tx, error) {
	return nil, errors.New("testsupport: no-statement database does not support transactions")
}
