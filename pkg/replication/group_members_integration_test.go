//go:build integration

package replication_test

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/replication"
)

// These tests pin docs/COMPAT.md entries 31 and 32, the two Performance Schema
// facts' privileges, and the four Group Replication checks against the
// topology of tests/docker/compose_group_replication.yaml. None calls
// t.Parallel: the cluster is shared and one subtest stops a member.

// Statements spelled independently of the library's constants.
const (
	grSQLMembersRows = "SELECT * FROM performance_schema.replication_group_members"
	grSQLStatsRows   = "SELECT * FROM performance_schema.replication_group_member_stats"
	grSQLVersion     = "SELECT VERSION()"

	grTableMembers = "performance_schema.replication_group_members"
	grTableStats   = "performance_schema.replication_group_member_stats"

	erTableAccessDenied = 1142 // ER_TABLEACCESS_DENIED_ERROR

	grApplierChannel = "group_replication_applier"
	grMemberOnline   = "ONLINE"
	grMemberOffline  = "OFFLINE"

	// Measured MEMBER_COMMUNICATION_STACK spellings (COMPAT entry 31): the
	// AdminAPI cluster runs the MySQL stack; a never-started 9.7 server
	// reports the default XCom. The manual's prose spells them MYSQL and XCOM.
	grStackMySQL = "MySQL"
	grStackXCom  = "XCom"
)

// versionFamily names the supported release family a VERSION() string
// belongs to, and its numeric version with any distribution build suffix
// removed (Percona's "8.0.46-37" is "8.0.46"). An unsupported family fails
// the test rather than falling into either branch.
func versionFamily(t *testing.T, version string) (family, numeric string) {
	t.Helper()

	numeric, _, _ = strings.Cut(version, "-")
	for _, family := range []string{"8.0", "8.4", "9.7"} {
		if strings.HasPrefix(numeric, family+".") {
			return family, numeric
		}
	}
	t.Fatalf("VERSION() = %q is not a supported family (8.0, 8.4, 9.7)", version)

	return "", ""
}

// waitForFullView polls db's view until it lists exactly the members in ids,
// all ONLINE, with exactly one Self row naming self. Local ONLINE states,
// which EnsureGroupOnline waits for, do not prove another member's view has
// converged.
func waitForFullView(t *testing.T, role string, db *sql.DB, ids map[string]string, self string) {
	t.Helper()

	testsupport.PollUntil(t, "the "+role+"'s view holds every member ONLINE", func() (bool, string) {
		view := groupMembers(t, db)
		if len(view) != len(ids) {
			return false, viewDescription(view)
		}
		selves := 0
		for index := range view {
			member := &view[index]
			if member.State != grMemberOnline || ids[member.MemberID] == "" {
				return false, viewDescription(view)
			}
			if member.Self {
				selves++
				if member.MemberID != self {
					return false, viewDescription(view)
				}
			}
		}

		return selves == 1, viewDescription(view)
	})
}

