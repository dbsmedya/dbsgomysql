package validations

import (
	"strings"
	"unicode/utf8"
)

// maxStatementParameters is the largest number of parameters this package puts
// in one statement.
//
// The COM_STMT_PREPARE response encodes num_params as a two-byte integer, so a
// statement carrying more parameters is not representable in the prepare
// response, and MySQL raises ER_PS_MANY_PARAM (1390), "Prepared statement
// contains too many placeholders". MySQL documents neither a maximum count nor
// the connection between the two; this ceiling follows from the field width.
//
// Measured 2026-08-08 on 8.0.46, 8.4.9 and 9.7.1: 65535 prepares, 65536 raises
// 1390, on all three.
const maxStatementParameters = 65535

// narrowNames returns the parameter values for a dynamic IN (...) predicate
// over names, and reports whether that predicate may be used at all.
//
// names must be the strings that will actually be bound, which are not always
// the caller's table names — the INNODB_FOREIGN predicate binds "schema/table",
// so it passes composed values. Guarding anything other than the real
// parameters leaves an unguarded value on the wire.
//
// A false result means the caller issues its unnarrowed query instead, either
// because a name is not representable as utf8mb3 or because the deduplicated
// count plus fixedArgs would exceed maxStatementParameters. Neither is an
// error. A requested name matching nothing is absent, and an unnarrowed query
// preserves that; every fact filters the returned rows in Go regardless, so
// narrowing is an optimization and never a decision.
func narrowNames(names []string, fixedArgs int) ([]string, bool) {
	for _, name := range names {
		if !representable(name) {
			return nil, false
		}
	}

	// Dedup runs before the bound, so the bound counts distinct names rather
	// than requests. Duplicate requested tables are meaningful and are
	// reconstructed from the caller's slice, never from the row set, which is
	// what makes deduplicating the predicate invisible in results.
	params := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		params = append(params, name)
	}

	if len(params)+fixedArgs > maxStatementParameters {
		return nil, false
	}

	return params, true
}

// representable reports whether name can be compared against an
// information_schema name column, whose collation is utf8mb3.
//
// Comparing a name carrying a character above U+FFFF raises error 3988,
// ER_IMPOSSIBLE_STRING_CONVERSION, rather than matching nothing — measured on
// 8.0.46, 8.4.9 and 9.7.1 for COLUMNS.TABLE_NAME, KEY_COLUMN_USAGE.TABLE_NAME
// and INNODB_FOREIGN.FOR_NAME. See docs/COMPAT.md entry 8.
//
// The order of the two checks is load-bearing. Go's for range decodes each
// invalid byte as utf8.RuneError, U+FFFD, which is below U+FFFF — so a rune
// comparison alone accepts invalid UTF-8, and utf8.ValidString must come first.
//
// This is deliberately not sqlutil.ValidateIdentifier, which also rejects empty
// names, NUL bytes, trailing spaces and names over 64 runes. Those are all
// accepted today and simply absent; rejecting them here would turn absence into
// an error, which is the defect this guard exists to prevent.
func representable(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	for _, char := range name {
		if char > '￿' {
			return false
		}
	}

	return true
}

func sqlPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
