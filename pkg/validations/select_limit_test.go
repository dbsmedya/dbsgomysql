package validations

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// wantLimitSuffix is spelled independently of limitAllRows, so a pin that
// reused the constant could not detect the library misspelling it.
const wantLimitSuffix = "LIMIT 18446744073709551615"

// requestedMarker is the alias requestedObjects gives its derived table; only
// the narrowed object-fact statements carry it.
const requestedMarker = ") AS requested"

// pastPointLookupTables names one table more than requestedObjects narrows,
// so the object facts take their unnarrowed fallback statement.
func pastPointLookupTables() []string {
	tables := make([]string, maxPointLookupTables+1)
	for index := range tables {
		tables[index] = fmt.Sprintf("t%04d", index)
	}

	return tables
}

func grantsLimitScript(currentUser string) []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{
		{
			Match:   "CURRENT_USER()",
			Columns: []string{"CURRENT_USER()"},
			Rows:    [][]driver.Value{{currentUser}},
		},
		{
			Match:   "@@global.partial_revokes",
			Columns: []string{"@@global.partial_revokes"},
			Rows:    [][]driver.Value{{int64(0)}},
		},
	}
}

func standardForeignKeyLimitScript() []testsupport.ScriptedQuery {
	return []testsupport.ScriptedQuery{
		{Match: "INNODB_FOREIGN", Err: errors.New("INNODB_FOREIGN is unreadable")},
		{
			Match: "information_schema.KEY_COLUMN_USAGE AS kcu",
			Columns: []string{
				"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME",
				"REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME",
				"REFERENCED_COLUMN_NAME", "DELETE_RULE", "UPDATE_RULE",
				"ORDINAL_POSITION",
			},
			Rows: [][]driver.Value{{
				"shop", "orders", "fk_customer", "customer_id",
				"shop", "customers", "id", "RESTRICT", "RESTRICT", int64(1),
			}},
		},
	}
}

