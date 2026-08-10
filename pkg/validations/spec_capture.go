package validations

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

const metadataYes = "YES"

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
	if request.sections.Has(SectionIndexes) {
		spec.Indexes, err = i.captureIndexes(ctx, ref)
		if err != nil {
			return TableSpec{}, err
		}
	}
	if request.sections.Has(SectionConstraints) {
		spec.Constraints, err = i.captureConstraints(ctx, ref)
		if err != nil {
			return TableSpec{}, err
		}
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
	// A schema or table rejected by representable cannot be the exact spelling of
	// anything information_schema reports, so the resolved-and-absent result is
	// known without asking. A supplementary character would make either fixed
	// comparison fail with ER_IMPOSSIBLE_STRING_CONVERSION (3988); invalid UTF-8
	// already matches nothing. See representable and docs/COMPAT.md entry 8.
	// Skipping the query preserves ErrTableNotFound for both cases and repairs the
	// supplementary one.
	//
	// Both parts are checked because this call compares both in Go below and a
	// supplementary character in either was measured raising 3988.
	if !representable(ref.schema) || !representable(ref.table) {
		return TableSpec{}, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("resolve table: %w", ErrTableNotFound))
	}

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
		column.Nullable = nullable == metadataYes
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

// captureIndexes reads every index, including the primary key and any unique
// keys. In MySQL a UNIQUE constraint is a unique index — the same object — so
// it is reported here and not again under constraints.
//
// An index is a list of key parts, not a list of column names. SUB_PART carries
// a prefix length, COLLATION carries the part's direction, and EXPRESSION
// carries a functional part. Dropping any of them would make INDEX(name) and
// INDEX(name(10)) — or an ascending and a descending index — compare equal.
//
// MySQL creates a supporting index for a foreign key and names it after the
// constraint; see docs/COMPAT.md entry 16. Such an index is real and is
// reported like any other.
func (i *Inspector) captureIndexes(ctx context.Context, ref TableRef) ([]IndexSpec, error) {
	const query = `
		SELECT TABLE_SCHEMA, TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX,
		       COLUMN_NAME, SUB_PART, COLLATION, EXPRESSION,
		       NON_UNIQUE, INDEX_TYPE, IS_VISIBLE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`

	rows, err := i.q.QueryContext(ctx, query, ref.schema, ref.table)
	if err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("query index metadata: %w", err))
	}
	defer rows.Close()

	var indexes []IndexSpec
	for rows.Next() {
		var (
			rowSchema  string
			rowTable   string
			name       string
			seq        int
			column     sql.NullString
			subPart    sql.NullInt64
			collation  sql.NullString
			expression sql.NullString
			nonUnique  int
			indexType  string
			visible    string
		)
		if err := rows.Scan(
			&rowSchema, &rowTable, &name, &seq,
			&column, &subPart, &collation, &expression,
			&nonUnique, &indexType, &visible,
		); err != nil {
			return nil, newObjectError(opTableSpec, ref.schema, ref.table,
				fmt.Errorf("scan index metadata: %w", err))
		}
		if !matchesResolved(ref, rowSchema, rowTable) {
			continue
		}

		if len(indexes) == 0 || indexes[len(indexes)-1].Name != name {
			indexes = append(indexes, IndexSpec{
				Name:    name,
				Unique:  nonUnique == 0,
				Type:    indexType,
				Visible: visible == metadataYes,
			})
		}

		part := IndexPart{
			Column:     column.String,
			Expression: expression.String,
			// COLLATION is 'A' ascending, 'D' descending, or NULL for an
			// unsorted part. Only 'D' is descending.
			Descending: collation.String == "D",
		}
		if subPart.Valid {
			part.SubPart = int(subPart.Int64)
		}

		last := &indexes[len(indexes)-1]
		last.Parts = append(last.Parts, part)
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("iterate index metadata: %w", err))
	}

	return indexes, nil
}

// captureConstraints reads CHECK and FOREIGN KEY constraints. Primary and
// unique keys are indexes and are captured by captureIndexes instead, so no
// object is reported twice and no difference is reported twice.
//
// Results are ordered by name so DiffSpecs output is deterministic.
func (i *Inspector) captureConstraints(
	ctx context.Context, ref TableRef,
) ([]ConstraintSpec, error) {
	checks, err := i.captureCheckConstraints(ctx, ref)
	if err != nil {
		return nil, err
	}
	foreignKeys, err := i.captureForeignKeyConstraints(ctx, ref)
	if err != nil {
		return nil, err
	}

	constraints := make([]ConstraintSpec, 0, len(checks)+len(foreignKeys))
	constraints = append(constraints, checks...)
	constraints = append(constraints, foreignKeys...)
	sort.Slice(constraints, func(a, b int) bool {
		return constraints[a].Name < constraints[b].Name
	})

	return constraints, nil
}

