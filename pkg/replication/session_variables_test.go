package replication_test

import (
	"fmt"
	"slices"
	"testing"
)

// sessionVariableChanges compares two snapshots of a session's variables and
// returns one line for every difference, in variable-name order: a value
// changed, a variable added, or a variable removed. A removed variable is
// reported whatever its value was, the empty string included. Contract (b)'s
// integration test fails on every line it returns. It is untagged so that
// make test runs its unit test.
func sessionVariableChanges(before, after map[string]string) []string {
	var changes []string
	for name, value := range before {
		later, ok := after[name]
		switch {
		case !ok:
			changes = append(changes, fmt.Sprintf("%s removed: %q before the call", name, value))
		case later != value:
			changes = append(changes, fmt.Sprintf("%s = %q after the call, %q before", name, later, value))
		}
	}
	for name, value := range after {
		if _, ok := before[name]; !ok {
			changes = append(changes, fmt.Sprintf("%s added: %q after the call", name, value))
		}
	}
	slices.Sort(changes)

	return changes
}

// TestSessionVariableChanges pins contract (b)'s comparison: every changed,
// added or removed variable is reported, a removed one whatever its value, and
// equal snapshots report nothing.
func TestSessionVariableChanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		before, after map[string]string
		want          []string
	}{
		{
			name:   "equal snapshots report nothing",
			before: map[string]string{"x": "1", "y": ""},
			after:  map[string]string{"x": "1", "y": ""},
		},
		{
			name:   "two empty snapshots report nothing",
			before: map[string]string{},
			after:  map[string]string{},
		},
		{
			name:   "a changed value",
			before: map[string]string{"x": "1"},
			after:  map[string]string{"x": "2"},
			want:   []string{`x = "2" after the call, "1" before`},
		},
		{
			name:   "a value changed to the empty string",
			before: map[string]string{"x": "1"},
			after:  map[string]string{"x": ""},
			want:   []string{`x = "" after the call, "1" before`},
		},
		{
			name:   "an added variable with an empty value",
			before: map[string]string{},
			after:  map[string]string{"x": ""},
			want:   []string{`x added: "" after the call`},
		},
		{
			name:   "a removed variable whose value was empty",
			before: map[string]string{"x": ""},
			after:  map[string]string{},
			want:   []string{`x removed: "" before the call`},
		},
		{
			name:   "a removed variable with a value",
			before: map[string]string{"x": "1"},
			after:  map[string]string{},
			want:   []string{`x removed: "1" before the call`},
		},
		{
			name:   "one of each, in name order",
			before: map[string]string{"a": "1", "b": "", "c": "3"},
			after:  map[string]string{"a": "1", "c": "4", "d": ""},
			want: []string{
				`b removed: "" before the call`,
				`c = "4" after the call, "3" before`,
				`d added: "" after the call`,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := sessionVariableChanges(testCase.before, testCase.after); !slices.Equal(got, testCase.want) {
				t.Errorf("sessionVariableChanges(%q, %q) = %q, want %q",
					testCase.before, testCase.after, got, testCase.want)
			}
		})
	}
}
