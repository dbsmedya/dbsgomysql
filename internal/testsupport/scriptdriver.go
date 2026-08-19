package testsupport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
)

// ScriptedQuery is one rule for OpenScriptedDB.
type ScriptedQuery struct {
	// Match is a substring of the SQL statement this rule answers. The first
	// rule whose Match appears in the statement wins, so order rules from most
	// to least specific.
	Match string
	// Err, when non-nil, fails the query instead of answering it.
	Err error
	// Columns names the result columns.
	Columns []string
	// Rows holds the values, one slice per row, aligned with Columns.
	Rows [][]driver.Value
	// RowsErr, when non-nil, is returned by the driver's Next once the scripted
	// rows are consumed, in place of io.EOF. It expresses an iteration failure
	// that strikes after some rows have already been delivered, which no
	// connection-level failure can express.
	RowsErr error
	// CloseErr, when non-nil, is returned by the driver rows' Close.
	CloseErr error
}

// OpenScriptedDB returns a database that answers queries from script. A
// statement matching no rule returns zero rows, which is an answer rather than
// an error.
//
// It complements OpenFailingDB, which fails at connection establishment and so
// cannot express "the third query fails" — the shape needed to test a fact that
// issues several queries, or one that must filter rows the server returned.
func OpenScriptedDB(script ...ScriptedQuery) *sql.DB {
	return sql.OpenDB(scriptedConnector{script: script})
}

// OpenScriptedDBWithLog is OpenScriptedDB that also appends every executed
// statement to log, in order, before matching it against the script. It is
// what pins how many statements a fact issues and what their exact text is —
// questions the returned rows cannot answer.
//
// The log is appended under a mutex, so a database shared across goroutines
// records every statement; the order between concurrent statements is then the
// order the driver saw them.
func OpenScriptedDBWithLog(log *[]string, script ...ScriptedQuery) *sql.DB {
	return sql.OpenDB(scriptedConnector{script: script, log: &statementLog{dst: log}})
}

// statementLog guards the caller's slice. It is held by pointer so that
// copying a connector never copies a mutex.
type statementLog struct {
	mu  sync.Mutex
	dst *[]string
}

func (l *statementLog) record(query string) {
	if l == nil || l.dst == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	*l.dst = append(*l.dst, query)
}

type scriptedConnector struct {
	script []ScriptedQuery
	log    *statementLog
}

func (c scriptedConnector) Connect(context.Context) (driver.Conn, error) {
	return &scriptedConn{script: c.script, log: c.log}, nil
}

func (c scriptedConnector) Driver() driver.Driver { return scriptedDriver{} }

type scriptedDriver struct{}

func (scriptedDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("testsupport: scripted driver requires OpenScriptedDB")
}

type scriptedConn struct {
	script []ScriptedQuery
	log    *statementLog
}

// QueryContext satisfies driver.QueryerContext, which database/sql prefers over
// Prepare, so statements never reach the unimplemented Prepare below.
func (c *scriptedConn) QueryContext(
	_ context.Context, query string, _ []driver.NamedValue,
) (driver.Rows, error) {
	c.log.record(query)

	for _, rule := range c.script {
		if !strings.Contains(query, rule.Match) {
			continue
		}
		if rule.Err != nil {
			return nil, rule.Err
		}

		return &scriptedRows{
			columns:  rule.Columns,
			rows:     rule.Rows,
			rowsErr:  rule.RowsErr,
			closeErr: rule.CloseErr,
		}, nil
	}

	return &scriptedRows{}, nil
}

func (c *scriptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("testsupport: scripted driver does not support Prepare")
}

func (c *scriptedConn) Close() error { return nil }

func (c *scriptedConn) Begin() (driver.Tx, error) {
	return nil, errors.New("testsupport: scripted driver does not support transactions")
}

type scriptedRows struct {
	columns  []string
	rows     [][]driver.Value
	next     int
	rowsErr  error
	closeErr error
}

func (r *scriptedRows) Columns() []string { return r.columns }

func (r *scriptedRows) Close() error { return r.closeErr }

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		if r.rowsErr != nil {
			return r.rowsErr
		}

		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++

	return nil
}
