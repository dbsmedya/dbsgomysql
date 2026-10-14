//go:build integration

package validations_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
	"github.com/dbsmedya/dbsgomysql/pkg/validations"
)

// These tests pin docs/COMPAT.md entry 34: the server lists a table's triggers
// only to an account holding TRIGGER on that table, so an empty Triggers result
// proves nothing without CheckTriggerVisibility. Each test reads, as one
// account variation, what the server shows and what the check reports. Where
// the server's answer is the measured behavior — partial revokes, roles, the
// definer — a failure is a divergence from the manual: stop and take it to the
// operator (spec §7.2).

const (
	trgGuarded = "guarded" // DELETE and INSERT triggers, BEFORE and AFTER
	trgSibling = "sibling" // one DELETE and one INSERT trigger
	trgPlain   = "plain"   // no triggers
	trgDefined = "defined" // dedicated to the definer case, which revokes TRIGGER here
)

var trgTables = []string{trgGuarded, trgSibling, trgPlain}

// The fixture's triggers as a privileged account sees them, in the fact's
// order: requested table, then BEFORE ahead of AFTER, then name as bytes.
var (
	trgAllDelete = []string{"guarded/g_bd_a/BEFORE", "guarded/g_bd_b/BEFORE", "guarded/g_ad/AFTER", "sibling/s_bd/BEFORE"}
	trgAllInsert = []string{"guarded/g_bi/BEFORE", "guarded/g_ai/AFTER", "sibling/s_ai/AFTER"}
)

type triggerFixture struct {
	admin  *sql.DB
	schema string
}

func (f *triggerFixture) table(name string) string {
	return sqlutil.QuoteQualified(f.schema, name)
}

func (f *triggerFixture) schemaGrant() string {
	return sqlutil.QuoteIdentifier(f.schema) + ".*"
}

// newTriggerFixture builds four tables and seven single-statement triggers,
// then proves a privileged account sees exactly the expected identities, so an
// empty answer elsewhere is never an empty fixture.
func newTriggerFixture(t *testing.T) *triggerFixture {
	t.Helper()

	admin, schema := testsupport.MySQLDatabase(t, "dbsgomysql_trgvis")
	f := &triggerFixture{admin: admin, schema: schema}
	for _, name := range []string{trgGuarded, trgSibling, trgPlain, trgDefined} {
		testsupport.ExecSQL(t, admin, "CREATE TABLE "+f.table(name)+" (id INT PRIMARY KEY) ENGINE=InnoDB")
	}
	for _, trigger := range []struct{ name, timing, event, table string }{
		{"g_bd_a", "BEFORE", "DELETE", trgGuarded},
		{"g_bd_b", "BEFORE", "DELETE", trgGuarded},
		{"g_ad", "AFTER", "DELETE", trgGuarded},
		{"g_bi", "BEFORE", "INSERT", trgGuarded},
		{"g_ai", "AFTER", "INSERT", trgGuarded},
		{"s_bd", "BEFORE", "DELETE", trgSibling},
		{"s_ai", "AFTER", "INSERT", trgSibling},
	} {
		row := "OLD"
		if trigger.event == "INSERT" {
			row = "NEW"
		}
		testsupport.ExecSQL(t, admin, "CREATE TRIGGER "+f.table(trigger.name)+" "+trigger.timing+" "+
			trigger.event+" ON "+f.table(trigger.table)+" FOR EACH ROW SET @dbsgomysql_trgvis = "+row+".id")
	}

	root := validations.NewInspector(admin, schema)
	if got := triggerIDs(t, root, trgTables, validations.TriggerDelete); !slices.Equal(got, trgAllDelete) {
		t.Fatalf("privileged DELETE triggers = %q, want %q", got, trgAllDelete)
	}
	if got := triggerIDs(t, root, trgTables, validations.TriggerInsert); !slices.Equal(got, trgAllInsert) {
		t.Fatalf("privileged INSERT triggers = %q, want %q", got, trgAllInsert)
	}

	return f
}

// account creates an account, runs each grant with {acct} replaced by its
// quoted name, and returns it with its grantee spelling.
func (f *triggerFixture) account(
	t *testing.T, prefix string, grants ...string,
) (account testsupport.MySQLAccount, grantee string) {
	t.Helper()

	account = testsupport.CreateMySQLAccount(t, f.admin, prefix)
	grantee = testsupport.GrantAccountSQL(account)
	for _, grant := range grants {
		testsupport.ExecSQL(t, f.admin, strings.ReplaceAll(grant, "{acct}", grantee))
	}

	return account, grantee
}

