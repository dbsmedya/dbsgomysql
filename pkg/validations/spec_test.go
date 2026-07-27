package validations

import "testing"

func TestTableRefZeroValueIsInvalid(t *testing.T) {
	t.Parallel()

	var zero TableRef
	if zero.valid() {
		t.Error("the TableRef zero value reports valid; it must be constructed with Ref " +
			"so an unset ref cannot reach a query")
	}
}

func TestRefValidity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema string
		table  string
		want   bool
	}{
		{name: "both set", schema: "sakila", table: "payment", want: true},
		{name: "empty schema", schema: "", table: "payment", want: false},
		{name: "empty table", schema: "sakila", table: "", want: false},
		{name: "both empty", schema: "", table: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := Ref(test.schema, test.table).valid(); got != test.want {
				t.Errorf("Ref(%q, %q).valid() = %t, want %t",
					test.schema, test.table, got, test.want)
			}
		})
	}
}

func TestRefPreservesExactSpelling(t *testing.T) {
	t.Parallel()

	ref := Ref("Sakila", "Payment")
	if ref.schema != "Sakila" || ref.table != "Payment" {
		t.Errorf("Ref stored (%q, %q), want (\"Sakila\", \"Payment\"); "+
			"name handling is case-exact throughout this package",
			ref.schema, ref.table)
	}
}

func TestSpecSectionsZeroValueCapturesNothing(t *testing.T) {
	t.Parallel()

	var zero SpecSections
	for _, section := range []SpecSections{
		SectionIndexes, SectionConstraints, SectionComment,
	} {
		if zero.Has(section) {
			t.Errorf("the SpecSections zero value reports section %d captured; "+
				"zero options is valid and must mean nothing optional was requested",
				section)
		}
	}
}

func TestSpecOptionsAccumulate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options []SpecOption
		want    SpecSections
	}{
		{name: "none", options: nil, want: 0},
		{
			name: "indexes", options: []SpecOption{WithIndexes()},
			want: SectionIndexes,
		},
		{
			name:    "all three",
			options: []SpecOption{WithIndexes(), WithConstraints(), WithComment()},
			want:    SectionIndexes | SectionConstraints | SectionComment,
		},
		{
			name:    "repeated option is idempotent",
			options: []SpecOption{WithIndexes(), WithIndexes()},
			want:    SectionIndexes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var request specRequest
			for _, option := range test.options {
				option(&request)
			}
			if request.sections != test.want {
				t.Errorf("accumulated sections = %d, want %d", request.sections, test.want)
			}
		})
	}
}

func TestConstraintKindZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero ConstraintKind
	if zero != ConstraintUnknown {
		t.Errorf("the ConstraintKind zero value is %d, want ConstraintUnknown (%d)",
			zero, ConstraintUnknown)
	}
}

func TestConstraintKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind ConstraintKind
		want string
	}{
		{name: "unknown", kind: ConstraintUnknown, want: "unknown"},
		{name: "check", kind: ConstraintCheck, want: "check"},
		{name: "foreign key", kind: ConstraintForeignKey, want: "foreign_key"},
		{name: "undeclared", kind: ConstraintKind(99), want: "ConstraintKind(99)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.kind.String(); got != test.want {
				t.Errorf("ConstraintKind(%d).String() = %q, want %q",
					test.kind, got, test.want)
			}
		})
	}
}
