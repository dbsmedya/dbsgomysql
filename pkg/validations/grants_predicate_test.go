package validations

import (
	"database/sql/driver"
	"strconv"
	"strings"
	"testing"
)

// grantsPreamble is the three queries every Grants call issues before it
// reaches a privilege table.
func grantsPreamble(currentUser string, roles [][]driver.Value) []queryStep {
	return []queryStep{
		{
			contains: "CURRENT_USER()",
			columns:  []string{"CURRENT_USER()"},
			rows:     [][]driver.Value{{currentUser}},
		},
		{
			contains: "ENABLED_ROLES",
			columns:  []string{"ROLE_NAME", "ROLE_HOST"},
			rows:     roles,
		},
		{
			contains: "@@global.partial_revokes",
			columns:  []string{"partial_revokes"},
			rows:     [][]driver.Value{{int64(0)}},
		},
	}
}

// privilegeSteps returns the four privilege-table steps, with subject's step
// carrying the caller's matcher and the other three left permissive. Only the
// subject is under test in any one case, which is what keeps four call sites
// from hiding behind one assertion.
func privilegeSteps(subject string, match *queryStep) []queryStep {
	sites := []struct {
		source  string
		columns []string
	}{
		{"USER_PRIVILEGES", []string{"GRANTEE", "PRIVILEGE_TYPE"}},
		{"SCHEMA_PRIVILEGES", []string{"GRANTEE", "TABLE_SCHEMA", "PRIVILEGE_TYPE"}},
		{
			"TABLE_PRIVILEGES",
			[]string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
		},
		{
			"COLUMN_PRIVILEGES",
			[]string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
		},
	}

	steps := make([]queryStep, 0, len(sites))
	for _, site := range sites {
		step := queryStep{contains: site.source, columns: site.columns}
		if site.source == subject {
			step.contains = match.contains
			step.lacks = match.lacks
			step.rows = match.rows
		}
		steps = append(steps, step)
	}

	return steps
}

func grantsSites() []string {
	return []string{
		"USER_PRIVILEGES",
		"SCHEMA_PRIVILEGES",
		"TABLE_PRIVILEGES",
		"COLUMN_PRIVILEGES",
	}
}

func roleRows(count int) [][]driver.Value {
	rows := make([][]driver.Value, 0, count)
	for index := range count {
		rows = append(rows, []driver.Value{"r" + strconv.Itoa(index), "%"})
	}

	return rows
}

func TestGrantsNarrowsEveryPrivilegeSiteOnDistinctGrantees(t *testing.T) {
	t.Parallel()

	// One account plus one enabled role is two distinct grantees, so each of
	// the four statements binds two account/role parameters. Each site is
	// asserted in its own case: they are four separate call sites, and one
	// shared assertion would pass while the others went unwired.
	for _, site := range grantsSites() {
		t.Run(site, func(t *testing.T) {
			t.Parallel()

			steps := grantsPreamble("app@%", [][]driver.Value{{"writer", "%"}})
			steps = append(steps, privilegeSteps(site, &queryStep{
				contains: "GRANTEE IN (?,?)",
			})...)
			script := &queryScript{steps: steps}

			if _, err := NewInspector(openScriptedDB(t, script), "shop").Grants(
				t.Context(),
			); err != nil {
				t.Fatalf("Grants: %v", err)
			}
			script.assertDone(t)
		})
	}
}

func TestGrantsOnlySchemaPredicateIncludesAnonymousAccounts(t *testing.T) {
	t.Parallel()

	for _, site := range grantsSites() {
		t.Run(site, func(t *testing.T) {
			t.Parallel()

			match := queryStep{contains: site}
			if site == "SCHEMA_PRIVILEGES" {
				match.contains = "OR GRANTEE LIKE ?"
			} else {
				match.lacks = "GRANTEE LIKE ?"
			}
			steps := grantsPreamble("app@%", nil)
			steps = append(steps, privilegeSteps(site, &match)...)
			script := &queryScript{steps: steps}

			if _, err := NewInspector(openScriptedDB(t, script), "shop").Grants(
				t.Context(),
			); err != nil {
				t.Fatalf("Grants: %v", err)
			}
			script.assertDone(t)
		})
	}
}

func TestAnonymousGranteePredicateBindsPattern(t *testing.T) {
	t.Parallel()

	query, args := granteePredicateWithAnonymous(
		"SELECT GRANTEE FROM information_schema.SCHEMA_PRIVILEGES",
		[]string{"'app'@'%'"},
	)
	if !strings.Contains(query, "GRANTEE IN (?)") ||
		!strings.Contains(query, "OR GRANTEE LIKE ?") {
		t.Errorf("query = %q, want bound account and anonymous predicates", query)
	}
	if strings.Contains(query, "''@%") {
		t.Errorf("query embeds anonymous pattern instead of binding it: %q", query)
	}
	if len(args) != 2 || args[0] != "'app'@'%'" || args[1] != "''@%" {
		t.Errorf("args = %#v, want account followed by anonymous pattern", args)
	}
}

