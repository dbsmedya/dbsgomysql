//go:build integration

package validations_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
	"github.com/dbsmedya/dbsgomysql/pkg/validations"
)

const fixturePath = "../../tests/fixtures/phase1b.sql"

func TestInspectorSmoke(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)
	tables := smokeTables()

	tableFacts, err := inspector.Tables(t.Context(), tables)
	if err != nil {
		t.Fatalf("Tables smoke call: %v", err)
	}
	pkFacts, err := inspector.PrimaryKeys(t.Context(), tables)
	if err != nil {
		t.Fatalf("PrimaryKeys smoke call: %v", err)
	}
	invisibleFacts, err := inspector.InvisibleColumns(t.Context(), tables)
	if err != nil {
		t.Fatalf("InvisibleColumns smoke call: %v", err)
	}
	triggerFacts, err := inspector.Triggers(t.Context(), tables, validations.TriggerDelete)
	if err != nil {
		t.Fatalf("Triggers smoke call: %v", err)
	}
	incoming, err := inspector.ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys IncomingTo smoke call: %v", err)
	}
	within, err := inspector.ForeignKeys(
		t.Context(),
		validations.Within("fk_parent", "fk_internal_child", "fk_cascade_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys Within smoke call: %v", err)
	}
	grants, err := inspector.Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants smoke call: %v", err)
	}
	spec, err := inspector.TableSpec(t.Context(),
		validations.Ref(schema, "clean_table"),
		validations.WithIndexes(), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}
	if spec.Table != "clean_table" {
		t.Errorf("TableSpec.Table = %q, want \"clean_table\"", spec.Table)
	}
	if spec.Engine != "InnoDB" {
		t.Errorf("TableSpec.Engine = %q, want \"InnoDB\"", spec.Engine)
	}
	if len(spec.Columns) == 0 {
		t.Error("TableSpec returned no columns for a table that has one")
	}
	if !spec.Captured.Has(validations.SectionIndexes) {
		t.Error("Captured does not record SectionIndexes despite WithIndexes")
	}
	if diffs := validations.DiffSpecs(spec, spec); len(diffs) != 0 {
		t.Errorf("DiffSpecs against itself returned %+v, want none", diffs)
	}

	expected := smokeExpectedPKs()
	_ = validations.CheckTablesExist(tables, tableFacts)
	_ = validations.CheckStorageEngine(tableFacts, "")
	_ = validations.CheckInvisibleColumns(invisibleFacts)
	_ = validations.CheckTriggersPresent(triggerFacts, validations.TriggerDelete)
	_ = validations.CheckPKExists(pkFacts)
	_ = validations.CheckPKSingleColumn(pkFacts)
	_ = validations.CheckPKMatchesExpected(pkFacts, expected)
	_ = validations.CheckPKNameCase(pkFacts, expected)
	_ = validations.CheckPKIntegerType(pkFacts)
	_ = validations.CheckFKIndexed(incoming.Keys)
	_ = validations.CheckFKClosure(incoming, schema, []string{"fk_parent"})
	_ = validations.CheckFKMetadataVisibility(incoming.Visibility)
	_ = validations.CheckCascadeRules(within.Keys)
	_ = validations.CheckTablePrivileges(
		grants,
		schema,
		[]string{"fk_parent"},
		validations.PrivilegeSelect,
	)
	_ = validations.CheckSchemaPrivileges(
		grants,
		schema,
		[]validations.Privilege{validations.PrivilegeCreate},
	)
}

func TestFindingsSmoke(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)
	tables := smokeTables()

	tableFacts, err := inspector.Tables(t.Context(), tables)
	if err != nil {
		t.Fatalf("Tables smoke call: %v", err)
	}
	pkFacts, err := inspector.PrimaryKeys(t.Context(), tables)
	if err != nil {
		t.Fatalf("PrimaryKeys smoke call: %v", err)
	}
	invisibleFacts, err := inspector.InvisibleColumns(t.Context(), tables)
	if err != nil {
		t.Fatalf("InvisibleColumns smoke call: %v", err)
	}
	triggerFacts, err := inspector.Triggers(t.Context(), tables, validations.TriggerDelete)
	if err != nil {
		t.Fatalf("Triggers smoke call: %v", err)
	}
	incoming, err := inspector.ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys smoke call: %v", err)
	}
	within, err := inspector.ForeignKeys(
		t.Context(),
		validations.Within("fk_parent", "fk_cascade_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys Within smoke call: %v", err)
	}

	expected := smokeExpectedPKs()
	var findings []validations.Finding
	findings = append(findings, validations.CheckTablesExist(tables, tableFacts)...)
	findings = append(findings, validations.CheckStorageEngine(tableFacts, "")...)
	findings = append(findings, validations.CheckInvisibleColumns(invisibleFacts)...)
	findings = append(findings, validations.CheckTriggersPresent(triggerFacts, validations.TriggerDelete)...)
	findings = append(findings, validations.CheckPKExists(pkFacts)...)
	findings = append(findings, validations.CheckPKSingleColumn(pkFacts)...)
	findings = append(findings, validations.CheckPKMatchesExpected(pkFacts, expected)...)
	findings = append(findings, validations.CheckPKNameCase(pkFacts, expected)...)
	findings = append(findings, validations.CheckPKIntegerType(pkFacts)...)
	findings = append(findings, validations.CheckFKClosure(
		incoming,
		schema,
		[]string{"fk_parent"},
	)...)
	findings = append(findings, validations.CheckCascadeRules(within.Keys)...)
	findings = append(findings, validations.CheckFKIndexed([]validations.ForeignKey{{
		ConstraintName: "synthetic_nonconforming",
		ChildSchema:    schema,
		ChildTable:     "fk_external_child",
	}})...)
	findings = append(findings, validations.CheckFKMetadataVisibility(
		validations.VisibilityUnconfirmed,
	)...)
	findings = append(findings, validations.CheckTablePrivileges(
		validations.Grants{},
		schema,
		[]string{"fk_parent"},
		validations.PrivilegeDelete,
	)...)
	findings = append(findings, validations.CheckSchemaPrivileges(
		validations.Grants{},
		schema,
		[]validations.Privilege{validations.PrivilegeCreate},
	)...)

	got := findingIDs(findings)
	slices.Sort(got)
	want := []string{
		validations.IDInvisibleColumns,
		validations.IDCascadeRules,
		validations.IDFKClosure,
		validations.IDFKClosure,
		validations.IDFKClosure,
		validations.IDFKIndexed,
		validations.IDFKMetadataVisibility,
		validations.IDPKExists,
		validations.IDPKIntegerType,
		validations.IDPKMatchesExpected,
		validations.IDPKNameCase,
		validations.IDPKSingleColumn,
		validations.IDStorageEngine,
		validations.IDSchemaPrivileges,
		validations.IDTablesExist,
		validations.IDTablePrivileges,
		validations.IDTriggersPresent,
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("smoke finding IDs = %v, want %v", got, want)
	}
}

