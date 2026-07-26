package validations

import "testing"

func TestPKKindZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero PKKind
	if zero != PKUnknown {
		t.Errorf("the PKKind zero value is %d, want PKUnknown (%d); an unpopulated PKInfo must be detectable",
			zero, PKUnknown)
	}
}

func TestPKKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind PKKind
		want string
	}{
		{name: "unknown", kind: PKUnknown, want: "unknown"},
		{name: "none", kind: PKNone, want: "none"},
		{name: "single", kind: PKSingle, want: "single"},
		{name: "composite", kind: PKComposite, want: "composite"},
		{name: "undeclared", kind: PKKind(99), want: "PKKind(99)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.kind.String(); got != test.want {
				t.Errorf("PKKind(%d).String() = %q, want %q", test.kind, got, test.want)
			}
		})
	}
}

func TestPKKindStringsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]PKKind)
	for _, kind := range []PKKind{PKUnknown, PKNone, PKSingle, PKComposite} {
		got := kind.String()
		if other, dup := seen[got]; dup {
			t.Errorf("PKKind(%d) and PKKind(%d) both render as %q", other, kind, got)
		}
		seen[got] = kind
	}
}