func (i *Inspector) captureCheckConstraints(
	ctx context.Context, ref TableRef,
) ([]ConstraintSpec, error) {
	// ENFORCED lives on TABLE_CONSTRAINTS, which this query already joins.
	const query = `
		SELECT tc.TABLE_SCHEMA, tc.TABLE_NAME, cc.CONSTRAINT_NAME,
		       cc.CHECK_CLAUSE, tc.ENFORCED
		FROM information_schema.CHECK_CONSTRAINTS AS cc
		JOIN information_schema.TABLE_CONSTRAINTS AS tc
		  ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA
		 AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
		WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ?
		ORDER BY cc.CONSTRAINT_NAME`

	rows, err := i.q.QueryContext(ctx, query, ref.schema, ref.table)
	if err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("query check-constraint metadata: %w", err))
	}
	defer rows.Close()

	var constraints []ConstraintSpec
	for rows.Next() {
		var rowSchema, rowTable, enforced string
		constraint := ConstraintSpec{Kind: ConstraintCheck}
		if err := rows.Scan(
			&rowSchema, &rowTable, &constraint.Name, &constraint.CheckClause,
			&enforced,
		); err != nil {
			return nil, newObjectError(opTableSpec, ref.schema, ref.table,
				fmt.Errorf("scan check-constraint metadata: %w", err))
		}
		if !matchesResolved(ref, rowSchema, rowTable) {
			continue
		}
		constraint.Enforced = enforced == metadataYes
		constraints = append(constraints, constraint)
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("iterate check-constraint metadata: %w", err))
	}

	return constraints, nil
}

func (i *Inspector) captureForeignKeyConstraints(
	ctx context.Context, ref TableRef,
) ([]ConstraintSpec, error) {
	const query = `
		SELECT kcu.TABLE_SCHEMA, kcu.TABLE_NAME,
		       rc.CONSTRAINT_NAME, rc.UPDATE_RULE, rc.DELETE_RULE,
		       kcu.COLUMN_NAME, kcu.REFERENCED_TABLE_SCHEMA,
		       kcu.REFERENCED_TABLE_NAME, kcu.REFERENCED_COLUMN_NAME
		FROM information_schema.REFERENTIAL_CONSTRAINTS AS rc
		JOIN information_schema.KEY_COLUMN_USAGE AS kcu
		  ON kcu.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA
		 AND kcu.CONSTRAINT_NAME = rc.CONSTRAINT_NAME
		 AND kcu.TABLE_NAME = rc.TABLE_NAME
		WHERE rc.CONSTRAINT_SCHEMA = ? AND rc.TABLE_NAME = ?
		  AND kcu.TABLE_SCHEMA = ? AND kcu.TABLE_NAME = ?
		ORDER BY rc.CONSTRAINT_NAME, kcu.ORDINAL_POSITION`

	rows, err := i.q.QueryContext(
		ctx,
		query,
		ref.schema,
		ref.table,
		ref.schema,
		ref.table,
	)
	if err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("query foreign-key metadata: %w", err))
	}
	defer rows.Close()

	var constraints []ConstraintSpec
	for rows.Next() {
		var (
			rowSchema  string
			rowTable   string
			name       string
			updateRule string
			deleteRule string
			column     sql.NullString
			refSchema  sql.NullString
			refTable   sql.NullString
			refColumn  sql.NullString
		)
		if err := rows.Scan(
			&rowSchema, &rowTable, &name, &updateRule, &deleteRule,
			&column, &refSchema, &refTable, &refColumn,
		); err != nil {
			return nil, newObjectError(opTableSpec, ref.schema, ref.table,
				fmt.Errorf("scan foreign-key metadata: %w", err))
		}
		if !matchesResolved(ref, rowSchema, rowTable) {
			continue
		}

		if len(constraints) == 0 || constraints[len(constraints)-1].Name != name {
			constraints = append(constraints, ConstraintSpec{
				Name:       name,
				Kind:       ConstraintForeignKey,
				RefSchema:  refSchema.String,
				RefTable:   refTable.String,
				UpdateRule: updateRule,
				DeleteRule: deleteRule,
				// A foreign key is always enforced; MySQL rejects
				// NOT ENFORCED on one. Setting it true keeps the field
				// meaningful for every kind rather than defaulting a
				// foreign key to "not enforced".
				Enforced: true,
			})
		}
		last := &constraints[len(constraints)-1]
		last.Columns = append(last.Columns, column.String)
		last.RefColumns = append(last.RefColumns, refColumn.String)
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(opTableSpec, ref.schema, ref.table,
			fmt.Errorf("iterate foreign-key metadata: %w", err))
	}

	return constraints, nil
}