func TestMetadataNameCollationsIntegration(t *testing.T) {
	db, _ := testsupport.MySQLDatabase(t, "dbsgomysql_collations")

	probes := []struct {
		table  string
		column string
		want   string
	}{
		{table: "TABLES", column: "TABLE_NAME", want: "utf8mb3_bin"},
		{table: "SCHEMATA", column: "SCHEMA_NAME", want: "utf8mb3_bin"},
		{table: "COLUMNS", column: "COLUMN_NAME", want: "utf8mb3_tolower_ci"},
		{table: "TABLE_CONSTRAINTS", column: "CONSTRAINT_NAME", want: "utf8mb3_tolower_ci"},
		{table: "TRIGGERS", column: "TRIGGER_NAME", want: "utf8mb3_general_ci"},
	}
	const query = `
		SELECT COLLATION_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = 'information_schema'
		  AND TABLE_NAME = ?
		  AND COLUMN_NAME = ?`

	for _, probe := range probes {
		var got sql.NullString
		if err := db.QueryRowContext(t.Context(), query, probe.table, probe.column).Scan(&got); err != nil {
			t.Fatalf("read collation for %s.%s: %v", probe.table, probe.column, err)
		}
		if !got.Valid || got.String != probe.want {
			t.Errorf("%s.%s collation = %q (valid %t), want %q",
				probe.table, probe.column, got.String, got.Valid, probe.want)
		}
	}
}

func TestColumnNameCaseInsensitivityIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	const query = `
		SELECT COLUMN_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ?
		  AND TABLE_NAME = 'pk_case_mismatch'
		  AND COLUMN_NAME = 'log_id'`
	var actual string
	if err := db.QueryRowContext(t.Context(), query, schema).Scan(&actual); err != nil {
		t.Fatalf("case-folded metadata lookup: %v", err)
	}
	if actual != "LOG_ID" {
		t.Errorf("case-folded metadata lookup returned %q, want server spelling LOG_ID", actual)
	}

	pks, err := validations.NewInspector(db, schema).PrimaryKeys(
		t.Context(),
		[]string{"pk_case_mismatch"},
	)
	if err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	expected := map[string]string{"pk_case_mismatch": "log_id"}
	if got := validations.CheckPKMatchesExpected(pks, expected); got != nil {
		t.Errorf("CheckPKMatchesExpected() = %#v, want nil for case-only difference", got)
	}
	got := validations.CheckPKNameCase(pks, expected)
	if len(got) != 1 || got[0].Check != validations.IDPKNameCase {
		t.Errorf("CheckPKNameCase() = %#v, want one PK_NAME_CASE finding", got)
	}
}

func TestTableNameCaseSensitivityIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_table_case")

	var lowerCaseTableNames int
	if err := db.QueryRowContext(t.Context(), "SELECT @@lower_case_table_names").Scan(&lowerCaseTableNames); err != nil {
		t.Fatalf("read lower_case_table_names: %v", err)
	}
	if lowerCaseTableNames != 0 {
		t.Skipf("lower_case_table_names=%d; distinct t1/T1 fixture requires 0", lowerCaseTableNames)
	}

	testsupport.ExecSQL(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "t1")+" (id INT PRIMARY KEY)",
	)
	testsupport.ExecSQL(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "T1")+" (id INT PRIMARY KEY)",
	)

	got, err := validations.NewInspector(db, schema).Tables(t.Context(), []string{"T1", "t1"})
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	want := []validations.TableInfo{
		{Table: "T1", Type: "BASE TABLE", Engine: "InnoDB"},
		{Table: "t1", Type: "BASE TABLE", Engine: "InnoDB"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tables() = %#v, want %#v", got, want)
	}
}

func TestPrimaryKeysIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	tables := []string{
		"single_unsigned",
		"big_pk",
		"composite_pk",
		"no_pk",
		"pk_varchar",
		"pk_secondary",
	}

	got, err := validations.NewInspector(db, schema).PrimaryKeys(t.Context(), tables)
	if err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	want := []validations.PKInfo{
		{
			Table: "single_unsigned", Kind: validations.PKSingle, Columns: []string{"id"},
			DataType: "int", IsInteger: true, Unsigned: true,
		},
		{
			Table: "big_pk", Kind: validations.PKSingle, Columns: []string{"id"},
			DataType: "bigint", IsInteger: true,
		},
		{
			Table: "composite_pk", Kind: validations.PKComposite,
			Columns: []string{"key_first", "ordinal_first"},
		},
		{Table: "no_pk", Kind: validations.PKNone},
		{
			Table: "pk_varchar", Kind: validations.PKSingle, Columns: []string{"id"},
			DataType: "varchar",
		},
		{
			Table: "pk_secondary", Kind: validations.PKSingle, Columns: []string{"id"},
			DataType: "int", IsInteger: true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PrimaryKeys() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestInvisibleColumnsIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	got, err := validations.NewInspector(db, schema).InvisibleColumns(
		t.Context(),
		[]string{"clean_table", "invisible_columns"},
	)
	if err != nil {
		t.Fatalf("InvisibleColumns: %v", err)
	}
	want := []validations.InvisibleColumns{{
		Table: "invisible_columns",
		Columns: []string{
			"plain_secret",
			"generated_secret",
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InvisibleColumns() = %#v, want %#v", got, want)
	}
}

func TestTriggersIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)

	tests := []struct {
		event   validations.TriggerEvent
		names   []string
		timings []string
	}{
		{
			event:   validations.TriggerDelete,
			names:   []string{"ADeleteBefore", "BDeleteBefore", "ZDeleteAfter"},
			timings: []string{"BEFORE", "BEFORE", "AFTER"},
		},
		{
			event:   validations.TriggerInsert,
			names:   []string{"InsertBefore"},
			timings: []string{"BEFORE"},
		},
		{
			event:   validations.TriggerUpdate,
			names:   []string{"UpdateAfter"},
			timings: []string{"AFTER"},
		},
	}
	for _, test := range tests {
		got, err := inspector.Triggers(t.Context(), []string{"delete_trigger"}, test.event)
		if err != nil {
			t.Fatalf("Triggers(%s): %v", test.event, err)
		}
		names := make([]string, 0, len(got))
		timings := make([]string, 0, len(got))
		for _, trigger := range got {
			names = append(names, trigger.Name)
			timings = append(timings, trigger.Timing)
			if trigger.Event != test.event.String() {
				t.Errorf("trigger %q event = %q, want %q", trigger.Name, trigger.Event, test.event)
			}
		}
		if !slices.Equal(names, test.names) {
			t.Errorf("Triggers(%s) names = %v, want %v", test.event, names, test.names)
		}
		if !slices.Equal(timings, test.timings) {
			t.Errorf("Triggers(%s) timings = %v, want %v", test.event, timings, test.timings)
		}
	}
}

// TestTriggerTimingEnumOrderIntegration pins docs/COMPAT.md entry 10: the
// BEFORE-ahead-of-AFTER order that Inspector.Triggers reports comes from
// information_schema.TRIGGERS.ACTION_TIMING being ENUM('BEFORE','AFTER') and
// MySQL sorting an ENUM by declaration index. If a server ever exposed that
// column as text, ORDER BY would invert the pair and this test fails rather
// than the ordering silently regressing.
func TestTriggerTimingEnumOrderIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	const columnType = `
		SELECT COLUMN_TYPE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = 'information_schema'
		  AND TABLE_NAME = 'TRIGGERS'
		  AND COLUMN_NAME = 'ACTION_TIMING'`
	var declared string
	if err := db.QueryRowContext(t.Context(), columnType).Scan(&declared); err != nil {
		t.Fatalf("read ACTION_TIMING column type: %v", err)
	}
	if declared != "enum('BEFORE','AFTER')" {
		t.Errorf("ACTION_TIMING type = %q, want %q", declared, "enum('BEFORE','AFTER')")
	}

	// The server's own ordering, independent of the library, so a change in
	// ENUM sort semantics is caught here and not only through the fact method.
	const ordered = `
		SELECT ACTION_TIMING
		FROM information_schema.TRIGGERS
		WHERE EVENT_OBJECT_SCHEMA = ?
		  AND EVENT_OBJECT_TABLE = 'delete_trigger'
		  AND EVENT_MANIPULATION = 'DELETE'
		ORDER BY ACTION_TIMING, TRIGGER_NAME`
	rows, err := db.QueryContext(t.Context(), ordered, schema)
	if err != nil {
		t.Fatalf("order triggers by timing: %v", err)
	}
	defer rows.Close()

	timings := make([]string, 0, 3)
	for rows.Next() {
		var timing string
		if err := rows.Scan(&timing); err != nil {
			t.Fatalf("scan timing: %v", err)
		}
		timings = append(timings, timing)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate timings: %v", err)
	}

	want := []string{"BEFORE", "BEFORE", "AFTER"}
	if !slices.Equal(timings, want) {
		t.Errorf("ORDER BY ACTION_TIMING = %v, want %v", timings, want)
	}
}

func TestStorageEngineIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	got, err := validations.NewInspector(db, schema).Tables(
		t.Context(),
		[]string{"clean_table", "myisam_table"},
	)
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	want := []validations.TableInfo{
		{Table: "clean_table", Type: "BASE TABLE", Engine: "InnoDB"},
		{Table: "myisam_table", Type: "BASE TABLE", Engine: "MyISAM"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tables() = %#v, want %#v", got, want)
	}
}

