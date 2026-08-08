package validations

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// ColumnInfo describes one column of a requested table or view.
//
// ColumnInfo is a plain value and is safe for concurrent use.
type ColumnInfo struct {
	// Name is the column's exact server-side spelling.
	Name string `json:"name"`
	// Ordinal is the column's one-based ORDINAL_POSITION.
	Ordinal int `json:"ordinal"`
	// DataType is information_schema.COLUMNS.DATA_TYPE verbatim.
	DataType string `json:"data_type"`
	// Unsigned reports whether the column has MySQL's UNSIGNED attribute.
	Unsigned bool `json:"unsigned"`
	// Invisible reports whether SELECT * omits the column.
	Invisible bool `json:"invisible"`
	// Generated reports whether the column is virtual or stored generated.
	Generated bool `json:"generated"`
}

// TableColumns describes every column of one requested table or view.
//
// Each returned value owns its Columns slice; see the package documentation on
// ownership of returned slices.
//
// TableColumns is safe for concurrent reads. Callers must synchronize
// mutations to Columns.
type TableColumns struct {
	// Table is the object's exact server-side spelling.
	Table string `json:"table"`
	// Columns contains every column in ordinal order.
	Columns []ColumnInfo `json:"columns"`
}

// Columns returns every column for requested tables and views in the
// Inspector's schema. Results preserve requested object order, including
// duplicates, and column order follows ORDINAL_POSITION. Missing or invisible
// objects are absent.
//
// Names retain exact server spelling and requested objects are matched by exact
// Go string equality. DataType is information_schema.COLUMNS.DATA_TYPE
// verbatim. Unsigned is derived from information_schema.COLUMNS.COLUMN_TYPE.
// Generated does not treat DEFAULT_GENERATED as a generated column.
//
// Columns is safe for concurrent use when the Inspector's Querier is safe for
// concurrent use and tables is not mutated concurrently.
func (i *Inspector) Columns(ctx context.Context, tables []string) ([]TableColumns, error) {
	if err := i.validate(opColumns, tables); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}

	// The predicate narrows; it never decides. Every returned name is compared
	// to the requested one in Go below, so dropping the predicate changes how
	// much the server reads and nothing else. See narrowNames.
	narrowed := `
		SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, DATA_TYPE, COLUMN_TYPE, EXTRA
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ?`
	args := make([]any, 0, len(tables)+1)
	args = append(args, i.schema)
	if params, ok := narrowNames(tables, len(args)); ok {
		narrowed += `
		  AND TABLE_NAME IN (` + sqlPlaceholders(len(params)) + `)`
		for _, table := range params {
			args = append(args, table)
		}
	}
	query := narrowed + `
		ORDER BY TABLE_NAME, ORDINAL_POSITION`

	rows, err := i.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, newObjectError(
			opColumns,
			i.schema,
			"",
			fmt.Errorf("query metadata: %w", err),
		)
	}
	defer rows.Close()

	byTable := make(map[string][]ColumnInfo)
	for rows.Next() {
		var (
			table      string
			column     ColumnInfo
			columnType string
			extra      sql.NullString
		)
		if err := rows.Scan(
			&table,
			&column.Name,
			&column.Ordinal,
			&column.DataType,
			&columnType,
			&extra,
		); err != nil {
			return nil, newObjectError(
				opColumns,
				i.schema,
				"",
				fmt.Errorf("scan metadata: %w", err),
			)
		}

		column.Unsigned = containsUnsigned(columnType)
		decomposed := parseColumnExtra(extra.String)
		column.Invisible = decomposed.invisible
		column.Generated = decomposed.generated == GeneratedVirtual ||
			decomposed.generated == GeneratedStored
		byTable[table] = append(byTable[table], column)
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(
			opColumns,
			i.schema,
			"",
			fmt.Errorf("iterate metadata: %w", err),
		)
	}

	found := make([]TableColumns, 0, len(tables))
	for _, table := range tables {
		columns, ok := byTable[table]
		if !ok {
			continue
		}
		found = append(found, TableColumns{
			Table:   table,
			Columns: slices.Clone(columns),
		})
	}

	return found, nil
}

// InvisibleColumns reports one table having at least one invisible column.
// Columns are in ordinal order and retain the server's exact spelling.
//
// Each returned value owns its Columns slice; see the package documentation on
// ownership of returned slices.
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
			found = append(found, InvisibleColumns{
				Table:   table,
				Columns: slices.Clone(columns),
			})
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
