//go:build integration

package replication_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// These tests pin GroupConsistency through both entry points, and the
// driver-level function's contract (a) and (b) and the end-to-end half of
// (c), against the Group Replication topology. Contract (c)'s held read is in
// fence_integration_test.go, which needs the fence.

const (
	gcSQLGlobalConsistency = "SELECT @@GLOBAL.group_replication_consistency"
	gcSQLSessionVariables  = "SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.session_variables " +
		"LIMIT 18446744073709551615"
	gcSQLThreadID          = "SELECT PS_CURRENT_THREAD_ID()"
	gcSQLHistoryConsumer   = "SELECT ENABLED FROM performance_schema.setup_consumers WHERE NAME = 'events_statements_history'"
	gcSQLAccountConnects   = "SELECT COALESCE(SUM(TOTAL_CONNECTIONS), 0) FROM performance_schema.accounts WHERE USER = ?"
	gcSQLAccountStatements = "SELECT h.THREAD_ID, h.SQL_TEXT FROM performance_schema.events_statements_history h " +
		"JOIN performance_schema.threads th ON th.THREAD_ID = h.THREAD_ID " +
		"WHERE th.PROCESSLIST_USER = ? AND h.SQL_TEXT LIKE '%performance\\_schema.%\\_variables%' " +
		"LIMIT 18446744073709551615"

	erTransactionInProgress = 1568 // ER_CANT_CHANGE_TX_CHARACTERISTICS
)

// gcValues are the five documented values, set on a session in turn.
var gcValues = []string{"EVENTUAL", "BEFORE_ON_PRIMARY_FAILOVER", "BEFORE", "AFTER", "BEFORE_AND_AFTER"}

// gcServer is one server state of the R2 matrix. admin is root on the server
// itself; creator is where its test accounts are created, which for the
// secondary is the primary, because a secondary refuses CREATE USER under
// super_read_only, and the account then replicates.
type gcServer struct {
	name       string
	admin      *sql.DB
	creator    *sql.DB
	dsn        string
	wantStatus string
}

func gcServers(t *testing.T, topology *testsupport.GRTopology) []gcServer {
	t.Helper()

	standalone, _ := testsupport.GroupReplicationStandalone(t, "gcabsent")

	return []gcServer{
		{name: "plugin not installed", admin: standalone, dsn: "", wantStatus: ""},
		{name: "plugin disabled", admin: topology.Disabled, dsn: topology.DisabledDSN, wantStatus: grPluginOff},
		{
			name: "plugin active, never started", admin: topology.Installed, dsn: topology.InstalledDSN,
			wantStatus: grPluginOn,
		},
		{name: "group primary", admin: topology.Primary, dsn: topology.PrimaryDSN, wantStatus: grPluginOn},
		{
			name: "group secondary", admin: topology.Secondary, creator: topology.Primary,
			dsn: topology.SecondaryDSN, wantStatus: grPluginOn,
		},
	}
}

// usageConn opens a pinned connection as a new USAGE-only account on the
// server. An empty dsn is the standalone server of DBSGOMYSQL_TEST_DSN.
func usageConn(t *testing.T, server gcServer, prefix string) (*sql.Conn, testsupport.MySQLAccount) {
	t.Helper()

	creator := server.creator
	if creator == nil {
		creator = server.admin
	}
	account := testsupport.CreateMySQLAccount(t, creator, prefix)
	if creator != server.admin {
		waitExecuted(t, creator, server.admin)
	}
	var db *sql.DB
	if server.dsn == "" {
		db = testsupport.MySQLDBAs(t, account)
	} else {
		db = testsupport.MySQLDBAsOn(t, server.dsn, account)
	}

	return pinnedConn(t, db), account
}