func TestViewsIntegration(t *testing.T) {
	db, schema := validationDatabase(t)

	got, err := validations.NewInspector(db, schema).Tables(t.Context(), []string{"report_view"})
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	want := []validations.TableInfo{{Table: "report_view", Type: "VIEW", Engine: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tables() = %#v, want %#v", got, want)
	}
	if findings := validations.CheckTablesExist([]string{"report_view"}, got); findings != nil {
		t.Errorf("CheckTablesExist(view) = %#v, want nil", findings)
	}
	if findings := validations.CheckStorageEngine(got, "InnoDB"); findings != nil {
		t.Errorf("CheckStorageEngine(view) = %#v, want nil", findings)
	}
}

func TestForeignKeysIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	externalDB, externalSchema := testsupport.MySQLDatabase(t, "dbsgomysql_fk_external")
	testsupport.ExecSQL(
		t,
		externalDB,
		"CREATE TABLE "+sqlutil.QuoteQualified(externalSchema, "fk_internal_child")+
			" (id INT NOT NULL PRIMARY KEY, parent_id INT NOT NULL, "+
			"CONSTRAINT `fk_cross_parent` FOREIGN KEY (parent_id) REFERENCES "+
			sqlutil.QuoteQualified(schema, "fk_parent")+" (id)) ENGINE=InnoDB",
	)

	inspector := validations.NewInspector(db, schema)
	incoming, err := inspector.ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys IncomingTo: %v", err)
	}
	if incoming.Visibility != validations.VisibilityComplete {
		t.Errorf("IncomingTo visibility = %s, want complete", incoming.Visibility)
	}
	if len(incoming.Keys) != 4 {
		t.Fatalf("IncomingTo keys = %d, want 4: %#v", len(incoming.Keys), incoming.Keys)
	}
	byConstraint := make(map[string]validations.ForeignKey, len(incoming.Keys))
	for _, key := range incoming.Keys {
		byConstraint[key.ConstraintName] = key
		if !key.Indexed {
			t.Errorf("authoritative key %q Indexed = false", key.ConstraintName)
		}
	}
	cross, ok := byConstraint["fk_cross_parent"]
	if !ok || cross.ChildSchema != externalSchema ||
		cross.ChildTable != "fk_internal_child" {
		t.Errorf("cross-schema key = %#v, want exact external identity", cross)
	}

	within, err := inspector.ForeignKeys(
		t.Context(),
		validations.Within("fk_parent", "fk_internal_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys Within: %v", err)
	}
	if len(within.Keys) != 1 ||
		within.Keys[0].ConstraintName != "fk_internal_parent" {
		t.Errorf("Within keys = %#v, want internal constraint only", within.Keys)
	}

	outgoing, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("fk_internal_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys OutgoingFrom: %v", err)
	}
	if len(outgoing.Keys) != 1 ||
		outgoing.Keys[0].ConstraintName != "fk_internal_parent" {
		t.Errorf("OutgoingFrom keys = %#v, want internal constraint", outgoing.Keys)
	}

	closure := validations.CheckFKClosure(
		incoming,
		schema,
		[]string{"fk_parent", "fk_internal_child"},
	)
	if len(closure) != 3 {
		t.Errorf("closure findings = %d, want 3 external children: %#v", len(closure), closure)
	}

	composite, err := inspector.ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_composite_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys composite: %v", err)
	}
	wantComposite := validations.ForeignKey{
		ConstraintName: "fk_composite_parent",
		ChildSchema:    schema,
		ChildTable:     "fk_composite_child",
		ChildColumns:   []string{"tenant_id", "parent_id"},
		ParentSchema:   schema,
		ParentTable:    "fk_composite_parent",
		ParentColumns:  []string{"tenant_id", "id"},
		OnDelete:       "SET NULL",
		OnUpdate:       "NO ACTION",
		Indexed:        true,
	}
	if len(composite.Keys) != 1 || !reflect.DeepEqual(composite.Keys[0], wantComposite) {
		t.Errorf("composite keys = %#v, want %#v", composite.Keys, wantComposite)
	}

	assertLeftmostCompositeIndex(t, db, schema)

	ruleKeys, err := inspector.ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_rule_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys rule matrix: %v", err)
	}
	wantRules := map[string][2]string{
		"fk_rule_cascade":   {"CASCADE", "CASCADE"},
		"fk_rule_no_action": {"NO ACTION", "NO ACTION"},
		"fk_rule_restrict":  {"RESTRICT", "RESTRICT"},
		"fk_rule_set_null":  {"SET NULL", "SET NULL"},
	}
	if len(ruleKeys.Keys) != len(wantRules) {
		t.Fatalf("rule matrix keys = %d, want %d: %#v", len(ruleKeys.Keys), len(wantRules), ruleKeys.Keys)
	}
	for _, key := range ruleKeys.Keys {
		want, ok := wantRules[key.ConstraintName]
		if !ok {
			t.Errorf("unexpected rule-matrix constraint %#v", key)
			continue
		}
		if key.OnDelete != want[0] || key.OnUpdate != want[1] {
			t.Errorf(
				"constraint %q rules = (%s, %s), want (%s, %s)",
				key.ConstraintName,
				key.OnDelete,
				key.OnUpdate,
				want[0],
				want[1],
			)
		}
	}

	mixedParent := "FK.Parent-Mixed"
	mixedChild := "Child.Mixed-Case"
	mixedConstraint := "FK-Mixed.dot"
	testsupport.ExecSQL(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, mixedParent)+
			" (id INT NOT NULL PRIMARY KEY) ENGINE=InnoDB",
	)
	testsupport.ExecSQL(
		t,
		db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, mixedChild)+
			" (id INT NOT NULL PRIMARY KEY, parent_id INT NOT NULL, CONSTRAINT "+
			sqlutil.QuoteIdentifier(mixedConstraint)+" FOREIGN KEY (parent_id) REFERENCES "+
			sqlutil.QuoteQualified(schema, mixedParent)+" (id)) ENGINE=InnoDB",
	)
	mixed, err := inspector.ForeignKeys(t.Context(), validations.IncomingTo(mixedParent))
	if err != nil {
		t.Fatalf("ForeignKeys mixed identifiers: %v", err)
	}
	wantMixed := validations.ForeignKey{
		ConstraintName: mixedConstraint,
		ChildSchema:    schema,
		ChildTable:     mixedChild,
		ChildColumns:   []string{"parent_id"},
		ParentSchema:   schema,
		ParentTable:    mixedParent,
		ParentColumns:  []string{"id"},
		OnDelete:       "NO ACTION",
		OnUpdate:       "NO ACTION",
		Indexed:        true,
	}
	if len(mixed.Keys) != 1 || !reflect.DeepEqual(mixed.Keys[0], wantMixed) {
		t.Errorf("mixed identifier key = %#v, want %#v", mixed.Keys, wantMixed)
	}

	myisam, err := inspector.ForeignKeys(
		t.Context(),
		validations.OutgoingFrom("myisam_ignored_fk"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys MyISAM: %v", err)
	}
	if len(myisam.Keys) != 0 || myisam.Visibility != validations.VisibilityComplete {
		t.Errorf("MyISAM ignored FK result = %#v, want empty complete", myisam)
	}
}

