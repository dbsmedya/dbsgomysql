package testsupport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
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
	// Rows holds the values, one slice per row, aligned with Columns; a
	// misaligned row fails the query at that row.
	Rows [][]driver.Value
	// RowsErr, when non-nil, is returned by the driver's Next once the scripted
	// rows are consumed, in place of io.EOF. It expresses an iteration failure
	// that strikes after some rows have already been delivered, which no
	// connection-level failure can express.
	RowsErr error
	// CloseErr, when non-nil, is returned by the driver rows' Close.
	CloseErr error
	// PendingResultSet leaves one empty result set after Rows. It prevents
	// database/sql from closing the rows automatically when the first result
	// set reaches EOF, so a deferred Close can be tested as the first close.
	PendingResultSet bool
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
			columns:          rule.Columns,
			rows:             rule.Rows,
			rowsErr:          rule.RowsErr,
			closeErr:         rule.CloseErr,
			pendingResultSet: rule.PendingResultSet,
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
	columns          []string
	rows             [][]driver.Value
	next             int
	rowsErr          error
	closeErr         error
	pendingResultSet bool
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
	row := r.rows[r.next]
	if len(row) != len(r.columns) {
		// database/sql reuses dest across rows, so a short row would leave the
		// previous row's trailing values in place and a long row would be
		// silently truncated; the Rows doc promises alignment, so hold it.
		return fmt.Errorf("testsupport: scripted row %d has %d values, want %d columns",
			r.next, len(row), len(r.columns))
	}
	copy(dest, row)
	r.next++

	return nil
}

func (r *scriptedRows) HasNextResultSet() bool {
	return r.pendingResultSet
}

func (r *scriptedRows) NextResultSet() error {
	if !r.pendingResultSet {
		return io.EOF
	}

	r.pendingResultSet = false
	r.rows = nil
	r.next = 0
	r.rowsErr = nil

	return nil
}

// ScriptedDriverConn is a driver.QueryerContext that answers from Script, for
// a unit test of a function that takes a database/sql/driver connection
// directly. Rules match as for OpenScriptedDB; a statement matching no rule
// returns zero rows.
//
// It records what the function under test did to it: every QueryContext call
// with the context and arguments it received, the number of result sets still
// open when each statement arrived, and every call to a method a reader must
// never make (Prepare, Begin, BeginTx, ExecContext, ResetSession, Close),
// which also fails with an error.
//
// With ReuseBuffers set, every []byte value is delivered in a buffer, one per
// column of the result set, that the next Next, and Close, overwrite: a
// driver may reuse dest's memory that way, so a caller that kept the slice
// instead of decoding it would read the overwritten bytes.
//
// The zero value with a Script is ready to use. A ScriptedDriverConn is safe
// for concurrent use, though the order between concurrent calls is the order
// it saw them.
type ScriptedDriverConn struct {
	Script       []ScriptedQuery
	ReuseBuffers bool

	mu        sync.Mutex
	calls     []DriverQueryCall
	forbidden []string
	open      int
}

// DriverQueryCall is one QueryContext call a ScriptedDriverConn received.
type DriverQueryCall struct {
	Ctx   context.Context
	Query string
	Args  []driver.NamedValue
	// OpenResults is how many of the connection's earlier result sets were
	// still open when this statement arrived.
	OpenResults int
}

var errForbiddenDriverCall = errors.New("testsupport: a reader must not call this method")

// QueryContext satisfies driver.QueryerContext.
func (c *ScriptedDriverConn) QueryContext(
	ctx context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	c.mu.Lock()
	c.calls = append(c.calls, DriverQueryCall{Ctx: ctx, Query: query, Args: args, OpenResults: c.open})
	c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for _, rule := range c.Script {
		if !strings.Contains(query, rule.Match) {
			continue
		}
		if rule.Err != nil {
			return nil, rule.Err
		}

		return c.opened(&scriptedRows{
			columns:  rule.Columns,
			rows:     rule.Rows,
			rowsErr:  rule.RowsErr,
			closeErr: rule.CloseErr,
		}), nil
	}

	return c.opened(&scriptedRows{}), nil
}

func (c *ScriptedDriverConn) opened(rows *scriptedRows) driver.Rows {
	c.mu.Lock()
	c.open++
	c.mu.Unlock()

	return &trackedDriverRows{conn: c, rows: rows, reuse: c.ReuseBuffers}
}

func (c *ScriptedDriverConn) forbid(method string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.forbidden = append(c.forbidden, method)
}

// Prepare is recorded and refused.
func (c *ScriptedDriverConn) Prepare(string) (driver.Stmt, error) {
	c.forbid("Prepare")

	return nil, errForbiddenDriverCall
}

// Begin is recorded and refused.
func (c *ScriptedDriverConn) Begin() (driver.Tx, error) {
	c.forbid("Begin")

	return nil, errForbiddenDriverCall
}

// BeginTx is recorded and refused.
func (c *ScriptedDriverConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.forbid("BeginTx")

	return nil, errForbiddenDriverCall
}

// ExecContext is recorded and refused.
func (c *ScriptedDriverConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.forbid("ExecContext")

	return nil, errForbiddenDriverCall
}

// ResetSession is recorded and refused.
func (c *ScriptedDriverConn) ResetSession(context.Context) error {
	c.forbid("ResetSession")

	return errForbiddenDriverCall
}

// Close is recorded and refused: the connection is the caller's to close.
func (c *ScriptedDriverConn) Close() error {
	c.forbid("Close")

	return errForbiddenDriverCall
}

// Calls returns the QueryContext calls received so far, in order.
func (c *ScriptedDriverConn) Calls() []DriverQueryCall {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]DriverQueryCall(nil), c.calls...)
}

// Forbidden returns the names of the refused methods called so far, in order.
func (c *ScriptedDriverConn) Forbidden() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.forbidden...)
}

// OpenResults is how many result sets are open now.
func (c *ScriptedDriverConn) OpenResults() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.open
}

// trackedDriverRows counts its own Close once, and with reuse delivers []byte
// values through one buffer per column.
type trackedDriverRows struct {
	conn   *ScriptedDriverConn
	rows   *scriptedRows
	reuse  bool
	bufs   map[int][]byte
	closed bool
}

func (r *trackedDriverRows) Columns() []string { return r.rows.Columns() }

func (r *trackedDriverRows) Next(dest []driver.Value) error {
	r.scribble()
	if err := r.rows.Next(dest); err != nil {
		return err
	}
	if !r.reuse {
		return nil
	}
	if r.bufs == nil {
		r.bufs = map[int][]byte{}
	}
	for position, value := range dest {
		text, ok := value.([]byte)
		if !ok {
			continue
		}
		r.bufs[position] = append(r.bufs[position][:0], text...)
		dest[position] = r.bufs[position][:len(text):len(text)]
	}

	return nil
}

// scribble overwrites every buffer handed out, as a driver reading the next
// packet into them would.
func (r *trackedDriverRows) scribble() {
	for _, buf := range r.bufs {
		for index := range buf {
			buf[index] = 'X'
		}
	}
}

func (r *trackedDriverRows) Close() error {
	r.scribble()
	if !r.closed {
		r.closed = true
		r.conn.mu.Lock()
		r.conn.open--
		r.conn.mu.Unlock()
	}

	return r.rows.Close()
}