// viaDriver runs GroupConsistencyOnDriverConn on conn's own driver connection.
func viaDriver(t *testing.T, ctx context.Context, conn *sql.Conn) (replication.GroupConsistency, error) {
	t.Helper()

	var got replication.GroupConsistency
	var factErr error
	if err := conn.Raw(func(driverConn any) error {
		queryer, ok := driverConn.(driver.QueryerContext)
		if !ok {
			return fmt.Errorf("driver connection %T has no QueryContext", driverConn)
		}
		got, factErr = replication.GroupConsistencyOnDriverConn(ctx, queryer)

		return nil
	}); err != nil {
		t.Fatalf("conn.Raw: %v", err)
	}

	return got, factErr
}

// readBoth reads GroupConsistency on conn through the Inspector and through
// the driver function, and requires the two to agree.
func readBoth(t *testing.T, conn *sql.Conn) replication.GroupConsistency {
	t.Helper()

	viaInspector, err := replication.NewInspector(conn).GroupConsistency(t.Context())
	if err != nil {
		t.Fatalf("GroupConsistency through the Inspector: %v", err)
	}
	fromDriver, err := viaDriver(t, t.Context(), conn)
	if err != nil {
		t.Fatalf("GroupConsistencyOnDriverConn: %v", err)
	}
	if fromDriver != viaInspector {
		t.Errorf("driver path = %#v, Inspector path = %#v, want the same", fromDriver, viaInspector)
	}

	return viaInspector
}

// TestGroupConsistencyStatesIntegration is R2's matrix: in every plugin state,
// read by a USAGE-only account on one session through both entry points, a
// fresh session reports the server's global value for both fields, a session
// set to each documented value reports it while the global stays, and
// PluginStatus equals the account's own information_schema.PLUGINS read.
// Then the same under the session's sql_select_limit = 0, asserting the whole
// result: a consistency read without its LIMIT would lose its row there.
func TestGroupConsistencyStatesIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	for _, server := range gcServers(t, topology) {
		t.Run(server.name, func(t *testing.T) {
			conn, _ := usageConn(t, server, "gcstate")
			global := scalarString(t, server.admin, gcSQLGlobalConsistency)

			want := replication.GroupConsistency{
				PluginStatus:       server.wantStatus,
				SessionConsistency: global, SessionConsistencyKnown: true,
				GlobalConsistency: global, GlobalConsistencyKnown: true,
			}
			if got := readBoth(t, conn); got != want {
				t.Errorf("a fresh session = %#v, want %#v", got, want)
			}

			for _, value := range gcValues {
				setSession(t, conn, "group_replication_consistency", "'"+value+"'")
				want.SessionConsistency = value
				if got := readBoth(t, conn); got != want {
					t.Errorf("after SET SESSION %s = %#v, want %#v", value, got, want)
				}
			}

			setSessionSelectLimit(t, conn, 0)
			if got := readBoth(t, conn); got != want {
				t.Errorf("under sql_select_limit = 0 = %#v, want %#v", got, want)
			}
		})
	}
}

// TestGroupConsistencyOnDriverConnContractAIntegration is contract (a): the
// function reads through the connection it is given or not at all. A
// dedicated account's connection count does not move across the call, and
// every statement the function issued is recorded under that connection's
// thread and no other.
func TestGroupConsistencyOnDriverConnContractAIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	ensureStatementsHistory(t, topology.Primary)

	conn, account := usageConn(t, gcServer{admin: topology.Primary, dsn: topology.PrimaryDSN}, "gcconn")
	thread := connScalar(t, conn, gcSQLThreadID)

	before := accountConnections(t, topology.Primary, account.User)
	if _, err := viaDriver(t, t.Context(), conn); err != nil {
		t.Fatalf("GroupConsistencyOnDriverConn: %v", err)
	}
	if after := accountConnections(t, topology.Primary, account.User); after != before {
		t.Errorf("TOTAL_CONNECTIONS for the account = %d after the call, %d before: the function opened a connection",
			after, before)
	}

	statements := accountStatements(t, topology.Primary, account.User)
	if len(statements) < 3 {
		t.Fatalf("history holds %d of the function's statements for the account, want at least 3: %v",
			len(statements), statements)
	}
	for _, statement := range statements {
		if statement.thread != thread {
			t.Errorf("%q ran on thread %s, want the connection's thread %s", statement.text, statement.thread, thread)
		}
	}
}

