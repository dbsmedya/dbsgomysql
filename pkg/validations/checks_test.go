package validations

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func TestCheckPrimaryKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pk       PKInfo
		expected string
		want     []string
	}{
		{
			name: "none",
			pk:   PKInfo{Table: "orders", Kind: PKNone},
			want: []string{IDPKExists},
		},
		{
			name: "integer single without expectation",
			pk: PKInfo{
				Table: "orders", Kind: PKSingle, Columns: []string{"id"},
				DataType: "bigint", IsInteger: true,
			},
		},
		{
			name: "noninteger single without expectation",
			pk: PKInfo{
				Table: "orders", Kind: PKSingle, Columns: []string{"id"},
				DataType: "varchar", IsInteger: false,
			},
			want: []string{IDPKIntegerType},
		},
		{
			name: "single exact expectation",
			pk: PKInfo{
				Table: "orders", Kind: PKSingle, Columns: []string{"LOG_ID"},
				DataType: "int", IsInteger: true,
			},
			expected: "LOG_ID",
		},
		{
			name: "single ASCII case mismatch",
			pk: PKInfo{
				Table: "orders", Kind: PKSingle, Columns: []string{"LOG_ID"},
				DataType: "int", IsInteger: true,
			},
			expected: "log_id",
			want:     []string{IDPKNameCase},
		},
		{
			name: "single non-ASCII difference does not fold",
			pk: PKInfo{
				Table: "orders", Kind: PKSingle, Columns: []string{"İD"},
				DataType: "int", IsInteger: true,
			},
			expected: "id",
			want:     []string{IDPKMatchesExpected},
		},
		{
			name: "single different expectation and noninteger type",
			pk: PKInfo{
				Table: "orders", Kind: PKSingle, Columns: []string{"actual_id"},
				DataType: "decimal", IsInteger: false,
			},
			expected: "configured_id",
			want:     []string{IDPKMatchesExpected, IDPKIntegerType},
		},
		{
			name: "composite without expectation",
			pk: PKInfo{
				Table: "orders", Kind: PKComposite, Columns: []string{"tenant_id", "order_id"},
			},
			want: []string{IDPKSingleColumn},
		},
		{
			name: "composite exact member",
			pk: PKInfo{
				Table: "orders", Kind: PKComposite, Columns: []string{"tenant_id", "order_id"},
			},
			expected: "order_id",
			want:     []string{IDPKSingleColumn},
		},
		{
			name: "composite member case mismatch",
			pk: PKInfo{
				Table: "orders", Kind: PKComposite, Columns: []string{"TENANT_ID", "order_id"},
			},
			expected: "tenant_id",
			want:     []string{IDPKSingleColumn, IDPKNameCase},
		},
		{
			name: "composite different expectation",
			pk: PKInfo{
				Table: "orders", Kind: PKComposite, Columns: []string{"tenant_id", "order_id"},
			},
			expected: "id",
			want:     []string{IDPKSingleColumn, IDPKMatchesExpected},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			expected := map[string]string{}
			if test.expected != "" {
				expected[test.pk.Table] = test.expected
			}

			got := checkIDsForPK(test.pk, expected)
			if !slices.Equal(got, test.want) {
				t.Errorf("PK checks = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCheckPrimaryKeysPayload(t *testing.T) {
	t.Parallel()

	pk := PKInfo{
		Table: "Orders", Kind: PKSingle, Columns: []string{"ID"},
		DataType: "varchar", IsInteger: false,
	}
	checks := [][]Finding{
		CheckPKMatchesExpected([]PKInfo{pk}, map[string]string{"Orders": "other"}),
		CheckPKNameCase([]PKInfo{pk}, map[string]string{"Orders": "id"}),
		CheckPKIntegerType([]PKInfo{pk}),
	}

	for _, findings := range checks {
		if len(findings) != 1 {
			t.Fatalf("check returned %d findings, want 1", len(findings))
		}
		got, ok := findings[0].Facts.(PKInfo)
		if !ok {
			t.Fatalf("finding Facts has type %T, want PKInfo", findings[0].Facts)
		}
		if !reflect.DeepEqual(got, pk) {
			t.Errorf("finding Facts = %#v, want %#v", got, pk)
		}
		if !reflect.DeepEqual(findings[0].Tables, []string{"Orders"}) {
			t.Errorf("finding Tables = %v, want [Orders]", findings[0].Tables)
		}
	}
}

func TestCheckTablesExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested []string
		found     []TableInfo
		want      []string
	}{
		{name: "empty"},
		{
			name:      "all present despite shuffled facts",
			requested: []string{"c", "a", "b"},
			found:     []TableInfo{{Table: "a"}, {Table: "b"}, {Table: "c"}},
		},
		{
			name:      "missing preserve requested order",
			requested: []string{"z", "present", "a"},
			found:     []TableInfo{{Table: "present"}},
			want:      []string{"z", "a"},
		},
		{
			name:      "none present",
			requested: []string{"second", "first"},
			want:      []string{"second", "first"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			findings := CheckTablesExist(test.requested, test.found)
			if len(findings) != len(test.want) {
				t.Fatalf("CheckTablesExist() returned %d findings, want %d", len(findings), len(test.want))
			}
			for index, want := range test.want {
				if !reflect.DeepEqual(findings[index].Tables, []string{want}) {
					t.Errorf("finding %d Tables = %v, want [%s]", index, findings[index].Tables, want)
				}
				if findings[index].Facts != nil {
					t.Errorf("finding %d Facts = %#v, want nil", index, findings[index].Facts)
				}
			}
		})
	}
}

func TestCheckStorageEngine(t *testing.T) {
	t.Parallel()

	found := []TableInfo{
		{Table: "clean", Type: "BASE TABLE", Engine: "InnoDB"},
		{Table: "folded", Type: "BASE TABLE", Engine: "innodb"},
		{Table: "legacy", Type: "BASE TABLE", Engine: "MyISAM"},
		{Table: "report", Type: "VIEW", Engine: ""},
	}

	got := CheckStorageEngine(found, "")
	if len(got) != 1 {
		t.Fatalf("CheckStorageEngine() returned %d findings, want 1", len(got))
	}
	if got[0].Check != IDStorageEngine || !reflect.DeepEqual(got[0].Tables, []string{"legacy"}) {
		t.Errorf("finding = %#v, want STORAGE_ENGINE for legacy", got[0])
	}
	if fact, ok := got[0].Facts.(TableInfo); !ok || fact != found[2] {
		t.Errorf("finding Facts = %#v, want %#v", got[0].Facts, found[2])
	}

	if got := CheckStorageEngine(found, "myisam"); len(got) != 2 {
		t.Errorf("CheckStorageEngine(..., myisam) returned %d findings, want 2", len(got))
	}
}

func TestCheckInvisibleColumns(t *testing.T) {
	t.Parallel()

	if got := CheckInvisibleColumns(nil); got != nil {
		t.Errorf("CheckInvisibleColumns(nil) = %#v, want nil", got)
	}

	facts := []InvisibleColumns{
		{Table: "second", Columns: []string{"hidden_b", "hidden_a"}},
		{Table: "first", Columns: []string{"secret"}},
	}
	got := CheckInvisibleColumns(facts)
	if len(got) != len(facts) {
		t.Fatalf("CheckInvisibleColumns() returned %d findings, want %d", len(got), len(facts))
	}
	for index, fact := range facts {
		if got[index].Check != IDInvisibleColumns {
			t.Errorf("finding %d Check = %q, want %q", index, got[index].Check, IDInvisibleColumns)
		}
		if payload, ok := got[index].Facts.(InvisibleColumns); !ok || !reflect.DeepEqual(payload, fact) {
			t.Errorf("finding %d Facts = %#v, want %#v", index, got[index].Facts, fact)
		}
	}
}

func TestCheckTriggersPresent(t *testing.T) {
	t.Parallel()

	facts := []TriggerInfo{
		{Table: "orders", Name: "z_after", Event: "DELETE", Timing: "AFTER"},
		{Table: "orders", Name: "ignored_insert", Event: "INSERT", Timing: "BEFORE"},
		{Table: "orders", Name: "b_before", Event: "DELETE", Timing: "BEFORE"},
		{Table: "other", Name: "only", Event: "DELETE", Timing: "AFTER"},
		{Table: "orders", Name: "a_before", Event: "DELETE", Timing: "BEFORE"},
	}

	got := CheckTriggersPresent(facts, TriggerDelete)
	if len(got) != 2 {
		t.Fatalf("CheckTriggersPresent() returned %d findings, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0].Tables, []string{"orders"}) ||
		!reflect.DeepEqual(got[1].Tables, []string{"other"}) {
		t.Errorf("finding table order = %v, %v; want orders, other", got[0].Tables, got[1].Tables)
	}
	wantOrders := []TriggerInfo{
		{Table: "orders", Name: "a_before", Event: "DELETE", Timing: "BEFORE"},
		{Table: "orders", Name: "b_before", Event: "DELETE", Timing: "BEFORE"},
		{Table: "orders", Name: "z_after", Event: "DELETE", Timing: "AFTER"},
	}
	if payload, ok := got[0].Facts.([]TriggerInfo); !ok || !reflect.DeepEqual(payload, wantOrders) {
		t.Errorf("orders trigger payload = %#v, want %#v", got[0].Facts, wantOrders)
	}

	if got := CheckTriggersPresent(facts, TriggerUpdate); got != nil {
		t.Errorf("CheckTriggersPresent(..., UPDATE) = %#v, want nil", got)
	}
}

func TestTriggersSortNamesInByteOrder(t *testing.T) {
	t.Parallel()

	// Rows arrive in the server's order: ORDER BY TRIGGER_NAME collates
	// case-insensitively, so a_trg precedes B_trg (docs/COMPAT.md entry 2).
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "information_schema.TRIGGERS AS tr",
		Columns: []string{"EVENT_OBJECT_TABLE", "TRIGGER_NAME", "EVENT_MANIPULATION", "ACTION_TIMING"},
		Rows: [][]driver.Value{
			{"orders", "a_trg", "DELETE", "BEFORE"},
			{"orders", "B_trg", "DELETE", "BEFORE"},
			{"orders", "c_trg", "DELETE", "AFTER"},
		},
	})
	defer db.Close()

	got, err := NewInspector(db, "shop").Triggers(t.Context(), []string{"orders"}, TriggerDelete)
	if err != nil {
		t.Fatalf("Triggers: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, trigger := range got {
		names = append(names, trigger.Name)
	}
	if want := []string{"B_trg", "a_trg", "c_trg"}; !slices.Equal(names, want) {
		t.Errorf("Triggers() names = %v, want %v (byte order within each timing)", names, want)
	}

	findings := CheckTriggersPresent(got, TriggerDelete)
	if len(findings) != 1 {
		t.Fatalf("CheckTriggersPresent() returned %d findings, want 1", len(findings))
	}
	payload, ok := findings[0].Facts.([]TriggerInfo)
	if !ok || !reflect.DeepEqual(payload, got) {
		t.Errorf("finding payload = %#v, want the fact slice %#v; fact and check must agree by construction",
			findings[0].Facts, got)
	}
}

func TestCheckTriggersPresentReportsInvalidEvent(t *testing.T) {
	t.Parallel()

	facts := []TriggerInfo{{Table: "orders", Name: "t", Event: "DELETE", Timing: "BEFORE"}}
	for _, event := range []TriggerEvent{TriggerEventUnknown, TriggerEvent(99)} {
		for _, input := range [][]TriggerInfo{nil, facts} {
			got := CheckTriggersPresent(input, event)
			if len(got) != 1 {
				t.Fatalf("CheckTriggersPresent(%d facts, %s) returned %d findings, want 1: "+
					"a nil result would read as passed", len(input), event, len(got))
			}
			if got[0].Check != IDTriggersPresent {
				t.Errorf("finding Check = %q, want %q", got[0].Check, IDTriggersPresent)
			}
			if got[0].Tables != nil {
				t.Errorf("finding Tables = %v, want nil: no table was inspected", got[0].Tables)
			}
			if got[0].Facts != event {
				t.Errorf("finding Facts = %#v, want the rejected event %s", got[0].Facts, event)
			}
		}
	}
}

func TestFindingDeterminism(t *testing.T) {
	t.Parallel()

	pks := []PKInfo{
		{Table: "z", Kind: PKSingle, Columns: []string{"ID"}, DataType: "varchar"},
		{Table: "a", Kind: PKComposite, Columns: []string{"tenant_id", "id"}},
	}
	expected := map[string]string{"a": "other", "z": "id"}

	want, err := json.Marshal(allPKFindings(pks, expected))
	if err != nil {
		t.Fatalf("marshal baseline findings: %v", err)
	}
	for range 100 {
		got, err := json.Marshal(allPKFindings(pks, expected))
		if err != nil {
			t.Fatalf("marshal repeated findings: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("findings changed across runs:\n got %s\nwant %s", got, want)
		}
	}
}

func checkIDsForPK(pk PKInfo, expected map[string]string) []string {
	findings := allPKFindings([]PKInfo{pk}, expected)
	ids := make([]string, 0, len(findings))
	for _, finding := range findings {
		ids = append(ids, finding.Check)
	}

	return ids
}

func allPKFindings(pks []PKInfo, expected map[string]string) []Finding {
	var findings []Finding
	findings = append(findings, CheckPKExists(pks)...)
	findings = append(findings, CheckPKSingleColumn(pks)...)
	findings = append(findings, CheckPKMatchesExpected(pks, expected)...)
	findings = append(findings, CheckPKNameCase(pks, expected)...)
	findings = append(findings, CheckPKIntegerType(pks)...)

	return findings
}