func TestForeignKeyVisibilityAccountsIntegration(t *testing.T) {
	admin, schema := validationDatabase(t)
	processAccount := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_process")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT PROCESS ON *.* TO "+testsupport.GrantAccountSQL(processAccount),
	)

	// The account must hold exactly PROCESS. Asserting the absence at all three
	// ordinary scopes is what makes this test evidence for the minimum foreign-key
	// metadata grant rather than an accident of the container's admin account.
	for _, scope := range []struct {
		name  string
		table string
	}{
		{name: "global", table: "USER_PRIVILEGES"},
		{name: "schema", table: "SCHEMA_PRIVILEGES"},
		{name: "table", table: "TABLE_PRIVILEGES"},
	} {
		var selects int
		if err := admin.QueryRowContext(
			t.Context(),
			`SELECT COUNT(*)
			 FROM information_schema.`+scope.table+`
			 WHERE GRANTEE = ? AND PRIVILEGE_TYPE = 'SELECT'`,
			"'"+processAccount.User+"'@'%'",
		).Scan(&selects); err != nil {
			t.Fatalf("count PROCESS-account %s SELECT grants: %v", scope.name, err)
		}
		if selects != 0 {
			t.Fatalf(
				"PROCESS-only account has %d %s SELECT rows, want 0",
				selects, scope.name,
			)
		}
	}

	processDB := testsupport.MySQLDBAs(t, processAccount)
	completeFromDB, err := validations.NewInspector(processDB, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("PROCESS-only ForeignKeys through DB: %v", err)
	}
	if completeFromDB.Visibility != validations.VisibilityComplete ||
		len(completeFromDB.Keys) != 3 {
		t.Errorf("PROCESS-only DB result = %#v, want three complete keys", completeFromDB)
	}

	processConn := testsupport.MySQLConnAs(t, processAccount)
	completeFromConn, err := validations.NewInspector(processConn, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("clean_table"),
	)
	if err != nil {
		t.Fatalf("PROCESS-only ForeignKeys through Conn: %v", err)
	}
	if completeFromConn.Visibility != validations.VisibilityComplete ||
		completeFromConn.Keys != nil {
		t.Errorf("PROCESS-only empty result = %#v, want empty complete", completeFromConn)
	}
	if got := validations.CheckFKClosure(
		completeFromConn,
		schema,
		[]string{"clean_table"},
	); got != nil {
		t.Errorf("complete empty closure = %#v, want nil", got)
	}

	restrictedAccount := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_fallback")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON "+sqlutil.QuoteQualified(schema, "fk_internal_child")+
			" TO "+testsupport.GrantAccountSQL(restrictedAccount),
	)
	restrictedConn := testsupport.MySQLConnAs(t, restrictedAccount)
	fallback, err := validations.NewInspector(restrictedConn, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("no-PROCESS ForeignKeys: %v", err)
	}
	if fallback.Visibility != validations.VisibilityUnconfirmed {
		t.Errorf("fallback visibility = %s, want unconfirmed", fallback.Visibility)
	}
	if len(fallback.Keys) >= len(completeFromDB.Keys) {
		t.Errorf(
			"fallback saw %d keys, PROCESS source saw %d; want a strict under-count",
			len(fallback.Keys),
			len(completeFromDB.Keys),
		)
	}

	fullFallbackAccount := testsupport.CreateMySQLAccount(
		t,
		admin,
		"dbsgomysql_full_fallback",
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON "+sqlutil.QuoteIdentifier(schema)+".* TO "+
			testsupport.GrantAccountSQL(fullFallbackAccount),
	)
	fullFallbackConn := testsupport.MySQLConnAs(t, fullFallbackAccount)
	fullFallback, err := validations.NewInspector(fullFallbackConn, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("full-visible fallback ForeignKeys: %v", err)
	}
	if fullFallback.Visibility != validations.VisibilityUnconfirmed {
		t.Errorf("full fallback visibility = %s, want unconfirmed", fullFallback.Visibility)
	}
	if !reflect.DeepEqual(fullFallback.Keys, completeFromDB.Keys) {
		t.Errorf(
			"fallback keys =\n%#v\nwant authoritative identity/rules/columns\n%#v",
			fullFallback.Keys,
			completeFromDB.Keys,
		)
	}
	completeRules, err := validations.NewInspector(admin, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_rule_parent"),
	)
	if err != nil {
		t.Fatalf("authoritative rule-matrix ForeignKeys: %v", err)
	}
	fallbackRules, err := validations.NewInspector(fullFallbackConn, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_rule_parent"),
	)
	if err != nil {
		t.Fatalf("fallback rule-matrix ForeignKeys: %v", err)
	}
	if !reflect.DeepEqual(fallbackRules.Keys, completeRules.Keys) {
		t.Errorf(
			"fallback rule-matrix keys =\n%#v\nwant authoritative keys\n%#v",
			fallbackRules.Keys,
			completeRules.Keys,
		)
	}

	if got := validations.CheckFKClosure(
		fallback,
		schema,
		[]string{"fk_parent", "fk_internal_child"},
	); len(got) == 0 {
		t.Error("unconfirmed fallback closure returned nil")
	}
}

func TestGranteeAndRolePrivilegesIntegration(t *testing.T) {
	admin, schema := validationDatabase(t)
	quotedAccount := testsupport.CreateNamedMySQLAccount(
		t,
		admin,
		"o'brien_"+schema[len(schema)-8:],
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON *.* TO "+testsupport.GrantAccountSQL(quotedAccount),
	)
	wantGrantee := "'" + quotedAccount.User + "'@'%'"
	var grantee string
	if err := admin.QueryRowContext(
		t.Context(),
		`SELECT GRANTEE
		 FROM information_schema.USER_PRIVILEGES
		 WHERE PRIVILEGE_TYPE = 'SELECT' AND GRANTEE = ?`,
		wantGrantee,
	).Scan(&grantee); err != nil {
		t.Fatalf("read embedded-quote GRANTEE: %v", err)
	}
	if grantee != wantGrantee {
		t.Errorf("GRANTEE = %q, want unescaped literal %q", grantee, wantGrantee)
	}

	account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_roles")
	outer := testsupport.CreateMySQLRole(t, admin, "dbsgomysql_outer")
	inner := testsupport.CreateMySQLRole(t, admin, "dbsgomysql_inner")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT DELETE ON "+sqlutil.QuoteIdentifier(schema)+".*"+
			" TO "+testsupport.GrantAccountSQL(outer),
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON "+sqlutil.QuoteIdentifier(schema)+".*"+
			" TO "+testsupport.GrantAccountSQL(inner),
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT "+testsupport.GrantAccountSQL(inner)+
			" TO "+testsupport.GrantAccountSQL(outer),
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT "+testsupport.GrantAccountSQL(outer)+
			" TO "+testsupport.GrantAccountSQL(account),
	)
	conn := testsupport.MySQLConnAs(t, account)
	if _, err := conn.ExecContext(t.Context(), "SET ROLE ALL"); err != nil {
		t.Fatalf("enable roles: %v", err)
	}
	grants, err := validations.NewInspector(conn, schema).Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants with enabled role: %v", err)
	}
	if _, execErr := conn.ExecContext(
		t.Context(),
		"DELETE FROM "+sqlutil.QuoteQualified(schema, "fk_parent")+" WHERE 1 = 0",
	); execErr != nil {
		t.Fatalf("execute direct enabled-role DELETE: %v", execErr)
	}
	var visibleRows int
	if queryErr := conn.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM "+sqlutil.QuoteQualified(schema, "fk_parent"),
	).Scan(&visibleRows); queryErr != nil {
		t.Fatalf("execute transitive enabled-role SELECT: %v", queryErr)
	}
	if got := grants.Schema(schema, validations.PrivilegeDelete); got != validations.GrantUnconfirmed {
		t.Errorf("direct enabled-role DELETE = %s, want unconfirmed", got)
	}
	if got := grants.Schema(schema, validations.PrivilegeSelect); got != validations.GrantUnconfirmed {
		t.Errorf("nested-role SELECT = %s, want unconfirmed", got)
	}

	roleFree := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_rolefree")
	roleFreeConn := testsupport.MySQLConnAs(t, roleFree)
	roleFreeGrants, err := validations.NewInspector(roleFreeConn, schema).Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants with zero enabled roles: %v", err)
	}
	if got := roleFreeGrants.Table(
		schema,
		"fk_parent",
		validations.PrivilegeDelete,
	); got != validations.GrantAbsent {
		t.Errorf("pinned role-free negative = %s, want absent", got)
	}
}