// TestCompat31And32GroupTablesByStateIntegration pins both tables in every
// state docs/COMPAT.md entries 31 and 32 record, through the shipped facts and
// the raw tables, and runs the member checks over each state.
func TestCompat31And32GroupTablesByStateIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	t.Run("no active plugin", func(t *testing.T) {
		standalone, _ := testsupport.MySQLDatabase(t, "grnoplugin")

		for role, db := range map[string]*sql.DB{"not installed": standalone, "disabled": topology.Disabled} {
			members := groupMembers(t, db)
			stats := groupMemberStats(t, db)
			if len(members) != 0 || len(stats) != 0 {
				t.Errorf("%s: GroupMembers = %+v, GroupMemberStats = %+v, want both empty", role, members, stats)
			}
			assertRawRows(t, role, db, 0, 0)

			if findings := replication.CheckGroupReplicationLocalMemberOnline(members); len(findings) != 1 {
				t.Errorf("%s: LOCAL_MEMBER_ONLINE returned %d findings, want 1", role, len(findings))
			}
		}
	})

	t.Run("never started", func(t *testing.T) {
		db := topology.Installed
		family, numeric := versionFamily(t, scalarString(t, db, grSQLVersion))

		members := groupMembers(t, db)
		if len(members) != 1 {
			t.Fatalf("GroupMembers = %+v, want exactly one row", members)
		}
		member := members[0]
		if member.ChannelName != grApplierChannel || member.State != grMemberOffline || member.Role != "" {
			t.Errorf("GroupMembers row = %+v, want an OFFLINE %s row with no role", member, grApplierChannel)
		}

		if family == "9.7" {
			// 9.7 identifies the never-started member in this table, with
			// the version's build suffix omitted and the default stack.
			if member.MemberID != testsupport.ServerUUID(t, db) || !member.Self || !member.Port.Valid ||
				member.Host == "" || member.CommunicationStack != grStackXCom || member.Version != numeric {
				t.Errorf("9.7 never-started member = %+v, want it identified as itself, version %q, stack %q",
					member, numeric, grStackXCom)
			}
		} else {
			// 8.0 and 8.4 report it anonymous, with a NULL port.
			want := replication.GroupMember{ChannelName: grApplierChannel, State: grMemberOffline}
			if member != want {
				t.Errorf("8.x never-started member = %+v, want %+v", member, want)
			}
		}

		// Every version, 9.7 included: one anonymous row of zeros.
		stats := groupMemberStats(t, db)
		want := []replication.GroupMemberStats{{ChannelName: grApplierChannel}}
		if len(stats) != 1 || stats[0] != want[0] {
			t.Errorf("GroupMemberStats = %+v, want %+v", stats, want)
		}
		assertRawRows(t, "never-started server", db, 1, 1)

		local := replication.CheckGroupReplicationLocalMemberOnline(members)
		if len(local) != 1 {
			t.Errorf("LOCAL_MEMBER_ONLINE on a never-started server returned %d findings, want 1", len(local))
		}
		if online := replication.CheckGroupReplicationMembersOnline(members); len(online) != 1 {
			t.Errorf("MEMBERS_ONLINE on a never-started server returned %d findings, want 1", len(online))
		}
	})

	t.Run("formed group", func(t *testing.T) {
		ids := map[string]string{
			"primary":   testsupport.ServerUUID(t, topology.Primary),
			"secondary": testsupport.ServerUUID(t, topology.Secondary),
			"rejoiner":  testsupport.ServerUUID(t, topology.Rejoiner),
		}
		memberRoles := make(map[string]string, len(ids))
		for role, id := range ids {
			memberRoles[id] = role
		}
		roles := map[string]string{"primary": "PRIMARY", "secondary": "SECONDARY"}
		_, numeric := versionFamily(t, scalarString(t, topology.Primary, grSQLVersion))

		for role, db := range map[string]*sql.DB{"primary": topology.Primary, "secondary": topology.Secondary} {
			waitForFullView(t, role, db, memberRoles, ids[role])
			members := groupMembers(t, db)
			if len(members) != 3 {
				t.Fatalf("GroupMembers on the %s = %+v, want three rows", role, members)
			}
			self := 0
			for index := range members {
				member := &members[index]
				if member.State != grMemberOnline || member.ChannelName != grApplierChannel || !member.Port.Valid {
					t.Errorf("GroupMembers on the %s: row %+v, want ONLINE with a port", role, member)
				}
				if member.Version != numeric || member.CommunicationStack != grStackMySQL {
					t.Errorf("GroupMembers on the %s: row %+v, want version %q and stack %q",
						role, member, numeric, grStackMySQL)
				}
				if member.Self {
					self++
					if member.MemberID != ids[role] || member.Role != roles[role] {
						t.Errorf("GroupMembers on the %s: own row %+v, want ID %s and role %s",
							role, member, ids[role], roles[role])
					}
				}
			}
			if self != 1 {
				t.Errorf("GroupMembers on the %s: %d rows marked Self, want 1", role, self)
			}
			assertRawRows(t, role, db, 3, -1)

			// Stats are periodic copies: wait until they cover every member
			// rather than assume they already do.
			var stats []replication.GroupMemberStats
			testsupport.PollUntil(t, "stats on the "+role+" cover every member", func() (bool, string) {
				stats = groupMemberStats(t, db)

				return statsCover(stats, ids), "stats rows for " + statsIDs(stats)
			})
			self = 0
			for _, row := range stats {
				if row.ViewID == "" || row.ChannelName != grApplierChannel {
					t.Errorf("GroupMemberStats on the %s: row %+v, want a view ID", role, row)
				}
				if row.Self {
					self++
					if row.MemberID != ids[role] {
						t.Errorf("GroupMemberStats on the %s: own row is %s, want %s", role, row.MemberID, ids[role])
					}
				}
			}
			if self != 1 {
				t.Errorf("GroupMemberStats on the %s: %d rows marked Self, want 1", role, self)
			}

			findings := replication.CheckGroupReplicationLocalMemberOnline(members)
			findings = append(findings, replication.CheckGroupReplicationMembersOnline(members)...)
			findings = append(findings, replication.CheckGroupReplicationApplierQueueWithin(stats, math.MaxInt64)...)
			findings = append(findings, replication.CheckGroupReplicationCertifierQueueWithin(stats, math.MaxInt64)...)
			if len(findings) != 0 {
				t.Errorf("checks on the healthy %s = %+v, want no findings", role, findings)
			}
			if fired := replication.CheckGroupReplicationApplierQueueWithin(stats, -1); len(fired) != len(stats) {
				t.Errorf("APPLIER_QUEUE_WITHIN at -1 on the %s returned %d findings, want %d", role, len(fired), len(stats))
			}
		}
	})

	t.Run("stopped after participating", func(t *testing.T) {
		rejoiner := testsupport.ServerUUID(t, topology.Rejoiner)

		// Registered before the stop, so a failure below still returns the
		// group to all-ONLINE for the next test.
		t.Cleanup(func() { testsupport.EnsureGroupOnline(t, topology) })
		execOn(t, topology.Rejoiner, "STOP GROUP_REPLICATION")
		testsupport.WaitMemberState(t, topology.Rejoiner, testsupport.MemberOffline)

		members := groupMembers(t, topology.Rejoiner)
		if len(members) != 1 {
			t.Fatalf("GroupMembers on the stopped member = %+v, want its own row only", members)
		}
		own := members[0]
		if own.MemberID != rejoiner || !own.Self || own.State != grMemberOffline || own.Role != "" || !own.Port.Valid {
			t.Errorf("GroupMembers on the stopped member = %+v, want its own OFFLINE row with no role", own)
		}

		stats := groupMemberStats(t, topology.Rejoiner)
		want := replication.GroupMemberStats{ChannelName: grApplierChannel, MemberID: rejoiner, Self: true}
		if len(stats) != 1 || stats[0] != want {
			t.Errorf("GroupMemberStats on the stopped member = %+v, want [%+v]", stats, want)
		}
		assertRawRows(t, "stopped member", topology.Rejoiner, 1, 1)

		if findings := replication.CheckGroupReplicationLocalMemberOnline(members); len(findings) != 1 {
			t.Errorf("LOCAL_MEMBER_ONLINE on the stopped member returned %d findings, want 1", len(findings))
		}

		// The others drop it from their view; they do not list it OFFLINE.
		testsupport.PollUntil(t, "the primary's view drops the stopped member", func() (bool, string) {
			view := groupMembers(t, topology.Primary)
			for index := range view {
				if member := &view[index]; member.MemberID == rejoiner {
					return false, "still listed as " + member.State
				}
			}

			return len(view) == 2, "view of " + viewDescription(view)
		})
	})
}

