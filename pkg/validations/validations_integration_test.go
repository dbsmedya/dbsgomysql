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

const (
	fixturePath                    = "../../tests/fixtures/phase1b.sql"
	constraintCollisionFixturePath = "../../tests/fixtures/constraint_collisions.sql"
)

func TestInspectorSmoke(t *testing.T) {
	db, schema := validationDatabase(t)
	inspector := validations.NewInspector(db, schema)
	tables := smokeTables()

	tableFacts, err := inspector.Tables(t.Context(), tables)
	if err != nil {
		t.Fatalf("Tables smoke call: %v", err)
	}
	columnFacts, err := inspector.Columns(t.Context(), tables)
	if err != nil {
		t.Fatalf("Columns smoke call: %v", err)
	}
	if len(columnFacts) == 0 {
		t.Error("Columns returned no facts for the smoke tables")
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
	assertNoForeignKeyDowngradeIntegration(t, incoming)
	within, err := inspector.ForeignKeys(
		t.Context(),
		validations.Within("fk_parent", "fk_internal_child", "fk_cascade_child"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys Within smoke call: %v", err)
	}
	assertNoForeignKeyDowngradeIntegration(t, within)
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
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "T1")+" (UPPER_ID INT PRIMARY KEY)",
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

	columns, err := validations.NewInspector(db, schema).Columns(
		t.Context(),
		[]string{"T1", "t1"},
	)
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	wantColumns := []validations.TableColumns{
		{
			Table: "T1",
			Columns: []validations.ColumnInfo{{
				Name: "UPPER_ID", Ordinal: 1, DataType: "int",
			}},
		},
		{
			Table: "t1",
			Columns: []validations.ColumnInfo{{
				Name: "id", Ordinal: 1, DataType: "int",
			}},
		},
	}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Errorf("Columns() = %#v, want %#v", columns, wantColumns)
	}
}