func TestPartialRevokesPrivilegeResolutionIntegration(t *testing.T) {
	admin, schema := validationDatabase(t)

	var original int
	if err := admin.QueryRowContext(
		t.Context(),
		"SELECT @@global.partial_revokes",
	).Scan(&original); err != nil {
		t.Fatalf("read original partial_revokes: %v", err)
	}
	if _, err := admin.ExecContext(
		t.Context(),
		"SET GLOBAL partial_revokes = ON",
	); err != nil {
		t.Fatalf("enable partial_revokes: %v", err)
	}
	t.Cleanup(func() {
		value := "OFF"
		if original != 0 {
			value = "ON"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(
			ctx,
			"SET GLOBAL partial_revokes = "+value,
		); err != nil {
			t.Errorf("restore partial_revokes=%s: %v", value, err)
		}
	})

	account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_partial")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON *.* TO "+testsupport.GrantAccountSQL(account),
	)
	testsupport.ExecSQL(
		t,
		admin,
		"REVOKE SELECT ON "+sqlutil.QuoteIdentifier(schema)+".* FROM "+
			testsupport.GrantAccountSQL(account),
	)
	conn := testsupport.MySQLConnAs(t, account)
	grants, err := validations.NewInspector(conn, schema).Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants under partial revokes: %v", err)
	}
	if got := grants.Schema(
		schema,
		validations.PrivilegeSelect,
	); got != validations.GrantUnconfirmed {
		t.Errorf("partially revoked schema SELECT = %s, want unconfirmed", got)
	}
	if got := grants.Table(
		schema,
		"fk_parent",
		validations.PrivilegeSelect,
	); got != validations.GrantUnconfirmed {
		t.Errorf("partially revoked table SELECT = %s, want unconfirmed", got)
	}
	if got := grants.Global(
		validations.PrivilegeSelect,
	); got != validations.GrantUnconfirmed {
		t.Errorf("global SELECT under partial revokes = %s, want unconfirmed", got)
	}
	// DELETE was never granted at any scope, so partial revokes leave nothing
	// for a restriction to have removed and the negative stays provable.
	if got := grants.Table(
		schema,
		"fk_parent",
		validations.PrivilegeDelete,
	); got != validations.GrantAbsent {
		t.Errorf("never-granted DELETE under partial revokes = %s, want absent", got)
	}
}

func assertLeftmostCompositeIndex(
	t *testing.T,
	db *sql.DB,
	schema string,
) {
	t.Helper()

	const query = `
		SELECT INDEX_NAME, COLUMN_NAME
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ?
		  AND TABLE_NAME = 'fk_composite_child'
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`
	rows, err := db.QueryContext(t.Context(), query, schema)
	if err != nil {
		t.Fatalf("query composite indexes: %v", err)
	}
	defer rows.Close()

	byIndex := make(map[string][]string)
	for rows.Next() {
		var index, column string
		if err := rows.Scan(&index, &column); err != nil {
			t.Fatalf("scan composite index: %v", err)
		}
		byIndex[index] = append(byIndex[index], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate composite indexes: %v", err)
	}
	found := false
	for name, columns := range byIndex {
		if name == "idx_nonleading" {
			continue
		}
		if len(columns) >= 2 &&
			columns[0] == "tenant_id" &&
			columns[1] == "parent_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("indexes = %#v, want an auto-created leftmost (tenant_id,parent_id) index", byIndex)
	}
}

func TestUnknownAndInvisibleSchemaIntegration(t *testing.T) {
	db, visibleSchema := validationDatabase(t)
	requested := []string{"clean_table"}

	unknown := factsForSchema(t, validations.NewInspector(db, visibleSchema+"_absent"), requested)

	user := "dbsgomysql_" + visibleSchema[len(visibleSchema)-12:]
	password := "dbsgomysql_test_password"
	testsupport.ExecSQL(
		t,
		db,
		"CREATE USER '"+user+"'@'%' IDENTIFIED BY '"+password+"'",
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "DROP USER IF EXISTS '"+user+"'@'%'"); err != nil {
			t.Errorf("drop least-privilege user: %v", err)
		}
	})

	cfg, err := mysql.ParseDSN(os.Getenv("DBSGOMYSQL_TEST_DSN"))
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	cfg.User = user
	cfg.Passwd = password
	cfg.DBName = ""
	restricted, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open least-privilege database: %v", err)
	}
	t.Cleanup(func() {
		if err := restricted.Close(); err != nil {
			t.Errorf("close least-privilege database: %v", err)
		}
	})
	if err := restricted.PingContext(t.Context()); err != nil {
		t.Fatalf("ping least-privilege database: %v", err)
	}

	invisible := factsForSchema(t, validations.NewInspector(restricted, visibleSchema), requested)
	if !reflect.DeepEqual(invisible, unknown) {
		t.Errorf("invisible schema facts = %#v, unknown schema facts = %#v", invisible, unknown)
	}

	findings := validations.CheckTablesExist(requested, unknown.tables)
	if len(findings) != 1 || findings[0].Check != validations.IDTablesExist {
		t.Errorf("CheckTablesExist(unknown schema) = %#v, want one TABLES_EXIST finding", findings)
	}
}

