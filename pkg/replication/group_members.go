package replication

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// sqlGroupMembers reads this member's view of its group. IS_SELF is computed
// by the server, so "which row is me" never depends on a string comparison in
// Go. Performance Schema is the only source for it; the account needs SELECT
// on this one table, and USAGE alone is refused.
const sqlGroupMembers = "SELECT CHANNEL_NAME, MEMBER_ID, MEMBER_HOST, MEMBER_PORT, MEMBER_STATE, " +
	"MEMBER_ROLE, MEMBER_VERSION, MEMBER_COMMUNICATION_STACK, " +
	"MEMBER_ID = @@server_uuid AS IS_SELF " +
	"FROM performance_schema.replication_group_members" + limitAllRows

// Result columns of the two Performance Schema facts, spelled as the server
// spells them. CHANNEL_NAME and MEMBER_ID are shared with GroupMemberStats.
const (
	colGroupChannelName       = "CHANNEL_NAME"
	colMemberID               = "MEMBER_ID"
	colMemberHost             = "MEMBER_HOST"
	colMemberPort             = "MEMBER_PORT"
	colMemberState            = "MEMBER_STATE"
	colMemberRole             = "MEMBER_ROLE"
	colMemberVersion          = "MEMBER_VERSION"
	colMemberCommunicationStk = "MEMBER_COMMUNICATION_STACK"
	colIsSelf                 = "IS_SELF"
)

// GroupMember is one row of this member's view of its group. Every string
// field carries the server's own spelling, unnormalized: MEMBER_VERSION omits
// a distribution's build suffix, and MEMBER_COMMUNICATION_STACK reads MySQL or
// XCom (docs/COMPAT.md entry 31).
//
// GroupMember is a value with no reference fields: copying it copies the whole
// fact, and it is safe for concurrent use.
type GroupMember struct {
	// ChannelName is the Group Replication channel the row belongs to.
	ChannelName string `json:"channel_name"`
	// MemberID is the member's server UUID. It is empty on the row an 8.0 or
	// 8.4 server reports before Group Replication was ever started on it.
	MemberID string `json:"member_id"`
	// Host is the member's host as the group knows it.
	Host string `json:"host"`
	// Port is the member's port. Valid is false if and only if the server sent
	// SQL NULL, as an 8.0 or 8.4 server does before Group Replication was ever
	// started on it; an undecodable value is an error instead. The JSON shape
	// is Go's sql.NullInt64 encoding, as for ChannelStatus.SecondsBehindSource.
	Port sql.NullInt64 `json:"port"`
	// State is the raw MEMBER_STATE: ONLINE, RECOVERING, OFFLINE, ERROR, and
	// UNREACHABLE are the documented values, and an unfamiliar one is reported
	// intact.
	State string `json:"state"`
	// Role is the raw MEMBER_ROLE: PRIMARY or SECONDARY in a formed group, and
	// empty on a member that is not in one.
	Role string `json:"role"`
	// Version is the member's MySQL version as the group reports it.
	Version string `json:"version"`
	// CommunicationStack is the group communication stack in use.
	CommunicationStack string `json:"communication_stack"`
	// Self reports whether this row describes the server that was queried. The
	// server computes it; a row with an empty MemberID is never Self.
	Self bool `json:"self"`
}

// GroupMembers reports this server's view of its Group Replication group, one
// GroupMember per row, in the order the server returned them.
//
// The slice describes the queried member's current view, never the group's
// configured size: a peer that leaves disappears from the others' view rather
// than being listed OFFLINE, and a member cut off from the group reports its
// peers UNREACHABLE. An empty slice means the server reported no row — on
// every server measured, that is a server without an ACTIVE Group Replication
// plugin. A server whose plugin is ACTIVE but which is not in a running group
// reports one OFFLINE row (docs/COMPAT.md entry 31).
//
// It reads performance_schema.replication_group_members and needs SELECT on
// that table; a refusal is an error, never an empty slice.
//
// The returned slice is owned by the caller and built fresh per call.
//
// GroupMembers is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GroupMembers(ctx context.Context) ([]GroupMember, error) {
	if err := i.validate(opGroupMembers); err != nil {
		return nil, err
	}

	rows, err := i.q.QueryContext(ctx, sqlGroupMembers)
	if err != nil {
		return nil, newOpError(opGroupMembers, "", "", err)
	}

	return parseGroupMembers(rows)
}

