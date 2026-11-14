package replication

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SQL read by GroupReplication, in the order it issues them. None needs a
// privilege beyond connecting: a USAGE-only account reads
// information_schema.PLUGINS and performance_schema.global_variables on every
// supported server (docs/COMPAT.md entry 30), and global_variables is
// populated even with Performance Schema disabled. It answers every plugin
// state as SHOW GLOBAL VARIABLES does, but takes a LIMIT: SHOW VARIABLES is
// capped by sql_select_limit and cannot (docs/COMPAT.md entry 35).
// Neither variable is read with @@GLOBAL: each raises ERROR 1193 whenever
// the variable is not registered.
//
// The variables are matched with =, not LIKE, because "_" is a LIKE wildcard.
const (
	sqlGroupReplicationPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication'" + limitAllRows
	sqlGroupReplicationGroupName = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name'" + limitAllRows
	sqlGroupReplicationTransactionSizeLimit = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = '" + varTransactionSizeLimit + "'" + limitAllRows
)

// varTransactionSizeLimit is the variable the third statement reads. Every
// failure of that read names it, because the group-name read fails with the
// same Op and Column.
const varTransactionSizeLimit = "group_replication_transaction_size_limit"

// Result columns GroupReplication decodes, spelled as the server spells them.
const (
	colPluginStatus  = "PLUGIN_STATUS"
	colVariableValue = "VARIABLE_VALUE"
)

// errMultipleRows means a statement that identifies one object by name
// returned more than one row. The fact fails rather than choosing one.
var errMultipleRows = errors.New("more than one row")

// GroupReplication reports whether the Group Replication plugin is active on
// this server, which group name it is configured with, and its transaction
// size limit.
//
// Every string is the server's own spelling, and the limit is the number the
// server sent. The fields come from three statements and are not one
// snapshot: each reports what its own statement saw, and the fact never
// reconciles them.
//
// The zero value is the answer for a server without the plugin: no status, no
// group name, and no known limit.
//
// GroupReplication is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type GroupReplication struct {
	// PluginStatus is the group_replication plugin's PLUGIN_STATUS from
	// information_schema.PLUGINS: ACTIVE, INACTIVE, DISABLED, DELETING, or
	// DELETED are the documented values, and an unfamiliar one is reported
	// intact. It is empty when the plugin is not installed.
	PluginStatus string `json:"plugin_status"`
	// GroupName is group_replication_group_name as the server reports it. It
	// is empty when the variable is not registered — the plugin is absent or
	// not ACTIVE: a DISABLED plugin hides it exactly as an absent one does
	// (docs/COMPAT.md entry 30) — and when the plugin is ACTIVE with no name
	// set.
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
}

// GroupReplication reports the Group Replication plugin's status, the
// configured group name, and the transaction size limit. A server without the
// plugin, or with it disabled, is a reported state, never an error. An error
// always means the inspection itself failed: a transport or privilege
// failure, more than one row, a NULL, or an undecodable value. All three
// statements must succeed; the fact never returns a half-filled value.
//
// It issues three statements and needs no privilege beyond connecting. Each
// statement's rows are closed before the next is issued, so a caller-owned
// *sql.Conn or *sql.Tx works. A failure of the third statement, the
// transaction size limit read, carries
// "variable group_replication_transaction_size_limit: " at the front of its
// cause, so it is never mistaken for a failure of the group-name read.
//
// GroupReplication is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GroupReplication(ctx context.Context) (GroupReplication, error) {
	if err := i.validate(opGroupReplication); err != nil {
		return GroupReplication{}, err
	}

	status, _, err := optionalValue(ctx, i.q, sqlGroupReplicationPluginStatus, colPluginStatus, decodeString)
	if err != nil {
		return GroupReplication{}, err
	}

	name, _, err := optionalValue(ctx, i.q, sqlGroupReplicationGroupName, colVariableValue, decodeString)
	if err != nil {
		return GroupReplication{}, err
	}

	limit, known, err := optionalValue(
		ctx, i.q, sqlGroupReplicationTransactionSizeLimit, colVariableValue, decodeUint64,
	)
	if err != nil {
		return GroupReplication{}, nameVariable(err, varTransactionSizeLimit)
	}

	return GroupReplication{
		PluginStatus:              status,
		GroupName:                 name,
		TransactionSizeLimit:      limit,
		TransactionSizeLimitKnown: known,
	}, nil
}

// optionalValue runs a statement that returns at most one row and decodes one
// column from it. Zero rows is the zero value with seen false; it is an
// answer, not an error.
func optionalValue[T any](
	ctx context.Context, q Querier, query, column string, decode func(any) (T, error),
) (value T, seen bool, err error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return value, false, newOpError(opGroupReplication, "", "", err)
	}

	return readOptionalValue(rows, column, decode)
}

// readOptionalValue owns the row lifecycle: the rows are always closed before
// it returns, and the error reported is the first of column, scan, decode,
// iteration, and close failures. Every failure returns the zero value with
// seen false.
func readOptionalValue[T any](
	rows *sql.Rows, column string, decode func(any) (T, error),
) (value T, seen bool, err error) {
	var zero T

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			value, seen = zero, false
			err = newOpError(opGroupReplication, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opGroupReplication, rows, []string{column})
	if err != nil {
		return zero, false, err
	}

	values, targets := index.scanTargets()
	row := rowDecoder{op: opGroupReplication, index: index, values: values}

	for rows.Next() {
		if seen {
			return zero, false, newOpError(opGroupReplication, "", column, errMultipleRows)
		}
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return zero, false, newOpError(opGroupReplication, "", "", scanErr)
		}
		if decodeErr := decodeColumn(row, column, decode, &value); decodeErr != nil {
			return zero, false, decodeErr
		}
		seen = true
	}
	if iterErr := rows.Err(); iterErr != nil {
		return zero, false, newOpError(opGroupReplication, "", "", iterErr)
	}

	return value, seen, nil
}

// nameVariable puts the system variable a failed read concerns at the front
// of the failure's cause. The group-name and transaction-size-limit reads
// share Op and Column, so without it a consumer could not tell which one
// failed. The OpError's fields and its cause chain are kept, so errors.Is and
// errors.As still reach the cause. Every error optionalValue returns is an
// *OpError; anything else is returned unchanged.
func nameVariable(err error, variable string) error {
	var opErr *OpError
	if !errors.As(err, &opErr) {
		return err
	}
	opErr.Err = fmt.Errorf("variable %s: %w", variable, opErr.Err)

	return opErr
}
