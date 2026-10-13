package replication

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

const testMemberC = "33333333-3333-3333-3333-333333333333"

func onlineMember(id string, self bool) GroupMember {
	return GroupMember{
		ChannelName: testApplierChannel, MemberID: id, Host: "host-" + id[:1],
		State: testStateOnline, Role: "SECONDARY", Self: self,
	}
}

func memberInState(id, state string, self bool) GroupMember {
	member := onlineMember(id, self)
	member.State = state

	return member
}

func statsRow(id string, applier, certifier uint64) GroupMemberStats {
	return GroupMemberStats{
		ChannelName: testApplierChannel, ViewID: testViewID, MemberID: id,
		TransactionsInQueue: certifier, TransactionsRemoteInApplierQueue: applier,
	}
}

func TestCheckGroupReplicationLocalMemberOnline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		members     []GroupMember
		wantFinding bool
		wantSummary string
	}{
		{
			name:        "empty view fails",
			members:     []GroupMember{},
			wantFinding: true,
			wantSummary: summaryNoOwnRow,
		},
		{
			name:        "nil view fails",
			members:     nil,
			wantFinding: true,
			wantSummary: summaryNoOwnRow,
		},
		{
			name:    "own row ONLINE passes",
			members: []GroupMember{onlineMember(testMemberB, false), onlineMember(testMemberA, true)},
		},
		{
			name:        "own row RECOVERING fails",
			members:     []GroupMember{onlineMember(testMemberB, false), memberInState(testMemberA, "RECOVERING", true)},
			wantFinding: true,
			wantSummary: `This server's own member state is "RECOVERING" rather than ONLINE`,
		},
		{
			name:        "every peer ONLINE but no own row fails",
			members:     []GroupMember{onlineMember(testMemberB, false), onlineMember(testMemberC, false)},
			wantFinding: true,
			wantSummary: summaryNoOwnRow,
		},
		{
			name:        "anonymous never-started row fails",
			members:     []GroupMember{{ChannelName: testApplierChannel, State: testStateOffline}},
			wantFinding: true,
			wantSummary: summaryNoOwnRow,
		},
		{
			name:        "spelling is not folded",
			members:     []GroupMember{memberInState(testMemberA, "online", true)},
			wantFinding: true,
			wantSummary: `This server's own member state is "online" rather than ONLINE`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			findings := CheckGroupReplicationLocalMemberOnline(testCase.members)
			if !testCase.wantFinding {
				if len(findings) != 0 {
					t.Errorf("findings = %#v, want none", findings)
				}

				return
			}
			if len(findings) != 1 {
				t.Fatalf("returned %d findings, want 1", len(findings))
			}
			assertFindingShape(t, findings[0], IDGroupReplicationLocalMemberOnline, nil)
			if !strings.HasPrefix(findings[0].Message, testCase.wantSummary+". ") {
				t.Errorf("Message = %q, want it to start with %q", findings[0].Message, testCase.wantSummary)
			}
			facts, ok := findings[0].Facts.([]GroupMember)
			if !ok {
				t.Fatalf("Finding.Facts is %T, want []GroupMember", findings[0].Facts)
			}
			if !reflect.DeepEqual(facts, testCase.members) {
				t.Errorf("Finding.Facts = %#v, want the whole view supplied", facts)
			}
		})
	}
}

func TestCheckGroupReplicationMembersOnline(t *testing.T) {
	t.Parallel()

	if findings := CheckGroupReplicationMembersOnline(nil); len(findings) != 0 {
		t.Errorf("empty view = %#v, want no findings", findings)
	}

	healthy := []GroupMember{onlineMember(testMemberA, true), onlineMember(testMemberB, false)}
	if findings := CheckGroupReplicationMembersOnline(healthy); len(findings) != 0 {
		t.Errorf("all ONLINE = %#v, want no findings", findings)
	}

	view := []GroupMember{
		memberInState(testMemberC, "UNREACHABLE", false),
		onlineMember(testMemberA, true),
		memberInState(testMemberB, "RECOVERING", false),
		memberInState(testMemberA, "Online", false),
	}
	findings := CheckGroupReplicationMembersOnline(view)

	want := []GroupMember{view[0], view[2], view[3]}
	if len(findings) != len(want) {
		t.Fatalf("returned %d findings, want %d: %#v", len(findings), len(want), findings)
	}
	for index, finding := range findings {
		assertFindingShape(t, finding, IDGroupReplicationMembersOnline, nil)
		facts, ok := finding.Facts.(GroupMember)
		if !ok {
			t.Fatalf("findings[%d].Facts is %T, want GroupMember", index, finding.Facts)
		}
		if facts != want[index] {
			t.Errorf("findings[%d].Facts = %#v, want %#v", index, facts, want[index])
		}
		if !strings.Contains(finding.Message, want[index].State) {
			t.Errorf("findings[%d].Message = %q, want it to name state %q", index, finding.Message, want[index].State)
		}
	}
}

