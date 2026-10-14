package replication

import (
	"context"
	"database/sql"
)

// sqlGroupMemberStats reads the certification and applier counters this
// member holds for each member of its group. IS_SELF is computed by the
// server, as in sqlGroupMembers. The two GTID-set columns and the local
// proposed and rollback counters are not read; no consumer question needs
// them. The account needs SELECT on this one table.
const sqlGroupMemberStats = "SELECT CHANNEL_NAME, VIEW_ID, MEMBER_ID, " +
	"COUNT_TRANSACTIONS_IN_QUEUE, COUNT_TRANSACTIONS_CHECKED, COUNT_CONFLICTS_DETECTED, " +
	"COUNT_TRANSACTIONS_ROWS_VALIDATING, COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE, " +
	"COUNT_TRANSACTIONS_REMOTE_APPLIED, MEMBER_ID = @@server_uuid AS IS_SELF " +
	"FROM performance_schema.replication_group_member_stats" + limitAllRows

// Result columns GroupMemberStats decodes beyond those it shares with
// GroupMembers, spelled as the server spells them.
const (
	colViewID                     = "VIEW_ID"
	colTransactionsInQueue        = "COUNT_TRANSACTIONS_IN_QUEUE"
	colTransactionsChecked        = "COUNT_TRANSACTIONS_CHECKED"
	colConflictsDetected          = "COUNT_CONFLICTS_DETECTED"
	colTransactionsRowsValidating = "COUNT_TRANSACTIONS_ROWS_VALIDATING"
	colRemoteInApplierQueue       = "COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE"
	colRemoteApplied              = "COUNT_TRANSACTIONS_REMOTE_APPLIED"
)

// GroupMemberStats is one row of the counters the queried member holds for a
// member of its group.
//
// A remote member's counters are periodic copies, refreshed on the group's
// flow-control period, so they can differ slightly from that member's own.
// Zero counters are not evidence of a healthy backlog: a member that is not in
// a running group reports a row of zeros (docs/COMPAT.md entry 32). Read the
// counters only alongside a GroupMembers snapshot that shows the member ONLINE.
//
// GroupMemberStats is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type GroupMemberStats struct {
	// ChannelName is the Group Replication channel the row belongs to.
	ChannelName string `json:"channel_name"`
	// ViewID is the group view this row was recorded in. It is empty on a
	// member that is not in a running group.
	ViewID string `json:"view_id"`
	// MemberID is the server UUID of the member the counters describe. It is
	// empty on the row a server reports before Group Replication was ever
	// started on it.
	MemberID string `json:"member_id"`
	// TransactionsInQueue is the certification queue: transactions waiting
	// for conflict detection.
	TransactionsInQueue uint64 `json:"transactions_in_queue"`
	// TransactionsChecked is the number of transactions certified.
	TransactionsChecked uint64 `json:"transactions_checked"`
	// ConflictsDetected is the number of transactions that failed
	// certification.
	ConflictsDetected uint64 `json:"conflicts_detected"`
	// TransactionsRowsValidating is the size of the certification database.
	TransactionsRowsValidating uint64 `json:"transactions_rows_validating"`
	// TransactionsRemoteInApplierQueue is the applier queue: remote
	// transactions this member has received and not yet applied.
	TransactionsRemoteInApplierQueue uint64 `json:"transactions_remote_in_applier_queue"`
	// TransactionsRemoteApplied is the number of remote transactions this
	// member has applied.
	TransactionsRemoteApplied uint64 `json:"transactions_remote_applied"`
	// Self reports whether this row describes the server that was queried. The
	// server computes it; a row with an empty MemberID is never Self.
	Self bool `json:"self"`
}

