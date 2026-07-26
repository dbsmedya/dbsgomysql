//go:build integration

package validations_test

import (
	"context"
	"database/sql"
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

	got := findingIDs(findings)
	slices.Sort(got)
	want := []string{
		validations.IDInvisibleColumns,
		validations.IDPKExists,
		validations.IDPKIntegerType,
		validations.IDPKMatchesExpected,
		validations.IDPKNameCase,
		validations.IDPKSingleColumn,
		validations.IDStorageEngine,
		validations.IDTablesExist,
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
