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

func TestAccountDSNFromKeepsAddressAndReplacesCredentials(t *testing.T) {
	account := MySQLAccount{User: "probe", Host: "%", Password: "secret"}

	cfg := accountDSNFrom(t, "root:root@tcp(127.0.0.1:4184)/someschema?parseTime=true", account)

	if cfg.Addr != "127.0.0.1:4184" {
		t.Errorf("Addr = %q, want 127.0.0.1:4184", cfg.Addr)
	}
	if cfg.User != "probe" || cfg.Passwd != "secret" {
		t.Errorf("credentials = %q/%q, want probe/secret", cfg.User, cfg.Passwd)
	}
	if cfg.DBName != "" {
		t.Errorf("DBName = %q, want empty", cfg.DBName)
	}
	if !cfg.ParseTime {
		t.Error("ParseTime was dropped; driver settings must be kept")
	}
}