// triggerView is what one session sees: the check's findings over trgTables
// and the trigger identities for both events.
type triggerView struct {
	findingTables []string
	states        []validations.GrantState
	deletes       []string
	inserts       []string
}

func (f *triggerFixture) view(t *testing.T, conn *sql.Conn) triggerView {
	t.Helper()

	inspector := validations.NewInspector(conn, f.schema)
	grants, err := inspector.Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	var view triggerView
	for _, finding := range validations.CheckTriggerVisibility(grants, f.schema, trgTables) {
		fact, ok := finding.Facts.(validations.PrivilegeFact)
		if !ok || fact.Privilege != validations.PrivilegeTrigger {
			t.Fatalf("finding Facts = %#v, want a TRIGGER PrivilegeFact", finding.Facts)
		}
		view.findingTables = append(view.findingTables, finding.Tables...)
		view.states = append(view.states, fact.State)
	}
	view.deletes = triggerIDs(t, inspector, trgTables, validations.TriggerDelete)
	view.inserts = triggerIDs(t, inspector, trgTables, validations.TriggerInsert)

	return view
}

func triggerIDs(t *testing.T, inspector *validations.Inspector, tables []string, event validations.TriggerEvent) []string {
	t.Helper()

	triggers, err := inspector.Triggers(t.Context(), tables, event)
	if err != nil {
		t.Fatalf("Triggers(%s): %v", event, err)
	}
	ids := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		ids = append(ids, trigger.Table+"/"+trigger.Name+"/"+trigger.Timing)
	}

	return ids
}

func onTable(ids []string, table string) []string {
	var kept []string
	for _, id := range ids {
		if strings.HasPrefix(id, table+"/") {
			kept = append(kept, id)
		}
	}

	return kept
}

func assertView(t *testing.T, got *triggerView, wantFindings, wantDeletes, wantInserts []string) {
	t.Helper()

	if !slices.Equal(got.findingTables, wantFindings) {
		t.Errorf("CheckTriggerVisibility reports %q, want %q", got.findingTables, wantFindings)
	}
	for index, state := range got.states {
		if state == validations.GrantPresent {
			t.Errorf("finding %d carries state present", index)
		}
	}
	if !slices.Equal(got.deletes, wantDeletes) {
		t.Errorf("visible DELETE triggers = %q, want %q", got.deletes, wantDeletes)
	}
	if !slices.Equal(got.inserts, wantInserts) {
		t.Errorf("visible INSERT triggers = %q, want %q", got.inserts, wantInserts)
	}
}

func requirePartialRevokesOff(t *testing.T, admin *sql.DB) {
	t.Helper()

	var enabled int
	if err := admin.QueryRowContext(t.Context(), "SELECT @@global.partial_revokes").Scan(&enabled); err != nil {
		t.Fatalf("read partial_revokes: %v", err)
	}
	if enabled != 0 {
		t.Fatal("partial_revokes is enabled instance-wide; this test needs it off")
	}
}

// requireRolesInactiveAtLogin: a fresh session has no active role only while
// activate_all_roles_on_login is OFF and mandatory_roles is empty, both the
// server defaults ("Activating Roles"). The role test's inactive half and the
// provable-absence test's role-free session depend on it.
func requireRolesInactiveAtLogin(t *testing.T, admin *sql.DB) {
	t.Helper()

	var activateAll int
	var mandatory string
	if err := admin.QueryRowContext(t.Context(),
		"SELECT @@global.activate_all_roles_on_login, @@global.mandatory_roles",
	).Scan(&activateAll, &mandatory); err != nil {
		t.Fatalf("read role activation settings: %v", err)
	}
	if activateAll != 0 || mandatory != "" {
		t.Fatalf("activate_all_roles_on_login = %d, mandatory_roles = %q; this test needs OFF and empty",
			activateAll, mandatory)
	}
}

