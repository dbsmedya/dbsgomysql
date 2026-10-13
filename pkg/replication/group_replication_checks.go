package replication

import (
	"fmt"
	"strconv"
)

const (
	// memberOnline is the one MEMBER_STATE the Group Replication checks
	// accept. Every other spelling, including one no server sends today, is a
	// finding.
	memberOnline = "ONLINE"
	// summaryNoOwnRow is CheckGroupReplicationLocalMemberOnline's summary when
	// no row of the view is identified as the queried server: an empty view,
	// or the anonymous row an 8.0 or 8.4 server reports before Group
	// Replication was ever started on it.
	summaryNoOwnRow = "No row of this server's group view is identified as this server"
)

// CheckGroupReplicationLocalMemberOnline reports a finding unless the supplied
// view has a row describing the queried server (Self) whose State is exactly
// ONLINE.
//
// A member is only fully synchronized with the group, and participating in it,
// when it is ONLINE, so any other own state leaves both unestablished. An
// empty view fails, never passes: a server without an active Group
// Replication plugin reports no rows, and the other member checks have
// nothing to report on an empty slice. This is the check that makes a server
// that is not a running member fail a Group Replication gate, the way
// CheckReplicationConfigured does for asynchronous channels. It guards the
// members input only; it says nothing about whether a GroupMemberStats
// snapshot covers the members a gate relies on.
//
// An own ONLINE row does not prove the member is in the group's majority: an
// expelled member "sees itself as online and the other members as
// unreachable" (MySQL manual, "Responses to Failure Detection and Network
// Partitioning"), and passes this check. CheckGroupReplicationMembersOnline is
// the check that fires on those UNREACHABLE rows; a gate runs both.
//
// The single finding carries the whole slice supplied.
//
// CheckGroupReplicationLocalMemberOnline is safe for concurrent use when
// members is not mutated concurrently.
func CheckGroupReplicationLocalMemberOnline(members []GroupMember) []Finding {
	summary := summaryNoOwnRow
	for index := range members {
		if !members[index].Self {
			continue
		}
		if members[index].State == memberOnline {
			return nil
		}
		summary = fmt.Sprintf("This server's own member state is %q rather than ONLINE", members[index].State)
	}

	return []Finding{{
		Check:   IDGroupReplicationLocalMemberOnline,
		Message: findingMessage(IDGroupReplicationLocalMemberOnline, summary),
		Facts:   members,
	}}
}

// CheckGroupReplicationMembersOnline reports a finding for each member of the
// supplied view whose State is not exactly ONLINE, preserving the order of the
// members supplied.
//
// A finding means this view does not report the member ONLINE, the only state
// in which a member is fully synchronized and participating; it does not
// prove the member stopped. UNREACHABLE is the queried member's suspicion,
// and a partition can put either end out of contact — the queried member may
// be the one cut off. The check describes the supplied view only. A member
// that left the group is absent from every other member's view rather than
// listed OFFLINE, so the check cannot prove the group's configured size or its
// fault tolerance. An empty slice produces no findings: detecting a server
// that is not a running member is CheckGroupReplicationLocalMemberOnline's job.
//
// Each finding carries the member's whole GroupMember.
//
// CheckGroupReplicationMembersOnline is safe for concurrent use when members
// is not mutated concurrently.
func CheckGroupReplicationMembersOnline(members []GroupMember) []Finding {
	var findings []Finding
	for index := range members {
		if members[index].State == memberOnline {
			continue
		}

		summary := fmt.Sprintf(
			"Group member %q on host %q is %q rather than ONLINE",
			members[index].MemberID,
			members[index].Host,
			members[index].State,
		)
		findings = append(findings, Finding{
			Check:   IDGroupReplicationMembersOnline,
			Message: findingMessage(IDGroupReplicationMembersOnline, summary),
			Facts:   members[index],
		})
	}

	return findings
}