// TestGroupMemberFactsPrivilegesIntegration proves each Performance Schema
// fact is refused to a USAGE-only account and allowed by SELECT on its own
// table alone: each success is observed on an account that holds only that
// one grant and is still refused the other fact.
func TestGroupMemberFactsPrivilegesIntegration(t *testing.T) {
	topology := testsupport.GroupReplicationTopology(t)

	inspectorFor := func(prefix, table string) *replication.Inspector {
		account := testsupport.CreateMySQLAccount(t, topology.Primary, prefix)
		if table != "" {
			execOn(t, topology.Primary, "GRANT SELECT ON "+table+" TO '"+account.User+"'@'"+account.Host+"'")
		}

		return replication.NewInspector(testsupport.MySQLDBAsOn(t, topology.PrimaryDSN, account))
	}

	usage := inspectorFor("grpusage", "")
	_, err := usage.GroupMembers(t.Context())
	assertRefused(t, err, "group_members")
	_, err = usage.GroupMemberStats(t.Context())
	assertRefused(t, err, "group_member_stats")

	membersOnly := inspectorFor("grpmembers", grTableMembers)
	members, err := membersOnly.GroupMembers(t.Context())
	if err != nil || len(members) != 3 {
		t.Errorf("GroupMembers with SELECT on its table only = %+v, %v; want three rows", members, err)
	}
	_, err = membersOnly.GroupMemberStats(t.Context())
	assertRefused(t, err, "group_member_stats")

	statsOnly := inspectorFor("grpstats", grTableStats)
	stats, err := statsOnly.GroupMemberStats(t.Context())
	if err != nil || len(stats) == 0 {
		t.Errorf("GroupMemberStats with SELECT on its table only = %+v, %v; want rows", stats, err)
	}
	_, err = statsOnly.GroupMembers(t.Context())
	assertRefused(t, err, "group_members")
}