// parseGroupMembers owns the row lifecycle: the rows are always closed, and
// the error reported is the first of scan, iteration, and close failures.
func parseGroupMembers(rows *sql.Rows) (members []GroupMember, err error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			members = nil
			err = newOpError(opGroupMembers, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opGroupMembers, rows, promisedGroupMembersColumns())
	if err != nil {
		return nil, err
	}

	values, targets := index.scanTargets()
	members = []GroupMember{}

	for rows.Next() {
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return nil, newOpError(opGroupMembers, "", "", scanErr)
		}

		member, decodeErr := decodeGroupMember(index, values)
		if decodeErr != nil {
			return nil, decodeErr
		}

		members = append(members, member)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, newOpError(opGroupMembers, "", "", iterErr)
	}

	return members, nil
}

func promisedGroupMembersColumns() []string {
	return []string{
		colGroupChannelName,
		colMemberID,
		colMemberHost,
		colMemberPort,
		colMemberState,
		colMemberRole,
		colMemberVersion,
		colMemberCommunicationStk,
		colIsSelf,
	}
}

// decodeGroupMember decodes one row. CHANNEL_NAME decodes first so that every
// later failure in the row names the channel it belongs to, and MEMBER_ID
// second so that every failure after it names the member; an anonymous row
// names none.
func decodeGroupMember(index columnIndex, values []any) (GroupMember, error) {
	row := rowDecoder{op: opGroupMembers, index: index, values: values}

	var member GroupMember
	if err := decodeColumn(row, colGroupChannelName, decodeString, &member.ChannelName); err != nil {
		return GroupMember{}, err
	}
	row.channel = member.ChannelName

	if err := decodeColumn(row, colMemberID, decodeString, &member.MemberID); err != nil {
		return GroupMember{}, err
	}
	if err := decodeMemberColumns(row, &member); err != nil {
		return GroupMember{}, nameMember(err, member.MemberID)
	}

	return member, nil
}

// decodeMemberColumns decodes the columns after MEMBER_ID into member.
func decodeMemberColumns(row rowDecoder, member *GroupMember) error {
	if err := decodeColumn(row, colMemberHost, decodeString, &member.Host); err != nil {
		return err
	}
	if err := decodeColumn(row, colMemberPort, decodeNullInt64, &member.Port); err != nil {
		return err
	}
	if err := decodeColumn(row, colMemberState, decodeString, &member.State); err != nil {
		return err
	}
	if err := decodeColumn(row, colMemberRole, decodeString, &member.Role); err != nil {
		return err
	}
	if err := decodeColumn(row, colMemberVersion, decodeString, &member.Version); err != nil {
		return err
	}
	if err := decodeColumn(
		row, colMemberCommunicationStk, decodeString, &member.CommunicationStack,
	); err != nil {
		return err
	}

	return decodeColumn(row, colIsSelf, decodeBool, &member.Self)
}

// nameMember adds a decoded member's UUID to a decode failure's cause, so the
// error names the object as well as the channel and column every row of these
// tables shares. The OpError's fields and its cause chain are kept, so
// errors.Is and errors.As still reach the cause. An empty memberID — an
// anonymous row — names nothing rather than guessing.
func nameMember(err error, memberID string) error {
	var opErr *OpError
	if memberID == "" || !errors.As(err, &opErr) {
		return err
	}
	opErr.Err = fmt.Errorf("member %s: %w", memberID, opErr.Err)

	return opErr
}
