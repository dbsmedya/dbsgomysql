package validations

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestForeignKeyDowngradeReasonStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "none",
			got:  ForeignKeyDowngradeNone.String(),
			want: "none",
		},
		{
			name: "primary query error",
			got:  ForeignKeyDowngradePrimaryQueryError.String(),
			want: "primary_query_error",
		},
		{
			name: "primary read error",
			got:  ForeignKeyDowngradePrimaryReadError.String(),
			want: "primary_read_error",
		},
		{
			name: "undeclared",
			got:  ForeignKeyDowngradeReason(99).String(),
			want: "ForeignKeyDowngradeReason(99)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.got != test.want {
				t.Errorf("String() = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestForeignKeyResultJSON(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("primary error must not be serialized")
	tests := []struct {
		name  string
		value ForeignKeyResult
		want  string
	}{
		{
			name: "zero",
			want: `{"keys":null,"visibility":0,"downgrade_reason":0}`,
		},
		{
			name: "primary query error",
			value: ForeignKeyResult{
				Visibility:      VisibilityUnconfirmed,
				DowngradeReason: ForeignKeyDowngradePrimaryQueryError,
				PrimaryError:    primaryErr,
			},
			want: `{"keys":null,"visibility":2,"downgrade_reason":1}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if string(got) != test.want {
				t.Errorf("json.Marshal() = %s, want %s", got, test.want)
			}
		})
	}
}

func assertNoForeignKeyDowngrade(t *testing.T, result ForeignKeyResult) {
	t.Helper()

	if result.DowngradeReason != ForeignKeyDowngradeNone {
		t.Errorf("downgrade reason = %s, want none", result.DowngradeReason)
	}
	if result.PrimaryError != nil {
		t.Errorf("primary error = %v, want nil", result.PrimaryError)
	}
}

func assertZeroForeignKeyResult(t *testing.T, result ForeignKeyResult) {
	t.Helper()

	if result.Keys != nil {
		t.Errorf("keys = %#v, want nil", result.Keys)
	}
	if result.Visibility != VisibilityUnknown {
		t.Errorf("visibility = %s, want unknown", result.Visibility)
	}
	assertNoForeignKeyDowngrade(t, result)
}

func TestPhase1cEnumStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "visibility zero", got: VisibilityUnknown.String(), want: unknownEnum},
		{name: "visibility complete", got: VisibilityComplete.String(), want: "complete"},
		{name: "visibility unconfirmed", got: VisibilityUnconfirmed.String(), want: "unconfirmed"},
		{name: "visibility undeclared", got: MetadataVisibility(99).String(), want: "MetadataVisibility(99)"},
		{name: "grant zero", got: GrantUnknown.String(), want: unknownEnum},
		{name: "grant present", got: GrantPresent.String(), want: "present"},
		{name: "grant absent", got: GrantAbsent.String(), want: "absent"},
		{name: "grant unconfirmed", got: GrantUnconfirmed.String(), want: "unconfirmed"},
		{name: "grant undeclared", got: GrantState(99).String(), want: "GrantState(99)"},
		{name: "privilege zero", got: PrivilegeUnknown.String(), want: unknownEnum},
		{name: "privilege select", got: PrivilegeSelect.String(), want: "SELECT"},
		{name: "privilege insert", got: PrivilegeInsert.String(), want: "INSERT"},
		{name: "privilege update", got: PrivilegeUpdate.String(), want: "UPDATE"},
		{name: "privilege delete", got: PrivilegeDelete.String(), want: "DELETE"},
		{name: "privilege create", got: PrivilegeCreate.String(), want: "CREATE"},
		{name: "privilege undeclared", got: Privilege(99).String(), want: "Privilege(99)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.got != test.want {
				t.Errorf("String() = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestFKSelectorValidationAndOwnership(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, "shop")
	for _, selector := range []FKSelector{{}, {kind: fkSelectorKind(99)}} {
		_, err := inspector.ForeignKeys(t.Context(), selector)
		assertObjectErrorCause(t, err, ErrInvalidFKSelector, opForeignKeys, "shop")
	}

	tables := []string{"orders"}
	selector := IncomingTo(tables...)
	tables[0] = "mutated"
	if !reflect.DeepEqual(selector.tables, []string{"orders"}) {
		t.Errorf("selector tables = %v, want copied [orders]", selector.tables)
	}

	result, err := inspector.ForeignKeys(t.Context(), IncomingTo())
	if err != nil {
		t.Fatalf("empty selector returned error: %v", err)
	}
	assertZeroForeignKeyResult(t, result)

	_, err = inspector.ForeignKeys(t.Context(), Within("orders", ""))
	assertObjectErrorCause(t, err, ErrEmptyTableName, opForeignKeys, "shop")
}

func TestDecodeInnoDBForeignKeyRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flags      uint64
		wantDelete string
		wantUpdate string
		wantErr    bool
	}{
		{flags: 0, wantDelete: "RESTRICT", wantUpdate: "RESTRICT"},
		{flags: 1, wantDelete: "CASCADE", wantUpdate: "RESTRICT"},
		{flags: 2, wantDelete: "SET NULL", wantUpdate: "RESTRICT"},
		{flags: 16, wantDelete: "NO ACTION", wantUpdate: "RESTRICT"},
		{flags: 4, wantDelete: "RESTRICT", wantUpdate: "CASCADE"},
		{flags: 8, wantDelete: "RESTRICT", wantUpdate: "SET NULL"},
		{flags: 32, wantDelete: "RESTRICT", wantUpdate: "NO ACTION"},
		{flags: 1 | 8, wantDelete: "CASCADE", wantUpdate: "SET NULL"},
		{flags: 1 | 2, wantErr: true},
		{flags: 4 | 32, wantErr: true},
		{flags: 64, wantErr: true},
	}

	for _, test := range tests {
		deleteRule, updateRule, err := decodeInnoDBForeignKeyRules(test.flags)
		if (err != nil) != test.wantErr {
			t.Errorf("decode flags %d error = %v, wantErr %t", test.flags, err, test.wantErr)
		}
		if deleteRule != test.wantDelete || updateRule != test.wantUpdate {
			t.Errorf(
				"decode flags %d = (%q, %q), want (%q, %q)",
				test.flags,
				deleteRule,
				updateRule,
				test.wantDelete,
				test.wantUpdate,
			)
		}
	}
}

func TestSplitInnoDBName(t *testing.T) {
	t.Parallel()

	left, right, err := splitInnoDBName("odd/schema/table")
	if err != nil {
		t.Fatalf("split valid name: %v", err)
	}
	if left != "odd" || right != "schema/table" {
		t.Errorf("split = (%q, %q), want (odd, schema/table)", left, right)
	}
	if _, _, err := splitInnoDBName("missing"); err == nil {
		t.Error("split name without slash returned nil error")
	}

	group := innoDBForeignKeyGroup{
		id:            "constraint_schema/fk_orders",
		forName:       "child_schema/orders",
		refName:       "parent_schema/customers",
		columnCount:   1,
		childColumns:  []string{"customer_id"},
		parentColumns: []string{"id"},
	}
	if _, err := group.foreignKey(); err == nil {
		t.Error("constraint and child schema mismatch returned nil error")
	}
}

func TestForeignKeyIndexMatcher(t *testing.T) {
	t.Parallel()

	indexes := [][]string{
		{"tenant_id", "order_id", "extra"},
		{"z", "tenant_id", "order_id"},
		{"order_id", "tenant_id"},
		{"tenant_id"},
	}
	tests := []struct {
		columns []string
		want    bool
	}{
		{columns: []string{"tenant_id"}, want: true},
		{columns: []string{"tenant_id", "order_id"}, want: true},
		{columns: []string{"order_id", "tenant_id"}, want: true},
		{columns: []string{"tenant_id", "missing"}, want: false},
		{columns: []string{"order_id"}, want: true},
		{columns: []string{"missing"}, want: false},
	}
	for _, test := range tests {
		if got := foreignKeyColumnsIndexed(test.columns, indexes); got != test.want {
			t.Errorf("foreignKeyColumnsIndexed(%v) = %t, want %t", test.columns, got, test.want)
		}
	}

	nonLeadingOnly := [][]string{{"z", "tenant_id", "order_id"}}
	if foreignKeyColumnsIndexed([]string{"tenant_id", "order_id"}, nonLeadingOnly) {
		t.Error("non-leading FK columns reported indexed")
	}
}

func TestForeignKeyChecks(t *testing.T) {
	t.Parallel()

	internal := ForeignKey{
		ConstraintName: "fk_internal",
		ChildSchema:    "shop",
		ChildTable:     "items",
		ParentSchema:   "shop",
		ParentTable:    "orders",
		Indexed:        true,
	}
	sameSchemaExternal := ForeignKey{
		ConstraintName: "fk_external",
		ChildSchema:    "shop",
		ChildTable:     "audit",
		ParentSchema:   "shop",
		ParentTable:    "orders",
		Indexed:        true,
	}
	crossSchemaSameName := ForeignKey{
		ConstraintName: "fk_cross",
		ChildSchema:    "archive",
		ChildTable:     "items",
		ParentSchema:   "shop",
		ParentTable:    "orders",
		Indexed:        true,
	}

	if got := CheckFKClosure(ForeignKeyResult{}, "shop", nil); got != nil {
		t.Errorf("empty closure = %#v, want nil", got)
	}
	// An empty target is vacuously closed under every visibility state, and
	// that short-circuit outranks both external keys and incomplete discovery.
	for _, visibility := range []MetadataVisibility{
		VisibilityUnknown,
		VisibilityComplete,
		VisibilityUnconfirmed,
		MetadataVisibility(99),
	} {
		if got := CheckFKClosure(
			ForeignKeyResult{
				Keys:       []ForeignKey{sameSchemaExternal},
				Visibility: visibility,
			},
			"shop",
			nil,
		); got != nil {
			t.Errorf("empty target under %s = %#v, want nil", visibility, got)
		}
	}
	for _, visibility := range []MetadataVisibility{
		VisibilityUnknown,
		VisibilityUnconfirmed,
		MetadataVisibility(99),
	} {
		got := CheckFKClosure(
			ForeignKeyResult{Visibility: visibility},
			"shop",
			[]string{"orders"},
		)
		if len(got) != 1 || got[0].Check != IDFKClosure || got[0].Facts != visibility {
			t.Errorf("unconfirmed closure for %s = %#v, want one exact-state finding", visibility, got)
		}
	}
	if got := CheckFKClosure(
		ForeignKeyResult{
			Keys:       []ForeignKey{internal},
			Visibility: VisibilityComplete,
		},
		"shop",
		[]string{"orders", "items"},
	); got != nil {
		t.Errorf("complete internal closure = %#v, want nil", got)
	}
	if got := CheckFKClosure(
		ForeignKeyResult{Visibility: VisibilityComplete},
		"shop",
		[]string{"orders"},
	); got != nil {
		t.Errorf("complete empty closure = %#v, want nil", got)
	}

	got := CheckFKClosure(
		ForeignKeyResult{
			Keys: []ForeignKey{
				crossSchemaSameName,
				internal,
				sameSchemaExternal,
			},
			Visibility: VisibilityComplete,
		},
		"shop",
		[]string{"orders", "items"},
	)
	if len(got) != 2 {
		t.Fatalf("external closure findings = %d, want 2: %#v", len(got), got)
	}
	first, ok := got[0].Facts.(ForeignKey)
	if !ok {
		t.Fatalf("first closure Facts has type %T, want ForeignKey", got[0].Facts)
	}
	second, ok := got[1].Facts.(ForeignKey)
	if !ok {
		t.Fatalf("second closure Facts has type %T, want ForeignKey", got[1].Facts)
	}
	names := []string{first.ConstraintName, second.ConstraintName}
	if !slices.Equal(names, []string{"fk_cross", "fk_external"}) {
		t.Errorf("external closure order = %v, want [fk_cross fk_external]", names)
	}

	// Closure membership is case-exact: a child differing only in case is not
	// in the target set, and a parent differing only in case is not the target.
	caseVariantChild := internal
	caseVariantChild.ConstraintName = "fk_case_child"
	caseVariantChild.ChildTable = "Items"
	caseFindings := CheckFKClosure(
		ForeignKeyResult{
			Keys:       []ForeignKey{caseVariantChild},
			Visibility: VisibilityComplete,
		},
		"shop",
		[]string{"orders", "items"},
	)
	if len(caseFindings) != 1 {
		t.Fatalf(
			"case-variant child findings = %d, want 1: %#v",
			len(caseFindings), caseFindings,
		)
	}

	caseVariantParent := internal
	caseVariantParent.ConstraintName = "fk_case_parent"
	caseVariantParent.ParentTable = "Orders"
	if got := CheckFKClosure(
		ForeignKeyResult{
			Keys:       []ForeignKey{caseVariantParent},
			Visibility: VisibilityComplete,
		},
		"shop",
		[]string{"orders", "items"},
	); got != nil {
		t.Errorf("case-variant parent = %#v, want nil", got)
	}

	unindexed := sameSchemaExternal
	unindexed.Indexed = false
	indexedFindings := CheckFKIndexed([]ForeignKey{internal, unindexed})
	if len(indexedFindings) != 1 || !reflect.DeepEqual(indexedFindings[0].Facts, unindexed) {
		t.Errorf("CheckFKIndexed() = %#v, want synthetic unindexed fact", indexedFindings)
	}

	cascade := internal
	cascade.OnDelete = "CASCADE"
	rules := CheckCascadeRules([]ForeignKey{
		cascade,
		{OnDelete: "RESTRICT"},
		{OnDelete: "NO ACTION"},
		{OnDelete: "SET NULL"},
	})
	if len(rules) != 1 || !reflect.DeepEqual(rules[0].Facts, cascade) {
		t.Errorf("CheckCascadeRules() = %#v, want cascade only", rules)
	}
}

func TestCheckFKClosurePreservesTargetOrderAndMultiplicity(t *testing.T) {
	t.Parallel()

	key := func(parent, child, constraint string) ForeignKey {
		return ForeignKey{
			ConstraintName: constraint,
			ChildSchema:    "external",
			ChildTable:     child,
			ParentSchema:   "shop",
			ParentTable:    parent,
			Indexed:        true,
		}
	}
	result := ForeignKeyResult{
		Keys: []ForeignKey{
			key("beta", "z_child", "fk_z"),
			key("alpha", "a_child", "fk_a"),
			key("beta", "a_child", "fk_b"),
		},
		Visibility: VisibilityComplete,
	}

	got := CheckFKClosure(result, "shop", []string{"beta", "alpha", "beta"})
	want := []string{"fk_b", "fk_z", "fk_a", "fk_b", "fk_z"}
	names := make([]string, 0, len(got))
	for _, finding := range got {
		fact, ok := finding.Facts.(ForeignKey)
		if !ok {
			t.Fatalf("finding Facts has type %T, want ForeignKey", finding.Facts)
		}
		names = append(names, fact.ConstraintName)
	}
	if !slices.Equal(names, want) {
		t.Errorf("constraint order = %v, want %v", names, want)
	}
}

func TestSelectForeignKeysPreservesSelectorOrderAndMultiplicity(t *testing.T) {
	t.Parallel()

	key := func(child, parent, constraint string) ForeignKey {
		return ForeignKey{
			ConstraintName: constraint,
			ChildSchema:    "shop",
			ChildTable:     child,
			ChildColumns:   []string{"parent_id"},
			ParentSchema:   "shop",
			ParentTable:    parent,
			ParentColumns:  []string{"id"},
			Indexed:        true,
		}
	}
	keys := []ForeignKey{
		key("z_child", "beta", "fk_z"),
		key("a_child", "alpha", "fk_a"),
		key("a_child", "beta", "fk_b"),
	}

	got := selectForeignKeys(keys, "shop", IncomingTo("beta", "alpha", "beta"))
	want := []string{"fk_b", "fk_z", "fk_a", "fk_b", "fk_z"}
	names := make([]string, 0, len(got))
	for _, fact := range got {
		names = append(names, fact.ConstraintName)
	}
	if !slices.Equal(names, want) {
		t.Errorf("constraint order = %v, want %v", names, want)
	}
}

func TestFKMetadataVisibilityCheck(t *testing.T) {
	t.Parallel()

	if got := CheckFKMetadataVisibility(VisibilityComplete); got != nil {
		t.Errorf("complete visibility = %#v, want nil", got)
	}
	for _, visibility := range []MetadataVisibility{
		VisibilityUnknown,
		VisibilityUnconfirmed,
		MetadataVisibility(99),
	} {
		got := CheckFKMetadataVisibility(visibility)
		if len(got) != 1 || got[0].Facts != visibility {
			t.Errorf("visibility %s findings = %#v, want one exact-state finding", visibility, got)
		}
	}
}

func TestInvalidSelectorErrorIsReachable(t *testing.T) {
	t.Parallel()

	_, err := NewInspector(panicQuerier{}, "shop").ForeignKeys(t.Context(), FKSelector{})
	if !errors.Is(err, ErrInvalidFKSelector) {
		t.Errorf("errors.Is(%v, ErrInvalidFKSelector) = false", err)
	}
}
