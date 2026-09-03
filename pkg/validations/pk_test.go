package validations

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func TestPKKindZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero PKKind
	if zero != PKUnknown {
		t.Errorf("the PKKind zero value is %d, want PKUnknown (%d); an unpopulated PKInfo must be detectable",
			zero, PKUnknown)
	}
}

func TestPKKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind PKKind
		want string
	}{
		{name: "unknown", kind: PKUnknown, want: "unknown"},
		{name: "none", kind: PKNone, want: "none"},
		{name: "single", kind: PKSingle, want: "single"},
		{name: "composite", kind: PKComposite, want: "composite"},
		{name: "undeclared", kind: PKKind(99), want: "PKKind(99)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.kind.String(); got != test.want {
				t.Errorf("PKKind(%d).String() = %q, want %q", test.kind, got, test.want)
			}
		})
	}
}

func TestPKKindStringsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]PKKind)
	for _, kind := range []PKKind{PKUnknown, PKNone, PKSingle, PKComposite} {
		got := kind.String()
		if other, dup := seen[got]; dup {
			t.Errorf("PKKind(%d) and PKKind(%d) both render as %q", other, kind, got)
		}
		seen[got] = kind
	}
}

func TestPrimaryKeysQueryRestrictsToBaseTables(t *testing.T) {
	t.Parallel()

	manyTables := make([]string, 0, maxPointLookupTables+1)
	for index := range maxPointLookupTables + 1 {
		manyTables = append(manyTables, fmt.Sprintf("t_%d", index))
	}
	cases := []struct {
		name   string
		tables []string
	}{
		{name: "requested-object shape", tables: []string{"orders"}},
		{name: "schema-scan shape above the point-lookup bound", tables: manyTables},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
				Match:   "information_schema.TABLES AS t",
				Columns: []string{"TABLE_NAME", "COLUMN_NAME", "DATA_TYPE", "COLUMN_TYPE"},
				Rows:    [][]driver.Value{{"orders", "id", "int", "int"}},
			})
			defer db.Close()

			if _, err := NewInspector(db, "shop").PrimaryKeys(t.Context(), testCase.tables); err != nil {
				t.Fatalf("PrimaryKeys: %v", err)
			}
			if len(log) != 1 {
				t.Fatalf("issued %d statements, want 1", len(log))
			}
			if !strings.Contains(log[0], "t.TABLE_TYPE = ?") {
				t.Errorf("PrimaryKeys statement does not restrict TABLE_TYPE:\n%s", log[0])
			}
		})
	}
}
