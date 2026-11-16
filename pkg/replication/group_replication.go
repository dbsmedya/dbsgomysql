package replication

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"slices"
)

// SQL read by GroupReplication and GroupConsistency. GroupReplication issues
// them in this order: the group name, the transaction size limit, the
// single-primary mode, and then, only when the group name's row was not
// returned, information_schema.PLUGINS. None needs a privilege beyond
// connecting: a USAGE-only account reads performance_schema.global_variables
// and information_schema.PLUGINS on every supported server (docs/COMPAT.md
// entry 30), and global_variables is populated even with Performance Schema
// disabled. The variables are not read with SHOW GLOBAL VARIABLES, which is
// capped by sql_select_limit and takes no LIMIT (docs/COMPAT.md entry 35),
// nor with @@GLOBAL, which raises ERROR 1193 whenever a plugin variable is
// not registered.
//
// Every statement except the conditional PLUGINS read reads only
// performance_schema, so a fence on a newly elected primary does not hold it
// (docs/COMPAT.md entry 36).
//
// The variables are matched with =, not LIKE, because "_" is a LIKE wildcard.
const (
	sqlGroupReplicationGroupName = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name'" + limitAllRows
	sqlGroupReplicationTransactionSizeLimit = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = '" + varTransactionSizeLimit + "'" + limitAllRows
	sqlGroupReplicationSinglePrimaryMode = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = '" + varSinglePrimaryMode + "'" + limitAllRows
	sqlGroupReplicationPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication'" + limitAllRows
)

// The variables whose failed reads name themselves, because the group-name,
// transaction-size-limit and single-primary-mode reads fail with the same Op
// and Column.
const (
	varGroupName            = "group_replication_group_name"
	varTransactionSizeLimit = "group_replication_transaction_size_limit"
	varSinglePrimaryMode    = "group_replication_single_primary_mode"
)

// pluginStatusActive is what PluginStatus reports when the group name's row
// was returned (docs/COMPAT.md entry 30).
const pluginStatusActive = "ACTIVE"

// Result columns GroupReplication decodes, spelled as the server spells them.
const (
	colPluginStatus  = "PLUGIN_STATUS"
	colVariableValue = "VARIABLE_VALUE"
)

// errMultipleRows means a statement that identifies one object by name
// returned more than one row. The fact fails rather than choosing one.
var errMultipleRows = errors.New("more than one row")

