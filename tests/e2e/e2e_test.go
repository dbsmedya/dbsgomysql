//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
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

func TestPhase1cFindingsE2E(t *testing.T) {
	admin, schema := testsupport.MySQLDatabase(t, "dbsgomysql_e2e_phase1c")
	testsupport.LoadSQLFixture(t, admin, schema, phase1bFixture)

	t.Run("same_schema_external_referrer", func(t *testing.T) {
		inspector := validations.NewInspector(admin, schema)
		incoming := mustForeignKeys(
			t,
			inspector,
			validations.IncomingTo("fk_parent"),
		)
		facts := phase1cFacts{
			incoming: &incoming,
			schema:   schema,
			tables: []string{
				"fk_parent", "fk_internal_child", "fk_cascade_child",
			},
		}
		got := phase1cFindingsInCatalogOrder(&facts)
		assertPhase1cGolden(
			t,
			got,
			"testdata/same_schema_external_referrer.json",
			schema,
			"",
		)
	})

	externalDB, externalSchema := testsupport.MySQLDatabase(t, "dbsgomysql_e2e_external")
	testsupport.ExecSQL(
		t,
		externalDB,
		"CREATE TABLE "+sqlutil.QuoteQualified(externalSchema, "fk_internal_child")+
			" (id INT NOT NULL PRIMARY KEY, parent_id INT NOT NULL, "+
			"CONSTRAINT `fk_cross_parent` FOREIGN KEY (parent_id) REFERENCES "+
			sqlutil.QuoteQualified(schema, "fk_parent")+" (id)) ENGINE=InnoDB",
	)

	t.Run("cross_schema_external_referrer", func(t *testing.T) {
		inspector := validations.NewInspector(admin, schema)
		incoming := mustForeignKeys(
			t,
			inspector,
			validations.IncomingTo("fk_parent"),
		)
		facts := phase1cFacts{
			incoming: &incoming,
			schema:   schema,
			tables: []string{
				"fk_parent", "fk_internal_child", "fk_external_child",
				"fk_cascade_child",
			},
		}
		got := phase1cFindingsInCatalogOrder(&facts)
		assertPhase1cGolden(
			t,
			got,
			"testdata/cross_schema_external_referrer.json",
			schema,
			externalSchema,
		)
	})

	t.Run("cascade_edge", func(t *testing.T) {
		inspector := validations.NewInspector(admin, schema)
		within := mustForeignKeys(
			t,
			inspector,
			validations.Within("fk_parent", "fk_cascade_child"),
		)
		got := phase1cFindingsInCatalogOrder(&phase1cFacts{
			within: &within,
		})
		assertPhase1cGolden(
			t,
			got,
			"testdata/cascade_edge.json",
			schema,
			externalSchema,
		)
	})

	t.Run("missing_table_privilege", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_e2e_missing")
		conn := testsupport.MySQLConnAs(t, account)
		grants := mustGrants(t, validations.NewInspector(conn, schema))
		got := phase1cFindingsInCatalogOrder(&phase1cFacts{
			grants:         &grants,
			schema:         schema,
			tables:         []string{"fk_parent"},
			tablePrivilege: validations.PrivilegeDelete,
		})
		assertPhase1cGolden(
			t,
			got,
			"testdata/missing_table_privilege.json",
			schema,
			externalSchema,
		)
	})

	t.Run("nested_role_unconfirmed", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_e2e_nested")
		outer := testsupport.CreateMySQLRole(t, admin, "dbsgomysql_e2e_outer")
		inner := testsupport.CreateMySQLRole(t, admin, "dbsgomysql_e2e_inner")
		testsupport.ExecSQL(
			t,
			admin,
			"GRANT SELECT ON "+sqlutil.QuoteIdentifier(schema)+".* TO "+
				testsupport.GrantAccountSQL(inner),
		)
		testsupport.ExecSQL(
			t,
			admin,
			"GRANT "+testsupport.GrantAccountSQL(inner)+" TO "+
				testsupport.GrantAccountSQL(outer),
		)
		testsupport.ExecSQL(
			t,
			admin,
			"GRANT "+testsupport.GrantAccountSQL(outer)+" TO "+
				testsupport.GrantAccountSQL(account),
		)
		conn := testsupport.MySQLConnAs(t, account)
		if _, err := conn.ExecContext(t.Context(), "SET ROLE ALL"); err != nil {
			t.Fatalf("enable outer role: %v", err)
		}
		grants := mustGrants(t, validations.NewInspector(conn, schema))
		got := phase1cFindingsInCatalogOrder(&phase1cFacts{
			grants:           &grants,
			schema:           schema,
			schemaPrivileges: []validations.Privilege{validations.PrivilegeSelect},
		})
		assertPhase1cGolden(
			t,
			got,
			"testdata/nested_role_unconfirmed.json",
			schema,
			externalSchema,
		)
	})

	t.Run("process_only_complete_visibility", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_e2e_process")
		testsupport.ExecSQL(
			t,
			admin,
			"GRANT PROCESS ON *.* TO "+testsupport.GrantAccountSQL(account),
		)
		conn := testsupport.MySQLConnAs(t, account)
		incoming := mustForeignKeys(
			t,
			validations.NewInspector(conn, schema),
			validations.IncomingTo("fk_parent"),
		)
		got := phase1cFindingsInCatalogOrder(&phase1cFacts{
			incoming: &incoming,
			schema:   schema,
			tables: []string{
				"fk_parent", "fk_internal_child", "fk_external_child",
				"fk_cascade_child",
			},
		})
		assertPhase1cGolden(
			t,
			got,
			"testdata/process_only_complete_visibility.json",
			schema,
			externalSchema,
		)
	})

	t.Run("fallback_visibility_unconfirmed", func(t *testing.T) {
		account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_e2e_fallback")
		testsupport.ExecSQL(
			t,
			admin,
			"GRANT SELECT ON "+sqlutil.QuoteQualified(schema, "fk_internal_child")+
				" TO "+testsupport.GrantAccountSQL(account),
		)
		conn := testsupport.MySQLConnAs(t, account)
		incoming := mustForeignKeys(
			t,
			validations.NewInspector(conn, schema),
			validations.IncomingTo("fk_parent"),
		)
		got := phase1cFindingsInCatalogOrder(&phase1cFacts{
			incoming: &incoming,
			schema:   schema,
			tables:   []string{"fk_parent", "fk_internal_child"},
		})
		assertPhase1cGolden(
			t,
			got,
			"testdata/fallback_visibility_unconfirmed.json",
			schema,
			externalSchema,
		)
	})
}

