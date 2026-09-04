package testsupport

import "testing"

func TestQuoteSQLStringEscapesBackslashAndQuote(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{in: "plain", want: "'plain'"},
		{in: "o'brien", want: "'o''brien'"},
		{in: `trailing\`, want: `'trailing\\'`},
		{in: `a\'b`, want: `'a\\''b'`},
	}
	for _, testCase := range cases {
		if got := quoteSQLString(testCase.in); got != testCase.want {
			t.Errorf("quoteSQLString(%q) = %s, want %s", testCase.in, got, testCase.want)
		}
	}
}