// TestGroupConsistencyOnDriverConnContractBIntegration is contract (b): with
// autocommit on and with it off, a session carrying values only this test
// knows is left exactly as found: every session variable but the two the
// server advances by itself (sessionAdvancing) is unchanged, and every change,
// addition or removal fails (sessionVariableChanges); the fact reports
// the session's own value; no transaction is open; and the connection still
// answers. The control shows that SET TRANSACTION READ ONLY does detect an
// open transaction: after a table read under autocommit = 0 it fails with
// 1568.
func TestGroupConsistencyOnDriverConnContractBIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	t.Run("control: an open transaction is detected", func(t *testing.T) {
		// A pooled root session: the cleanup ends the transaction and puts
		// autocommit back before the pool can hand the session to anyone else.
		conn := pinnedConn(t, topology.Primary)
		t.Cleanup(func() {
			execConn(t, conn, "ROLLBACK")
			execConn(t, conn, "SET SESSION autocommit = 1")
		})
		setSession(t, conn, "autocommit", "0")
		_ = countRows(t, conn, "SELECT COUNT(*) FROM mysql.user")
		err := execConnErr(t, conn, "SET TRANSACTION READ ONLY")
		if number := serverErrorNumber(err); number != erTransactionInProgress {
			t.Fatalf("SET TRANSACTION READ ONLY after a table read under autocommit = 0: %v, want server error %d",
				err, erTransactionInProgress)
		}
	})

	for _, autocommit := range []string{"1", "0"} {
		t.Run("autocommit "+autocommit, func(t *testing.T) {
			conn, _ := usageConn(t, gcServer{admin: topology.Primary, dsn: topology.PrimaryDSN}, "gcleave")
			setSession(t, conn, "autocommit", autocommit)
			setSession(t, conn, "group_replication_consistency", "'AFTER'")
			setSessionSelectLimit(t, conn, 7)

			before := sessionVariables(t, conn)
			got, err := viaDriver(t, t.Context(), conn)
			if err != nil {
				t.Fatalf("GroupConsistencyOnDriverConn: %v", err)
			}
			if got.SessionConsistency != "AFTER" || !got.SessionConsistencyKnown {
				t.Errorf("GroupConsistencyOnDriverConn = %#v, want the session's own AFTER", got)
			}
			for _, change := range sessionVariableChanges(before, sessionVariables(t, conn)) {
				t.Errorf("session variable %s", change)
			}
			if err := execConnErr(t, conn, "SET TRANSACTION READ ONLY"); err != nil {
				t.Errorf("SET TRANSACTION READ ONLY after the call: %v, want success: a transaction is open", err)
			}
			if one := connScalar(t, conn, "SELECT 1"); one != "1" {
				t.Errorf("SELECT 1 after the call = %q", one)
			}
		})
	}
}

// TestGroupConsistencyOnDriverConnPreCanceledIntegration is contract (c)'s
// end-to-end part: through the exported function on a live go-sql-driver
// connection, an already-canceled context returns context.Canceled wrapped,
// none of the function's statements reaches the server, and the connection
// still answers afterwards. The held read is fence_integration_test.go's.
func TestGroupConsistencyOnDriverConnPreCanceledIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)
	ensureStatementsHistory(t, topology.Primary)

	conn, account := usageConn(t, gcServer{admin: topology.Primary, dsn: topology.PrimaryDSN}, "gccancel")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := viaDriver(t, ctx, conn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GroupConsistencyOnDriverConn on a canceled context = %#v, %v; want context.Canceled", got, err)
	}
	var opErr *replication.OpError
	if !errors.As(err, &opErr) || opErr.Op != "group_consistency" {
		t.Errorf("error %v, want *OpError with Op group_consistency", err)
	}

	if statements := accountStatements(t, topology.Primary, account.User); len(statements) != 0 {
		t.Errorf("statements reached the server on a canceled context: %v", statements)
	}
	if one := connScalar(t, conn, "SELECT 1"); one != "1" {
		t.Errorf("SELECT 1 after the canceled call = %q", one)
	}
}