func groupMembers(t *testing.T, db *sql.DB) []replication.GroupMember {
	t.Helper()

	members, err := replication.NewInspector(db).GroupMembers(t.Context())
	if err != nil {
		t.Fatalf("GroupMembers: %v", err)
	}

	return members
}

func groupMemberStats(t *testing.T, db *sql.DB) []replication.GroupMemberStats {
	t.Helper()

	stats, err := replication.NewInspector(db).GroupMemberStats(t.Context())
	if err != nil {
		t.Fatalf("GroupMemberStats: %v", err)
	}

	return stats
}

// assertRawRows counts each table's rows with a plain SELECT, so a
// library-side filter could not fake a row count. A negative want skips that
// table.
func assertRawRows(t *testing.T, role string, db *sql.DB, wantMembers, wantStats int) {
	t.Helper()

	if wantMembers >= 0 {
		if got := countRows(t, db, grSQLMembersRows); got != wantMembers {
			t.Errorf("%s: replication_group_members has %d rows, want %d", role, got, wantMembers)
		}
	}
	if wantStats >= 0 {
		if got := countRows(t, db, grSQLStatsRows); got != wantStats {
			t.Errorf("%s: replication_group_member_stats has %d rows, want %d", role, got, wantStats)
		}
	}
}

func assertRefused(t *testing.T, err error, op string) {
	t.Helper()

	var opErr *replication.OpError
	if !errors.As(err, &opErr) || opErr.Op != op {
		t.Fatalf("error %v, want *OpError with Op %s", err, op)
	}
	var serverErr *mysql.MySQLError
	if !errors.As(err, &serverErr) || serverErr.Number != erTableAccessDenied {
		t.Errorf("%s failed with %v, want server error %d", op, err, erTableAccessDenied)
	}
}

func statsCover(stats []replication.GroupMemberStats, ids map[string]string) bool {
	for _, id := range ids {
		found := false
		for _, row := range stats {
			if row.MemberID == id {
				found = true
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func statsIDs(stats []replication.GroupMemberStats) string {
	ids := make([]string, 0, len(stats))
	for _, row := range stats {
		ids = append(ids, row.MemberID)
	}

	return strings.Join(ids, ", ")
}

func viewDescription(members []replication.GroupMember) string {
	ids := make([]string, 0, len(members))
	for index := range members {
		ids = append(ids, members[index].MemberID+" "+members[index].State)
	}

	return strings.Join(ids, ", ")
}