// TestEverySelectEndsWithMaximumLimit pins that each fact ends every SELECT it
// issues with the maximum LIMIT, in the narrowed form and in the unnarrowed
// fallback, so an inherited sql_select_limit cannot truncate a catalog read
// (docs/COMPAT.md entry 35). wantStatements pins that every statement of the
// scenario was reached, so a scenario that stopped early cannot pass.
// A new fact, or a new statement in an existing one, adds its scenarios here:
// nothing else notices a SELECT issued without the suffix (spec D4).
func TestEverySelectEndsWithMaximumLimit(t *testing.T) {
	t.Parallel()

	many := pastPointLookupTables()
	cases := []struct {
		name           string
		script         []testsupport.ScriptedQuery
		call           func(*Inspector) error
		wantStatements int
		// contains and lacks prove which branch the scenario took: the
		// narrowed form carries its predicate, the fallback does not.
		contains, lacks string
	}{
		{
			name:     "tables narrowed",
			contains: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.Tables(t.Context(), []string{"orders"})
				return err
			},
			wantStatements: 1,
		},
		{
			name:  "tables unnarrowed",
			lacks: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.Tables(t.Context(), many)
				return err
			},
			wantStatements: 1,
		},
		{
			name:     "columns narrowed",
			contains: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.Columns(t.Context(), []string{"orders"})
				return err
			},
			wantStatements: 1,
		},
		{
			name:  "columns unnarrowed",
			lacks: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.Columns(t.Context(), many)
				return err
			},
			wantStatements: 1,
		},
		{
			name:     "invisible columns narrowed",
			contains: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.InvisibleColumns(t.Context(), []string{"orders"})
				return err
			},
			wantStatements: 1,
		},
		{
			name:  "invisible columns unnarrowed",
			lacks: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.InvisibleColumns(t.Context(), many)
				return err
			},
			wantStatements: 1,
		},
		{
			name:     "primary keys narrowed",
			contains: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.PrimaryKeys(t.Context(), []string{"orders"})
				return err
			},
			wantStatements: 1,
		},
		{
			name:  "primary keys unnarrowed",
			lacks: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.PrimaryKeys(t.Context(), many)
				return err
			},
			wantStatements: 1,
		},
		{
			name:     "triggers narrowed",
			contains: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.Triggers(t.Context(), []string{"orders"}, TriggerDelete)
				return err
			},
			wantStatements: 1,
		},
		{
			name:  "triggers unnarrowed",
			lacks: requestedMarker,
			call: func(i *Inspector) error {
				_, err := i.Triggers(t.Context(), many, TriggerDelete)
				return err
			},
			wantStatements: 1,
		},
		{
			name:     "foreign keys innodb narrowed",
			contains: "f.REF_NAME IN (",
			call: func(i *Inspector) error {
				_, err := i.ForeignKeys(t.Context(), IncomingTo("orders"))
				return err
			},
			wantStatements: 1,
		},
		{
			// An invalid-UTF-8 name is not representable, so narrowNames
			// drops the IN list and the InnoDB read is unnarrowed.
			name:  "foreign keys innodb unnarrowed",
			lacks: "f.REF_NAME IN (",
			call: func(i *Inspector) error {
				_, err := i.ForeignKeys(t.Context(), IncomingTo("\xff"))
				return err
			},
			wantStatements: 1,
		},
		{
			// The InnoDB source fails, the standard source answers with one
			// key, and its supporting indexes are read: three statements.
			name:     "foreign keys standard with supporting indexes",
			contains: "kcu.TABLE_NAME IN (",
			script:   standardForeignKeyLimitScript(),
			call: func(i *Inspector) error {
				_, err := i.ForeignKeys(t.Context(), OutgoingFrom("orders"))
				return err
			},
			wantStatements: 3,
		},
		{
			name:   "foreign keys standard unnarrowed",
			lacks:  "kcu.TABLE_NAME IN (",
			script: standardForeignKeyLimitScript()[:1],
			call: func(i *Inspector) error {
				_, err := i.ForeignKeys(t.Context(), OutgoingFrom("\xff"))
				return err
			},
			wantStatements: 2,
		},
		{
			name:   "table spec with every option",
			script: []testsupport.ScriptedQuery{tableRowScript("shop", "items")},
			call: func(i *Inspector) error {
				_, err := i.TableSpec(t.Context(), Ref("shop", "items"),
					WithIndexes(), WithConstraints(), WithComment())
				return err
			},
			wantStatements: 5,
		},
		{
			name:     "grants narrowed",
			contains: "GRANTEE IN (",
			script:   grantsLimitScript("app@%"),
			call: func(i *Inspector) error {
				_, err := i.Grants(t.Context())
				return err
			},
			wantStatements: 7,
		},
		{
			// An unrepresentable account makes granteePredicate fall back to
			// the unnarrowed privilege reads.
			name:   "grants unnarrowed",
			lacks:  "GRANTEE IN (",
			script: grantsLimitScript("\xff@%"),
			call: func(i *Inspector) error {
				_, err := i.Grants(t.Context())
				return err
			},
			wantStatements: 7,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, testCase.script...)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Errorf("close scripted database: %v", err)
				}
			})

			if err := testCase.call(NewInspector(db, "shop")); err != nil {
				t.Fatalf("fact returned %v, want nil", err)
			}
			if len(log) != testCase.wantStatements {
				t.Fatalf("issued %d statements, want %d: %q",
					len(log), testCase.wantStatements, log)
			}
			joined := strings.Join(log, "\n")
			if testCase.contains != "" && !strings.Contains(joined, testCase.contains) {
				t.Errorf("statements lack %q, so the narrowed branch was not taken", testCase.contains)
			}
			if testCase.lacks != "" && strings.Contains(joined, testCase.lacks) {
				t.Errorf("statements carry %q, so the fallback branch was not taken", testCase.lacks)
			}
			for _, statement := range log {
				if !strings.HasSuffix(strings.TrimSpace(statement), wantLimitSuffix) {
					t.Errorf("statement does not end with %q:\n%s", wantLimitSuffix, statement)
				}
			}
		})
	}
}