type phase1cFacts struct {
	incoming         *validations.ForeignKeyResult
	within           *validations.ForeignKeyResult
	grants           *validations.Grants
	schema           string
	tables           []string
	tablePrivilege   validations.Privilege
	schemaPrivileges []validations.Privilege
}

func phase1cFindingsInCatalogOrder(facts *phase1cFacts) []validations.Finding {
	var findings []validations.Finding
	for _, check := range validations.Catalog() {
		switch check.ID {
		case validations.IDCascadeRules:
			if facts.within != nil {
				findings = append(
					findings,
					validations.CheckCascadeRules(facts.within.Keys)...,
				)
			}
		case validations.IDFKClosure:
			if facts.incoming != nil {
				findings = append(findings, validations.CheckFKClosure(
					*facts.incoming,
					facts.schema,
					facts.tables,
				)...)
			}
		case validations.IDFKIndexed:
			if facts.incoming != nil {
				findings = append(
					findings,
					validations.CheckFKIndexed(facts.incoming.Keys)...,
				)
			}
		case validations.IDFKMetadataVisibility:
			if facts.incoming != nil {
				findings = append(
					findings,
					validations.CheckFKMetadataVisibility(facts.incoming.Visibility)...,
				)
			}
		case validations.IDSchemaPrivileges:
			if facts.grants != nil && len(facts.schemaPrivileges) > 0 {
				findings = append(findings, validations.CheckSchemaPrivileges(
					*facts.grants,
					facts.schema,
					facts.schemaPrivileges,
				)...)
			}
		case validations.IDTablePrivileges:
			if facts.grants != nil && facts.tablePrivilege != validations.PrivilegeUnknown {
				findings = append(findings, validations.CheckTablePrivileges(
					*facts.grants,
					facts.schema,
					facts.tables,
					facts.tablePrivilege,
				)...)
			}
		}
	}

	return findings
}

func mustForeignKeys(
	t *testing.T,
	inspector *validations.Inspector,
	selector validations.FKSelector,
) validations.ForeignKeyResult {
	t.Helper()

	result, err := inspector.ForeignKeys(t.Context(), selector)
	if err != nil {
		t.Fatalf("inspect foreign keys: %v", err)
	}

	return result
}

func mustGrants(
	t *testing.T,
	inspector *validations.Inspector,
) validations.Grants {
	t.Helper()

	grants, err := inspector.Grants(t.Context())
	if err != nil {
		t.Fatalf("inspect grants: %v", err)
	}

	return grants
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

func assertPhase1cGolden(
	t *testing.T,
	findings []validations.Finding,
	path string,
	targetSchema string,
	externalSchema string,
) {
	t.Helper()

	projected := make([]projectedFinding, 0, len(findings))
	for _, finding := range findings {
		projected = append(projected, projectedFinding{
			Check:  finding.Check,
			Tables: append([]string(nil), finding.Tables...),
			Facts: normalizePhase1cFact(
				finding.Facts,
				targetSchema,
				externalSchema,
			),
		})
	}
	actualJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected phase-1c findings: %v", err)
	}
	expectedJSON, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read phase-1c golden %q: %v", path, err)
	}

	var actual, expected any
	if err := json.Unmarshal(actualJSON, &actual); err != nil {
		t.Fatalf("parse actual phase-1c findings JSON: %v", err)
	}
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatalf("parse phase-1c golden findings JSON: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("findings differ from %s:\n got %s\nwant %s", path, actualJSON, expectedJSON)
	}
}

func normalizePhase1cFact(
	fact any,
	targetSchema string,
	externalSchema string,
) any {
	normalizeSchema := func(schema string) string {
		switch schema {
		case targetSchema:
			return "{{target_schema}}"
		case externalSchema:
			if externalSchema != "" {
				return "{{external_schema}}"
			}
		}

		return schema
	}

	switch value := fact.(type) {
	case validations.ForeignKey:
		copyValue := value
		copyValue.ChildColumns = append([]string(nil), value.ChildColumns...)
		copyValue.ParentColumns = append([]string(nil), value.ParentColumns...)
		copyValue.ChildSchema = normalizeSchema(value.ChildSchema)
		copyValue.ParentSchema = normalizeSchema(value.ParentSchema)

		return copyValue
	case validations.PrivilegeFact:
		copyValue := value
		copyValue.Schema = normalizeSchema(value.Schema)

		return copyValue
	default:
		return fact
	}
}
