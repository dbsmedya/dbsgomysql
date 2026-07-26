//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/validations"
)

const phase1bFixture = "../fixtures/phase1b.sql"

func TestValidationFindingsE2E(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_e2e")
	testsupport.LoadSQLFixture(t, db, schema, phase1bFixture)
	inspector := validations.NewInspector(db, schema)

	scenarios := []struct {
		name                  string
		tables                []string
		expected              map[string]string
		golden                string
		checkNoInsertTriggers bool
	}{
		{
			name: "composite_pk", tables: []string{"composite_pk"},
			expected: map[string]string{"composite_pk": "key_first"},
			golden:   "testdata/composite_pk.json",
		},
		{
			name: "no_pk", tables: []string{"no_pk"},
			expected: map[string]string{"no_pk": "id"},
			golden:   "testdata/no_pk.json",
		},
		{
			name: "pk_case_mismatch", tables: []string{"pk_case_mismatch"},
			expected: map[string]string{"pk_case_mismatch": "log_id"},
			golden:   "testdata/pk_case_mismatch.json",
		},
		{
			name: "pk_varchar", tables: []string{"pk_varchar"},
			expected: map[string]string{"pk_varchar": "id"},
			golden:   "testdata/pk_varchar.json",
		},
		{
			name: "invisible_columns", tables: []string{"invisible_columns"},
			expected: map[string]string{"invisible_columns": "id"},
			golden:   "testdata/invisible_columns.json",
		},
		{
			name: "myisam_table", tables: []string{"myisam_table"},
			expected: map[string]string{"myisam_table": "id"},
			golden:   "testdata/myisam_table.json",
		},
		{
			name: "delete_trigger", tables: []string{"delete_only_trigger"},
			expected: map[string]string{"delete_only_trigger": "id"},
			golden:   "testdata/delete_trigger.json", checkNoInsertTriggers: true,
		},
		{
			name: "missing_table", tables: []string{"missing_table"},
			expected: map[string]string{"missing_table": "id"},
			golden:   "testdata/missing_table.json",
		},
		{
			name: "all_defects",
			tables: []string{
				"delete_only_trigger",
				"no_pk",
				"myisam_table",
				"pk_case_mismatch",
				"missing_table",
				"invisible_columns",
				"expected_mismatch",
				"pk_varchar",
				"composite_pk",
			},
			expected: map[string]string{
				"delete_only_trigger": "id",
				"no_pk":               "id",
				"myisam_table":        "id",
				"pk_case_mismatch":    "log_id",
				"missing_table":       "id",
				"invisible_columns":   "id",
				"expected_mismatch":   "configured_id",
				"pk_varchar":          "id",
				"composite_pk":        "key_first",
			},
			golden: "testdata/all_defects.json",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			facts := inspectScenario(t, inspector, scenario.tables, validations.TriggerDelete)
			got := findingsInTableAndCatalogOrder(scenario.tables, scenario.expected, &facts)
			assertGoldenFindings(t, got, scenario.golden)

			if scenario.checkNoInsertTriggers {
				insertFacts, err := inspector.Triggers(
					t.Context(),
					scenario.tables,
					validations.TriggerInsert,
				)
				if err != nil {
					t.Fatalf("inspect INSERT triggers: %v", err)
				}
				if findings := validations.CheckTriggersPresent(
					insertFacts,
					validations.TriggerInsert,
				); findings != nil {
					t.Errorf("INSERT trigger findings = %#v, want nil", findings)
				}
			}
		})
	}
}

type scenarioFacts struct {
	tables    []validations.TableInfo
	pks       []validations.PKInfo
	invisible []validations.InvisibleColumns
	triggers  []validations.TriggerInfo
}