// CheckGroupReplicationApplierQueueWithin reports a finding for each row whose
// TransactionsRemoteInApplierQueue exceeds maxTransactions, preserving the
// order of the rows supplied.
//
// The caller supplies the bound, so no threshold policy enters the library. A
// queue equal to maxTransactions passes; a larger one fails. A negative bound
// produces a finding for every supplied row, since no queue can satisfy it;
// the value is never clamped and never defaulted. The queue is the number of
// remote transactions the member has received and not yet applied: a growing
// one means an application backlog, not an exact measure of how far the
// member's data trails the group, and a remote member's value is a periodic
// copy.
//
// An empty slice, one missing a member, and a stopped member's own row of
// zeros all pass: the stats are a separate read from GroupMembers, so a gate
// must itself require, for every member it relies on, a row with a non-empty
// ViewID (docs/replication.md, "Gating on a group").
//
// Each finding carries the row's whole GroupMemberStats.
//
// CheckGroupReplicationApplierQueueWithin is safe for concurrent use when
// stats is not mutated concurrently.
func CheckGroupReplicationApplierQueueWithin(stats []GroupMemberStats, maxTransactions int64) []Finding {
	return queueFindings(stats, maxTransactions, IDGroupReplicationApplierQueueWithin, "applier",
		func(row *GroupMemberStats) uint64 { return row.TransactionsRemoteInApplierQueue })
}

// CheckGroupReplicationCertifierQueueWithin reports a finding for each row
// whose TransactionsInQueue exceeds maxTransactions, preserving the order of
// the rows supplied.
//
// The bound rules are CheckGroupReplicationApplierQueueWithin's: equal
// passes, larger fails, a negative bound fires for every row, and nothing is
// clamped or defaulted. The queue is the number of transactions waiting for
// conflict detection: a growing one means a certification backlog. With
// group_replication_flow_control_mode at its default, QUOTA, a queue past its
// configured threshold also throttles the group's writers; that is
// background, not part of the predicate.
//
// An empty slice, one missing a member, and a stopped member's own row of
// zeros all pass, for the reason given in
// CheckGroupReplicationApplierQueueWithin.
//
// Each finding carries the row's whole GroupMemberStats.
//
// CheckGroupReplicationCertifierQueueWithin is safe for concurrent use when
// stats is not mutated concurrently.
func CheckGroupReplicationCertifierQueueWithin(stats []GroupMemberStats, maxTransactions int64) []Finding {
	return queueFindings(stats, maxTransactions, IDGroupReplicationCertifierQueueWithin, "certification",
		func(row *GroupMemberStats) uint64 { return row.TransactionsInQueue })
}

// queueFindings is the shared predicate of the two queue checks. The bound is
// signed and the counters unsigned: a negative bound is decided before any
// conversion, so uint64(maxTransactions) never wraps, and a counter above
// math.MaxInt64 is compared exactly rather than narrowed.
func queueFindings(
	stats []GroupMemberStats,
	maxTransactions int64,
	id, queue string,
	pick func(*GroupMemberStats) uint64,
) []Finding {
	var findings []Finding
	for index := range stats {
		depth := pick(&stats[index])

		var summary string
		switch {
		case maxTransactions < 0:
			summary = fmt.Sprintf(
				"Group member %q reports a %s queue of %s against the negative bound %s, which no queue can satisfy",
				stats[index].MemberID,
				queue,
				strconv.FormatUint(depth, 10),
				strconv.FormatInt(maxTransactions, 10),
			)
		case depth > uint64(maxTransactions):
			summary = fmt.Sprintf(
				"Group member %q reports a %s queue of %s, above the bound %s",
				stats[index].MemberID,
				queue,
				strconv.FormatUint(depth, 10),
				strconv.FormatInt(maxTransactions, 10),
			)
		default:
			continue
		}

		findings = append(findings, Finding{
			Check:   id,
			Message: findingMessage(id, summary),
			Facts:   stats[index],
		})
	}

	return findings
}
