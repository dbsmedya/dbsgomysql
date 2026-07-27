package validations

import (
	"context"
	"database/sql"
	"fmt"
)

// TableSpec returns a specification for one base table, complete enough to
// compare it with a table on any other server via DiffSpecs. The ref names the
// table and need not lie in the Inspector's own schema, so one Inspector per
// server can describe a table in any schema that server exposes.
//
// Name matching is case-exact: on success Schema and Table equal what the ref
// requested, because a row whose spelling differs is not the requested table
// and yields ErrTableNotFound. A missing table is an error, not an empty spec —
// the caller asked about a specific object, and a silent empty result would
// read as "no differences" downstream.
//
// Only BASE TABLE objects are supported. A view yields ErrUnsupportedTableType:
// information_schema exposes a view's columns but not its defining query, so a
// view spec would compare equal to any other view over the same columns.
//
// Optional sections are captured only when their option is passed, and
// TableSpec.Captured records which were requested. Unlike the other facts in
// this package, TableSpec issues more than one query per call.
//
// TableSpec is safe for concurrent use when the Inspector's Querier is safe for
// concurrent use.
func (i *Inspector) TableSpec(
	ctx context.Context, ref TableRef, opts ...SpecOption,
) (TableSpec, error) {
	if err := i.validate(opTableSpec, nil); err != nil {
		return TableSpec{}, err
	}
	if !ref.valid() {
		return TableSpec{}, newObjectError(
			opTableSpec, ref.schema, ref.table, ErrInvalidTableRef)
	}

	var request specRequest
	for _, option := range opts {
		if option != nil {
			option(&request)
		}
	}

	spec, err := i.resolveTable(ctx, ref)
	if err != nil {
		return TableSpec{}, err
	}
	spec.Captured = request.sections
	if !request.sections.Has(SectionComment) {
		spec.Comment = ""
	}

	// resolveTable proved spec.Schema and spec.Table equal ref, so ref is the
	// resolved identity and every later query filters on it.
	spec.Columns, err = i.captureColumns(ctx, ref)
	if err != nil {
		return TableSpec{}, err
	}

	return spec, nil
}

// resolveTable finds the single base table whose schema and name equal ref
// exactly, and returns its table-level facts.
//
// The Go comparison is not compensating for a loose predicate. On the supported
// matrix TABLE_NAME is utf8mb3_bin, so the WHERE clause already matches exactly
// — measured on 8.0.46, 8.4.9, and 9.7.1, where `Items` and `items` coexist and
// a query for `items` returns one row. It is here because
// lower_case_table_names is server configuration this package refuses to branch
// on, because docs/COMPAT.md entry 2 exists precisely to record that these
// collations are not uniform, and because comparing names in Go is what this
// package does everywhere else. Inspector.Tables has the same shape.
//
// TABLE_TYPE is checked because information_schema exposes a view's columns but
// not its defining query, so a view would yield a spec that compares equal to
// any other view over the same columns.
//
// The character set comes from a join against information_schema.COLLATIONS
// rather than by slicing the collation name at its first underscore. The prefix
// happens to be the charset today, but deriving one identifier from another by
// string surgery is exactly the fragility this package avoids elsewhere.
func (i *Inspector) resolveTable(ctx context.Context, ref TableRef) (TableSpec, error) {
	const query = `
		SELECT t.TABLE_SCHEMA, t.TABLE_NAME, t.TABLE_TYPE, t.ENGINE,
		       c.CHARACTER_SET_NAME, t.TABLE_COLLATION, t.TABLE_COMMENT
		FROM information_schema.TABLES AS t
		LEFT JOIN information_schema.COLLATIONS AS c
		       ON c.COLLATION_NAME = t.TABLE_COLLATION
		WHERE t.TABLE_SCHEMA = ? AND t.TABLE_NAME = ?`

	rows, err := i.q.QueryContext(ctx, query, ref.schema, ref.table)
	if err != nil {
		return TableSpec{}, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("query table metadata: %w", err))
	}
	defer rows.Close()

	var (
		spec      TableSpec
		found     bool
		foundType string
	)
	for rows.Next() {
		var (
			candidate TableSpec
			tableType string
			engine    sql.NullString
			charset   sql.NullString
			collation sql.NullString
			comment   sql.NullString
		)
		if err := rows.Scan(&candidate.Schema, &candidate.Table, &tableType, &engine,
			&charset, &collation, &comment); err != nil {
			return TableSpec{}, newObjectError(opTableSpec, ref.schema, ref.table,
				fmt.Errorf("scan table metadata: %w", err))
		}
		if candidate.Schema != ref.schema || candidate.Table != ref.table {
			continue
		}

		candidate.Engine = engine.String
		candidate.Charset = charset.String
		candidate.Collation = collation.String
		candidate.Comment = comment.String
		spec, found, foundType = candidate, true, tableType
	}
	if err := rows.Err(); err != nil {
		return TableSpec{}, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("iterate table metadata: %w", err))
	}
	if !found {
		return TableSpec{}, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("resolve table: %w", ErrTableNotFound))
	}
	if foundType != tableTypeBase {
		return TableSpec{}, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("resolve table: %q: %w", foundType, ErrUnsupportedTableType))
	}

	return spec, nil
}

// matchesResolved reports whether a subordinate metadata row belongs to the
// resolved table. Every capture query selects its own TABLE_SCHEMA and
// TABLE_NAME so the package can enforce its case-exact contract in Go. On the
// supported matrix the predicate is already exact for these two columns; this
// comparison is defense in depth against configuration and metadata drift.
func matchesResolved(ref TableRef, rowSchema, rowTable string) bool {
	return rowSchema == ref.schema && rowTable == ref.table
}

func (i *Inspector) captureColumns(ctx context.Context, ref TableRef) ([]ColumnSpec, error) {
	const query = `
		SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION,
		       COLUMN_TYPE, IS_NULLABLE, CHARACTER_SET_NAME, COLLATION_NAME,
		       COLUMN_DEFAULT, EXTRA, GENERATION_EXPRESSION
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`

	rows, err := i.q.QueryContext(ctx, query, ref.schema, ref.table)
	if err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("query column metadata: %w", err))
	}
	defer rows.Close()

	var columns []ColumnSpec
	for rows.Next() {
		var (
			column         ColumnSpec
			rowSchema      string
			rowTable       string
			nullable       string
			charset        sql.NullString
			collation      sql.NullString
			columnDefault  sql.NullString
			extra          sql.NullString
			generationExpr sql.NullString
		)
		if err := rows.Scan(
			&rowSchema, &rowTable,
			&column.Name, &column.Ordinal, &column.Type, &nullable,
			&charset, &collation, &columnDefault, &extra, &generationExpr,
		); err != nil {
			return nil, newObjectError(opTableSpec, ref.schema, ref.table,
				fmt.Errorf("scan column metadata: %w", err))
		}
		if !matchesResolved(ref, rowSchema, rowTable) {
			continue
		}

		column.NormalizedType = normalizeColumnType(column.Type)
		column.Nullable = nullable == "YES"
		column.Charset = charset.String
		column.Collation = collation.String
		if columnDefault.Valid {
			value := columnDefault.String
			column.Default = &value
		}
		column.Extra = extra.String
		column.GenerationExpr = generationExpr.String

		decomposed := parseColumnExtra(column.Extra)
		column.DefaultIsExpression = decomposed.defaultIsExpression
		column.Invisible = decomposed.invisible
		column.Generated = decomposed.generated
		column.AutoIncrement = decomposed.autoIncrement
		column.OnUpdate = decomposed.onUpdate

		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("iterate column metadata: %w", err))
	}

	return columns, nil
}
