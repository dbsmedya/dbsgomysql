package validations

import "strings"

// normalizeColumnType removes the deprecated integer display width from an
// information_schema.COLUMNS.COLUMN_TYPE value, so a column created before
// MySQL 8.0.17 compares equal to the same column created after it. Display
// width is stored per column, so an instance upgraded in place from an earlier
// release keeps reporting the old form until the table is rebuilt.
//
// tinyint(1) is preserved. BOOLEAN is an alias for TINYINT(1) and MySQL keeps
// that width where it strips every other, so erasing it would report a BOOLEAN
// and a plain TINYINT as identical. Attributes following the width — unsigned,
// zerofill — are preserved because they change the value range and are real
// differences rather than formatting noise. Types whose parenthesised part is
// semantic, such as decimal(3,2) and varchar(50), are returned unchanged.
//
// Malformed input is returned unchanged rather than guessed at.
func normalizeColumnType(columnType string) string {
	open := strings.IndexByte(columnType, '(')
	if open < 0 {
		return columnType
	}

	closing := strings.IndexByte(columnType[open:], ')')
	if closing < 0 {
		return columnType
	}
	closing += open

	base := columnType[:open]
	if !hasDisplayWidth(strings.ToLower(base)) {
		return columnType
	}

	width := columnType[open+1 : closing]
	if !isASCIIDigits(width) {
		return columnType
	}
	if strings.EqualFold(base, "tinyint") && width == "1" {
		return columnType
	}

	return base + columnType[closing+1:]
}

// hasDisplayWidth reports whether an integer type carries a display width MySQL
// deprecated in 8.0.17. It is a switch rather than a package-level set because
// library rules forbid global mutable state.
func hasDisplayWidth(base string) bool {
	switch base {
	case "bigint", "int", "integer", "mediumint", "smallint", "tinyint":
		return true
	default:
		return false
	}
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