// GroupReplication reports whether the Group Replication plugin is active on
// this server, which group name it is configured with, its transaction size
// limit, and its configured single-primary mode.
//
// Every string is the server's own spelling, and the limit is the number the
// server sent. The fields come from three statements when the group-name read
// returns a row and four when it returns none, and are not one snapshot: each
// reports what its own statement saw, and the fact never reconciles them.
//
// The zero value is the answer for a server without the plugin: no status, no
// group name, no known limit and no known mode.
//
// GroupReplication is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type GroupReplication struct {
	// PluginStatus is "ACTIVE" exactly when the server returns the
	// group_replication_group_name row; otherwise it is PLUGIN_STATUS from
	// information_schema.PLUGINS, or empty when that has no row. ACTIVE,
	// INACTIVE, DISABLED, DELETING and DELETED are the documented statuses,
	// and an unfamiliar one is reported intact.
	//
	// The row can be returned while the plugin is not READY: from INSTALL
	// PLUGIN group_replication until initialization completes, or until a
	// failed install's variables are finally removed, which can be after the
	// statement. Then this field reads ACTIVE where the server lists
	// INACTIVE, DELETING or DELETED. No short bound applies. Once
	// initialization has completed, a hidden row means the plugin is not
	// ACTIVE (docs/COMPAT.md entry 30).
	PluginStatus string `json:"plugin_status"`
	// GroupName is group_replication_group_name as the server reports it. It
	// is empty when the server returns no row for it, and when the plugin is
	// ACTIVE with no name set. No row is not the same as not registered: a
	// DISABLED plugin never registers the variable, and an initialized plugin
	// being uninstalled hides the row while the variable is still registered
	// (docs/COMPAT.md entry 30).
	//
	// A non-empty GroupName proves the server is configured with a group name,
	// not that it is or ever was a member of a running group. The value is a
	// UUID today and is never parsed.
	GroupName string `json:"group_name"`
	// TransactionSizeLimit is group_replication_transaction_size_limit in
	// bytes, as this server reports it. It means something only when
	// TransactionSizeLimitKnown is true; then 0 is the server's own "no
	// limit", which lifts this variable's limit only and does not mean a
	// transaction of any size is accepted (see docs/replication.md). When
	// TransactionSizeLimitKnown is false it is 0 and means nothing.
	//
	// The value is the queried server's own setting. The manual asks for the
	// same value on every member, but members can differ; read each member to
	// compare them. A known limit proves configuration, not that this server
	// is in a running group whose transactions it bounds. The documented
	// maximum is not applied: the field reports what the server sent.
	TransactionSizeLimit uint64 `json:"transaction_size_limit"`
	// TransactionSizeLimitKnown is true if and only if the server returned the
	// variable's row. Every server measured returned it with the plugin ACTIVE
	// and not with it DISABLED or absent (docs/COMPAT.md entry 30). False
	// means the limit is unknown, never that there is none.
	TransactionSizeLimitKnown bool `json:"transaction_size_limit_known"`

	// SinglePrimaryMode is group_replication_single_primary_mode as
	// performance_schema.global_variables spells it: "ON" means the server is
	// configured single-primary, "OFF" multi-primary. Any other text is
	// reported as sent. It is empty when SinglePrimaryModeKnown is false.
	//
	// On a member that is ONLINE it is the group's mode, which changes only
	// by a group action; on a member whose Group Replication is stopped, or
	// on a server in no group, it is that server's own configured value and
	// may differ from a group's (docs/COMPAT.md entry 37). Read it as the
	// mode in force only together with the member's own ONLINE row from
	// GroupMembers.
	SinglePrimaryMode string `json:"single_primary_mode"`
	// SinglePrimaryModeKnown is true if and only if the server returned the
	// variable's row. Every server measured returned it with the plugin ACTIVE
	// and not with it DISABLED or absent (docs/COMPAT.md entry 30). False
	// means the mode is unknown.
	SinglePrimaryModeKnown bool `json:"single_primary_mode_known"`
}

// GroupReplication reports the Group Replication plugin's status, the
// configured group name, the transaction size limit, and the configured
// single-primary mode. A server without the plugin, or with it disabled, is a
// reported state, never an error. An error always means the inspection itself
// failed: a transport or privilege failure, more than one row, a NULL, or an
// undecodable value. Every statement it issues must succeed; the fact never
// returns a half-filled value.
//
// It issues three statements when the group-name read returns a row and four
// when it returns none, in this order: the group name, the transaction size
// limit, the single-primary mode, and then, only without the group name's
// row, information_schema.PLUGINS. An error from an earlier statement ends the
// call before the later ones. It needs no privilege beyond connecting. Each
// statement's rows are closed before the next is issued, so a caller-owned
// *sql.Conn works. A *sql.Tx is one session too, but on a newly elected
// primary under a fence the server holds START TRANSACTION itself, so a
// transaction cannot be begun there (docs/COMPAT.md entry 36).
//
// A failure of the transaction size limit read carries
// "variable group_replication_transaction_size_limit: " at the front of its
// cause, and a failure of the mode read
// "variable group_replication_single_primary_mode: ", so neither is mistaken
// for a failure of the group-name read.
//
// GroupReplication is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GroupReplication(ctx context.Context) (GroupReplication, error) {
	if err := i.validate(opGroupReplication); err != nil {
		return GroupReplication{}, err
	}

	return readGroupReplication(ctx, querierStatements(i.q))
}

