package validations

import (
	"strconv"
	"strings"
)

// normalizeColumnType removes the deprecated display width from an integer or
// YEAR information_schema.COLUMNS.COLUMN_TYPE value, so a column created
// before MySQL 8.0.19 compares equal to the same column created after it.
// Display width is stored per column, so an instance upgraded in place from an
// earlier release keeps reporting the old form until the table is rebuilt.
//
// MySQL's own two exceptions are mirrored here, because on a current server
// both still appear in COLUMN_TYPE and neither is formatting noise:
//
//   - tinyint(1). BOOLEAN is an alias for TINYINT(1) and MySQL keeps that width
//     where it strips every other, so erasing it would report a BOOLEAN and a
//     plain TINYINT as identical.
//   - Any type carrying ZEROFILL. Retrieved values are zero-padded to the
//     display width, so int(5) zerofill yields 00042 where int(10) zerofill
//     yields 0000000042 — different client-visible schemas, kept distinct.
//
// Attributes following the width, such as unsigned, are preserved because they
// change the value range. Types whose parenthesised part is semantic, such as
// decimal(3,2) and varchar(50), are returned unchanged.
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
	if strings.EqualFold(base, dataTypeTinyint) && width == "1" {
		return columnType
	}

	attributes := columnType[closing+1:]
	if containsZerofill(attributes) {
		return columnType
	}

	return base + attributes
}

// containsZerofill reports whether a COLUMN_TYPE's trailing attributes include
// ZEROFILL. It matches whole attribute words rather than a substring, so a
// future attribute merely containing the text does not trigger the carve-out.
func containsZerofill(attributes string) bool {
	for _, field := range strings.Fields(attributes) {
		if strings.EqualFold(field, "zerofill") {
			return true
		}
	}

	return false
}

// hasDisplayWidth reports whether a type carries the legacy display width
// MySQL stopped emitting in 8.0.19: the integer types and YEAR (WL #13528 and
// WL #13537, same release, same data-dictionary retention rule). It is a
// switch rather than a package-level set because library rules forbid global
// mutable state.
func hasDisplayWidth(base string) bool {
	switch base {
	case dataTypeBigint, dataTypeInt, "integer", "mediumint", "smallint", dataTypeTinyint, "year":
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

// GeneratedKind reports how a column's value is produced. Its zero value is
// GeneratedUnknown, reserved for "not populated" so an unset ColumnSpec is
// detectable rather than silently meaning GeneratedNone.
//
// GeneratedKind is a plain value and is safe for concurrent use.
type GeneratedKind uint8

// Generated kinds. GeneratedUnknown is the zero value and means the field was
// never populated.
const (
	GeneratedUnknown GeneratedKind = iota
	GeneratedNone
	GeneratedVirtual
	GeneratedStored
)

// String returns the kind as a lowercase word. An undeclared value renders as
// GeneratedKind(N) rather than being reported as a known kind.
//
// String is safe for concurrent use.
func (k GeneratedKind) String() string {
	switch k {
	case GeneratedUnknown:
		return unknownEnum
	case GeneratedNone:
		return enumNoneName
	case GeneratedVirtual:
		return "virtual"
	case GeneratedStored:
		return "stored"
	default:
		return "GeneratedKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// columnExtra is information_schema.COLUMNS.EXTRA decomposed into the
// independent facts it packs together.
type columnExtra struct {
	autoIncrement       bool
	defaultIsExpression bool
	invisible           bool
	onUpdate            bool
	generated           GeneratedKind
}

// parseColumnExtra decomposes an EXTRA value. EXTRA is a space-separated
// composite — "DEFAULT_GENERATED on update CURRENT_TIMESTAMP" is one column —
// so each fact is detected independently.
//
// The generated-column markers are matched as two words. DEFAULT_GENERATED
// contains the substring GENERATED but describes an expression default, not a
// generated column, and must not be mistaken for one.
func parseColumnExtra(extra string) columnExtra {
	folded := strings.ToUpper(extra)

	parsed := columnExtra{
		autoIncrement:       strings.Contains(folded, "AUTO_INCREMENT"),
		defaultIsExpression: strings.Contains(folded, "DEFAULT_GENERATED"),
		invisible:           strings.Contains(folded, "INVISIBLE"),
		onUpdate:            strings.Contains(folded, "ON UPDATE"),
		generated:           GeneratedNone,
	}

	switch {
	case strings.Contains(folded, "VIRTUAL GENERATED"):
		parsed.generated = GeneratedVirtual
	case strings.Contains(folded, "STORED GENERATED"):
		parsed.generated = GeneratedStored
	}

	return parsed
}