// TestGroupReplicationQueueChecks runs both queue checks through the bound
// cases spec §6 names: narrowing must never turn a large queue into a pass.
func TestGroupReplicationQueueChecks(t *testing.T) {
	t.Parallel()

	checks := []struct {
		name  string
		id    string
		check func([]GroupMemberStats, int64) []Finding
		row   func(id string, depth uint64) GroupMemberStats
	}{
		{
			name:  "applier",
			id:    IDGroupReplicationApplierQueueWithin,
			check: CheckGroupReplicationApplierQueueWithin,
			row:   func(id string, depth uint64) GroupMemberStats { return statsRow(id, depth, 7) },
		},
		{
			name:  "certifier",
			id:    IDGroupReplicationCertifierQueueWithin,
			check: CheckGroupReplicationCertifierQueueWithin,
			row:   func(id string, depth uint64) GroupMemberStats { return statsRow(id, 7, depth) },
		},
	}

	bounds := []struct {
		name  string
		depth uint64
		bound int64
		fires bool
	}{
		{name: "negative bound fires on an empty queue", depth: 0, bound: -1, fires: true},
		{name: "minimum bound fires", depth: 0, bound: math.MinInt64, fires: true},
		{name: "zero bound, empty queue passes", depth: 0, bound: 0},
		{name: "zero bound, one queued fires", depth: 1, bound: 0, fires: true},
		{name: "equal passes", depth: 30, bound: 30},
		{name: "one above fires", depth: 31, bound: 30, fires: true},
		{name: "MaxInt64 queue at MaxInt64 bound passes", depth: math.MaxInt64, bound: math.MaxInt64},
		{name: "MaxInt64+1 queue fires at MaxInt64 bound", depth: math.MaxInt64 + 1, bound: math.MaxInt64, fires: true},
		{name: "MaxUint64 queue fires at MaxInt64 bound", depth: math.MaxUint64, bound: math.MaxInt64, fires: true},
		{name: "MaxUint64 queue fires at zero bound", depth: math.MaxUint64, bound: 0, fires: true},
	}

	for _, check := range checks {
		for _, bound := range bounds {
			t.Run(check.name+"/"+bound.name, func(t *testing.T) {
				t.Parallel()

				row := check.row(testMemberA, bound.depth)
				findings := check.check([]GroupMemberStats{row}, bound.bound)
				if !bound.fires {
					if len(findings) != 0 {
						t.Errorf("findings = %#v, want none", findings)
					}

					return
				}
				if len(findings) != 1 {
					t.Fatalf("returned %d findings, want 1", len(findings))
				}
				assertFindingShape(t, findings[0], check.id, nil)
				facts, ok := findings[0].Facts.(GroupMemberStats)
				if !ok {
					t.Fatalf("Finding.Facts is %T, want GroupMemberStats", findings[0].Facts)
				}
				if facts != row {
					t.Errorf("Finding.Facts = %#v, want %#v", facts, row)
				}
			})
		}

		t.Run(check.name+"/order kept and empty passes", func(t *testing.T) {
			t.Parallel()

			if findings := check.check(nil, 0); len(findings) != 0 {
				t.Errorf("empty stats = %#v, want no findings", findings)
			}

			stats := []GroupMemberStats{
				check.row(testMemberC, 50), check.row(testMemberA, 5), check.row(testMemberB, 40),
			}
			findings := check.check(stats, 10)
			if len(findings) != 2 {
				t.Fatalf("returned %d findings, want 2", len(findings))
			}
			for index, wantID := range []string{testMemberC, testMemberB} {
				facts, ok := findings[index].Facts.(GroupMemberStats)
				if !ok {
					t.Fatalf("findings[%d].Facts is %T, want GroupMemberStats", index, findings[index].Facts)
				}
				if facts.MemberID != wantID {
					t.Errorf("findings[%d] is member %q, want %q: supplied order not kept", index, facts.MemberID, wantID)
				}
			}
		})
	}
}