func inspectScenario(
	t *testing.T,
	inspector *validations.Inspector,
	tables []string,
	event validations.TriggerEvent,
) scenarioFacts {
	t.Helper()

	tableFacts, err := inspector.Tables(t.Context(), tables)
	if err != nil {
		t.Fatalf("inspect tables: %v", err)
	}
	pks, err := inspector.PrimaryKeys(t.Context(), tables)
	if err != nil {
		t.Fatalf("inspect primary keys: %v", err)
	}
	invisible, err := inspector.InvisibleColumns(t.Context(), tables)
	if err != nil {
		t.Fatalf("inspect invisible columns: %v", err)
	}
	triggers, err := inspector.Triggers(t.Context(), tables, event)
	if err != nil {
		t.Fatalf("inspect triggers: %v", err)
	}

	return scenarioFacts{
		tables: tableFacts, pks: pks, invisible: invisible, triggers: triggers,
	}
}

func findingsInTableAndCatalogOrder(
	requested []string,
	expected map[string]string,
	facts *scenarioFacts,
) []validations.Finding {
	var findings []validations.Finding
	for _, table := range requested {
		tableFacts := tableInfoFor(facts.tables, table)
		pks := pkInfoFor(facts.pks, table)
		invisible := invisibleFor(facts.invisible, table)
		triggers := triggersFor(facts.triggers, table)

		for _, check := range validations.Catalog() {
			if check.Status != validations.StatusImplemented {
				continue
			}
			switch check.ID {
			case validations.IDInvisibleColumns:
				findings = append(findings, validations.CheckInvisibleColumns(invisible)...)
			case validations.IDPKExists:
				findings = append(findings, validations.CheckPKExists(pks)...)
			case validations.IDPKIntegerType:
				findings = append(findings, validations.CheckPKIntegerType(pks)...)
			case validations.IDPKMatchesExpected:
				findings = append(
					findings,
					validations.CheckPKMatchesExpected(pks, expected)...,
				)
			case validations.IDPKNameCase:
				findings = append(findings, validations.CheckPKNameCase(pks, expected)...)
			case validations.IDPKSingleColumn:
				findings = append(findings, validations.CheckPKSingleColumn(pks)...)
			case validations.IDStorageEngine:
				findings = append(findings, validations.CheckStorageEngine(tableFacts, "")...)
			case validations.IDTablesExist:
				findings = append(
					findings,
					validations.CheckTablesExist([]string{table}, tableFacts)...,
				)
			case validations.IDTriggersPresent:
				findings = append(
					findings,
					validations.CheckTriggersPresent(triggers, validations.TriggerDelete)...,
				)
			}
		}
	}

	return findings
}

func tableInfoFor(facts []validations.TableInfo, table string) []validations.TableInfo {
	for _, fact := range facts {
		if fact.Table == table {
			return []validations.TableInfo{fact}
		}
	}

	return nil
}

func pkInfoFor(facts []validations.PKInfo, table string) []validations.PKInfo {
	for _, fact := range facts {
		if fact.Table == table {
			return []validations.PKInfo{fact}
		}
	}

	return nil
}

func invisibleFor(
	facts []validations.InvisibleColumns,
	table string,
) []validations.InvisibleColumns {
	for _, fact := range facts {
		if fact.Table == table {
			return []validations.InvisibleColumns{fact}
		}
	}

	return nil
}

func triggersFor(facts []validations.TriggerInfo, table string) []validations.TriggerInfo {
	var found []validations.TriggerInfo
	for _, fact := range facts {
		if fact.Table == table {
			found = append(found, fact)
		}
	}

	return found
}

type projectedFinding struct {
	Check  string   `json:"check"`
	Tables []string `json:"tables"`
	Facts  any      `json:"facts"`
}

func assertGoldenFindings(t *testing.T, findings []validations.Finding, path string) {
	t.Helper()

	projected := make([]projectedFinding, 0, len(findings))
	for _, finding := range findings {
		projected = append(projected, projectedFinding{
			Check: finding.Check, Tables: finding.Tables, Facts: finding.Facts,
		})
	}
	actualJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected findings: %v", err)
	}
	expectedJSON, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v", path, err)
	}

	var actual, expected any
	if err := json.Unmarshal(actualJSON, &actual); err != nil {
		t.Fatalf("parse actual findings JSON: %v", err)
	}
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatalf("parse golden findings JSON: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("findings differ from %s:\n got %s\nwant %s", path, actualJSON, expectedJSON)
	}
}
