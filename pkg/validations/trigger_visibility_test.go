package validations

import (
	"database/sql/driver"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestPrivilegeTriggerIsAppended pins the appended value: every earlier
// Privilege keeps its number, the new one is 6, and the value after it is still
// undeclared and rendered by number.
func TestPrivilegeTriggerIsAppended(t *testing.T) {
	t.Parallel()

	if got := int(PrivilegeCreate); got != 5 {
		t.Errorf("PrivilegeCreate = %d, want 5: an existing value moved", got)
	}
	if got := int(PrivilegeTrigger); got != 6 {
		t.Errorf("PrivilegeTrigger = %d, want 6, appended after PrivilegeCreate", got)
	}
	if got := PrivilegeTrigger.String(); got != "TRIGGER" {
		t.Errorf("PrivilegeTrigger.String() = %q, want %q", got, "TRIGGER")
	}
	if got, ok := privilegeFromString("TRIGGER"); !ok || got != PrivilegeTrigger {
		t.Errorf("privilegeFromString(%q) = (%s, %t), want (TRIGGER, true)", "TRIGGER", got, ok)
	}
	if !PrivilegeTrigger.valid() {
		t.Error("PrivilegeTrigger.valid() = false, want true")
	}
	if Privilege(7).valid() {
		t.Error("Privilege(7).valid() = true, want false")
	}
	if got := Privilege(7).String(); got != "Privilege(7)" {
		t.Errorf("Privilege(7).String() = %q, want %q", got, "Privilege(7)")
	}
}

// TestGrantsRecognizeTriggerRows feeds TRIGGER rows at schema and table scope
// through the real Grants read on a pinned, role-free session with provable
// visibility. A column SELECT grant on a table must not weaken its TRIGGER
// answer: column weakening is keyed by privilege, and TRIGGER has no column
// scope.
func TestGrantsRecognizeTriggerRows(t *testing.T) {
	t.Parallel()

	privilegeColumns := []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"}
	script := &queryScript{steps: []queryStep{
		{contains: "CURRENT_USER()", columns: []string{"CURRENT_USER()"}, rows: [][]driver.Value{{"app@%"}}},
		{contains: "ENABLED_ROLES", columns: []string{"ROLE_NAME", "ROLE_HOST"}},
		{contains: "@@global.partial_revokes", columns: []string{"partial_revokes"}, rows: [][]driver.Value{{int64(0)}}},
		{contains: "USER_PRIVILEGES", columns: []string{"GRANTEE", "PRIVILEGE_TYPE"}},
		{
			contains: "SCHEMA_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "PRIVILEGE_TYPE"},
			rows: [][]driver.Value{
				{"'app'@'%'", "mysql", "SELECT"},
				{"'app'@'%'", "shop", "TRIGGER"},
			},
		},
		{
			contains: "TABLE_PRIVILEGES",
			columns:  privilegeColumns,
			rows:     [][]driver.Value{{"'app'@'%'", "sales", "ledger", "TRIGGER"}},
		},
		{
			contains: "COLUMN_PRIVILEGES",
			columns:  privilegeColumns,
			rows:     [][]driver.Value{{"'app'@'%'", "sales", "items", "SELECT"}},
		},
	}}
	db := openScriptedDB(t, script)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("open pinned connection: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close pinned connection: %v", closeErr)
		}
	})

	grants, err := NewInspector(conn, "shop").Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	cases := []struct {
		name string
		got  GrantState
		want GrantState
	}{
		{"schema row", grants.Schema("shop", PrivilegeTrigger), GrantPresent},
		{"table covered by the schema row", grants.Table("shop", "orders", PrivilegeTrigger), GrantPresent},
		{"table row", grants.Table("sales", "ledger", PrivilegeTrigger), GrantPresent},
		{"sibling of the table row", grants.Table("sales", "audit", PrivilegeTrigger), GrantAbsent},
		{"table with only a column SELECT", grants.Table("sales", "items", PrivilegeTrigger), GrantAbsent},
		{"the column SELECT itself weakens SELECT", grants.Table("sales", "items", PrivilegeSelect), GrantUnconfirmed},
		{"no global row", grants.Global(PrivilegeTrigger), GrantAbsent},
	}
	for _, test := range cases {
		if test.got != test.want {
			t.Errorf("%s = %s, want %s", test.name, test.got, test.want)
		}
	}
	script.assertDone(t)
}