// runningStatsRow is the gate recipe's coverage rule from docs/replication.md,
// "Gating on a group": a stats row for the member, with a non-empty ViewID.
// It lives here, not in the library, because the recipe leaves it to the
// consumer; the test proves the rule catches what the checks pass.
func runningStatsRow(stats []GroupMemberStats, memberID string) bool {
	for index := range stats {
		if stats[index].MemberID == memberID && stats[index].ViewID != "" {
			return true
		}
	}

	return false
}

// TestGroupReplicationGateComposition pins why the documented gate recipe must
// itself require running stats for every member it gates: these snapshots
// pass every check, and only the recipe's coverage rule rejects them.
func TestGroupReplicationGateComposition(t *testing.T) {
	t.Parallel()

	gate := func(members []GroupMember, stats []GroupMemberStats, maxTransactions int64) []Finding {
		findings := CheckGroupReplicationLocalMemberOnline(members)
		findings = append(findings, CheckGroupReplicationMembersOnline(members)...)
		findings = append(findings, CheckGroupReplicationApplierQueueWithin(stats, maxTransactions)...)
		findings = append(findings, CheckGroupReplicationCertifierQueueWithin(stats, maxTransactions)...)

		return findings
	}
	covered := func(members []GroupMember, stats []GroupMemberStats) bool {
		for index := range members {
			if !runningStatsRow(stats, members[index].MemberID) {
				return false
			}
		}

		return true
	}
	members := []GroupMember{
		onlineMember(testMemberA, true), onlineMember(testMemberB, false), onlineMember(testMemberC, false),
	}

	passing := []struct {
		name    string
		members []GroupMember
		stats   []GroupMemberStats
	}{
		{name: "ONLINE members with empty stats", members: members, stats: []GroupMemberStats{}},
		{
			name: "ONLINE members with stats missing one peer", members: members,
			stats: []GroupMemberStats{statsRow(testMemberA, 0, 0), statsRow(testMemberB, 0, 0)},
		},
		{
			// A members read that still shows the member ONLINE, paired with a
			// stats read taken after it stopped: its own row, no view, zeros.
			name:    "an ONLINE member with its stopped stats row",
			members: []GroupMember{onlineMember(testMemberA, true)},
			stats:   []GroupMemberStats{{ChannelName: testApplierChannel, MemberID: testMemberA, Self: true}},
		},
	}
	for _, testCase := range passing {
		t.Run(testCase.name+" pass every check and fail the recipe", func(t *testing.T) {
			t.Parallel()

			if findings := gate(testCase.members, testCase.stats, 0); len(findings) != 0 {
				t.Errorf("gate = %#v, want no findings — the recipe's coverage rule is what catches this", findings)
			}
			if covered(testCase.members, testCase.stats) {
				t.Error("the recipe's coverage rule accepted these stats; it must reject them")
			}
		})
	}

	t.Run("running stats for every member satisfy the recipe", func(t *testing.T) {
		t.Parallel()

		stats := []GroupMemberStats{
			statsRow(testMemberA, 0, 0), statsRow(testMemberB, 0, 0), statsRow(testMemberC, 0, 0),
		}
		if findings := gate(members, stats, 0); len(findings) != 0 {
			t.Errorf("gate = %#v, want no findings", findings)
		}
		if !covered(members, stats) {
			t.Error("the recipe's coverage rule rejected running stats for every member")
		}
	})

	t.Run("a server that is not a member fails the gate", func(t *testing.T) {
		t.Parallel()

		findings := gate([]GroupMember{}, []GroupMemberStats{}, 0)
		if len(findings) != 1 || findings[0].Check != IDGroupReplicationLocalMemberOnline {
			t.Errorf("gate = %#v, want exactly one %s finding", findings, IDGroupReplicationLocalMemberOnline)
		}
	})
}

func TestGroupReplicationFindingJSON(t *testing.T) {
	t.Parallel()

	findings := CheckGroupReplicationMembersOnline([]GroupMember{memberInState(testMemberB, "ERROR", false)})
	if len(findings) != 1 {
		t.Fatalf("returned %d findings, want 1", len(findings))
	}
	encoded, err := json.Marshal(findings[0])
	if err != nil {
		t.Fatalf("json.Marshal returned error %v, want nil", err)
	}
	if !strings.Contains(string(encoded), `"channels":null`) ||
		!strings.Contains(string(encoded), `"facts":{"channel_name":"group_replication_applier","member_id":"`+testMemberB+`"`) {
		t.Errorf("json.Marshal = %s, want channels null and the whole GroupMember as facts", encoded)
	}
}
