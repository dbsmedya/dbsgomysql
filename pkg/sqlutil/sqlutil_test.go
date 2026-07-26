package sqlutil

import (
	"errors"
	"strings"
	"testing"
)

func TestQuoteIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "users", want: "`users`"},
		{name: "order_items", want: "`order_items`"},
		{name: "MyTable", want: "`MyTable`"},
		{name: "table123", want: "`table123`"},
		{name: "", want: "``"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := QuoteIdentifier(test.name); got != test.want {
				t.Errorf("QuoteIdentifier(%q) = %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestQuoteIdentifierDoublesBackticks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "my`table", want: "`my``table`"},
		{name: "ta`bl`e", want: "`ta``bl``e`"},
		{name: "`table", want: "```table`"},
		{name: "table`", want: "`table```"},
		{name: "```", want: "````````"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := QuoteIdentifier(test.name); got != test.want {
				t.Errorf("QuoteIdentifier(%q) = %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestQuoteQualified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{name: "two parts", parts: []string{"sakila", "payment"}, want: "`sakila`.`payment`"},
		{name: "one part", parts: []string{"payment"}, want: "`payment`"},
		{name: "zero parts", parts: nil, want: ""},
		{name: "empty part", parts: []string{"", "payment"}, want: "``.`payment`"},
		{name: "backtick and dot", parts: []string{"my`schema", "odd.table"}, want: "`my``schema`.`odd.table`"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := QuoteQualified(test.parts...); got != test.want {
				t.Errorf("QuoteQualified(%q) = %q, want %q", test.parts, got, test.want)
			}
		})
	}
}

func TestIsSimpleIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{name: "users", want: true},
		{name: "order_items", want: true},
		{name: "MyTable", want: true},
		{name: "table123", want: true},
		{name: "___", want: true},
		{name: "", want: false},
		{name: "my table", want: false},
		{name: "my-table", want: false},
		{name: "db.table", want: false},
		{name: "my`table", want: false},
		{name: "table$name", want: false},
		{name: "table'name", want: false},
		{name: "table(1)", want: false},
		{name: "table*", want: false},
		{name: "tábla", want: false},
		{name: "users; DROP TABLE users--", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := IsSimpleIdentifier(test.name); got != test.want {
				t.Errorf("IsSimpleIdentifier(%q) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()

	invalidUTF8 := string([]byte{0xff, 0x00})
	validBMP64 := strings.Repeat("界", 64)

	tests := []struct {
		name       string
		identifier string
		wantErr    error
	}{
		{name: "ordinary", identifier: "payment"},
		{name: "quoted legal punctuation", identifier: "order-`items$"},
		{name: "exactly 64 runes", identifier: strings.Repeat("a", 64)},
		{name: "64 multibyte BMP runes", identifier: validBMP64},
		{name: "invalid UTF-8 precedes NUL", identifier: invalidUTF8, wantErr: ErrIdentifierInvalidUTF8},
		{name: "empty", identifier: "", wantErr: ErrIdentifierEmpty},
		{name: "NUL", identifier: "pay\x00ment", wantErr: ErrIdentifierNulByte},
		{
			name:       "NUL precedes supplementary trailing and length",
			identifier: "\x00\U00010000" + strings.Repeat("a", 65) + " ",
			wantErr:    ErrIdentifierNulByte,
		},
		{
			name:       "supplementary",
			identifier: "payment\U00010000",
			wantErr:    ErrIdentifierSupplementary,
		},
		{
			name:       "supplementary precedes trailing and length",
			identifier: "\U00010000" + strings.Repeat("a", 65) + " ",
			wantErr:    ErrIdentifierSupplementary,
		},
		{name: "trailing ASCII space", identifier: "payment ", wantErr: ErrIdentifierTrailingSpace},
		{
			name:       "trailing space precedes length",
			identifier: strings.Repeat("a", 65) + " ",
			wantErr:    ErrIdentifierTrailingSpace,
		},
		{name: "65 runes", identifier: strings.Repeat("a", 65), wantErr: ErrIdentifierTooLong},
		{name: "65 multibyte BMP runes", identifier: validBMP64 + "界", wantErr: ErrIdentifierTooLong},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateIdentifier(test.identifier)
			if test.wantErr == nil {
				if err != nil {
					t.Errorf("ValidateIdentifier(%q) returned unexpected error: %v", test.identifier, err)
				}

				return
			}

			if !errors.Is(err, test.wantErr) {
				t.Errorf("ValidateIdentifier(%q) error = %v, want errors.Is(_, %v)", test.identifier, err, test.wantErr)
			}
		})
	}
}
