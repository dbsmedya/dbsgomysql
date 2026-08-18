package replication

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
)

var (
	// errUnexpectedNULL means the server sent SQL NULL for a column that is
	// never NULL. Only Seconds_Behind_Source may be NULL; every other promised
	// column fails closed rather than fabricating a zero value.
	errUnexpectedNULL = errors.New("unexpected NULL")
	// errValueOutOfRange means a decoded number does not fit the field it was
	// read into, or is negative where the field is unsigned.
	errValueOutOfRange = errors.New("value out of range")
	// errUnrecognizedValue means the server sent a value outside the set this
	// column is documented to use.
	errUnrecognizedValue = errors.New("unrecognized value")
)

// decodeString reads a text column. Driver text arrives as string or []byte
// depending on the driver and the connection settings, so both are accepted.
func decodeString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case nil:
		return "", errUnexpectedNULL
	default:
		return "", unexpectedTypeError(value)
	}
}

// decodeInt64 reads a signed integer column in whichever representation the
// driver chose for it.
func decodeInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case uint64:
		if typed > math.MaxInt64 {
			return 0, fmt.Errorf("%w: %d exceeds int64", errValueOutOfRange, typed)
		}

		return int64(typed), nil
	case []byte:
		return parseInt64(string(typed))
	case string:
		return parseInt64(typed)
	case nil:
		return 0, errUnexpectedNULL
	default:
		return 0, unexpectedTypeError(value)
	}
}

// decodeInt reads a signed integer column into the platform int.
func decodeInt(value any) (int, error) {
	decoded, err := decodeInt64(value)
	if err != nil {
		return 0, err
	}

	// A round trip rather than a comparison against math.MaxInt: int is 64-bit
	// on every platform this library targets, so a constant bound would be
	// dead code there while still being wrong to omit elsewhere.
	narrowed := int(decoded)
	if int64(narrowed) != decoded {
		return 0, fmt.Errorf("%w: %d exceeds int", errValueOutOfRange, decoded)
	}

	return narrowed, nil
}

// decodeUint64 reads an unsigned integer column. A negative value is a range
// failure, not a wraparound.
func decodeUint64(value any) (uint64, error) {
	switch typed := value.(type) {
	case uint64:
		return typed, nil
	case int64:
		if typed < 0 {
			return 0, fmt.Errorf("%w: %d is negative", errValueOutOfRange, typed)
		}

		return uint64(typed), nil
	case []byte:
		return parseUint64(string(typed))
	case string:
		return parseUint64(typed)
	case nil:
		return 0, errUnexpectedNULL
	default:
		return 0, unexpectedTypeError(value)
	}
}

// decodeUint32 reads an unsigned integer column that must fit 32 bits, such as
// a server id.
func decodeUint32(value any) (uint32, error) {
	decoded, err := decodeUint64(value)
	if err != nil {
		return 0, err
	}
	if decoded > math.MaxUint32 {
		return 0, fmt.Errorf("%w: %d exceeds uint32", errValueOutOfRange, decoded)
	}

	return uint32(decoded), nil
}

// decodeUint16 reads an unsigned integer column that must fit 16 bits, such as
// a TCP port.
func decodeUint16(value any) (uint16, error) {
	decoded, err := decodeUint64(value)
	if err != nil {
		return 0, err
	}
	if decoded > math.MaxUint16 {
		return 0, fmt.Errorf("%w: %d exceeds uint16", errValueOutOfRange, decoded)
	}

	return uint16(decoded), nil
}

// decodeBool reads a boolean column or system variable. Text is matched
// exactly and never case-folded: the server's own spellings are the whole
// accepted set, so an unfamiliar spelling is reported rather than guessed.
func decodeBool(value any) (bool, error) {
	switch typed := value.(type) {
	case int64:
		switch typed {
		case 0:
			return false, nil
		case 1:
			return true, nil
		default:
			return false, fmt.Errorf("%w: %d is neither 0 nor 1", errUnrecognizedValue, typed)
		}
	case []byte:
		return parseBool(string(typed))
	case string:
		return parseBool(typed)
	case nil:
		return false, errUnexpectedNULL
	default:
		return false, unexpectedTypeError(value)
	}
}

// decodeNullSeconds reads the one promised column that may be NULL,
// Seconds_Behind_Source. An invalid result is reachable only through a driver
// nil: every other input either decodes to a valid value or returns an error,
// so a consumer reading Valid false learns that the server sent SQL NULL and
// nothing else.
func decodeNullSeconds(value any) (sql.NullInt64, error) {
	if value == nil {
		return sql.NullInt64{}, nil
	}

	seconds, err := decodeInt64(value)
	if err != nil {
		return sql.NullInt64{}, err
	}

	return sql.NullInt64{Int64: seconds, Valid: true}, nil
}

func parseInt64(text string) (int64, error) {
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed signed integer %q: %w", text, err)
	}

	return parsed, nil
}

func parseUint64(text string) (uint64, error) {
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed unsigned integer %q: %w", text, err)
	}

	return parsed, nil
}

func parseBool(text string) (bool, error) {
	switch text {
	case "0", "OFF":
		return false, nil
	case "1", "ON":
		return true, nil
	default:
		return false, fmt.Errorf("%w: %q is not one of 0, 1, OFF, ON", errUnrecognizedValue, text)
	}
}

func unexpectedTypeError(value any) error {
	return fmt.Errorf("unexpected driver type %T", value)
}
