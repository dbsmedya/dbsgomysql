package validations

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

func TestFormatGranteeEmbeddedQuote(t *testing.T) {
	t.Parallel()

	if got, want := formatCurrentUserGrantee("o'brien@%"), "'o'brien'@'%'"; got != want {
		t.Errorf("formatCurrentUserGrantee() = %q, want %q", got, want)
	}
	if got, want := formatRoleGrantee("r'ole", "local@host"), "'r'ole'@'local@host'"; got != want {
		t.Errorf("formatRoleGrantee() = %q, want %q", got, want)
	}
}

// TestFormatGranteeEmbeddedAtSign pins that the user/host split is taken at the
// last @, not the first.
//
// CURRENT_USER() returns one undelimited string, so 'a@b'@'%' arrives as the
// ambiguous a@b@%. Splitting on the first @ would yield 'a'@'b@%' — a grantee
// that matches no row, which degrades every answer for that account from
// present to absent or unconfirmed. Nothing downstream distinguishes that from
// an account genuinely holding no privileges, so the failure is silent.
//
// The @-in-host direction is covered above by formatRoleGrantee; this is the
// @-in-user direction, which only formatCurrentUserGrantee has to resolve
// because only it receives the two halves already concatenated.
func TestFormatGranteeEmbeddedAtSign(t *testing.T) {
	t.Parallel()

	if got, want := formatCurrentUserGrantee("a@b@%"), "'a@b'@'%'"; got != want {
		t.Errorf("formatCurrentUserGrantee() = %q, want %q", got, want)
	}
	// A host is always concatenated by MySQL, so a bare user with no @ is not a
	// shape the server produces; the fallback still has to name some host rather
	// than emit a malformed grantee.
	if got, want := formatCurrentUserGrantee("nohost"), "'nohost'@'%'"; got != want {
		t.Errorf("formatCurrentUserGrantee() = %q, want %q", got, want)
	}
}

func TestGrantResolutionByAffinityAndProvenance(t *testing.T) {
	t.Parallel()

	account := "'app'@'%'"
	role := "'writer'@'%'"
	base := Grants{
		populated:      true,
		accountGrantee: account,
		roleGrantees:   map[string]struct{}{role: {}},
		global: map[Privilege]grantSources{
			PrivilegeSelect: grantSourceAccount,
			PrivilegeInsert: grantSourceRole,
			PrivilegeUpdate: grantSourceAccount | grantSourceRole,
		},
	}

	tests := []struct {
		name     string
		affinity querierAffinity
		priv     Privilege
		want     GrantState
	}{
		{name: "pinned account", affinity: affinityPinned, priv: PrivilegeSelect, want: GrantPresent},
		{name: "pinned role", affinity: affinityPinned, priv: PrivilegeInsert, want: GrantUnconfirmed},
		{name: "pool account", affinity: affinityPool, priv: PrivilegeSelect, want: GrantPresent},
		{name: "pool role", affinity: affinityPool, priv: PrivilegeInsert, want: GrantUnconfirmed},
		{name: "pool account wins", affinity: affinityPool, priv: PrivilegeUpdate, want: GrantPresent},
		{name: "opaque account", affinity: affinityOpaque, priv: PrivilegeSelect, want: GrantUnconfirmed},
		{name: "opaque role", affinity: affinityOpaque, priv: PrivilegeInsert, want: GrantUnconfirmed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := base
			fact.affinity = test.affinity
			if got := fact.Global(test.priv); got != test.want {
				t.Errorf("Global(%s) = %s, want %s", test.priv, got, test.want)
			}
		})
	}
}

func TestQuerierAffinityClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    Querier
		want querierAffinity
	}{
		{name: "DB", q: (*sql.DB)(nil), want: affinityPool},
		{name: "Conn", q: (*sql.Conn)(nil), want: affinityPinned},
		{name: "Tx", q: (*sql.Tx)(nil), want: affinityPinned},
		{name: "wrapper", q: wrappedQuerier{}, want: affinityOpaque},
		{name: "custom", q: panicQuerier{}, want: affinityOpaque},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := classifyQuerier(test.q); got != test.want {
				t.Errorf("classifyQuerier(%T) = %d, want %d", test.q, got, test.want)
			}
		})
	}
}

type wrappedQuerier struct {
	Querier
}

