package replication

import (
	"context"
	"database/sql"
	"errors"
)

// SQL read by GroupReplication, in the order it issues them. Neither needs a
// privilege beyond connecting: a USAGE-only account reads
// information_schema.PLUGINS and performance_schema.global_variables on every
// supported server (docs/COMPAT.md entry 30), and global_variables is
// populated even with Performance Schema disabled. It answers every plugin
// state as SHOW GLOBAL VARIABLES does, but takes a LIMIT: SHOW VARIABLES is
// capped by sql_select_limit and cannot (docs/COMPAT.md entry 35).
// @@GLOBAL.group_replication_group_name is never read: it raises ERROR 1193
// whenever the variable is not registered.
//
// The variable is matched with =, not LIKE, because "_" is a LIKE wildcard.
const (
	sqlGroupReplicationPluginStatus = "SELECT PLUGIN_STATUS FROM information_schema.PLUGINS " +
		"WHERE PLUGIN_NAME = 'group_replication'" + limitAllRows
	sqlGroupReplicationGroupName = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = 'group_replication_group_name'" + limitAllRows
)

// Result columns GroupReplication decodes, spelled as the server spells them.
const (
	colPluginStatus  = "PLUGIN_STATUS"
	colVariableValue = "VARIABLE_VALUE"
)

// errMultipleRows means a statement that identifies one object by name
// returned more than one row. The fact fails rather than choosing one.
var errMultipleRows = errors.New("more than one row")

// GroupReplication reports whether the Group Replication plugin is active on
// this server and which group name it is configured with.
//
// Every value is the server's own spelling. The two fields come from two
// statements and are not one snapshot: each reports what its own statement
// saw, and the fact never reconciles them.
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
}

// GroupReplication reports the Group Replication plugin's status and the
// configured group name. A server without the plugin, or with it disabled, is
// a reported state, never an error: an error always means the inspection
// itself failed — a transport or privilege failure, more than one row, a
// NULL, or an undecodable value. Both statements must succeed; the fact never
// returns a half-filled value.
//
// It issues two statements and needs no privilege beyond connecting. The first
// statement's rows are closed before the second is issued, so a caller-owned
// *sql.Conn or *sql.Tx works.
//
// GroupReplication is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GroupReplication(ctx context.Context) (GroupReplication, error) {
	if err := i.validate(opGroupReplication); err != nil {
		return GroupReplication{}, err
	}

	status, err := i.optionalValue(ctx, sqlGroupReplicationPluginStatus, colPluginStatus)
	if err != nil {
		return GroupReplication{}, err
	}

	name, err := i.optionalValue(ctx, sqlGroupReplicationGroupName, colVariableValue)
	if err != nil {
		return GroupReplication{}, err
	}

	return GroupReplication{PluginStatus: status, GroupName: name}, nil
}

// optionalValue runs a statement that returns at most one row and reads one
// text column from it. Zero rows is the empty string; it is an answer, not an
// error.
func (i *Inspector) optionalValue(ctx context.Context, query, column string) (string, error) {
	rows, err := i.q.QueryContext(ctx, query)
	if err != nil {
		return "", newOpError(opGroupReplication, "", "", err)
	}

	return readOptionalValue(rows, column)
}

// readOptionalValue owns the row lifecycle: the rows are always closed before
// it returns, and the error reported is the first of column, scan, decode,
// iteration, and close failures.
func readOptionalValue(rows *sql.Rows, column string) (value string, err error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			value = ""
			err = newOpError(opGroupReplication, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opGroupReplication, rows, []string{column})
	if err != nil {
		return "", err
	}

	values, targets := index.scanTargets()
	row := rowDecoder{op: opGroupReplication, index: index, values: values}
	seen := false

	for rows.Next() {
		if seen {
			return "", newOpError(opGroupReplication, "", column, errMultipleRows)
		}
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return "", newOpError(opGroupReplication, "", "", scanErr)
		}
		if decodeErr := decodeColumn(row, column, decodeString, &value); decodeErr != nil {
			return "", decodeErr
		}
		seen = true
	}
	if iterErr := rows.Err(); iterErr != nil {
		return "", newOpError(opGroupReplication, "", "", iterErr)
	}

	return value, nil
}