// ensureStatementsHistory makes sure events_statements_history is collected
// on db's server, enabling it as root for the test and restoring it after if
// it is off.
func ensureStatementsHistory(t *testing.T, db *sql.DB) {
	t.Helper()

	if enabled := scalarString(t, db, gcSQLHistoryConsumer); enabled == "YES" {
		return
	}
	t.Cleanup(func() {
		execOn(t, db, "UPDATE performance_schema.setup_consumers SET ENABLED = 'NO' WHERE NAME = 'events_statements_history'")
	})
	execOn(t, db, "UPDATE performance_schema.setup_consumers SET ENABLED = 'YES' WHERE NAME = 'events_statements_history'")
}

func accountConnections(t *testing.T, db *sql.DB, user string) int64 {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	var total int64
	if err := db.QueryRowContext(ctx, gcSQLAccountConnects, user).Scan(&total); err != nil {
		t.Fatalf("%q: %v", gcSQLAccountConnects, err)
	}

	return total
}

type gcStatement struct{ thread, text string }

// accountStatements lists the variable-table reads in
// events_statements_history issued by user's threads.
func accountStatements(t *testing.T, db *sql.DB, user string) []gcStatement {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	rows, err := db.QueryContext(ctx, gcSQLAccountStatements, user)
	if err != nil {
		t.Fatalf("%q: %v", gcSQLAccountStatements, err)
	}
	defer rows.Close()

	var statements []gcStatement
	for rows.Next() {
		var statement gcStatement
		if err := rows.Scan(&statement.thread, &statement.text); err != nil {
			t.Fatalf("scan %q: %v", gcSQLAccountStatements, err)
		}
		statements = append(statements, statement)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", gcSQLAccountStatements, err)
	}

	return statements
}

// sessionAdvancing names the rows of performance_schema.session_variables the
// server advances inside one session without any SET: timestamp with the
// clock, statement_id with every statement. Measured on 8.4.13 across three
// SELECTs and a 1.2 s wait, with autocommit on and off; no other row of the
// 722 changed (plan reference session-variables-probe.sh).
var sessionAdvancing = map[string]bool{"timestamp": true, "statement_id": true}

// sessionVariables snapshots the session's own variables, all but
// sessionAdvancing.
func sessionVariables(t *testing.T, conn *sql.Conn) map[string]string {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), gcSQLSessionVariables)
	if err != nil {
		t.Fatalf("%q: %v", gcSQLSessionVariables, err)
	}
	defer rows.Close()

	values := map[string]string{}
	for rows.Next() {
		var name string
		var value sql.NullString
		if err := rows.Scan(&name, &value); err != nil {
			t.Fatalf("scan %q: %v", gcSQLSessionVariables, err)
		}
		if !sessionAdvancing[name] {
			values[name] = value.String
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", gcSQLSessionVariables, err)
	}

	return values
}

func connScalar(t *testing.T, conn *sql.Conn, query string) string {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	var value string
	if err := conn.QueryRowContext(ctx, query).Scan(&value); err != nil {
		t.Fatalf("%q: %v", query, err)
	}

	return value
}

// setSession sets a session variable on conn to literal, which is SQL.
func setSession(t *testing.T, conn *sql.Conn, name, literal string) {
	t.Helper()

	execConn(t, conn, "SET SESSION "+name+" = "+literal)
}

func execConn(t *testing.T, conn *sql.Conn, statement string) {
	t.Helper()

	if err := execConnErr(t, conn, statement); err != nil {
		t.Fatalf("%q: %v", statement, err)
	}
}

func execConnErr(t *testing.T, conn *sql.Conn, statement string) error {
	t.Helper()

	ctx, cancel := statementContext(t)
	defer cancel()

	_, err := conn.ExecContext(ctx, statement)

	return err
}

// serverErrorNumber returns the server error number err carries, or 0.
func serverErrorNumber(err error) uint16 {
	var serverErr *mysql.MySQLError
	if errors.As(err, &serverErr) {
		return serverErr.Number
	}

	return 0
}