func TestGrantsDeduplicatesAnAccountThatIsAlsoAnEnabledRole(t *testing.T) {
	t.Parallel()

	// MySQL puts users and roles in one namespace — refman 8.4 §8.2.5,
	// "Specifying Role Names": "It is possible for a row in the mysql.user
	// system table to serve as both an account and a role."
	//
	// formatCurrentUserGrantee and formatRoleGrantee produce the same 'x'@'y'
	// spelling, so when the connected account is also one of its own enabled
	// roles the grantee list carries it twice. roleGrantees being a map does
	// not prevent that: the account is prepended to it.
	for _, site := range grantsSites() {
		t.Run(site, func(t *testing.T) {
			t.Parallel()

			steps := grantsPreamble("writer@%", [][]driver.Value{{"writer", "%"}})
			steps = append(steps, privilegeSteps(site, &queryStep{
				contains: "GRANTEE IN (?)",
			})...)
			script := &queryScript{steps: steps}

			if _, err := NewInspector(openScriptedDB(t, script), "shop").Grants(
				t.Context(),
			); err != nil {
				t.Fatalf("Grants: %v", err)
			}
			script.assertDone(t)
		})
	}
}

func TestGrantsFallsBackAboveTheBound(t *testing.T) {
	t.Parallel()

	// grantees is server-derived, which is not the same as bounded: with
	// activate_all_roles_on_login enabled the session activates every granted
	// role, and mandatory_roles are granted to all accounts. No documented
	// maximum was found in the 8.0, 8.4 or 9.7 manuals.
	//
	// These statements bind nothing else, so the budget is the whole ceiling
	// and the account occupies one of it.
	for _, testCase := range []struct {
		name  string
		roles int
		lacks string
	}{
		{"at the bound", maxStatementParameters - 1, ""},
		{"above the bound", maxStatementParameters, "IN ("},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			match := queryStep{contains: "USER_PRIVILEGES", lacks: testCase.lacks}
			if testCase.lacks == "" {
				match.contains = "GRANTEE IN ("
			}
			steps := grantsPreamble("app@%", roleRows(testCase.roles))
			steps = append(steps, privilegeSteps("USER_PRIVILEGES", &match)...)
			script := &queryScript{steps: steps}

			if _, err := NewInspector(openScriptedDB(t, script), "shop").Grants(
				t.Context(),
			); err != nil {
				t.Fatalf("Grants: %v", err)
			}
			script.assertDone(t)
		})
	}
}

func TestGrantsIgnoresRowsForGranteesThatAreNeitherAccountNorRole(t *testing.T) {
	t.Parallel()

	// Under the unnarrowed fallback these statements return every row the
	// account can see, including other accounts' privileges. sourceFor reports
	// 0 for those, but `m[k] |= 0` still creates the key — so the maps must
	// skip the row rather than record it as a zero.
	//
	// Asserting the maps are empty is the only assertion that distinguishes
	// the fix from the accident. Global/Schema/Table read a missing key and a
	// zero-valued key identically, so they answer GrantAbsent either way.
	steps := grantsPreamble("app@%", nil)
	steps = append(steps,
		queryStep{
			contains: "USER_PRIVILEGES",
			columns:  []string{"GRANTEE", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'stranger'@'%'", "SELECT"}},
		},
		queryStep{
			contains: "SCHEMA_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'stranger'@'%'", "shop", "CREATE"}},
		},
		queryStep{
			contains: "TABLE_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'stranger'@'%'", "shop", "orders", "DELETE"}},
		},
		queryStep{
			contains: "COLUMN_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'stranger'@'%'", "shop", "orders", "SELECT"}},
		},
	)
	script := &queryScript{steps: steps}

	fact, err := NewInspector(openScriptedDB(t, script), "shop").Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	script.assertDone(t)

	if len(fact.global) != 0 {
		t.Errorf("global = %#v, want empty", fact.global)
	}
	if len(fact.schema) != 0 {
		t.Errorf("schema = %#v, want empty", fact.schema)
	}
	if len(fact.table) != 0 {
		t.Errorf("table = %#v, want empty", fact.table)
	}
	if len(fact.column) != 0 {
		t.Errorf("column = %#v, want empty", fact.column)
	}
}

func TestGrantsRecordsAnonymousSchemaRowsOnly(t *testing.T) {
	t.Parallel()

	steps := grantsPreamble("app@%", nil)
	steps = append(steps,
		queryStep{
			contains: "USER_PRIVILEGES",
			columns:  []string{"GRANTEE", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"''@'%'", "SELECT"}},
		},
		queryStep{
			contains: "SCHEMA_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"''@'%'", "shop", "SELECT"}},
		},
		queryStep{
			contains: "TABLE_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"''@'%'", "shop", "orders", "SELECT"}},
		},
		queryStep{
			contains: "COLUMN_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"''@'%'", "shop", "orders", "SELECT"}},
		},
	)
	script := &queryScript{steps: steps}

	fact, err := NewInspector(openScriptedDB(t, script), "shop").Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	script.assertDone(t)

	if got := fact.schema[schemaPrivilegeKey{
		schema: "shop", privilege: PrivilegeSelect,
	}]; got != grantSourceAnonymous {
		t.Errorf("anonymous schema source = %d, want anonymous", got)
	}
	if len(fact.global) != 0 || len(fact.table) != 0 || len(fact.column) != 0 {
		t.Errorf(
			"anonymous non-schema rows recorded: global=%#v table=%#v column=%#v",
			fact.global,
			fact.table,
			fact.column,
		)
	}
}
