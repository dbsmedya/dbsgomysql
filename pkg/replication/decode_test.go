package replication

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
)

// adaptDecoder erases a decoder's result type so that every helper fits one
// table. Comparing the erased results with != also pins the concrete type each
// helper returns, not just its value.
func adaptDecoder[T any](decode func(any) (T, error)) func(any) (any, error) {
	return func(value any) (any, error) {
		result, err := decode(value)

		return result, err
	}
}

func TestDecodeProvenance(t *testing.T) {
	t.Parallel()

	decodeStringAny := adaptDecoder(decodeString)
	decodeInt64Any := adaptDecoder(decodeInt64)
	decodeIntAny := adaptDecoder(decodeInt)
	decodeUint16Any := adaptDecoder(decodeUint16)
	decodeUint32Any := adaptDecoder(decodeUint32)
	decodeUint64Any := adaptDecoder(decodeUint64)
	decodeBoolAny := adaptDecoder(decodeBool)
	decodeNullSecondsAny := adaptDecoder(decodeNullSeconds)

	cases := []struct {
		name            string
		decode          func(any) (any, error)
		input           any
		want            any
		wantErrIs       error
		wantErrContains string
	}{
		// decodeString.
		{
			name:   "string from string",
			decode: decodeStringAny,
			input:  "ON",
			want:   "ON",
		},
		{
			name:   "string from bytes",
			decode: decodeStringAny,
			input:  []byte("uuid:1-5"),
			want:   "uuid:1-5",
		},
		{
			name:            "string from NULL",
			decode:          decodeStringAny,
			input:           nil,
			wantErrIs:       errUnexpectedNULL,
			wantErrContains: "unexpected NULL",
		},
		{
			name:            "string from surprise type",
			decode:          decodeStringAny,
			input:           float64(1.5),
			wantErrContains: "float64",
		},

		// decodeInt64.
		{
			name:   "int64 from int64",
			decode: decodeInt64Any,
			input:  int64(12),
			want:   int64(12),
		},
		{
			name:   "int64 from uint64",
			decode: decodeInt64Any,
			input:  uint64(12),
			want:   int64(12),
		},
		{
			name:      "int64 from uint64 above MaxInt64",
			decode:    decodeInt64Any,
			input:     uint64(math.MaxInt64) + 1,
			wantErrIs: errValueOutOfRange,
		},
		{
			name:   "int64 from bytes",
			decode: decodeInt64Any,
			input:  []byte("-12"),
			want:   int64(-12),
		},
		{
			name:   "int64 from string",
			decode: decodeInt64Any,
			input:  "12",
			want:   int64(12),
		},
		{
			name:            "int64 from malformed bytes",
			decode:          decodeInt64Any,
			input:           []byte("12x"),
			wantErrContains: "12x",
		},
		{
			name:            "int64 from empty bytes",
			decode:          decodeInt64Any,
			input:           []byte(""),
			wantErrContains: "malformed",
		},
		{
			name:      "int64 from NULL",
			decode:    decodeInt64Any,
			input:     nil,
			wantErrIs: errUnexpectedNULL,
		},
		{
			name:            "int64 from surprise type",
			decode:          decodeInt64Any,
			input:           true,
			wantErrContains: "bool",
		},

		// decodeInt.
		{
			name:   "int from bytes",
			decode: decodeIntAny,
			input:  []byte("4"),
			want:   4,
		},
		{
			name:   "int from int64",
			decode: decodeIntAny,
			input:  int64(0),
			want:   0,
		},
		{
			name:      "int from NULL",
			decode:    decodeIntAny,
			input:     nil,
			wantErrIs: errUnexpectedNULL,
		},

		// decodeUint16.
		{
			name:   "uint16 from int64",
			decode: decodeUint16Any,
			input:  int64(3306),
			want:   uint16(3306),
		},
		{
			name:      "uint16 overflow",
			decode:    decodeUint16Any,
			input:     int64(70000),
			wantErrIs: errValueOutOfRange,
		},
		{
			name:      "uint16 from negative",
			decode:    decodeUint16Any,
			input:     int64(-1),
			wantErrIs: errValueOutOfRange,
		},

		// decodeUint32.
		{
			name:   "uint32 at boundary",
			decode: decodeUint32Any,
			input:  []byte("4294967295"),
			want:   uint32(math.MaxUint32),
		},
		{
			name:      "uint32 overflow",
			decode:    decodeUint32Any,
			input:     []byte("4294967296"),
			wantErrIs: errValueOutOfRange,
		},
		{
			name:            "uint32 from surprise type",
			decode:          decodeUint32Any,
			input:           struct{}{},
			wantErrContains: "struct {}",
		},

		// decodeUint64.
		{
			name:   "uint64 from uint64",
			decode: decodeUint64Any,
			input:  uint64(math.MaxUint64),
			want:   uint64(math.MaxUint64),
		},
		{
			name:   "uint64 from bytes above MaxInt64",
			decode: decodeUint64Any,
			input:  []byte("18446744073709551615"),
			want:   uint64(math.MaxUint64),
		},
		{
			name:      "uint64 from negative int64",
			decode:    decodeUint64Any,
			input:     int64(-5),
			wantErrIs: errValueOutOfRange,
		},
		{
			name:      "uint64 from NULL",
			decode:    decodeUint64Any,
			input:     nil,
			wantErrIs: errUnexpectedNULL,
		},

		// decodeBool.
		{
			name:   "bool from int64 one",
			decode: decodeBoolAny,
			input:  int64(1),
			want:   true,
		},
		{
			name:   "bool from int64 zero",
			decode: decodeBoolAny,
			input:  int64(0),
			want:   false,
		},
		{
			name:   "bool from uint64 one",
			decode: decodeBoolAny,
			input:  uint64(1),
			want:   true,
		},
		{
			name:   "bool from uint64 zero",
			decode: decodeBoolAny,
			input:  uint64(0),
			want:   false,
		},
		{
			name:      "bool from uint64 two",
			decode:    decodeBoolAny,
			input:     uint64(2),
			wantErrIs: errUnrecognizedValue,
		},
		{
			name:      "bool from int64 two",
			decode:    decodeBoolAny,
			input:     int64(2),
			wantErrIs: errUnrecognizedValue,
		},
		{
			name:   "bool from ON",
			decode: decodeBoolAny,
			input:  []byte("ON"),
			want:   true,
		},
		{
			name:   "bool from OFF",
			decode: decodeBoolAny,
			input:  "OFF",
			want:   false,
		},
		{
			name:   "bool from one text",
			decode: decodeBoolAny,
			input:  []byte("1"),
			want:   true,
		},
		{
			name:   "bool from zero text",
			decode: decodeBoolAny,
			input:  "0",
			want:   false,
		},
		{
			name:      "bool rejects lowercase on",
			decode:    decodeBoolAny,
			input:     []byte("on"),
			wantErrIs: errUnrecognizedValue,
		},
		{
			name:      "bool rejects lowercase off",
			decode:    decodeBoolAny,
			input:     "off",
			wantErrIs: errUnrecognizedValue,
		},
		{
			name:      "bool rejects mixed case On",
			decode:    decodeBoolAny,
			input:     []byte("On"),
			wantErrIs: errUnrecognizedValue,
		},
		{
			name:      "bool from NULL",
			decode:    decodeBoolAny,
			input:     nil,
			wantErrIs: errUnexpectedNULL,
		},
		{
			name:            "bool from surprise type",
			decode:          decodeBoolAny,
			input:           float64(1),
			wantErrContains: "float64",
		},

		// decodeNullSeconds.
		{
			name:   "null seconds from NULL",
			decode: decodeNullSecondsAny,
			input:  nil,
			want:   sql.NullInt64{},
		},
		{
			name:   "null seconds from bytes",
			decode: decodeNullSecondsAny,
			input:  []byte("12"),
			want:   sql.NullInt64{Int64: 12, Valid: true},
		},
		{
			name:   "null seconds from zero",
			decode: decodeNullSecondsAny,
			input:  int64(0),
			want:   sql.NullInt64{Int64: 0, Valid: true},
		},
		{
			name:            "null seconds from malformed text",
			decode:          decodeNullSecondsAny,
			input:           []byte("soon"),
			wantErrContains: "malformed",
		},
		{
			name:            "null seconds from surprise type",
			decode:          decodeNullSecondsAny,
			input:           float64(3),
			wantErrContains: "float64",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := testCase.decode(testCase.input)
			wantError := testCase.wantErrIs != nil || testCase.wantErrContains != ""

			if !wantError {
				if err != nil {
					t.Fatalf("decode(%#v) returned error %v, want value %#v", testCase.input, err, testCase.want)
				}
				if got != testCase.want {
					t.Errorf("decode(%#v) = %#v, want %#v", testCase.input, got, testCase.want)
				}

				return
			}

			if err == nil {
				t.Fatalf("decode(%#v) = %#v, nil; want an error", testCase.input, got)
			}
			if testCase.wantErrIs != nil && !errors.Is(err, testCase.wantErrIs) {
				t.Errorf("errors.Is(%v, %v) = false, want true", err, testCase.wantErrIs)
			}
			if testCase.wantErrContains != "" && !strings.Contains(err.Error(), testCase.wantErrContains) {
				t.Errorf("decode(%#v) error = %q, want it to contain %q",
					testCase.input, err.Error(), testCase.wantErrContains)
			}
		})
	}
}

// TestDecodeNullSecondsProvenance pins the provenance guarantee: an invalid
// sql.NullInt64 is reachable only through a driver nil. Any other input either
// decodes to a valid value or fails loudly.
func TestDecodeNullSecondsProvenance(t *testing.T) {
	t.Parallel()

	nullSeconds, err := decodeNullSeconds(nil)
	if err != nil {
		t.Fatalf("decodeNullSeconds(nil) returned error %v, want nil", err)
	}
	if nullSeconds.Valid {
		t.Errorf("decodeNullSeconds(nil) = %#v, want Valid false", nullSeconds)
	}

	nonNull := []any{
		int64(0),
		int64(-1),
		uint64(7),
		[]byte("0"),
		[]byte("42"),
		"9",
		[]byte(""),
		[]byte("soon"),
		float64(3),
		true,
		struct{}{},
	}
	for _, input := range nonNull {
		seconds, err := decodeNullSeconds(input)
		if err != nil {
			continue
		}
		if !seconds.Valid {
			t.Errorf("decodeNullSeconds(%#v) = %#v, nil; a non-nil input must never yield Valid false",
				input, seconds)
		}
	}
}