type schemaFacts struct {
	tables    []validations.TableInfo
	pks       []validations.PKInfo
	invisible []validations.InvisibleColumns
	triggers  []validations.TriggerInfo
}

func factsForSchema(
	t *testing.T,
	inspector *validations.Inspector,
	requested []string,
) schemaFacts {
	t.Helper()

	tables, err := inspector.Tables(t.Context(), requested)
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	pks, err := inspector.PrimaryKeys(t.Context(), requested)
	if err != nil {
		t.Fatalf("PrimaryKeys: %v", err)
	}
	invisible, err := inspector.InvisibleColumns(t.Context(), requested)
	if err != nil {
		t.Fatalf("InvisibleColumns: %v", err)
	}
	triggers, err := inspector.Triggers(t.Context(), requested, validations.TriggerDelete)
	if err != nil {
		t.Fatalf("Triggers: %v", err)
	}

	return schemaFacts{tables: tables, pks: pks, invisible: invisible, triggers: triggers}
}

func TestTableSpecCompatPinsIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_spec_compat")

	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+schema+`.compat_pins (
			widths        TINYINT(1),
			plain_tiny    TINYINT,
			big           BIGINT(20) UNSIGNED,
			exact         DECIMAL(3,2),
			created       DATE DEFAULT (CURRENT_DATE),
			literal_text  VARCHAR(20) DEFAULT 'Active',
			checked       INT,
			gpa           DECIMAL(3,2),
			CONSTRAINT pk_declared_name PRIMARY KEY (plain_tiny),
			CONSTRAINT uq_declared_name UNIQUE (literal_text),
			CONSTRAINT chk_declared_name CHECK (checked >= 16),
			CONSTRAINT chk_declared_range CHECK (gpa BETWEEN 0.00 AND 4.00)
		) ENGINE=InnoDB`)

	inspector := validations.NewInspector(db, schema)
	spec, err := inspector.TableSpec(t.Context(),
		validations.Ref(schema, "compat_pins"),
		validations.WithIndexes(), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}

	byName := make(map[string]validations.ColumnSpec, len(spec.Columns))
	for _, column := range spec.Columns {
		byName[column.Name] = column
	}

	t.Run("compat 1: tinyint(1) survives normalization", func(t *testing.T) {
		if got := byName["widths"].NormalizedType; got != "tinyint(1)" {
			t.Errorf("NormalizedType for TINYINT(1) = %q, want \"tinyint(1)\"; "+
				"BOOLEAN is an alias for it and the width carries that meaning", got)
		}
		if got := byName["plain_tiny"].NormalizedType; got != "tinyint" {
			t.Errorf("NormalizedType for TINYINT = %q, want \"tinyint\"", got)
		}
		if byName["widths"].NormalizedType == byName["plain_tiny"].NormalizedType {
			t.Error("TINYINT(1) and TINYINT normalized to the same type; a BOOLEAN " +
				"and a plain TINYINT would compare equal")
		}
	})

	t.Run("compat 1: bigint width stripped, unsigned kept", func(t *testing.T) {
		if got := byName["big"].NormalizedType; got != "bigint unsigned" {
			t.Errorf("NormalizedType = %q, want \"bigint unsigned\"", got)
		}
	})

	t.Run("compat 1: decimal precision untouched", func(t *testing.T) {
		if got := byName["exact"].NormalizedType; got != "decimal(3,2)" {
			t.Errorf("NormalizedType = %q, want \"decimal(3,2)\"; precision and scale "+
				"are semantic, not display width", got)
		}
	})

	t.Run("compat 13: declared primary key name is discarded", func(t *testing.T) {
		var names []string
		for _, index := range spec.Indexes {
			names = append(names, index.Name)
		}
		found := false
		for _, name := range names {
			if name == "PRIMARY" {
				found = true
			}
			if name == "pk_declared_name" {
				t.Error("the declared primary-key name survived; MySQL stores it as " +
					"PRIMARY and this package must not expect otherwise")
			}
		}
		if !found {
			t.Errorf("indexes = %v, want one named PRIMARY", names)
		}
	})

	t.Run("compat 13: other constraint names survive", func(t *testing.T) {
		var constraintNames []string
		for _, constraint := range spec.Constraints {
			constraintNames = append(constraintNames, constraint.Name)
		}
		if !slices.Contains(constraintNames, "chk_declared_name") {
			t.Errorf("constraints = %v, want chk_declared_name; MySQL discards only "+
				"the primary-key name", constraintNames)
		}
	})

	t.Run("compat 14: expression default is rewritten and marked", func(t *testing.T) {
		created := byName["created"]
		if !created.DefaultIsExpression {
			t.Error("DEFAULT (CURRENT_DATE) was not marked as an expression; without " +
				"DEFAULT_GENERATED it is indistinguishable from a literal of the same text")
		}
		if created.Default == nil {
			t.Fatal("expression default was not captured")
		}
		if *created.Default != "curdate()" {
			t.Errorf("Default = %q, want \"curdate()\"", *created.Default)
		}

		literal := byName["literal_text"]
		if literal.DefaultIsExpression {
			t.Error("a literal default was marked as an expression")
		}
		if literal.Default == nil || *literal.Default != "Active" {
			t.Errorf("literal Default = %v, want \"Active\"; COLUMN_DEFAULT holds the "+
				"value, not its SQL literal form", literal.Default)
		}
	})

	t.Run("compat 15: check clause is server-normalized", func(t *testing.T) {
		var clause string
		for _, constraint := range spec.Constraints {
			if constraint.Name == "chk_declared_name" {
				clause = constraint.CheckClause
			}
		}
		if clause != "(`checked` >= 16)" {
			t.Errorf("CHECK_CLAUSE = %q, want \"(`checked` >= 16)\"", clause)
		}
	})

	t.Run("compat 15: BETWEEN is lowercased", func(t *testing.T) {
		var clause string
		for _, constraint := range spec.Constraints {
			if constraint.Name == "chk_declared_range" {
				clause = constraint.CheckClause
			}
		}
		if clause != "(`gpa` between 0.00 and 4.00)" {
			t.Errorf("CHECK_CLAUSE = %q, want \"(`gpa` between 0.00 and 4.00)\"; the "+
				"server rewrites keyword case and this package compares the rewritten "+
				"form verbatim", clause)
		}
	})
}

func TestTableSpecCompatEnforcementIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_spec_enforced")

	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+schema+`.enforced_t (
			a INT,
			CONSTRAINT chk_on  CHECK (a > 0),
			CONSTRAINT chk_off CHECK (a > 0) NOT ENFORCED
		) ENGINE=InnoDB`)

	inspector := validations.NewInspector(db, schema)
	spec, err := inspector.TableSpec(t.Context(),
		validations.Ref(schema, "enforced_t"), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}

	byName := make(map[string]validations.ConstraintSpec, len(spec.Constraints))
	for _, constraint := range spec.Constraints {
		byName[constraint.Name] = constraint
	}
	if !byName["chk_on"].Enforced {
		t.Error("chk_on reported unenforced despite being declared without NOT ENFORCED")
	}
	if byName["chk_off"].Enforced {
		t.Error("chk_off reported enforced; the server records ENFORCED='NO' and " +
			"never evaluates the constraint")
	}
	if byName["chk_on"].CheckClause != byName["chk_off"].CheckClause {
		t.Errorf("clauses differ: chk_on=%q chk_off=%q; the fixture declares "+
			"identical clauses so enforcement is the only semantic difference",
			byName["chk_on"].CheckClause, byName["chk_off"].CheckClause)
	}
}

func TestTableSpecRejectsAViewIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_spec_view")

	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+schema+`.base_t (id INT PRIMARY KEY, amount INT) ENGINE=InnoDB`)
	testsupport.ExecSQL(t, db, `
		CREATE VIEW `+schema+`.v_positive AS
			SELECT id, amount FROM `+schema+`.base_t WHERE amount > 0`)

	inspector := validations.NewInspector(db, schema)
	_, err := inspector.TableSpec(t.Context(), validations.Ref(schema, "v_positive"))
	if !errors.Is(err, validations.ErrUnsupportedTableType) {
		t.Errorf("TableSpec for a view returned %v, want ErrUnsupportedTableType; "+
			"information_schema exposes a view's columns but not its defining query, "+
			"so a view spec would compare equal to any other view over the same "+
			"columns", err)
	}
}

func TestTableSpecRejectsCaseVariantIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_spec_case")

	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+schema+`.CaseTable (id INT PRIMARY KEY) ENGINE=InnoDB`)

	inspector := validations.NewInspector(db, schema)
	if _, err := inspector.TableSpec(
		t.Context(), validations.Ref(schema, "CaseTable")); err != nil {
		t.Fatalf("TableSpec for the exact name: %v", err)
	}
	if _, err := inspector.TableSpec(
		t.Context(), validations.Ref(schema, "casetable")); !errors.Is(
		err, validations.ErrTableNotFound) {
		t.Errorf("TableSpec for \"casetable\" returned %v, want ErrTableNotFound; "+
			"name matching is case-exact", err)
	}
}

func TestForeignKeyCreatesSupportingIndexIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_spec_fkindex")

	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+schema+`.parent (id INT PRIMARY KEY) ENGINE=InnoDB`)
	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+schema+`.child (
			id INT PRIMARY KEY,
			parent_id INT,
			CONSTRAINT fk_child_parent FOREIGN KEY (parent_id)
				REFERENCES `+schema+`.parent(id) ON DELETE SET NULL
		) ENGINE=InnoDB`)

	inspector := validations.NewInspector(db, schema)
	spec, err := inspector.TableSpec(t.Context(), validations.Ref(schema, "child"),
		validations.WithIndexes(), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec: %v", err)
	}

	var indexNames []string
	for _, index := range spec.Indexes {
		indexNames = append(indexNames, index.Name)
	}
	if !slices.Contains(indexNames, "fk_child_parent") {
		t.Errorf("indexes = %v, want one named fk_child_parent; MySQL creates a "+
			"supporting index for a foreign key and names it after the constraint",
			indexNames)
	}

	var rule string
	for _, constraint := range spec.Constraints {
		if constraint.Name == "fk_child_parent" {
			rule = constraint.DeleteRule
		}
	}
	if rule != "SET NULL" {
		t.Errorf("DeleteRule = %q, want \"SET NULL\"; referential rules are a real "+
			"schema difference and must reach DiffSpecs", rule)
	}
}

func validationDatabase(t *testing.T) (db *sql.DB, schema string) {
	t.Helper()

	db, schema = testsupport.MySQLDatabase(t, "dbsgomysql_validations")
	testsupport.LoadSQLFixture(t, db, schema, fixturePath)

	return db, schema
}

func smokeTables() []string {
	return []string{
		"clean_table",
		"composite_pk",
		"no_pk",
		"pk_varchar",
		"pk_case_mismatch",
		"expected_mismatch",
		"invisible_columns",
		"myisam_table",
		"delete_trigger",
		"missing_table",
	}
}

func smokeExpectedPKs() map[string]string {
	return map[string]string{
		"clean_table":       "id",
		"composite_pk":      "key_first",
		"no_pk":             "id",
		"pk_varchar":        "id",
		"pk_case_mismatch":  "log_id",
		"expected_mismatch": "configured_id",
		"invisible_columns": "id",
		"myisam_table":      "id",
		"delete_trigger":    "id",
	}
}

func findingIDs(findings []validations.Finding) []string {
	ids := make([]string, 0, len(findings))
	for _, finding := range findings {
		ids = append(ids, finding.Check)
	}

	return ids
}