// TestCompat34WithoutTriggerPrivilegeIntegration: DML and SELECT on the schema,
// no TRIGGER. The account sees no trigger on any table, so the guarded table
// looks exactly like the trigger-free one, CheckTriggersPresent passes, and
// the new check reports every table.
func TestCompat34WithoutTriggerPrivilegeIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	account, _ := f.account(t, "dbsgomysql_trg_none",
		"GRANT SELECT, INSERT, DELETE ON "+f.schemaGrant()+" TO {acct}")

	conn := testsupport.MySQLConnAs(t, account)
	got := f.view(t, conn)
	assertView(t, &got, trgTables, []string{}, []string{})

	// The consumer's experience without this check: the trigger check passes.
	triggers, err := validations.NewInspector(conn, f.schema).Triggers(t.Context(), trgTables, validations.TriggerDelete)
	if err != nil {
		t.Fatalf("Triggers: %v", err)
	}
	if findings := validations.CheckTriggersPresent(triggers, validations.TriggerDelete); findings != nil {
		t.Errorf("CheckTriggersPresent over the account's answer = %#v, want nil", findings)
	}
}

// TestCompat34TableTriggerGrantIntegration: TRIGGER on one table. In the same
// Triggers read over both tables, the granted table's rows are present and the
// sibling's are absent — the server's per-table gate — and the check proves the
// granted table only.
func TestCompat34TableTriggerGrantIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	account, _ := f.account(t, "dbsgomysql_trg_table",
		"GRANT SELECT ON "+f.schemaGrant()+" TO {acct}",
		"GRANT TRIGGER ON "+f.table(trgGuarded)+" TO {acct}")

	got := f.view(t, testsupport.MySQLConnAs(t, account))
	assertView(t, &got, []string{trgSibling, trgPlain},
		onTable(trgAllDelete, trgGuarded), onTable(trgAllInsert, trgGuarded))
}

// TestCompat34SchemaTriggerGrantIntegration: TRIGGER on the exact schema proves
// every table, and every trigger is visible.
func TestCompat34SchemaTriggerGrantIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	account, _ := f.account(t, "dbsgomysql_trg_schema",
		"GRANT SELECT, TRIGGER ON "+f.schemaGrant()+" TO {acct}")

	got := f.view(t, testsupport.MySQLConnAs(t, account))
	assertView(t, &got, nil, trgAllDelete, trgAllInsert)
}

// TestCompat34TriggerWithoutSelectIntegration: TRIGGER alone, no SELECT
// anywhere. The rows are visible and the check passes for that table.
func TestCompat34TriggerWithoutSelectIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	account, _ := f.account(t, "dbsgomysql_trg_noselect",
		"GRANT TRIGGER ON "+f.table(trgGuarded)+" TO {acct}")

	conn := testsupport.MySQLConnAs(t, account)
	got := f.view(t, conn)
	assertView(t, &got, []string{trgSibling, trgPlain},
		onTable(trgAllDelete, trgGuarded), onTable(trgAllInsert, trgGuarded))

	grants, err := validations.NewInspector(conn, f.schema).Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if state := grants.Table(f.schema, trgGuarded, validations.PrivilegeSelect); state == validations.GrantPresent {
		t.Errorf("SELECT on %s = present, want the account to hold no SELECT", trgGuarded)
	}
}

// TestCompat34GlobalTriggerIntegration: a global TRIGGER with partial revokes
// off proves every table, and every trigger is visible.
func TestCompat34GlobalTriggerIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	requirePartialRevokesOff(t, f.admin)
	account, _ := f.account(t, "dbsgomysql_trg_global", "GRANT TRIGGER ON *.* TO {acct}")

	got := f.view(t, testsupport.MySQLConnAs(t, account))
	assertView(t, &got, nil, trgAllDelete, trgAllInsert)
}