func TestGrantResolutionNegativesAndPartialRevokes(t *testing.T) {
	t.Parallel()

	roleFreePinned := Grants{
		populated: true,
		affinity:  affinityPinned,
		global:    map[Privilege]grantSources{},
		schema:    map[schemaPrivilegeKey]grantSources{},
		table:     map[tablePrivilegeKey]grantSources{},
	}
	if got := roleFreePinned.Table("shop", "orders", PrivilegeDelete); got != GrantAbsent {
		t.Errorf("pinned role-free negative = %s, want absent", got)
	}

	withRole := roleFreePinned
	withRole.roleGrantees = map[string]struct{}{"'role'@'%'": {}}
	if got := withRole.Table("shop", "orders", PrivilegeDelete); got != GrantUnconfirmed {
		t.Errorf("enabled-role negative = %s, want unconfirmed", got)
	}

	for _, affinity := range []querierAffinity{affinityPool, affinityOpaque} {
		fact := roleFreePinned
		fact.affinity = affinity
		if got := fact.Schema("shop", PrivilegeCreate); got != GrantUnconfirmed {
			t.Errorf("affinity %d negative = %s, want unconfirmed", affinity, got)
		}
	}

	partial := roleFreePinned
	partial.partialRevokes = true
	partial.global = map[Privilege]grantSources{PrivilegeSelect: grantSourceAccount}
	if got := partial.Table("shop", "orders", PrivilegeSelect); got != GrantUnconfirmed {
		t.Errorf("partial-revoke global table answer = %s, want unconfirmed", got)
	}
	partial.schema = map[schemaPrivilegeKey]grantSources{
		{schema: "shop", privilege: PrivilegeSelect}: grantSourceAccount,
	}
	if got := partial.Table("shop", "orders", PrivilegeSelect); got != GrantPresent {
		t.Errorf("specific schema grant = %s, want present", got)
	}

	if got := roleFreePinned.Global(PrivilegeUnknown); got != GrantUnknown {
		t.Errorf("unknown privilege = %s, want unknown", got)
	}
	if got := roleFreePinned.Global(Privilege(99)); got != GrantUnknown {
		t.Errorf("undeclared privilege = %s, want unknown", got)
	}
	if got := (Grants{}).Global(PrivilegeSelect); got != GrantUnknown {
		t.Errorf("zero Grants = %s, want unknown", got)
	}
}

func TestPartialRevokesDoNotHideProvableAbsence(t *testing.T) {
	t.Parallel()

	// Restrictions only ever subtract from an existing global grant, so a
	// privilege with no row at any scope stays provably absent no matter what
	// @@global.partial_revokes says.
	fact := Grants{
		populated:      true,
		affinity:       affinityPinned,
		partialRevokes: true,
		global:         map[Privilege]grantSources{},
		schema:         map[schemaPrivilegeKey]grantSources{},
		table:          map[tablePrivilegeKey]grantSources{},
	}

	if got := fact.Global(PrivilegeDelete); got != GrantAbsent {
		t.Errorf("global answer with no grant row = %s, want absent", got)
	}
	if got := fact.Schema("shop", PrivilegeDelete); got != GrantAbsent {
		t.Errorf("schema answer with no grant row = %s, want absent", got)
	}
	if got := fact.Table("shop", "orders", PrivilegeDelete); got != GrantAbsent {
		t.Errorf("table answer with no grant row = %s, want absent", got)
	}
}

func TestPartialRevokesDegradeEveryAnswerBackedByGlobalRow(t *testing.T) {
	t.Parallel()

	fact := Grants{
		populated:      true,
		affinity:       affinityPinned,
		partialRevokes: true,
		global: map[Privilege]grantSources{
			PrivilegeSelect: grantSourceAccount,
		},
		schema: map[schemaPrivilegeKey]grantSources{},
		table:  map[tablePrivilegeKey]grantSources{},
	}

	// The global answer degrades too: this package never reads the restriction
	// list, so a global row cannot prove an unrestricted privilege, and no
	// more-specific row exists that could prove a global-scope question.
	if got := fact.Global(PrivilegeSelect); got != GrantUnconfirmed {
		t.Errorf("global row under partial revokes = %s, want unconfirmed", got)
	}
	if got := fact.Schema("shop", PrivilegeSelect); got != GrantUnconfirmed {
		t.Errorf("schema answer from global row = %s, want unconfirmed", got)
	}
	if got := fact.Table("shop", "orders", PrivilegeSelect); got != GrantUnconfirmed {
		t.Errorf("table answer from global row = %s, want unconfirmed", got)
	}
}