func readGroupReplication(ctx context.Context, run runStatement) (GroupReplication, error) {
	const op = opGroupReplication

	name, nameSeen, err := readOptional(ctx, run, op, sqlGroupReplicationGroupName, colVariableValue, decodeString)
	if err != nil {
		return GroupReplication{}, err
	}

	limit, limitKnown, err := readOptional(
		ctx, run, op, sqlGroupReplicationTransactionSizeLimit, colVariableValue, decodeUint64,
	)
	if err != nil {
		return GroupReplication{}, nameVariable(err, varTransactionSizeLimit)
	}

	mode, modeKnown, err := readOptional(
		ctx, run, op, sqlGroupReplicationSinglePrimaryMode, colVariableValue, decodeString,
	)
	if err != nil {
		return GroupReplication{}, nameVariable(err, varSinglePrimaryMode)
	}

	status, err := readPluginStatus(ctx, run, op, nameSeen)
	if err != nil {
		return GroupReplication{}, err
	}

	return GroupReplication{
		PluginStatus:              status,
		GroupName:                 name,
		TransactionSizeLimit:      limit,
		TransactionSizeLimitKnown: limitKnown,
		SinglePrimaryMode:         mode,
		SinglePrimaryModeKnown:    modeKnown,
	}, nil
}

// readPluginStatus is the second step of the plugin-state derivation; the
// group-name read is the first. With the group name's row returned it issues
// nothing and reports ACTIVE. Without it, it reads information_schema.PLUGINS
// last, and reports the status there, or "" with no row. That read is the
// only statement either fact issues outside performance_schema
// (docs/COMPAT.md entries 30 and 36).
func readPluginStatus(ctx context.Context, run runStatement, op string, groupNameSeen bool) (string, error) {
	if groupNameSeen {
		return pluginStatusActive, nil
	}

	status, _, err := readOptional(ctx, run, op, sqlGroupReplicationPluginStatus, colPluginStatus, decodeString)
	if err != nil {
		return "", err
	}

	return status, nil
}

// runStatement issues one statement for the fact op, without arguments, and
// returns its result. querierStatements and driverStatements are the two
// implementations: every statement of every Group Replication fact goes
// through one of them. Each adapts one row API, and is where that API's
// failures enter this package, so each wraps them in an *OpError with op and
// the reader returns them as they come.
type runStatement func(ctx context.Context, op, query string) (oneRowResult, error)

// querierStatements issues each statement through q.QueryContext.
func querierStatements(q Querier) runStatement {
	return func(ctx context.Context, op, query string) (oneRowResult, error) {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return nil, newOpError(op, "", "", err)
		}

		return newSQLResult(op, rows), nil
	}
}

// driverStatements issues each statement through conn.QueryContext with the
// caller's ctx, unchanged, and nil arguments: no placeholder, so a driver has
// nothing to interpolate.
func driverStatements(conn driver.QueryerContext) runStatement {
	return func(ctx context.Context, op, query string) (oneRowResult, error) {
		rows, err := conn.QueryContext(ctx, query, nil)
		if err != nil {
			return nil, newOpError(op, "", "", err)
		}

		return &driverResult{op: op, rows: rows}, nil
	}
}

// readOptional runs a statement that returns at most one row and decodes one
// column from it. Zero rows is the zero value with seen false; it is an
// answer, not an error. Every failure is an *OpError with op.
func readOptional[T any](
	ctx context.Context, run runStatement, op, query, column string, decode func(any) (T, error),
) (value T, seen bool, err error) {
	result, err := run(ctx, op, query)
	if err != nil {
		return value, false, err
	}

	return readOneValue(op, result, column, decode)
}

// oneRowResult is the part of a result set readOneValue needs, over either
// database/sql's *sql.Rows (sqlResult) or a driver.Rows (driverResult). Every
// error a method returns is already an *OpError with the fact's Op.
type oneRowResult interface {
	// columns returns the result's column names.
	columns() ([]string, error)
	// advance moves to the next row: false with a nil error at the end of
	// the result, false with the error when moving failed.
	advance() (bool, error)
	// read loads the row advance moved to.
	read() error
	// value returns the column at position in the row read last. Its memory
	// may be reused by the next advance, so it is decoded before then.
	value(position int) any
	close() error
}

