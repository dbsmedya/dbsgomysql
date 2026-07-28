package validations

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func TestColumnsReturnsEveryColumnInRequestedObjectOrder(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match: "information_schema.COLUMNS",
		Columns: []string{
			"TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION", "DATA_TYPE", "COLUMN_TYPE", "EXTRA",
		},
		Rows: [][]driver.Value{
			{"Alpha", "wrong_case_table", int64(1), "int", "int", ""},
			{"alpha", "id", int64(1), "bigint", "bigint unsigned", "auto_increment"},
			{"alpha", "hidden", int64(2), "varchar", "varchar(20)", "INVISIBLE"},
			{"alpha", "virtual_value", int64(3), "int", "int", "VIRTUAL GENERATED"},
			{
				"alpha", "stored_hidden", int64(4), "int", "int(10) unsigned zerofill",
				"STORED GENERATED INVISIBLE",
			},
			{
				"alpha", "expression_default", int64(5), "datetime", "datetime",
				"DEFAULT_GENERATED",
			},
			{"beta", "code", int64(1), "char", "char(4)", ""},
		},
	})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted database: %v", err)
		}
	})

	got, err := NewInspector(db, "shop").Columns(
		t.Context(),
		[]string{"beta", "missing", "alpha", "alpha"},
	)
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}

	alphaColumns := []ColumnInfo{
		{Name: "id", Ordinal: 1, DataType: "bigint", Unsigned: true},
		{Name: "hidden", Ordinal: 2, DataType: "varchar", Invisible: true},
		{Name: "virtual_value", Ordinal: 3, DataType: "int", Generated: true},
		{
			Name: "stored_hidden", Ordinal: 4, DataType: "int",
			Unsigned: true, Invisible: true, Generated: true,
		},
		{Name: "expression_default", Ordinal: 5, DataType: "datetime"},
	}
	want := []TableColumns{
		{Table: "beta", Columns: []ColumnInfo{{Name: "code", Ordinal: 1, DataType: "char"}}},
		{Table: "alpha", Columns: alphaColumns},
		{Table: "alpha", Columns: alphaColumns},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Columns() =\n%#v\nwant\n%#v", got, want)
	}
}
