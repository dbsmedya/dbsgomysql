package validations

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

const (
	dataTypeBigint  = "bigint"
	dataTypeInt     = "int"
	dataTypeTinyint = "tinyint"
)

// PKInfo describes one table's primary key.
//
// Each returned value owns its Columns slice; see the package documentation on
// ownership of returned slices.
//
// PKInfo is safe for concurrent reads. Callers must synchronize mutations to
// Columns.
type PKInfo struct {
	// Table is the table's exact server-side spelling.
	Table string `json:"table"`
	// Kind classifies the primary key as absent, single-column, or composite.
	Kind PKKind `json:"kind"`
	// Columns contains exact column names in primary-key order. It is nil when
	// Kind is PKNone.
	Columns []string `json:"columns"`
	// DataType is information_schema.COLUMNS.DATA_TYPE for a single-column key.
	DataType string `json:"data_type"`
	// IsInteger reports whether a single-column key is an integer type.
	IsInteger bool `json:"is_integer"`
	// Unsigned reports whether a single-column integer key is unsigned.
	Unsigned bool `json:"unsigned"`
}

// PrimaryKeys returns one primary-key fact per requested object that exists in
// the Inspector's schema. Results preserve requested order. Columns use primary
// key order rather than table ordinal order; absent keys carry PKNone. Missing
// or invisible objects are absent.
//
// PrimaryKeys is safe for concurrent use when the Inspector's Querier is safe
// for concurrent use and tables is not mutated concurrently.
func (i *Inspector) PrimaryKeys(ctx context.Context, tables []string) ([]PKInfo, error) {
	if err := i.validate(opPrimaryKeys, tables); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}

	const query = `
		SELECT
			t.TABLE_NAME,
			s.COLUMN_NAME,
			c.DATA_TYPE,
			c.COLUMN_TYPE
		FROM information_schema.TABLES AS t
		LEFT JOIN information_schema.STATISTICS AS s
		  ON s.TABLE_SCHEMA = t.TABLE_SCHEMA
		 AND s.TABLE_NAME = t.TABLE_NAME
		 AND s.INDEX_NAME = 'PRIMARY'
		LEFT JOIN information_schema.COLUMNS AS c
		  ON c.TABLE_SCHEMA = s.TABLE_SCHEMA
		 AND c.TABLE_NAME = s.TABLE_NAME
		 AND c.COLUMN_NAME = s.COLUMN_NAME
		WHERE t.TABLE_SCHEMA = ?
		ORDER BY t.TABLE_NAME, s.SEQ_IN_INDEX`

	rows, err := i.q.QueryContext(ctx, query, i.schema)
	if err != nil {
		return nil, newObjectError(opPrimaryKeys, i.schema, "", fmt.Errorf("query metadata: %w", err))
	}
	defer rows.Close()

	byTable := make(map[string]*PKInfo)
	for rows.Next() {
		var (
			table      string
			column     sql.NullString
			dataType   sql.NullString
			columnType sql.NullString
		)
		if err := rows.Scan(&table, &column, &dataType, &columnType); err != nil {
			return nil, newObjectError(
				opPrimaryKeys,
				i.schema,
				"",
				fmt.Errorf("scan metadata: %w", err),
			)
		}

		pk, ok := byTable[table]
		if !ok {
			pk = &PKInfo{Table: table, Kind: PKNone}
			byTable[table] = pk
		}
		if column.Valid {
			pk.Columns = append(pk.Columns, column.String)
			if dataType.Valid {
				pk.DataType = dataType.String
			}
			if columnType.Valid {
				pk.Unsigned = containsUnsigned(columnType.String)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(
			opPrimaryKeys,
			i.schema,
			"",
			fmt.Errorf("iterate metadata: %w", err),
		)
	}

	for _, pk := range byTable {
		switch len(pk.Columns) {
		case 0:
			pk.Kind = PKNone
			pk.DataType = ""
			pk.Unsigned = false
		case 1:
			pk.Kind = PKSingle
			pk.IsInteger = isIntegerDataType(pk.DataType)
		default:
			pk.Kind = PKComposite
			pk.DataType = ""
			pk.IsInteger = false
			pk.Unsigned = false
		}
	}

	found := make([]PKInfo, 0, len(tables))
	for _, table := range tables {
		if pk, ok := byTable[table]; ok {
			fact := *pk
			fact.Columns = slices.Clone(pk.Columns)
			found = append(found, fact)
		}
	}

	return found, nil
}

// CheckPKExists reports tables without a primary key.
//
// Without a primary key no column provably identifies one row, so deleting by
// a configured key can over-match. CheckPKExists is safe for concurrent use
// when pks is not mutated concurrently.
func CheckPKExists(pks []PKInfo) []Finding {
	var findings []Finding
	for _, pk := range pks {
		if pk.Kind == PKNone {
			findings = append(findings, pkFinding(IDPKExists, "table has no primary key", pk))
		}
	}

	return findings
}

// CheckPKSingleColumn reports tables with composite primary keys.
//
// Filtering a composite key by only one member can over-match rows outside the
// intended set. CheckPKSingleColumn is safe for concurrent use when pks is not
// mutated concurrently.
func CheckPKSingleColumn(pks []PKInfo) []Finding {
	var findings []Finding
	for _, pk := range pks {
		if pk.Kind == PKComposite {
			findings = append(findings, pkFinding(
				IDPKSingleColumn,
				"table has a composite primary key",
				pk,
			))
		}
	}

	return findings
}

// CheckPKMatchesExpected reports primary keys that do not contain the caller's
// expected column.
//
// A configured column that is not part of the primary key may not be unique,
// so deleting by it can over-match. Case-only differences belong exclusively
// to CheckPKNameCase. CheckPKMatchesExpected is safe for concurrent use when
// its arguments are not mutated concurrently.
func CheckPKMatchesExpected(pks []PKInfo, expected map[string]string) []Finding {
	var findings []Finding
	for _, pk := range pks {
		want := expected[pk.Table]
		if want == "" || pk.Kind == PKNone || pk.Kind == PKUnknown {
			continue
		}
		if matchesPKColumn(pk.Columns, want) {
			continue
		}
		findings = append(findings, pkFinding(
			IDPKMatchesExpected,
			"expected column is not part of the table's primary key",
			pk,
		))
	}

	return findings
}

// CheckPKNameCase reports expected primary-key columns whose ASCII letter case
// differs from the server's spelling.
//
// information_schema column-name predicates can match case-insensitively, so a
// configured log_id can silently find LOG_ID. ASCII-only folding deliberately
// fails safe for non-ASCII differences. CheckPKNameCase is safe for concurrent
// use when its arguments are not mutated concurrently.
func CheckPKNameCase(pks []PKInfo, expected map[string]string) []Finding {
	var findings []Finding
	for _, pk := range pks {
		want := expected[pk.Table]
		if want == "" || pk.Kind == PKNone || pk.Kind == PKUnknown {
			continue
		}
		if hasCaseOnlyPKColumn(pk.Columns, want) {
			findings = append(findings, pkFinding(
				IDPKNameCase,
				"expected primary-key column differs from the server's spelling only in ASCII case",
				pk,
			))
		}
	}

	return findings
}

// CheckPKIntegerType reports single-column primary keys that are not integer
// types.
//
// A numeric high-water-mark checkpoint cannot be advanced or resumed through
// a UUID, varchar, decimal, or datetime key. Composite and absent keys are left
// to their own checks. CheckPKIntegerType is safe for concurrent use when pks
// is not mutated concurrently.
func CheckPKIntegerType(pks []PKInfo) []Finding {
	var findings []Finding
	for _, pk := range pks {
		if pk.Kind == PKSingle && !pk.IsInteger {
			findings = append(findings, pkFinding(
				IDPKIntegerType,
				"single-column primary key is not an integer type",
				pk,
			))
		}
	}

	return findings
}

func pkFinding(id, message string, pk PKInfo) Finding {
	return Finding{
		Check:   id,
		Message: findingMessage(id, message),
		Tables:  []string{pk.Table},
		Facts:   pk,
	}
}

func matchesPKColumn(columns []string, expected string) bool {
	for _, column := range columns {
		if column == expected || asciiFoldEqual(column, expected) {
			return true
		}
	}

	return false
}

func hasCaseOnlyPKColumn(columns []string, expected string) bool {
	for _, column := range columns {
		if column != expected && asciiFoldEqual(column, expected) {
			return true
		}
	}

	return false
}

func asciiFoldEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range len(left) {
		l := left[index]
		r := right[index]
		if l == r {
			continue
		}
		if l >= 'A' && l <= 'Z' {
			l += 'a' - 'A'
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if l != r {
			return false
		}
	}

	return true
}

func isIntegerDataType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case dataTypeTinyint, "smallint", "mediumint", dataTypeInt, "integer", dataTypeBigint:
		return true
	default:
		return false
	}
}

func containsUnsigned(columnType string) bool {
	for _, field := range strings.Fields(columnType) {
		if strings.EqualFold(field, "unsigned") {
			return true
		}
	}

	return false
}