// TestCompat34GlobalTriggerUnderPartialRevokesIntegration: with partial revokes
// on, a global-only TRIGGER stays unconfirmed even where the server shows the
// rows, and a direct table grant still proves its table. The server's own
// visibility is recorded: rows in an unrestricted schema are visible, rows in
// a schema with TRIGGER partially revoked are not.
func TestCompat34GlobalTriggerUnderPartialRevokesIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	restrictedDB, restricted := testsupport.MySQLDatabase(t, "dbsgomysql_trgvis_r")
	restrictedTable := sqlutil.QuoteQualified(restricted, "locked")
	testsupport.ExecSQL(t, restrictedDB, "CREATE TABLE "+restrictedTable+" (id INT PRIMARY KEY) ENGINE=InnoDB")
	testsupport.ExecSQL(t, restrictedDB, "CREATE TRIGGER "+sqlutil.QuoteQualified(restricted, "l_bd")+
		" BEFORE DELETE ON "+restrictedTable+" FOR EACH ROW SET @dbsgomysql_trgvis = OLD.id")

	var original int
	if err := f.admin.QueryRowContext(t.Context(), "SELECT @@global.partial_revokes").Scan(&original); err != nil {
		t.Fatalf("read partial_revokes: %v", err)
	}
	// Registered before any account exists, so it runs after their removal:
	// the server refuses to turn partial revokes off while one is recorded.
	t.Cleanup(func() {
		value := "OFF"
		if original != 0 {
			value = "ON"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := f.admin.ExecContext(ctx, "SET GLOBAL partial_revokes = "+value); err != nil {
			t.Errorf("restore partial_revokes=%s: %v", value, err)
		}
	})
	testsupport.ExecSQL(t, f.admin, "SET GLOBAL partial_revokes = ON")
	revoke := "REVOKE TRIGGER ON " + sqlutil.QuoteIdentifier(restricted) + ".* FROM {acct}"

	globalOnly, _ := f.account(t, "dbsgomysql_trg_partial", "GRANT TRIGGER ON *.* TO {acct}", revoke)
	conn := testsupport.MySQLConnAs(t, globalOnly)
	got := f.view(t, conn)
	assertView(t, &got, trgTables, trgAllDelete, trgAllInsert)
	for index, state := range got.states {
		if state != validations.GrantUnconfirmed {
			t.Errorf("global-only finding %d state = %s, want unconfirmed", index, state)
		}
	}
	lockedIDs := triggerIDs(t, validations.NewInspector(conn, restricted), []string{"locked"}, validations.TriggerDelete)
	if len(lockedIDs) != 0 {
		t.Errorf("partially revoked schema shows triggers %q, want none", lockedIDs)
	}

	withTable, _ := f.account(t, "dbsgomysql_trg_partialtbl",
		"GRANT TRIGGER ON *.* TO {acct}", revoke,
		"GRANT TRIGGER ON "+f.table(trgGuarded)+" TO {acct}")
	got = f.view(t, testsupport.MySQLConnAs(t, withTable))
	assertView(t, &got, []string{trgSibling, trgPlain}, trgAllDelete, trgAllInsert)
}

// TestCompat34RoleHeldTriggerIntegration: TRIGGER through a role, held directly
// or through a nested role. Active on a pinned session the rows are visible,
// but the check cannot prove it and reports every table unconfirmed; in a
// fresh session with the role inactive the rows are hidden.
func TestCompat34RoleHeldTriggerIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	requireRolesInactiveAtLogin(t, f.admin)
	direct := testsupport.CreateMySQLRole(t, f.admin, "dbsgomysql_trg_role")
	inner := testsupport.CreateMySQLRole(t, f.admin, "dbsgomysql_trg_inner")
	outer := testsupport.CreateMySQLRole(t, f.admin, "dbsgomysql_trg_outer")
	for _, role := range []testsupport.MySQLAccount{direct, inner} {
		testsupport.ExecSQL(t, f.admin, "GRANT TRIGGER ON "+f.schemaGrant()+" TO "+testsupport.GrantAccountSQL(role))
	}
	testsupport.ExecSQL(t, f.admin, "GRANT "+testsupport.GrantAccountSQL(inner)+" TO "+testsupport.GrantAccountSQL(outer))

	for _, held := range []struct {
		name string
		role testsupport.MySQLAccount
	}{{"direct role", direct}, {"nested role", outer}} {
		t.Run(held.name, func(t *testing.T) {
			account, _ := f.account(t, "dbsgomysql_trg_rolehold",
				"GRANT "+testsupport.GrantAccountSQL(held.role)+" TO {acct}")

			active := testsupport.MySQLConnAs(t, account)
			if _, err := active.ExecContext(t.Context(), "SET ROLE ALL"); err != nil {
				t.Fatalf("activate roles: %v", err)
			}
			got := f.view(t, active)
			assertView(t, &got, trgTables, trgAllDelete, trgAllInsert)
			for index, state := range got.states {
				if state != validations.GrantUnconfirmed {
					t.Errorf("role-held finding %d state = %s, want unconfirmed", index, state)
				}
			}

			inactive := f.view(t, testsupport.MySQLConnAs(t, account))
			assertView(t, &inactive, trgTables, []string{}, []string{})
		})
	}
}