// TestCheckTriggerVisibility pins the check's whole contract: only GrantPresent
// passes; every other state is one finding per requested table, in requested
// order with duplicates, carrying a PrivilegeFact for TRIGGER and the caller's
// spelling of the table.
func TestCheckTriggerVisibility(t *testing.T) {
	t.Parallel()

	grants := Grants{
		populated:    true,
		affinity:     affinityPinned,
		roleGrantees: map[string]struct{}{},
		global:       map[Privilege]grantSources{},
		schema: map[schemaPrivilegeKey]grantSources{
			{schema: mysqlSchemaName, privilege: PrivilegeSelect}: grantSourceAccount,
		},
		table: map[tablePrivilegeKey]grantSources{
			{schema: "shop", table: "orders", privilege: PrivilegeTrigger}: grantSourceAccount,
			{schema: "shop", table: "items", privilege: PrivilegeTrigger}:  grantSourceRole,
			{schema: "shop", table: "audit", privilege: PrivilegeDelete}:   grantSourceAccount,
		},
	}

	if got := CheckTriggerVisibility(grants, "shop", nil); got != nil {
		t.Errorf("nil tables = %#v, want nil", got)
	}
	if got := CheckTriggerVisibility(grants, "shop", []string{}); got != nil {
		t.Errorf("empty tables = %#v, want nil", got)
	}
	if got := CheckTriggerVisibility(grants, "shop", []string{"orders", "orders"}); got != nil {
		t.Errorf("present tables = %#v, want nil", got)
	}

	requested := []string{"orders", "items", "audit", "Missing", "audit", "items"}
	got := CheckTriggerVisibility(grants, "shop", requested)
	wantTables := []string{"items", "audit", "Missing", "audit", "items"}
	wantStates := []GrantState{GrantUnconfirmed, GrantAbsent, GrantAbsent, GrantAbsent, GrantUnconfirmed}
	if len(got) != len(wantTables) {
		t.Fatalf("findings = %d, want %d: %#v", len(got), len(wantTables), got)
	}
	for index, finding := range got {
		if finding.Check != IDTriggerMetadataVisibility {
			t.Errorf("finding %d Check = %q, want %q", index, finding.Check, IDTriggerMetadataVisibility)
		}
		if !slices.Equal(finding.Tables, []string{wantTables[index]}) {
			t.Errorf("finding %d Tables = %q, want [%q]", index, finding.Tables, wantTables[index])
		}
		want := PrivilegeFact{
			Schema: "shop", Table: wantTables[index], Privilege: PrivilegeTrigger, State: wantStates[index],
		}
		if !reflect.DeepEqual(finding.Facts, want) {
			t.Errorf("finding %d Facts = %#v, want %#v", index, finding.Facts, want)
		}
		if !strings.Contains(finding.Message, "trigger metadata completeness is not established") {
			t.Errorf("finding %d Message = %q, want the completeness summary", index, finding.Message)
		}
		if strings.Contains(strings.ToLower(finding.Message), "hides") {
			t.Errorf("finding %d Message = %q claims the server hides triggers", index, finding.Message)
		}
	}
	if !strings.Contains(got[0].Message, "unconfirmed") || !strings.Contains(got[1].Message, "absent") {
		t.Errorf("messages do not name their states: %q, %q", got[0].Message, got[1].Message)
	}

	// A zero Grants answers nothing: every table is a finding, never a pass.
	zero := CheckTriggerVisibility(Grants{}, "shop", []string{"orders", "items"})
	if len(zero) != 2 {
		t.Fatalf("zero Grants findings = %d, want 2: %#v", len(zero), zero)
	}
	for index, finding := range zero {
		fact, ok := finding.Facts.(PrivilegeFact)
		if !ok || fact.State != GrantUnknown {
			t.Errorf("zero Grants finding %d Facts = %#v, want state unknown", index, finding.Facts)
		}
		if !strings.Contains(finding.Message, "state is unknown") {
			t.Errorf("zero Grants finding %d Message = %q, want the unknown wording", index, finding.Message)
		}
	}
}

// TestCheckTriggerVisibilityAffinity: the same account grant proves the table
// on a pinned connection or a pool, and only weakens through an opaque
// Querier; a negative proves absence only when pinned and role-free.
func TestCheckTriggerVisibilityAffinity(t *testing.T) {
	t.Parallel()

	base := Grants{
		populated: true,
		global:    map[Privilege]grantSources{},
		schema: map[schemaPrivilegeKey]grantSources{
			{schema: mysqlSchemaName, privilege: PrivilegeSelect}: grantSourceAccount,
		},
		table: map[tablePrivilegeKey]grantSources{
			{schema: "shop", table: "orders", privilege: PrivilegeTrigger}: grantSourceAccount,
		},
	}
	cases := []struct {
		name        string
		affinity    querierAffinity
		roles       map[string]struct{}
		wantOrders  []GrantState
		wantMissing GrantState
	}{
		{"pinned, role-free", affinityPinned, nil, nil, GrantAbsent},
		{"pinned, role enabled", affinityPinned, map[string]struct{}{"'r'@'%'": {}}, nil, GrantUnconfirmed},
		{"pool", affinityPool, nil, nil, GrantUnconfirmed},
		{"opaque", affinityOpaque, nil, []GrantState{GrantUnconfirmed}, GrantUnconfirmed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			grants := base
			grants.affinity = test.affinity
			grants.roleGrantees = test.roles
			gotOrders := findingStates(t, CheckTriggerVisibility(grants, "shop", []string{"orders"}))
			if !slices.Equal(gotOrders, test.wantOrders) {
				t.Errorf("orders states = %v, want %v", gotOrders, test.wantOrders)
			}
			gotMissing := findingStates(t, CheckTriggerVisibility(grants, "shop", []string{"missing"}))
			if !slices.Equal(gotMissing, []GrantState{test.wantMissing}) {
				t.Errorf("missing states = %v, want [%s]", gotMissing, test.wantMissing)
			}
		})
	}
}

