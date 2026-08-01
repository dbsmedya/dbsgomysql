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
			explanation: "a column created before 8.0.19 must compare equal to one created after",
		},
		{
			name:       "bigint width stripped, unsigned preserved",
			columnType: "bigint(20) unsigned", want: "bigint unsigned",
			explanation: "unsigned changes the value range and is a real difference",
		},
		{
			name: "zerofill width preserved", columnType: "int(11) unsigned zerofill",
			want: "int(11) unsigned zerofill",
			explanation: "retrieved values are zero-padded to the display width, so the width " +
				"is semantic under ZEROFILL and MySQL keeps emitting it",
		},
		{
			name:       "zerofill width preserved at a narrower width",
			columnType: "int(5) unsigned zerofill", want: "int(5) unsigned zerofill",
			explanation: "int(5) zerofill renders 00042 and int(10) zerofill renders " +
				"0000000042; stripping both to int unsigned zerofill makes two different " +
				"client-visible schemas compare equal",
		},
		{
			name:       "zerofill matched case-insensitively",
			columnType: "INT(5) UNSIGNED ZEROFILL", want: "INT(5) UNSIGNED ZEROFILL",
			explanation: "MySQL emits lowercase, but matching must not depend on that",
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
			want:        "tinyint(1) unsigned",
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

func TestParseColumnExtra(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		extra string
		want  columnExtra
	}{
		{
			name: "empty", extra: "",
			want: columnExtra{generated: GeneratedNone},
		},
		{
			name: "auto increment", extra: "auto_increment",
			want: columnExtra{autoIncrement: true, generated: GeneratedNone},
		},
		{
			name: "expression default", extra: "DEFAULT_GENERATED",
			want: columnExtra{defaultIsExpression: true, generated: GeneratedNone},
		},
		{
			name: "on update timestamp", extra: "on update CURRENT_TIMESTAMP",
			want: columnExtra{onUpdate: true, generated: GeneratedNone},
		},
		{
			name:  "expression default with on update",
			extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP",
			want: columnExtra{
				defaultIsExpression: true, onUpdate: true, generated: GeneratedNone,
			},
		},
		{
			name: "invisible", extra: "INVISIBLE",
			want: columnExtra{invisible: true, generated: GeneratedNone},
		},
		{
			name: "virtual generated", extra: "VIRTUAL GENERATED",
			want: columnExtra{generated: GeneratedVirtual},
		},
		{
			name: "stored generated", extra: "STORED GENERATED",
			want: columnExtra{generated: GeneratedStored},
		},
		{
			name: "invisible virtual generated", extra: "VIRTUAL GENERATED INVISIBLE",
			want: columnExtra{invisible: true, generated: GeneratedVirtual},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := parseColumnExtra(test.extra); got != test.want {
				t.Errorf("parseColumnExtra(%q) = %+v, want %+v", test.extra, got, test.want)
			}
		})
	}
}

func TestParseColumnExtraDefaultGeneratedIsNotAGeneratedColumn(t *testing.T) {
	t.Parallel()

	got := parseColumnExtra("DEFAULT_GENERATED")
	if got.generated != GeneratedNone {
		t.Errorf("parseColumnExtra(\"DEFAULT_GENERATED\").generated = %v, want GeneratedNone; "+
			"an expression default is not a generated column, and DEFAULT_GENERATED contains "+
			"the substring GENERATED",
			got.generated)
	}
}

func TestGeneratedKindZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero GeneratedKind
	if zero != GeneratedUnknown {
		t.Errorf("the GeneratedKind zero value is %d, want GeneratedUnknown (%d); "+
			"an unpopulated ColumnSpec must be detectable rather than silently meaning "+
			"GeneratedNone",
			zero, GeneratedUnknown)
	}
}

func TestGeneratedKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind GeneratedKind
		want string
	}{
		{name: "unknown", kind: GeneratedUnknown, want: "unknown"},
		{name: "none", kind: GeneratedNone, want: "none"},
		{name: "virtual", kind: GeneratedVirtual, want: "virtual"},
		{name: "stored", kind: GeneratedStored, want: "stored"},
		{name: "undeclared", kind: GeneratedKind(99), want: "GeneratedKind(99)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.kind.String(); got != test.want {
				t.Errorf("GeneratedKind(%d).String() = %q, want %q", test.kind, got, test.want)
			}
		})
	}
}
