package replication

import (
	"context"
	"database/sql/driver"
)

// SQL read by GroupConsistency besides the group-name and plugin reads it
// shares with GroupReplication. group_replication_consistency is a server
// system variable, not a plugin variable: both tables return its row whatever
// the plugin's state (docs/COMPAT.md entry 30), and neither read is held by a
// fence (docs/COMPAT.md entry 36).
const (
	sqlGroupConsistencySession = "SELECT VARIABLE_VALUE FROM performance_schema.session_variables " +
		"WHERE VARIABLE_NAME = '" + varConsistency + "'" + limitAllRows
	sqlGroupConsistencyGlobal = "SELECT VARIABLE_VALUE FROM performance_schema.global_variables " +
		"WHERE VARIABLE_NAME = '" + varConsistency + "'" + limitAllRows
)

// varConsistency is the variable both consistency reads concern. They differ
// only in scope, so each failure names its scope too.
const varConsistency = "group_replication_consistency"

// GroupConsistency reports, for the session it is read on, the Group
// Replication plugin's state and group_replication_consistency, both for that
// session and for the server.
//
// Every string is the server's own spelling. The fields come from three
// statements when the group-name read returns a row and four when it returns
// none, and are not one snapshot. The fact never infers one field from
// another: the plugin's state does not change the variable, which the server
// defines whether or not the plugin is installed.
//
// GroupConsistency is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type GroupConsistency struct {
	// PluginStatus is derived exactly as GroupReplication.PluginStatus is,
	// by the same statements: "ACTIVE" exactly when the server returns the
	// group_replication_group_name row, otherwise PLUGIN_STATUS from
	// information_schema.PLUGINS, or empty when that has no row. Its GoDoc
	// on GroupReplication describes the one window where it differs from the
	// server's own listing.
	PluginStatus string `json:"plugin_status"`

	// SessionConsistency is the session's group_replication_consistency as
	// performance_schema.session_variables spells it: EVENTUAL,
	// BEFORE_ON_PRIMARY_FAILOVER, BEFORE, AFTER or BEFORE_AND_AFTER are the
	// documented values, and an unfamiliar one is reported intact. It is
	// empty when SessionConsistencyKnown is false.
	SessionConsistency string `json:"session_consistency"`
	// SessionConsistencyKnown is true if and only if the server returned the
	// variable's session row. Every server measured returned it in every
	// plugin state; false means no value was returned, never a value.
	SessionConsistencyKnown bool `json:"session_consistency_known"`

	// GlobalConsistency is the server's global group_replication_consistency,
	// from performance_schema.global_variables, spelled as for
	// SessionConsistency. A new session starts with it; a session's own SET
	// changes SessionConsistency only. It is empty when
	// GlobalConsistencyKnown is false.
	GlobalConsistency string `json:"global_consistency"`
	// GlobalConsistencyKnown is true if and only if the server returned the
	// variable's global row.
	GlobalConsistencyKnown bool `json:"global_consistency_known"`
}

// GroupConsistency reads GroupConsistency through the Inspector's Querier.
// The session value is the session each statement runs on: with a *sql.Conn
// that is the connection's own session, and with a *sql.DB it is whichever
// pooled session the statement drew, so pass a *sql.Conn, or use
// GroupConsistencyOnDriverConn, when the session matters. A *sql.Tx is one
// session too, but on a newly elected primary under a fence the server holds
// START TRANSACTION itself, so a transaction cannot be begun there
// (docs/COMPAT.md entry 36).
//
// It issues three statements when the group-name read returns a row and four
// when it returns none, in this order: the group name, the session value, the
// global value, and then, only without the group name's row,
// information_schema.PLUGINS. An error from an earlier statement ends the call
// before the later ones. Every statement must succeed; the fact never returns
// a half-filled value. It needs no privilege beyond connecting, sets nothing,
// and leaves no transaction open, with autocommit on or off.
//
// A failed variable read names its variable at the front of its cause:
// "variable group_replication_group_name: ",
// "session variable group_replication_consistency: " or
// "global variable group_replication_consistency: ". Every error is an
// *OpError with Op "group_consistency".
//
// GroupConsistency is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GroupConsistency(ctx context.Context) (GroupConsistency, error) {
	if err := i.validate(opGroupConsistency); err != nil {
		return GroupConsistency{}, err
	}

	return readGroupConsistency(ctx, querierStatements(i.q))
}

// GroupConsistencyOnDriverConn reads GroupConsistency on conn: a
// database/sql/driver connection the caller owns, for example inside its
// driver.Connector's Connect, before database/sql hands the connection out.
// It issues the same statements in the same order as
// (*Inspector).GroupConsistency and decodes them the same way.
//
// It keeps three promises about conn:
//
//   - It reads through conn or not at all. Every statement is issued as
//     conn.QueryContext; it opens no connection, holds no reference to conn
//     after it returns, and runs nothing elsewhere.
//   - It leaves conn as it found it. Every driver.Rows is closed before the
//     next statement is issued and before it returns, on every path. It never
//     calls Prepare, Begin, BeginTx, ExecContext, ResetSession or Close,
//     issues no SET, and issues only SELECTs on performance_schema and, only
//     without the group name's row, information_schema; none leaves a
//     transaction open, with autocommit on or off.
//   - It hands ctx to the driver. Every statement receives the caller's ctx,
//     unwrapped, so a deadline or a cancellation ends a read the server holds.
//     After a canceled read the driver may have invalidated conn; closing it
//     is the caller's, and this function never closes it.
//
// Every statement is a constant passed with nil arguments. A driver that
// returns driver.ErrSkip anyway gets it back wrapped in an *OpError, never
// bare: there is no fallback, and errors.Is still finds it. A connection
// without QueryContext cannot be passed. A nil conn, untyped or a typed nil,
// returns an *OpError with Op "group_consistency" wrapping ErrNilConn and
// issues nothing.
//
// GroupConsistencyOnDriverConn is safe for concurrent use with distinct
// connections; one driver connection is not safe for concurrent use.
func GroupConsistencyOnDriverConn(ctx context.Context, conn driver.QueryerContext) (GroupConsistency, error) {
	if isNilConn(conn) {
		return GroupConsistency{}, newOpError(opGroupConsistency, "", "", ErrNilConn)
	}

	return readGroupConsistency(ctx, driverStatements(conn))
}

func readGroupConsistency(ctx context.Context, run runStatement) (GroupConsistency, error) {
	const op = opGroupConsistency

	_, nameSeen, err := readOptional(ctx, run, op, sqlGroupReplicationGroupName, colVariableValue, decodeString)
	if err != nil {
		return GroupConsistency{}, nameVariable(err, varGroupName)
	}

	session, sessionKnown, err := readOptional(
		ctx, run, op, sqlGroupConsistencySession, colVariableValue, decodeString,
	)
	if err != nil {
		return GroupConsistency{}, nameRead(err, "session variable "+varConsistency)
	}

	global, globalKnown, err := readOptional(
		ctx, run, op, sqlGroupConsistencyGlobal, colVariableValue, decodeString,
	)
	if err != nil {
		return GroupConsistency{}, nameRead(err, "global variable "+varConsistency)
	}

	status, err := readPluginStatus(ctx, run, op, nameSeen)
	if err != nil {
		return GroupConsistency{}, err
	}

	return GroupConsistency{
		PluginStatus:            status,
		SessionConsistency:      session,
		SessionConsistencyKnown: sessionKnown,
		GlobalConsistency:       global,
		GlobalConsistencyKnown:  globalKnown,
	}, nil
}