// TestCompat34DefinerWithoutTriggerIntegration: being a trigger's definer does
// not make it visible. On a dedicated table — the trigger cannot fire once its
// definer loses TRIGGER, so no other case may use it — the account sees its
// trigger while it holds TRIGGER, and neither sees it nor is proven once that
// is revoked.
func TestCompat34DefinerWithoutTriggerIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	account, grantee := f.account(t, "dbsgomysql_trg_definer",
		"GRANT TRIGGER ON "+f.table(trgDefined)+" TO {acct}")
	testsupport.ExecSQL(t, f.admin, "CREATE DEFINER = "+grantee+" TRIGGER "+f.table("d_bd")+
		" BEFORE DELETE ON "+f.table(trgDefined)+" FOR EACH ROW SET @dbsgomysql_trgvis = OLD.id")
	want := []string{"defined/d_bd/BEFORE"}

	holding := validations.NewInspector(testsupport.MySQLConnAs(t, account), f.schema)
	if got := triggerIDs(t, holding, []string{trgDefined}, validations.TriggerDelete); !slices.Equal(got, want) {
		t.Fatalf("definer holding TRIGGER sees %q, want %q", got, want)
	}

	testsupport.ExecSQL(t, f.admin, "REVOKE TRIGGER ON "+f.table(trgDefined)+" FROM "+grantee)
	conn := testsupport.MySQLConnAs(t, account)
	inspector := validations.NewInspector(conn, f.schema)
	if got := triggerIDs(t, inspector, []string{trgDefined}, validations.TriggerDelete); len(got) != 0 {
		t.Errorf("definer without TRIGGER sees %q, want none", got)
	}
	grants, err := inspector.Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if findings := validations.CheckTriggerVisibility(grants, f.schema, []string{trgDefined}); len(findings) != 1 {
		t.Errorf("CheckTriggerVisibility for the definer = %#v, want one finding", findings)
	}
	root := validations.NewInspector(f.admin, f.schema)
	if got := triggerIDs(t, root, []string{trgDefined}, validations.TriggerDelete); !slices.Equal(got, want) {
		t.Errorf("privileged account sees %q, want %q: the trigger itself must still exist", got, want)
	}
}

// TestCompat34ReadOnlyPrivilegesDoNotProveIntegration: neither SELECT nor
// PROCESS shows trigger rows or proves anything.
func TestCompat34ReadOnlyPrivilegesDoNotProveIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	for _, variant := range []struct{ name, grant string }{
		{"SELECT only", "GRANT SELECT ON " + f.schemaGrant() + " TO {acct}"},
		{"PROCESS only", "GRANT PROCESS ON *.* TO {acct}"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			account, _ := f.account(t, "dbsgomysql_trg_readonly", variant.grant)
			got := f.view(t, testsupport.MySQLConnAs(t, account))
			assertView(t, &got, trgTables, []string{}, []string{})
		})
	}
}

// TestCompat34ProvableAbsenceIntegration: on a pinned, role-free session with
// partial revokes off and a direct SELECT on mysql.*, a missing TRIGGER is
// GrantAbsent, not merely unconfirmed — and a column SELECT grant on the table
// does not weaken it, because column weakening is keyed by privilege.
func TestCompat34ProvableAbsenceIntegration(t *testing.T) {
	f := newTriggerFixture(t)
	requirePartialRevokesOff(t, f.admin)
	requireRolesInactiveAtLogin(t, f.admin)
	account, grantee := f.account(t, "dbsgomysql_trg_absent",
		"GRANT SELECT ON mysql.* TO {acct}",
		"GRANT DELETE ON "+f.schemaGrant()+" TO {acct}")
	conn := testsupport.MySQLConnAs(t, account)
	inspector := validations.NewInspector(conn, f.schema)

	for _, step := range []struct {
		name  string
		grant string
	}{
		{"without a column grant", ""},
		{"with a column SELECT grant", "GRANT SELECT (id) ON " + f.table(trgGuarded) + " TO " + grantee},
	} {
		if step.grant != "" {
			testsupport.ExecSQL(t, f.admin, step.grant)
		}
		grants, err := inspector.Grants(t.Context())
		if err != nil {
			t.Fatalf("%s: Grants: %v", step.name, err)
		}
		findings := validations.CheckTriggerVisibility(grants, f.schema, []string{trgGuarded})
		if len(findings) != 1 {
			t.Fatalf("%s: findings = %#v, want one", step.name, findings)
		}
		if fact, ok := findings[0].Facts.(validations.PrivilegeFact); !ok || fact.State != validations.GrantAbsent {
			t.Errorf("%s: finding Facts = %#v, want state absent", step.name, findings[0].Facts)
		}
	}
}
