package validations

import "testing"

func TestNormalizeColumnType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		columnType  string
		want        string
		explanation string
	}{
		{
			name: "int display width stripped", columnType: "int(11)", want: "int",
			explanation: "a column created before 8.0.17 must compare equal to one created after",
		},
		{
			name: "bigint width stripped, unsigned preserved",
			columnType: "bigint(20) unsigned", want: "bigint unsigned",
			explanation: "unsigned changes the value range and is a real difference",
		},
		{
			name: "zerofill preserved", columnType: "int(11) unsigned zerofill",
			want: "int unsigned zerofill",
			explanation: "zerofill is an attribute, not formatting noise",
		},
		{
			name: "tinyint(1) preserved", columnType: "tinyint(1)", want: "tinyint(1)",
			explanation: "BOOLEAN is an alias for TINYINT(1); stripping it would report a BOOLEAN and a plain TINYINT as identical",
		},
		{
			name: "tinyint(4) stripped", columnType: "tinyint(4)", want: "tinyint",
			explanation: "only width 1 carries the boolean meaning",
		},
		{
			name: "tinyint(1) unsigned preserved", columnType: "tinyint(1) unsigned",
			want: "tinyint(1) unsigned",
			explanation: "the carve-out survives trailing attributes",
		},
		{
			name: "smallint stripped", columnType: "smallint(6)", want: "smallint",
		},
		{
			name: "mediumint stripped", columnType: "mediumint(9)", want: "mediumint",
		},
		{
			name: "integer alias stripped", columnType: "integer(11)", want: "integer",
		},
		{
			name: "decimal untouched", columnType: "decimal(3,2)", want: "decimal(3,2)",
			explanation: "precision and scale are semantic, not display width",
		},
		{
			name: "varchar untouched", columnType: "varchar(50)", want: "varchar(50)",
		},
		{
			name: "enum untouched", columnType: "enum('a','b')", want: "enum('a','b')",
		},
		{
			name: "no parenthesis", columnType: "bigint unsigned", want: "bigint unsigned",
			explanation: "current servers already emit the bare form",
		},
		{name: "empty", columnType: "", want: ""},
		{
			name: "unterminated parenthesis", columnType: "int(11", want: "int(11",
			explanation: "malformed input is returned unchanged rather than guessed at",
		},
		{
			name: "non-numeric width", columnType: "int(x)", want: "int(x)",
		},
		{
			name: "uppercase base folded for matching", columnType: "INT(11)", want: "INT",
			explanation: "MySQL emits lowercase, but matching must not depend on that",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := normalizeColumnType(test.columnType); got != test.want {
				t.Errorf("normalizeColumnType(%q) = %q, want %q; %s",
					test.columnType, got, test.want, test.explanation)
			}
		})
	}
}