// TestCheckTriggerVisibilityNeverProvenByPatternOrCase: a wildcard schema
// grant and a table name that matches a grant only case-insensitively weaken
// an absent answer to unconfirmed; neither proves the table.
func TestCheckTriggerVisibilityNeverProvenByPatternOrCase(t *testing.T) {
	t.Parallel()

	grants := Grants{
		populated: true,
		affinity:  affinityPinned,
		global:    map[Privilege]grantSources{},
		schema: map[schemaPrivilegeKey]grantSources{
			{schema: mysqlSchemaName, privilege: PrivilegeSelect}: grantSourceAccount,
			{schema: "sho%", privilege: PrivilegeTrigger}:         grantSourceAccount,
		},
		table: map[tablePrivilegeKey]grantSources{
			{schema: "sales", table: "orders", privilege: PrivilegeTrigger}: grantSourceAccount,
		},
	}
	cases := []struct {
		name, schema, table string
	}{
		{"wildcard schema grant", "shop", "orders"},
		{"case-variant table name", "sales", "ORDERS"},
	}
	for _, test := range cases {
		got := findingStates(t, CheckTriggerVisibility(grants, test.schema, []string{test.table}))
		if !slices.Equal(got, []GrantState{GrantUnconfirmed}) {
			t.Errorf("%s: states = %v, want [unconfirmed]", test.name, got)
		}
	}
	if got := CheckTriggerVisibility(grants, "sales", []string{"orders"}); got != nil {
		t.Errorf("exact table grant = %#v, want a pass", got)
	}
}

func findingStates(t *testing.T, findings []Finding) []GrantState {
	t.Helper()

	var states []GrantState
	for index, finding := range findings {
		fact, ok := finding.Facts.(PrivilegeFact)
		if !ok {
			t.Fatalf("finding %d Facts has type %T, want PrivilegeFact", index, finding.Facts)
		}
		states = append(states, fact.State)
	}

	return states
}

// TestTriggerVisibilitySummaryDefaultBranch covers a state Grants cannot
// produce: an undeclared GrantState still reads as unknown, never as a pass.
func TestTriggerVisibilitySummaryDefaultBranch(t *testing.T) {
	t.Parallel()

	got := triggerVisibilitySummary(GrantState(99))
	if !strings.Contains(got, "not established") || !strings.Contains(got, "unknown") {
		t.Errorf("triggerVisibilitySummary(GrantState(99)) = %q, want the unknown wording", got)
	}
}

// TestTriggerMetadataVisibilityCatalogEntry pins the catalog entry: implemented,
// version-named phase, a rationale about proof rather than hiding, and the sort
// position after TRIGGERS_PRESENT.
func TestTriggerMetadataVisibilityCatalogEntry(t *testing.T) {
	t.Parallel()

	entry, ok := LookupCheck(IDTriggerMetadataVisibility)
	if !ok || entry.ID != "TRIGGER_METADATA_VISIBILITY" {
		t.Fatalf("LookupCheck(%q) = (%#v, %t)", IDTriggerMetadataVisibility, entry, ok)
	}
	if entry.Status != StatusImplemented || entry.Phase != "v1.4.0" {
		t.Errorf("entry status and phase = %s, %q; want implemented, v1.4.0", entry.Status, entry.Phase)
	}
	if !strings.Contains(entry.Rationale, "TRIGGER privilege") ||
		!strings.Contains(entry.Rationale, "no triggers exist") {
		t.Errorf("rationale %q does not state what the privilege proves", entry.Rationale)
	}

	catalog := Catalog()
	last := catalog[len(catalog)-1]
	if last.ID != IDTriggerMetadataVisibility || catalog[len(catalog)-2].ID != IDTriggersPresent {
		t.Errorf("catalog ends %q, %q; want TRIGGERS_PRESENT, TRIGGER_METADATA_VISIBILITY",
			catalog[len(catalog)-2].ID, last.ID)
	}
}