func TestColumnsIntegration(t *testing.T) {
	db, schema := validationDatabase(t)
	testsupport.ExecSQL(t, db, `
		CREATE TABLE `+sqlutil.QuoteQualified(schema, "column_signedness")+` (
			signed_tiny       TINYINT,
			unsigned_tiny     TINYINT UNSIGNED,
			signed_small      SMALLINT,
			unsigned_small    SMALLINT UNSIGNED,
			signed_medium     MEDIUMINT,
			unsigned_medium   MEDIUMINT UNSIGNED,
			signed_int        INT,
			unsigned_int      INT UNSIGNED,
			signed_integer    INTEGER,
			unsigned_integer  INTEGER UNSIGNED,
			signed_big        BIGINT,
			unsigned_big      BIGINT UNSIGNED
		)`)

	got, err := validations.NewInspector(db, schema).Columns(
		t.Context(),
		[]string{
			"report_view",
			"missing",
			"column_signedness",
			"invisible_columns",
			"clean_table",
		},
	)
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	want := []validations.TableColumns{
		{
			Table: "report_view",
			Columns: []validations.ColumnInfo{{
				Name: "id", Ordinal: 1, DataType: "int",
			}},
		},
		{
			Table: "column_signedness",
			Columns: []validations.ColumnInfo{
				{Name: "signed_tiny", Ordinal: 1, DataType: "tinyint"},
				{Name: "unsigned_tiny", Ordinal: 2, DataType: "tinyint", Unsigned: true},
				{Name: "signed_small", Ordinal: 3, DataType: "smallint"},
				{Name: "unsigned_small", Ordinal: 4, DataType: "smallint", Unsigned: true},
				{Name: "signed_medium", Ordinal: 5, DataType: "mediumint"},
				{
					Name: "unsigned_medium", Ordinal: 6, DataType: "mediumint",
					Unsigned: true,
				},
				{Name: "signed_int", Ordinal: 7, DataType: "int"},
				{Name: "unsigned_int", Ordinal: 8, DataType: "int", Unsigned: true},
				{Name: "signed_integer", Ordinal: 9, DataType: "int"},
				{Name: "unsigned_integer", Ordinal: 10, DataType: "int", Unsigned: true},
				{Name: "signed_big", Ordinal: 11, DataType: "bigint"},
				{Name: "unsigned_big", Ordinal: 12, DataType: "bigint", Unsigned: true},
			},
		},
		{
			Table: "invisible_columns",
			Columns: []validations.ColumnInfo{
				{Name: "id", Ordinal: 1, DataType: "int"},
				{Name: "plain_secret", Ordinal: 2, DataType: "int", Invisible: true},
				{
					Name: "generated_secret", Ordinal: 3, DataType: "int",
					Invisible: true, Generated: true,
				},
				{
					Name: "visible_generated", Ordinal: 4, DataType: "int",
					Generated: true,
				},
			},
		},
		{
			Table: "clean_table",
			Columns: []validations.ColumnInfo{{
				Name: "id", Ordinal: 1, DataType: "int",
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Columns() =\n%#v\nwant\n%#v", got, want)
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
	assertInnoDBForeignColsPositionBase(t, db, schema)

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
	assertNoForeignKeyDowngradeIntegration(t, completeFromDB)

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
	assertNoForeignKeyDowngradeIntegration(t, completeFromConn)
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
	assertPrimaryQueryDowngradeIntegration(t, fallback)
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
	assertPrimaryQueryDowngradeIntegration(t, fullFallback)
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

	t.Run("compat 18: a functional index does not break the fallback", func(t *testing.T) {
		// The fallback reads information_schema.STATISTICS for every child
		// table, and a functional key part reports COLUMN_NAME as NULL. This
		// account holds SELECT on the whole schema and no PROCESS, which is the
		// shape of a real inspection account, so before the fix one functional
		// index made ForeignKeys fail outright here.
		functional, err := validations.NewInspector(fullFallbackConn, schema).ForeignKeys(
			t.Context(),
			validations.OutgoingFrom("fk_functional_child"),
		)
		if err != nil {
			t.Fatalf("fallback over a child table with a functional index: %v", err)
		}
		if functional.Visibility != validations.VisibilityUnconfirmed {
			t.Errorf("visibility = %s, want unconfirmed", functional.Visibility)
		}
		want := validations.ForeignKey{
			ConstraintName: "fk_functional_parent",
			ChildSchema:    schema,
			ChildTable:     "fk_functional_child",
			ChildColumns:   []string{"parent_id"},
			ParentSchema:   schema,
			ParentTable:    "fk_functional_parent",
			ParentColumns:  []string{"id"},
			OnDelete:       "NO ACTION",
			OnUpdate:       "NO ACTION",
			// True through the supporting index MySQL created for the
			// constraint (docs/COMPAT.md entry 16), never through the
			// functional one.
			Indexed: true,
		}
		if len(functional.Keys) != 1 || !reflect.DeepEqual(functional.Keys[0], want) {
			t.Errorf("functional-index keys = %#v, want %#v", functional.Keys, want)
		}
		assertFunctionalIndexReportsNullColumn(t, admin, schema)
	})
}

func assertNoForeignKeyDowngradeIntegration(
	t *testing.T,
	result validations.ForeignKeyResult,
) {
	t.Helper()

	if result.DowngradeReason != validations.ForeignKeyDowngradeNone {
		t.Errorf("downgrade reason = %s, want none", result.DowngradeReason)
	}
	if result.PrimaryError != nil {
		t.Errorf("primary error = %v, want nil", result.PrimaryError)
	}
}

func assertPrimaryQueryDowngradeIntegration(
	t *testing.T,
	result validations.ForeignKeyResult,
) {
	t.Helper()

	if result.DowngradeReason != validations.ForeignKeyDowngradePrimaryQueryError {
		t.Errorf(
			"downgrade reason = %s, want primary_query_error",
			result.DowngradeReason,
		)
	}
	if result.PrimaryError == nil {
		t.Fatal("primary error = nil, want wrapped MySQL query error")
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(result.PrimaryError, &mysqlErr) {
		t.Errorf("errors.As(%v, *mysql.MySQLError) = false", result.PrimaryError)
	}
}

// assertFunctionalIndexReportsNullColumn pins the server behavior the fix
// accommodates rather than only the library's response to it: STATISTICS
// reports COLUMN_NAME NULL and EXPRESSION non-NULL for a functional key part.
// Without this, a server that stopped emitting NULL would leave the fallback
// test passing for the wrong reason.
func assertFunctionalIndexReportsNullColumn(t *testing.T, db *sql.DB, schema string) {
	t.Helper()

	const query = `
		SELECT COLUMN_NAME, EXPRESSION
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ?
		  AND TABLE_NAME = 'fk_functional_child'
		  AND INDEX_NAME = 'idx_functional'`
	var (
		column     sql.NullString
		expression sql.NullString
	)
	if err := db.QueryRowContext(t.Context(), query, schema).Scan(&column, &expression); err != nil {
		t.Fatalf("read functional index part: %v", err)
	}
	if column.Valid {
		t.Errorf("COLUMN_NAME = %q, want NULL for a functional key part", column.String)
	}
	if !expression.Valid || expression.String == "" {
		t.Errorf("EXPRESSION = %#v, want the key part's expression", expression)
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

	// A global privilege row proves the privilege at every scope only while
	// partial revokes are disabled, so the positives below assert that
	// precondition rather than assume the server default. See docs/COMPAT.md
	// entry 11 for the enabled case, pinned by
	// TestPartialRevokesPrivilegeResolutionIntegration.
	var partialRevokes int
	if revokesErr := admin.QueryRowContext(
		t.Context(),
		"SELECT @@global.partial_revokes",
	).Scan(&partialRevokes); revokesErr != nil {
		t.Fatalf("read partial_revokes: %v", revokesErr)
	}
	if partialRevokes != 0 {
		t.Fatalf(
			"partial_revokes is enabled instance-wide; the global-backed " +
				"positives below require it disabled",
		)
	}

	roleFree := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_rolefree")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON *.* TO "+testsupport.GrantAccountSQL(roleFree),
	)
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

	// The ordinary positive: a pinned, role-free account whose global row is not
	// degraded resolves present at every scope. This is the only server-backed
	// coverage of the branch that reads the global source, because the partial
	// revokes test short-circuits before reaching it, and a directly granted
	// schema row is resolved before it. Every other server-backed grant
	// assertion in this repository expects a non-present state, so without this
	// a positive-path regression degrades silently into an expected value.
	for _, scope := range []struct {
		name string
		got  validations.GrantState
	}{
		{name: "global", got: roleFreeGrants.Global(validations.PrivilegeSelect)},
		{name: "schema", got: roleFreeGrants.Schema(schema, validations.PrivilegeSelect)},
		{
			name: "table",
			got:  roleFreeGrants.Table(schema, "fk_parent", validations.PrivilegeSelect),
		},
	} {
		if scope.got != validations.GrantPresent {
			t.Errorf("global-backed %s SELECT = %s, want present", scope.name, scope.got)
		}
	}
}

// TestRoleHeldProcessCompletesFKVisibilityIntegration is the deliberate
// counterpart to TestGranteeAndRolePrivilegesIntegration, and the two must be
// read together.
//
// Both accounts hold a privilege only through an activated role, and they
// resolve oppositely: Grants reports role-held DELETE as GrantUnconfirmed,
// while ForeignKeys reports role-held PROCESS as VisibilityComplete. That looks
// like an inconsistency and is not one. Evidence, not privilege type, is what
// differs — Grants depends on privilege bookkeeping the account cannot read for
// its own roles (docs/COMPAT.md entry 4), whereas PROCESS proves itself by the
// PROCESS-gated metadata read having succeeded. One successful primary
// statement is the proof, so it needs no grant row and no session-affinity
// reasoning.
//
// Without this test, tightening ForeignKeys to require a directly granted
// PROCESS breaks nothing in this repository: both existing GRANT PROCESS
// statements are direct. Harmonizing the two rows is exactly what a careful
// maintainer does when only one half is pinned.
func TestRoleHeldProcessCompletesFKVisibilityIntegration(t *testing.T) {
	admin, schema := validationDatabase(t)

	account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_roleprocess")
	role := testsupport.CreateMySQLRole(t, admin, "dbsgomysql_processrole")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT PROCESS ON *.* TO "+testsupport.GrantAccountSQL(role),
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT "+testsupport.GrantAccountSQL(role)+
			" TO "+testsupport.GrantAccountSQL(account),
	)

	// The account must hold no PROCESS of its own, or the role proves nothing.
	var direct int
	if err := admin.QueryRowContext(
		t.Context(),
		`SELECT COUNT(*)
		 FROM information_schema.USER_PRIVILEGES
		 WHERE GRANTEE = ? AND PRIVILEGE_TYPE = 'PROCESS'`,
		"'"+account.User+"'@'%'",
	).Scan(&direct); err != nil {
		t.Fatalf("count direct PROCESS rows: %v", err)
	}
	if direct != 0 {
		t.Fatalf("account holds %d direct PROCESS rows, want 0", direct)
	}

	// Negative control. Granting a role does not activate it, so this session
	// reaches the visibility-filtered fallback. Asserting it establishes that
	// the completeness below comes from the activated role and not from
	// something ambient about the account or the server.
	inactiveConn := testsupport.MySQLConnAs(t, account)
	var currentRole string
	if err := inactiveConn.QueryRowContext(
		t.Context(),
		"SELECT CURRENT_ROLE()",
	).Scan(&currentRole); err != nil {
		t.Fatalf("read CURRENT_ROLE before SET ROLE: %v", err)
	}
	if currentRole != "NONE" {
		t.Fatalf(
			"CURRENT_ROLE() = %q before SET ROLE, want NONE; roles activate on "+
				"login here, so the negative control proves nothing",
			currentRole,
		)
	}
	inactive, err := validations.NewInspector(inactiveConn, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys with the role inactive: %v", err)
	}
	if inactive.Visibility != validations.VisibilityUnconfirmed {
		t.Errorf(
			"inactive-role visibility = %s, want unconfirmed",
			inactive.Visibility,
		)
	}

	// The pin: the same account, same grants, with the role activated.
	activeConn := testsupport.MySQLConnAs(t, account)
	if _, roleErr := activeConn.ExecContext(t.Context(), "SET ROLE ALL"); roleErr != nil {
		t.Fatalf("activate role: %v", roleErr)
	}
	active, err := validations.NewInspector(activeConn, schema).ForeignKeys(
		t.Context(),
		validations.IncomingTo("fk_parent"),
	)
	if err != nil {
		t.Fatalf("ForeignKeys with the role activated: %v", err)
	}
	if active.Visibility != validations.VisibilityComplete {
		t.Errorf(
			"role-held PROCESS visibility = %s, want complete",
			active.Visibility,
		)
	}
	if len(active.Keys) != 3 {
		t.Errorf(
			"role-held PROCESS saw %d incoming keys, want the same 3 a directly "+
				"granted PROCESS sees",
			len(active.Keys),
		)
	}
	if len(inactive.Keys) >= len(active.Keys) {
		t.Errorf(
			"inactive-role fallback saw %d keys and the activated role saw %d; "+
				"want a strict under-count proving the role changed the source",
			len(inactive.Keys),
			len(active.Keys),
		)
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

	assertDirectGrantSurvivesPartialRevokes(t, admin, schema)
}

// assertDirectGrantSurvivesPartialRevokes pins the half of docs/COMPAT.md entry
// 11 that its own named test does not reach: partial revokes degrade every
// answer backed by a global privilege row, and *only* those. Entry 11 states
// that schema and table answers are unconfirmed "until a direct schema or table
// grant proves the requested object" — the clause after "until" has no live
// coverage. Every other server-backed grant assertion in this repository expects
// a non-present state, so nothing currently fails if the positive path stops
// resolving.
//
// The mechanism is resolution order, not a partial-revoke exemption: Grants.Schema
// and Grants.Table pass their direct rows as the specific sources, and resolve
// returns GrantPresent from those before it ever consults the global row that
// partial revokes degrade. A restriction can only subtract from a grant that
// exists, so a directly granted object is not something it can have removed.
//
// One account carries both halves, so the assertions cannot pass for unrelated
// reasons: a global SELECT row that must degrade at every scope, and a direct
// schema INSERT row that must still prove its object while the same global
// switch is on.
//
// The caller owns the SET GLOBAL partial_revokes mutation and its restore, and
// this runs inside that window deliberately — it must not become a separate test
// that races the global variable.
func assertDirectGrantSurvivesPartialRevokes(t *testing.T, admin *sql.DB, schema string) {
	t.Helper()

	account := testsupport.CreateMySQLAccount(t, admin, "dbsgomysql_direct")
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT SELECT ON *.* TO "+testsupport.GrantAccountSQL(account),
	)
	testsupport.ExecSQL(
		t,
		admin,
		"GRANT INSERT ON "+sqlutil.QuoteIdentifier(schema)+".* TO "+
			testsupport.GrantAccountSQL(account),
	)

	conn := testsupport.MySQLConnAs(t, account)
	grants, err := validations.NewInspector(conn, schema).Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants for direct-grant account under partial revokes: %v", err)
	}

	// The global row is degraded at every scope, entry 11's first half.
	if got := grants.Global(validations.PrivilegeSelect); got != validations.GrantUnconfirmed {
		t.Errorf("global-backed SELECT = %s, want unconfirmed", got)
	}
	if got := grants.Schema(
		schema,
		validations.PrivilegeSelect,
	); got != validations.GrantUnconfirmed {
		t.Errorf("global-backed schema SELECT = %s, want unconfirmed", got)
	}

	// The direct schema row proves its object anyway, entry 11's "until" clause.
	if got := grants.Schema(
		schema,
		validations.PrivilegeInsert,
	); got != validations.GrantPresent {
		t.Errorf("direct schema INSERT under partial revokes = %s, want present", got)
	}

	// A direct schema row is a specific source for the table question too, so it
	// proves the table without a table-scoped grant row existing.
	if got := grants.Table(
		schema,
		"fk_parent",
		validations.PrivilegeInsert,
	); got != validations.GrantPresent {
		t.Errorf("direct schema INSERT for table under partial revokes = %s, want present", got)
	}
}

// assertInnoDBForeignColsPositionBase pins that INNODB_FOREIGN_COLS.POS counts
// from 1, which is what scanInnoDBForeignKeys requires and the opposite of what
// every supported manual documents. See docs/COMPAT.md entry 19.
//
// It reads POS directly rather than inferring the base from a VisibilityComplete
// assertion several steps downstream. A 0-based server would make the primary
// source error, and ForeignKeys routes any primary-source error to the fallback,
// so the only symptom elsewhere is a silent demotion to VisibilityUnconfirmed.
//
// The constraint is composite on purpose: two rows pin the base, the increment,
// and the ordering in one read.
func assertInnoDBForeignColsPositionBase(
	t *testing.T,
	db *sql.DB,
	schema string,
) {
	t.Helper()

	// Querying this table needs PROCESS, which the admin connection holds.
	const query = `
		SELECT POS
		FROM information_schema.INNODB_FOREIGN_COLS
		WHERE ID = ?
		ORDER BY POS`
	rows, err := db.QueryContext(t.Context(), query, schema+"/fk_composite_parent")
	if err != nil {
		t.Fatalf("query InnoDB foreign-key column positions: %v", err)
	}
	defer rows.Close()

	var positions []uint64
	for rows.Next() {
		var position uint64
		if err := rows.Scan(&position); err != nil {
			t.Fatalf("scan foreign-key column position: %v", err)
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign-key column positions: %v", err)
	}

	// 1-based, though §28.4.13 and Example 17.3 both say 0. The server wins.
	want := []uint64{1, 2}
	if !reflect.DeepEqual(positions, want) {
		t.Errorf(
			"INNODB_FOREIGN_COLS.POS for a two-column key = %v, want %v",
			positions,
			want,
		)
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
			zf_narrow     INT(5) ZEROFILL,
			zf_wide       INT(10) ZEROFILL,
			exact         DECIMAL(3,2),
			created       DATE DEFAULT (CURRENT_DATE),
			literal_text  VARCHAR(20) DEFAULT 'Active',
			checked       INT,
			gpa           DECIMAL(3,2),
			CONSTRAINT pk_declared_name PRIMARY KEY (plain_tiny),
			CONSTRAINT uq_declared_name UNIQUE (literal_text),
			CONSTRAINT chk_declared_name CHECK (checked >= 16),
			CONSTRAINT chk_declared_range CHECK (gpa BETWEEN 0.00 AND 4.00),
			KEY idx_parts (literal_text(10), checked DESC, ((checked * 2)))
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

	t.Run("compat 1: zerofill keeps its width", func(t *testing.T) {
		narrow := byName["zf_narrow"].NormalizedType
		wide := byName["zf_wide"].NormalizedType
		if narrow != "int(5) unsigned zerofill" {
			t.Errorf("NormalizedType for INT(5) ZEROFILL = %q, want "+
				"\"int(5) unsigned zerofill\"; retrieved values are zero-padded to the "+
				"display width, so the width is semantic here", narrow)
		}
		if wide != "int(10) unsigned zerofill" {
			t.Errorf("NormalizedType for INT(10) ZEROFILL = %q, want "+
				"\"int(10) unsigned zerofill\"", wide)
		}

		// The reported failure mode: the same column captured from two servers at
		// different zerofill widths must not compare equal.
		specA := validations.TableSpec{Columns: []validations.ColumnSpec{byName["zf_narrow"]}}
		widened := byName["zf_narrow"]
		widened.Type = byName["zf_wide"].Type
		widened.NormalizedType = wide
		specB := validations.TableSpec{Columns: []validations.ColumnSpec{widened}}

		diffs := validations.DiffSpecs(specA, specB)
		if len(diffs) != 1 || diffs[0].Kind != validations.ColumnTypeMismatch {
			t.Errorf("diffing int(5) zerofill against int(10) zerofill produced %+v, "+
				"want one ColumnTypeMismatch; 00042 and 0000000042 are different "+
				"client-visible schemas", diffs)
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

	// The expression rendering pinned on part 2 below — "(`checked` * 2)" — was
	// confirmed identical on 8.0.46, 8.4.9 and 9.7.1, one run per server,
	// 2026-08-07. It is not transcribed from a probe.
	//
	// Parts 0/1's empty Expression and part 2's empty Column are documented
	// behavior, not an assumption: the manual states that for a nonfunctional
	// key part COLUMN_NAME names the column and EXPRESSION is NULL, and for a
	// functional key part the reverse.
	//
	// The 2026-07-27 probe queried information_schema.STATISTICS directly and
	// recorded COLLATION='A' for a functional key part. This test does not read
	// the raw column — so that observation is context, not something proven
	// here.
	//
	// IndexPart deliberately records only whether the value is 'D'. The raw
	// 'A'-versus-NULL distinction is not exposed on the public type and is not
	// load-bearing for DiffSpecs.
	//
	// These assertions therefore prove "not descending" — not 'A' versus NULL.
	// A server changing 'A' to NULL would keep this test green.
	t.Run("idx_parts: literal, DESC, and functional key-part shapes", func(t *testing.T) {
		var idxParts *validations.IndexSpec
		for i := range spec.Indexes {
			if spec.Indexes[i].Name == "idx_parts" {
				idxParts = &spec.Indexes[i]
			}
		}
		if idxParts == nil {
			t.Fatal("no index named idx_parts; want KEY idx_parts " +
				"(literal_text(10), checked DESC, ((checked * 2)))")
		}
		if len(idxParts.Parts) != 3 {
			t.Fatalf("idx_parts has %d parts, want 3: literal_text(10), checked DESC, "+
				"((checked * 2))", len(idxParts.Parts))
		}

		part0 := idxParts.Parts[0]
		if part0.Column != "literal_text" {
			t.Errorf("part 0 Column = %q, want \"literal_text\"", part0.Column)
		}
		if part0.SubPart != 10 {
			t.Errorf("part 0 SubPart = %d, want 10; literal_text(10) is a prefix index "+
				"and must not compare equal to INDEX(literal_text)", part0.SubPart)
		}
		if part0.Descending {
			t.Error("part 0 reported descending; literal_text(10) carries no DESC and " +
				"must not compare as descending")
		}
		if part0.Expression != "" {
			t.Errorf("part 0 Expression = %q, want empty; a nonfunctional part must not "+
				"carry EXPRESSION text", part0.Expression)
		}

		part1 := idxParts.Parts[1]
		if part1.Column != "checked" {
			t.Errorf("part 1 Column = %q, want \"checked\"", part1.Column)
		}
		if part1.SubPart != 0 {
			t.Errorf("part 1 SubPart = %d, want 0; checked DESC indexes the whole "+
				"column, no prefix", part1.SubPart)
		}
		if !part1.Descending {
			t.Error("part 1 did not report descending; checked DESC must record " +
				"COLLATION='D', a real schema difference DiffSpecs relies on")
		}
		if part1.Expression != "" {
			t.Errorf("part 1 Expression = %q, want empty; a nonfunctional part must not "+
				"carry EXPRESSION text", part1.Expression)
		}

		part2 := idxParts.Parts[2]
		if part2.Column != "" {
			t.Errorf("part 2 Column = %q, want empty; COLUMN_NAME is NULL for a "+
				"functional key part", part2.Column)
		}
		if part2.SubPart != 0 {
			t.Errorf("part 2 SubPart = %d, want 0; the manual forbids a prefix length "+
				"on a functional key part, so a nonzero value here would mean the "+
				"server did something the manual rules out", part2.SubPart)
		}
		if part2.Descending {
			t.Error("part 2 reported descending; the capture must treat a functional " +
				"key part as not descending, or DiffSpecs would start distinguishing " +
				"indexes that are identical — this asserts that boolean, not the raw " +
				"COLLATION value the server reports")
		}
		if part2.Expression != "(`checked` * 2)" {
			t.Errorf("part 2 Expression = %q, want \"(`checked` * 2)\"; confirmed "+
				"identical on 8.0.46, 8.4.9 and 9.7.1 — without it two different "+
				"functional indexes compare equal", part2.Expression)
		}
		t.Logf("part 2 Expression (server rendering of ((checked * 2))) = %q",
			part2.Expression)
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

func TestTableSpecConstraintNameCollisionsIntegration(t *testing.T) {
	db, schema := constraintCollisionDatabase(t)
	inspector := validations.NewInspector(db, schema)

	users, err := inspector.TableSpec(
		t.Context(), validations.Ref(schema, "cc_users"), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec(cc_users): %v", err)
	}
	if len(users.Constraints) != 0 {
		t.Errorf("cc_users constraints = %#v, want none", users.Constraints)
	}

	contacts, err := inspector.TableSpec(
		t.Context(), validations.Ref(schema, "cc_contacts"), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec(cc_contacts): %v", err)
	}
	if len(contacts.Constraints) != 1 {
		t.Fatalf("cc_contacts constraints = %#v, want one CHECK", contacts.Constraints)
	}
	contactCheck := contacts.Constraints[0]
	if contactCheck.Name != "email" || contactCheck.Kind != validations.ConstraintCheck ||
		!contactCheck.Enforced || contactCheck.CheckClause == "" {
		t.Errorf("cc_contacts CHECK = %#v, want enforced email CHECK with its clause", contactCheck)
	}

	child, err := inspector.TableSpec(
		t.Context(), validations.Ref(schema, "cc_child"), validations.WithConstraints())
	if err != nil {
		t.Fatalf("TableSpec(cc_child): %v", err)
	}
	wantOrder := []struct {
		name string
		kind validations.ConstraintKind
	}{
		{name: "fk_ab", kind: validations.ConstraintForeignKey},
		{name: "k", kind: validations.ConstraintCheck},
		{name: "k", kind: validations.ConstraintForeignKey},
	}
	if len(child.Constraints) != len(wantOrder) {
		t.Fatalf("cc_child constraints = %#v, want three constraints", child.Constraints)
	}
	for index, want := range wantOrder {
		got := child.Constraints[index]
		if got.Name != want.name || got.Kind != want.kind {
			t.Errorf("cc_child constraint %d = %s/%s, want %s/%s",
				index, got.Name, got.Kind, want.name, want.kind)
		}
	}

	fkAB := child.Constraints[0]
	if !slices.Equal(fkAB.Columns, []string{"a", "b"}) ||
		!slices.Equal(fkAB.RefColumns, []string{"a", "b"}) ||
		fkAB.RefSchema != schema || fkAB.RefTable != "cc_parent" {
		t.Errorf("fk_ab = %#v, want composite reference to cc_parent(a,b)", fkAB)
	}
	kCheck := child.Constraints[1]
	if !kCheck.Enforced || kCheck.CheckClause == "" {
		t.Errorf("k CHECK = %#v, want enforced CHECK with its clause", kCheck)
	}
	kFK := child.Constraints[2]
	if !slices.Equal(kFK.Columns, []string{"pid"}) ||
		!slices.Equal(kFK.RefColumns, []string{"id"}) ||
		kFK.RefSchema != schema || kFK.RefTable != "cc_parent" {
		t.Errorf("k FOREIGN KEY = %#v, want one-part reference to cc_parent(id)", kFK)
	}
}

func TestForeignKeyNamesAreCaseInsensitiveIntegration(t *testing.T) {
	db, schema := testsupport.MySQLDatabase(t, "dbsgomysql_fk_name_case")
	parent := sqlutil.QuoteQualified(schema, "parent")
	testsupport.ExecSQL(t, db, "CREATE TABLE "+parent+" (id INT PRIMARY KEY)")

	_, err := db.ExecContext(t.Context(),
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "same_table")+" ("+
			"id INT PRIMARY KEY, parent_a INT, parent_b INT, "+
			"CONSTRAINT Fk1 FOREIGN KEY (parent_a) REFERENCES "+parent+" (id), "+
			"CONSTRAINT fk1 FOREIGN KEY (parent_b) REFERENCES "+parent+" (id))")
	assertMySQLErrorNumber(t, err, 1061, "case-variant foreign keys on one table")

	testsupport.ExecSQL(t, db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "first_child")+" ("+
			"id INT PRIMARY KEY, parent_id INT, "+
			"CONSTRAINT Fk1 FOREIGN KEY (parent_id) REFERENCES "+parent+" (id))")
	_, err = db.ExecContext(t.Context(),
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "second_child")+" ("+
			"id INT PRIMARY KEY, parent_id INT, "+
			"CONSTRAINT fk1 FOREIGN KEY (parent_id) REFERENCES "+parent+" (id))")
	assertMySQLErrorNumber(t, err, 1826, "case-variant foreign keys across tables")

	testsupport.ExecSQL(t, db,
		"CREATE TABLE "+sqlutil.QuoteQualified(schema, "distinct_names")+" ("+
			"id INT PRIMARY KEY, parent_a INT, parent_b INT, "+
			"CONSTRAINT fk_distinct_1 FOREIGN KEY (parent_a) REFERENCES "+parent+" (id), "+
			"CONSTRAINT fk_distinct_2 FOREIGN KEY (parent_b) REFERENCES "+parent+" (id))")
}

func assertMySQLErrorNumber(t *testing.T, err error, want uint16, operation string) {
	t.Helper()

	if err == nil {
		t.Errorf("%s succeeded, want MySQL error %d", operation, want)

		return
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		t.Errorf("%s returned %v, want MySQL error %d", operation, err, want)

		return
	}
	if mysqlErr.Number != want {
		t.Errorf("%s returned MySQL error %d, want %d", operation, mysqlErr.Number, want)
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

func constraintCollisionDatabase(t *testing.T) (db *sql.DB, schema string) {
	t.Helper()

	db, schema = testsupport.MySQLDatabase(t, "dbsgomysql_constraint_collisions")
	testsupport.LoadSQLFixture(t, db, schema, constraintCollisionFixturePath)

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