// GroupMemberStats reports the Group Replication counters the queried member
// holds, one GroupMemberStats per row, in the order the server returned them.
//
// An empty slice means the server reported no row; on every server measured,
// that is a server without an ACTIVE Group Replication plugin. A server whose
// plugin is ACTIVE but which is not in a running group reports one row of
// zeros, although the manual says the table is populated only while Group
// Replication runs (docs/COMPAT.md entry 32). A slice can also lack a row for
// a member GroupMembers lists: the two are separate reads, not one snapshot.
//
// It reads performance_schema.replication_group_member_stats and needs SELECT
// on that table; a refusal is an error, never an empty slice.
//
// The returned slice is owned by the caller and built fresh per call.
//
// GroupMemberStats is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GroupMemberStats(ctx context.Context) ([]GroupMemberStats, error) {
	if err := i.validate(opGroupMemberStats); err != nil {
		return nil, err
	}

	rows, err := i.q.QueryContext(ctx, sqlGroupMemberStats)
	if err != nil {
		return nil, newOpError(opGroupMemberStats, "", "", err)
	}

	return parseGroupMemberStats(rows)
}

// parseGroupMemberStats owns the row lifecycle: the rows are always closed,
// and the error reported is the first of scan, iteration, and close failures.
func parseGroupMemberStats(rows *sql.Rows) (stats []GroupMemberStats, err error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			stats = nil
			err = newOpError(opGroupMemberStats, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opGroupMemberStats, rows, promisedGroupMemberStatsColumns())
	if err != nil {
		return nil, err
	}

	values, targets := index.scanTargets()
	stats = []GroupMemberStats{}

	for rows.Next() {
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return nil, newOpError(opGroupMemberStats, "", "", scanErr)
		}

		row, decodeErr := decodeGroupMemberStats(index, values)
		if decodeErr != nil {
			return nil, decodeErr
		}

		stats = append(stats, row)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, newOpError(opGroupMemberStats, "", "", iterErr)
	}

	return stats, nil
}

func promisedGroupMemberStatsColumns() []string {
	return []string{
		colGroupChannelName,
		colViewID,
		colMemberID,
		colTransactionsInQueue,
		colTransactionsChecked,
		colConflictsDetected,
		colTransactionsRowsValidating,
		colRemoteInApplierQueue,
		colRemoteApplied,
		colIsSelf,
	}
}

// decodeGroupMemberStats decodes one row, attributing failures as
// decodeGroupMember does: the channel once CHANNEL_NAME decodes, the member
// once a non-empty MEMBER_ID does. MEMBER_ID therefore decodes before VIEW_ID.
func decodeGroupMemberStats(index columnIndex, values []any) (GroupMemberStats, error) {
	row := rowDecoder{op: opGroupMemberStats, index: index, values: values}

	var stats GroupMemberStats
	if err := decodeColumn(row, colGroupChannelName, decodeString, &stats.ChannelName); err != nil {
		return GroupMemberStats{}, err
	}
	row.channel = stats.ChannelName

	if err := decodeColumn(row, colMemberID, decodeString, &stats.MemberID); err != nil {
		return GroupMemberStats{}, err
	}
	if err := decodeStatsColumns(row, &stats); err != nil {
		return GroupMemberStats{}, nameMember(err, stats.MemberID)
	}

	return stats, nil
}

// decodeStatsColumns decodes the columns after MEMBER_ID into stats.
func decodeStatsColumns(row rowDecoder, stats *GroupMemberStats) error {
	if err := decodeColumn(row, colViewID, decodeString, &stats.ViewID); err != nil {
		return err
	}

	counters := []struct {
		column string
		target *uint64
	}{
		{colTransactionsInQueue, &stats.TransactionsInQueue},
		{colTransactionsChecked, &stats.TransactionsChecked},
		{colConflictsDetected, &stats.ConflictsDetected},
		{colTransactionsRowsValidating, &stats.TransactionsRowsValidating},
		{colRemoteInApplierQueue, &stats.TransactionsRemoteInApplierQueue},
		{colRemoteApplied, &stats.TransactionsRemoteApplied},
	}
	for _, counter := range counters {
		if err := decodeColumn(row, counter.column, decodeUint64, counter.target); err != nil {
			return err
		}
	}

	return decodeColumn(row, colIsSelf, decodeBool, &stats.Self)
}
