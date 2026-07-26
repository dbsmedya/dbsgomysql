// Package sqlutil makes MySQL identifiers safe to interpolate into SQL.
package sqlutil

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	// ErrIdentifierInvalidUTF8 means an identifier is not valid UTF-8.
	ErrIdentifierInvalidUTF8 = errors.New("identifier is not valid UTF-8")
	// ErrIdentifierEmpty means an identifier has no characters.
	ErrIdentifierEmpty = errors.New("identifier is empty")
	// ErrIdentifierTooLong means an identifier exceeds 64 characters.
	ErrIdentifierTooLong = errors.New("identifier exceeds 64 characters")
	// ErrIdentifierNulByte means an identifier contains U+0000.
	ErrIdentifierNulByte = errors.New("identifier contains U+0000")
	// ErrIdentifierSupplementary means an identifier contains a character above
	// U+FFFF.
	ErrIdentifierSupplementary = errors.New("identifier contains a character above U+FFFF")
	// ErrIdentifierTrailingSpace means an identifier ends with U+0009 through
	// U+000D or U+0020.
	ErrIdentifierTrailingSpace = errors.New("identifier ends with a space character")
)

// QuoteIdentifier wraps one identifier in backticks, doubling any backticks it
// contains.
//
// It is total: it never fails or rejects input. It makes name safe to
// interpolate as one identifier, but does not check whether MySQL would accept
// and preserve the name. Use ValidateIdentifier at trust boundaries.
//
// QuoteIdentifier is safe for concurrent use. To quote a qualified reference,
// use QuoteQualified rather than passing a dot-separated name.
func QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// QuoteQualified quotes each identifier part separately and joins the results
// with dots. Empty parts are preserved and quoted; with no parts it returns an
// empty string.
//
// QuoteQualified is safe for concurrent use.
func QuoteQualified(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = QuoteIdentifier(part)
	}

	return strings.Join(quoted, ".")
}

// IsSimpleIdentifier reports whether name contains at least one character and
// consists only of ASCII letters, digits, and underscores.
//
// This deliberately narrow allowlist is not a MySQL validity check and
// produces false negatives by design. IsSimpleIdentifier is safe for
// concurrent use.
func IsSimpleIdentifier(name string) bool {
	if name == "" {
		return false
	}

	for i := range len(name) {
		char := name[i]
		if (char < 'a' || char > 'z') &&
			(char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') &&
			char != '_' {
			return false
		}
	}

	return true
}

// ValidateIdentifier reports why MySQL would reject or fail to preserve name
// as a database, table, or column identifier, or returns nil.
//
// It enforces the constraints common to those 64-character identifier
// categories. Validation is deterministic: invalid UTF-8, empty input, U+0000,
// supplementary characters, a trailing ASCII space character (U+0009 through
// U+000D or U+0020), and length are checked in that order. ValidateIdentifier
// is safe for concurrent use.
func ValidateIdentifier(name string) error {
	if !utf8.ValidString(name) {
		return ErrIdentifierInvalidUTF8
	}
	if name == "" {
		return ErrIdentifierEmpty
	}
	if strings.IndexByte(name, 0) >= 0 {
		return ErrIdentifierNulByte
	}
	for _, char := range name {
		if char > '\uFFFF' {
			return ErrIdentifierSupplementary
		}
	}
	last, _ := utf8.DecodeLastRuneInString(name)
	if last == ' ' || (last >= '\t' && last <= '\r') {
		return ErrIdentifierTrailingSpace
	}
	if utf8.RuneCountInString(name) > 64 {
		return ErrIdentifierTooLong
	}

	return nil
}