// readOneValue owns the row lifecycle: the result is always closed before it
// returns, and the error reported is the first of column, row read, decode,
// a second row, iteration, and close failures. Every failure returns the zero
// value with seen false.
func readOneValue[T any](
	op string, result oneRowResult, column string, decode func(any) (T, error),
) (value T, seen bool, err error) {
	var zero T

	defer func() {
		closeErr := result.close()
		if closeErr != nil && err == nil {
			value, seen, err = zero, false, closeErr
		}
	}()

	names, err := result.columns()
	if err != nil {
		return zero, false, err
	}
	// The first column of that name, as indexColumns chooses.
	position := slices.Index(names, column)
	if position < 0 {
		return zero, false, newOpError(op, "", column, errMissingColumn)
	}

	for {
		more, advanceErr := result.advance()
		if advanceErr != nil {
			return zero, false, advanceErr
		}
		if !more {
			return value, seen, nil
		}
		if seen {
			return zero, false, newOpError(op, "", column, errMultipleRows)
		}
		if readErr := result.read(); readErr != nil {
			return zero, false, readErr
		}
		decoded, decodeErr := decode(result.value(position))
		if decodeErr != nil {
			return zero, false, newOpError(op, "", column, decodeErr)
		}
		value, seen = decoded, true
	}
}

// sqlResult is a oneRowResult over *sql.Rows.
type sqlResult struct {
	op      string
	rows    *sql.Rows
	values  []any
	targets []any
}

func newSQLResult(op string, rows *sql.Rows) *sqlResult {
	return &sqlResult{op: op, rows: rows}
}

func (r *sqlResult) columns() ([]string, error) {
	names, err := r.rows.Columns()
	if err != nil {
		return nil, newOpError(r.op, "", "", err)
	}

	// Scan requires one destination per column the server sent, including
	// columns the fact does not read.
	r.values = make([]any, len(names))
	r.targets = make([]any, len(names))
	for position := range r.values {
		r.targets[position] = &r.values[position]
	}

	return names, nil
}

func (r *sqlResult) advance() (bool, error) {
	if r.rows.Next() {
		return true, nil
	}
	if err := r.rows.Err(); err != nil {
		return false, newOpError(r.op, "", "", err)
	}

	return false, nil
}

func (r *sqlResult) read() error {
	if err := r.rows.Scan(r.targets...); err != nil {
		return newOpError(r.op, "", "", err)
	}

	return nil
}

func (r *sqlResult) value(position int) any { return r.values[position] }

func (r *sqlResult) close() error {
	if err := r.rows.Close(); err != nil {
		return newOpError(r.op, "", "", err)
	}

	return nil
}

// driverResult is a oneRowResult over a driver.Rows. The driver may reuse the
// memory of dest's values at each Next, which is why readOneValue decodes a
// value before it advances again.
type driverResult struct {
	op   string
	rows driver.Rows
	dest []driver.Value
}

func (r *driverResult) columns() ([]string, error) {
	names := r.rows.Columns()
	r.dest = make([]driver.Value, len(names))

	return names, nil
}

func (r *driverResult) advance() (bool, error) {
	err := r.rows.Next(r.dest)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, newOpError(r.op, "", "", err)
	}

	return true, nil
}

// read has nothing to do: Next already wrote the row into dest.
func (r *driverResult) read() error { return nil }

func (r *driverResult) value(position int) any { return r.dest[position] }

func (r *driverResult) close() error {
	if err := r.rows.Close(); err != nil {
		return newOpError(r.op, "", "", err)
	}

	return nil
}

// nameVariable puts the system variable a failed read concerns at the front
// of the failure's cause, as "variable <name>: ".
func nameVariable(err error, variable string) error {
	return nameRead(err, "variable "+variable)
}

// nameRead puts label at the front of a failed read's cause. Several reads of
// one fact share Op and Column, so without it a consumer could not tell which
// one failed. The OpError's fields and its cause chain are kept, so errors.Is
// and errors.As still reach the cause. Every error readOptional returns is an
// *OpError; anything else is returned unchanged.
func nameRead(err error, label string) error {
	var opErr *OpError
	if !errors.As(err, &opErr) {
		return err
	}
	opErr.Err = fmt.Errorf("%s: %w", label, opErr.Err)

	return opErr
}
