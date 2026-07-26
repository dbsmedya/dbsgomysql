package validations

import (
	"context"
	"fmt"
)

// InvisibleColumns reports one table having at least one invisible column.
// Columns are in ordinal order and retain the server's exact spelling.
//
// InvisibleColumns is safe for concurrent reads. Callers must synchronize
// mutations to Columns.
type InvisibleColumns struct {
	// Table is the table's exact server-side spelling.
	Table string `json:"table"`
	// Columns contains one or more invisible column names in ordinal order.
	Columns []string `json:"columns"`
}

// InvisibleColumns returns one fact per requested table having invisible
// columns. Table order follows the request and column order follows
// ORDINAL_POSITION. Tables without invisible columns and missing or invisible
// tables are absent.
//
// InvisibleColumns is safe for concurrent use when the Inspector's Querier is
// safe for concurrent use and tables is not mutated concurrently.
func (i *Inspector) InvisibleColumns(
	ctx context.Context,
	tables []string,
) ([]InvisibleColumns, error) {
	if err := i.validate(opInvisibleColumns, tables); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}

	const query = `
		SELECT TABLE_NAME, COLUMN_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ?
		  AND EXTRA LIKE '%INVISIBLE%'
		ORDER BY TABLE_NAME, ORDINAL_POSITION`

	rows, err := i.q.QueryContext(ctx, query, i.schema)
	if err != nil {
		return nil, newObjectError(
			opInvisibleColumns,
			i.schema,
			"",
			fmt.Errorf("query metadata: %w", err),
		)
	}
	defer rows.Close()

	byTable := make(map[string][]string)
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, newObjectError(
				opInvisibleColumns,
				i.schema,
				"",
				fmt.Errorf("scan metadata: %w", err),
			)
		}
		byTable[table] = append(byTable[table], column)
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(
			opInvisibleColumns,
			i.schema,
			"",
			fmt.Errorf("iterate metadata: %w", err),
		)
	}

	found := make([]InvisibleColumns, 0, len(byTable))
	for _, table := range tables {
		if columns := byTable[table]; len(columns) > 0 {
			found = append(found, InvisibleColumns{Table: table, Columns: columns})
		}
	}

	return found, nil
}

// CheckInvisibleColumns reports each table having invisible columns.
//
// SELECT * omits invisible columns, so their values can disappear from a copy
// and its verification before the source rows are deleted. The check emits one
// finding per table and preserves fact order. CheckInvisibleColumns is safe for
// concurrent use when inv is not mutated concurrently.
func CheckInvisibleColumns(inv []InvisibleColumns) []Finding {
	var findings []Finding
	for _, fact := range inv {
		findings = append(findings, Finding{
			Check: IDInvisibleColumns,
			Message: findingMessage(
				IDInvisibleColumns,
				"table has columns that SELECT * omits",
			),
			Tables: []string{fact.Table},
			Facts:  fact,
		})
	}

	return findings
}