func TestWildcardSchemaGrantDowngradesAbsenceOnly(t *testing.T) {
	t.Parallel()

	fact := Grants{
		populated:      true,
		affinity:       affinityPinned,
		accountGrantee: "'app'@'%'",
		global:         map[Privilege]grantSources{},
		schema: map[schemaPrivilegeKey]grantSources{
			{schema: "shop%", privilege: PrivilegeSelect}: grantSourceAccount,
		},
		table: map[tablePrivilegeKey]grantSources{},
	}

	if got := fact.Schema("shop_eu", PrivilegeSelect); got != GrantUnconfirmed {
		t.Errorf("pattern-covered schema = %s, want unconfirmed", got)
	}
	if got := fact.Table("shop_eu", "orders", PrivilegeSelect); got != GrantUnconfirmed {
		t.Errorf("pattern-covered table = %s, want unconfirmed", got)
	}
	if got := fact.Schema("archive", PrivilegeSelect); got != GrantAbsent {
		t.Errorf("schema outside the pattern = %s, want absent", got)
	}
	if got := fact.Schema("shop_eu", PrivilegeDelete); got != GrantAbsent {
		t.Errorf("pattern recorded for another privilege = %s, want absent", got)
	}

	// A pattern never proves an object; only an exact stored key does.
	exact := fact
	exact.schema = map[schemaPrivilegeKey]grantSources{
		{schema: "shop", privilege: PrivilegeSelect}: grantSourceAccount,
	}
	if got := exact.Schema("shop", PrivilegeSelect); got != GrantPresent {
		t.Errorf("exact schema grant = %s, want present", got)
	}
	if got := exact.Schema("shopping", PrivilegeSelect); got != GrantAbsent {
		t.Errorf("literal key against a longer name = %s, want absent", got)
	}
}

func TestLikePatternMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{pattern: "shop", name: "shop", want: true},
		{pattern: "shop", name: "shopping", want: false},
		{pattern: "shop%", name: "shop", want: true},
		{pattern: "shop%", name: "shop_eu", want: true},
		{pattern: "%", name: "", want: true},
		{pattern: "%shop%", name: "eu_shop_1", want: true},
		{pattern: "my_db", name: "myXdb", want: true},
		{pattern: "my_db", name: "my_db", want: true},
		{pattern: "my_db", name: "mydb", want: false},
		{pattern: `my\_db`, name: "my_db", want: true},
		{pattern: `my\_db`, name: "myXdb", want: false},
		{pattern: `100\%`, name: "100%", want: true},
		{pattern: `100\%`, name: "100x", want: false},
		{pattern: "a%b%c", name: "axxbyyc", want: true},
		{pattern: "a%b%c", name: "abcd", want: false},
		{pattern: "", name: "", want: true},
		{pattern: "", name: "x", want: false},
		{pattern: "ünï%", name: "ünïcode", want: true},
		// A trailing lone backslash has nothing to escape, so the escape arm's
		// bounds guard declines it and it falls through to the literal compare.
		// The guard is what keeps this from indexing past the pattern.
		{pattern: `shop\`, name: `shop\`, want: true},
		{pattern: `shop\`, name: "shopx", want: false},
		{pattern: `shop\`, name: "shop", want: false},
	}
	for _, test := range tests {
		if got := likePatternMatches(test.pattern, test.name); got != test.want {
			t.Errorf(
				"likePatternMatches(%q, %q) = %t, want %t",
				test.pattern, test.name, got, test.want,
			)
		}
	}
}

func TestPrivilegeChecksTotalStateTable(t *testing.T) {
	t.Parallel()

	grants := Grants{
		populated: true,
		affinity:  affinityPinned,
		global: map[Privilege]grantSources{
			PrivilegeSelect: grantSourceAccount,
		},
		schema: map[schemaPrivilegeKey]grantSources{},
		table: map[tablePrivilegeKey]grantSources{
			{schema: "shop", table: "orders", privilege: PrivilegeDelete}: grantSourceAccount,
		},
	}

	if got := CheckTablePrivileges(grants, "shop", nil, PrivilegeDelete); got != nil {
		t.Errorf("empty table input = %#v, want nil", got)
	}
	if got := CheckSchemaPrivileges(grants, "shop", nil); got != nil {
		t.Errorf("empty privilege input = %#v, want nil", got)
	}

	got := CheckTablePrivileges(
		grants,
		"shop",
		[]string{"orders", "missing", "missing"},
		PrivilegeDelete,
	)
	if len(got) != 2 {
		t.Fatalf("table privilege findings = %d, want 2: %#v", len(got), got)
	}
	for _, finding := range got {
		want := PrivilegeFact{
			Schema: "shop", Table: "missing",
			Privilege: PrivilegeDelete, State: GrantAbsent,
		}
		if !reflect.DeepEqual(finding.Facts, want) {
			t.Errorf("table privilege payload = %#v, want %#v", finding.Facts, want)
		}
	}

	schemaFindings := CheckSchemaPrivileges(
		grants,
		"shop",
		[]Privilege{PrivilegeSelect, PrivilegeCreate, PrivilegeUnknown, Privilege(99)},
	)
	if len(schemaFindings) != 3 {
		t.Fatalf("schema privilege findings = %d, want 3: %#v", len(schemaFindings), schemaFindings)
	}
	wantStates := []GrantState{GrantAbsent, GrantUnknown, GrantUnknown}
	for index, finding := range schemaFindings {
		fact, ok := finding.Facts.(PrivilegeFact)
		if !ok {
			t.Fatalf("schema finding %d Facts has type %T, want PrivilegeFact", index, finding.Facts)
		}
		if fact.State != wantStates[index] {
			t.Errorf("schema finding %d state = %s, want %s", index, fact.State, wantStates[index])
		}
	}
}

func TestGrantsAssemblesProvenanceAndPoolDegradation(t *testing.T) {
	t.Parallel()

	script := grantsScript()
	db := openScriptedDB(t, script)
	grants, err := NewInspector(db, "shop").Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if got := grants.Global(PrivilegeSelect); got != GrantPresent {
		t.Errorf("direct account global SELECT = %s, want present", got)
	}
	if got := grants.Schema("shop", PrivilegeCreate); got != GrantUnconfirmed {
		t.Errorf("role-only pooled CREATE = %s, want unconfirmed", got)
	}
	if got := grants.Table("shop", "orders", PrivilegeDelete); got != GrantPresent {
		t.Errorf("direct account table DELETE = %s, want present", got)
	}
	if got := grants.Table("shop", "orders", PrivilegeUpdate); got != GrantUnconfirmed {
		t.Errorf("pooled negative with enabled role = %s, want unconfirmed", got)
	}
	script.assertDone(t)
}

func TestGrantsPinnedRoleFreeNegativeIsAbsent(t *testing.T) {
	t.Parallel()

	script := &queryScript{steps: []queryStep{
		{contains: "CURRENT_USER()", columns: []string{"CURRENT_USER()"}, rows: [][]driver.Value{{"app@%"}}},
		{contains: "ENABLED_ROLES", columns: []string{"ROLE_NAME", "ROLE_HOST"}},
		{contains: "@@global.partial_revokes", columns: []string{"partial_revokes"}, rows: [][]driver.Value{{int64(0)}}},
		{contains: "USER_PRIVILEGES", columns: []string{"GRANTEE", "PRIVILEGE_TYPE"}},
		{contains: "SCHEMA_PRIVILEGES", columns: []string{"GRANTEE", "TABLE_SCHEMA", "PRIVILEGE_TYPE"}},
		{contains: "TABLE_PRIVILEGES", columns: []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"}},
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
		t.Fatalf("Grants pinned: %v", err)
	}
	if got := grants.Table("shop", "orders", PrivilegeDelete); got != GrantAbsent {
		t.Errorf("pinned role-free negative = %s, want absent", got)
	}
	script.assertDone(t)
}

func TestGrantsQueryErrorIsWrapped(t *testing.T) {
	t.Parallel()

	cause := errors.New("current user failed")
	script := &queryScript{steps: []queryStep{{
		contains: "CURRENT_USER()",
		err:      cause,
	}}}
	db := openScriptedDB(t, script)
	_, err := NewInspector(db, "shop").Grants(t.Context())
	assertObjectErrorCause(t, err, cause, opGrants, "shop")
	script.assertDone(t)
}

func grantsScript() *queryScript {
	return &queryScript{steps: []queryStep{
		{
			contains: "CURRENT_USER()",
			columns:  []string{"CURRENT_USER()"},
			rows:     [][]driver.Value{{"app@%"}},
		},
		{
			contains: "ENABLED_ROLES",
			columns:  []string{"ROLE_NAME", "ROLE_HOST"},
			rows:     [][]driver.Value{{"writer", "%"}},
		},
		{
			contains: "@@global.partial_revokes",
			columns:  []string{"partial_revokes"},
			rows:     [][]driver.Value{{int64(0)}},
		},
		{
			contains: "USER_PRIVILEGES",
			columns:  []string{"GRANTEE", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'app'@'%'", "SELECT"}},
		},
		{
			contains: "SCHEMA_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'writer'@'%'", "shop", "CREATE"}},
		},
		{
			contains: "TABLE_PRIVILEGES",
			columns:  []string{"GRANTEE", "TABLE_SCHEMA", "TABLE_NAME", "PRIVILEGE_TYPE"},
			rows:     [][]driver.Value{{"'app'@'%'", "shop", "orders", "DELETE"}},
		},
	}}
}
