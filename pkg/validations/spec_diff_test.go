package validations

import (
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestDiffSpecsIdenticalSpecsProduceNoDiffs(t *testing.T) {
	t.Parallel()

	spec := TableSpec{
		Schema: "sakila", Table: "payment", Engine: "InnoDB",
		Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci",
	}

	if diffs := DiffSpecs(spec, spec); len(diffs) != 0 {
		t.Errorf("DiffSpecs on identical specs returned %d diffs, want 0: %+v",
			len(diffs), diffs)
	}
}

func TestDiffSpecsTableLevelMismatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*TableSpec)
		wantKind SpecDiffKind
		wantA    string
		wantB    string
	}{
		{
			name:     "engine",
			mutate:   func(s *TableSpec) { s.Engine = "MyISAM" },
			wantKind: EngineMismatch, wantA: "InnoDB", wantB: "MyISAM",
		},
		{
			name:     "charset",
			mutate:   func(s *TableSpec) { s.Charset = "latin1" },
			wantKind: CharsetMismatch, wantA: "utf8mb4", wantB: "latin1",
		},
		{
			name:     "collation",
			mutate:   func(s *TableSpec) { s.Collation = "utf8mb4_bin" },
			wantKind: CollationMismatch,
			wantA:    "utf8mb4_0900_ai_ci", wantB: "utf8mb4_bin",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			specA := TableSpec{
				Engine: "InnoDB", Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci",
			}
			specB := specA
			test.mutate(&specB)

			diffs := DiffSpecs(specA, specB)
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs returned %d diffs, want 1: %+v", len(diffs), diffs)
			}
			got := diffs[0]
			if got.Kind != test.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, test.wantKind)
			}
			if got.Side != SideBoth {
				t.Errorf("Side = %v, want SideBoth; both sides supplied a value", got.Side)
			}
			if got.A != test.wantA || got.B != test.wantB {
				t.Errorf("(A, B) = (%q, %q), want (%q, %q)",
					got.A, got.B, test.wantA, test.wantB)
			}
		})
	}
}

func TestDiffSpecsCommentRequiresBothSidesToOptIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		capturedA  SpecSections
		capturedB  SpecSections
		commentA   string
		commentB   string
		wantKind   SpecDiffKind
		wantSide   DiffSide
		wantNoDiff bool
	}{
		{
			name:      "both captured and equal",
			capturedA: SectionComment, capturedB: SectionComment,
			commentA: "ledger", commentB: "ledger",
			wantNoDiff: true,
		},
		{
			name:      "both captured and differing",
			capturedA: SectionComment, capturedB: SectionComment,
			commentA: "ledger", commentB: "archive",
			wantKind: CommentMismatch, wantSide: SideBoth,
		},
		{
			name:      "only A captured",
			capturedA: SectionComment, capturedB: 0,
			commentA: "ledger",
			wantKind: CommentUnconfirmed, wantSide: SideB,
		},
		{
			name:      "only B captured",
			capturedA: 0, capturedB: SectionComment,
			commentB: "ledger",
			wantKind: CommentUnconfirmed, wantSide: SideA,
		},
		{
			name:      "neither captured",
			capturedA: 0, capturedB: 0,
			wantNoDiff: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			specA := TableSpec{Comment: test.commentA, Captured: test.capturedA}
			specB := TableSpec{Comment: test.commentB, Captured: test.capturedB}

			diffs := DiffSpecs(specA, specB)
			if test.wantNoDiff {
				if len(diffs) != 0 {
					t.Fatalf("DiffSpecs returned %d diffs, want 0: %+v", len(diffs), diffs)
				}

				return
			}
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs returned %d diffs, want 1: %+v", len(diffs), diffs)
			}
			if diffs[0].Kind != test.wantKind || diffs[0].Side != test.wantSide {
				t.Errorf("(Kind, Side) = (%v, %v), want (%v, %v)",
					diffs[0].Kind, diffs[0].Side, test.wantKind, test.wantSide)
			}
		})
	}
}

func TestDiffSideZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero DiffSide
	if zero != SideUnknown {
		t.Errorf("the DiffSide zero value is %d, want SideUnknown (%d)", zero, SideUnknown)
	}
}

func specWithColumns(columns ...ColumnSpec) TableSpec {
	return TableSpec{Columns: columns}
}

func column(name string, ordinal int, columnType string) ColumnSpec {
	return ColumnSpec{
		Name: name, Ordinal: ordinal, Type: columnType,
		NormalizedType: normalizeColumnType(columnType), Generated: GeneratedNone,
	}
}

func TestDiffSpecsMatchesColumnsByName(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(
		column("id", 1, "int"),
		column("amount", 2, "decimal(5,2)"),
		column("created_at", 3, "datetime"),
	)
	specB := specWithColumns(
		column("id", 1, "int"),
		column("created_at", 2, "datetime"),
		column("amount", 3, "decimal(5,2)"),
	)

	diffs := DiffSpecs(specA, specB)
	for _, diff := range diffs {
		if diff.Kind == ColumnTypeMismatch {
			t.Errorf("reordering columns produced a ColumnTypeMismatch on %q; "+
				"columns must match by name, not position", diff.Column)
		}
	}

	orderDiffs := 0
	for _, diff := range diffs {
		if diff.Kind == ColumnOrderMismatch {
			orderDiffs++
		}
	}
	if orderDiffs != 2 {
		t.Errorf("got %d ColumnOrderMismatch diffs, want 2 (amount and created_at); "+
			"order matters to INSERT INTO dest SELECT * FROM src", orderDiffs)
	}
}

func TestDiffSpecsColumnAbsenceNamesTheSideThatLacksIt(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(column("id", 1, "int"), column("only_in_a", 2, "int"))
	specB := specWithColumns(column("id", 1, "int"), column("only_in_b", 2, "int"))

	diffs := DiffSpecs(specA, specB)

	found := map[string]DiffSide{}
	for _, diff := range diffs {
		if diff.Kind == ColumnAbsent {
			found[diff.Column] = diff.Side
		}
	}

	if found["only_in_a"] != SideB {
		t.Errorf("only_in_a absence reported Side %v, want SideB; "+
			"Side names the spec that lacks the column", found["only_in_a"])
	}
	if found["only_in_b"] != SideA {
		t.Errorf("only_in_b absence reported Side %v, want SideA", found["only_in_b"])
	}
}

func TestDiffSpecsDisplayWidthIsNotADifference(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(column("id", 1, "int(11)"))
	specB := specWithColumns(column("id", 1, "int"))

	if diffs := DiffSpecs(specA, specB); len(diffs) != 0 {
		t.Errorf("int(11) against int produced %d diffs, want 0; comparison uses "+
			"NormalizedType so an upgraded server compares equal: %+v", len(diffs), diffs)
	}

	specA = specWithColumns(column("id", 1, "int(11)"), column("y", 2, "year(4)"))
	specB = specWithColumns(column("id", 1, "int"), column("y", 2, "year"))
	if diffs := DiffSpecs(specA, specB); len(diffs) != 0 {
		t.Errorf("year(4) against year produced %d diffs, want 0: %+v", len(diffs), diffs)
	}
}

func TestDiffSpecsBooleanIsNotAPlainTinyint(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(column("flag", 1, "tinyint(1)"))
	specB := specWithColumns(column("flag", 1, "tinyint"))

	diffs := DiffSpecs(specA, specB)
	if len(diffs) != 1 || diffs[0].Kind != ColumnTypeMismatch {
		t.Fatalf("tinyint(1) against tinyint produced %+v, want one ColumnTypeMismatch; "+
			"BOOLEAN is an alias for TINYINT(1) and is a real difference", diffs)
	}
	if diffs[0].A != "tinyint(1)" || diffs[0].B != "tinyint" {
		t.Errorf("(A, B) = (%q, %q), want the raw COLUMN_TYPE values",
			diffs[0].A, diffs[0].B)
	}
}

func TestDiffSpecsZerofillWidthIsADifference(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(column("code", 1, "int(5) unsigned zerofill"))
	specB := specWithColumns(column("code", 1, "int(10) unsigned zerofill"))

	diffs := DiffSpecs(specA, specB)
	if len(diffs) != 1 || diffs[0].Kind != ColumnTypeMismatch {
		t.Fatalf("int(5) zerofill against int(10) zerofill produced %+v, want one "+
			"ColumnTypeMismatch; the two zero-pad retrieved values to different lengths "+
			"(00042 against 0000000042), so they are different client-visible schemas",
			diffs)
	}
	if diffs[0].A != "int(5) unsigned zerofill" || diffs[0].B != "int(10) unsigned zerofill" {
		t.Errorf("(A, B) = (%q, %q), want the raw COLUMN_TYPE values",
			diffs[0].A, diffs[0].B)
	}
}

func TestDiffSpecsColumnAttributeMismatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*ColumnSpec)
		wantKind SpecDiffKind
	}{
		{
			name:     "nullability",
			mutate:   func(c *ColumnSpec) { c.Nullable = true },
			wantKind: ColumnNullabilityMismatch,
		},
		{
			name:     "charset",
			mutate:   func(c *ColumnSpec) { c.Charset = "latin1" },
			wantKind: ColumnCharsetMismatch,
		},
		{
			name:     "collation",
			mutate:   func(c *ColumnSpec) { c.Collation = "utf8mb4_bin" },
			wantKind: ColumnCollationMismatch,
		},
		{
			name:     "invisible",
			mutate:   func(c *ColumnSpec) { c.Invisible = true },
			wantKind: ColumnVisibilityMismatch,
		},
		{
			name:     "generated kind",
			mutate:   func(c *ColumnSpec) { c.Generated = GeneratedStored },
			wantKind: ColumnGeneratedMismatch,
		},
		{
			name:     "generation expression",
			mutate:   func(c *ColumnSpec) { c.GenerationExpr = "(`a` + 1)" },
			wantKind: ColumnGenerationExprMismatch,
		},
		{
			name:     "auto increment",
			mutate:   func(c *ColumnSpec) { c.AutoIncrement = true },
			wantKind: ColumnAutoIncrementMismatch,
		},
		{
			name:     "on update",
			mutate:   func(c *ColumnSpec) { c.OnUpdate = true },
			wantKind: ColumnOnUpdateMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			base := ColumnSpec{
				Name: "c", Ordinal: 1, Type: "int", NormalizedType: "int",
				Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci",
				Generated: GeneratedNone,
			}
			changed := base
			test.mutate(&changed)

			diffs := DiffSpecs(specWithColumns(base), specWithColumns(changed))
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs returned %d diffs, want 1: %+v", len(diffs), diffs)
			}
			if diffs[0].Kind != test.wantKind {
				t.Errorf("Kind = %v, want %v", diffs[0].Kind, test.wantKind)
			}
			if diffs[0].Column != "c" {
				t.Errorf("Column = %q, want \"c\"", diffs[0].Column)
			}
		})
	}
}

func TestDiffSpecsExpressionDefaultDiffersFromLiteral(t *testing.T) {
	t.Parallel()

	literal := "curdate()"
	expression := "curdate()"

	specA := specWithColumns(ColumnSpec{
		Name: "d", Ordinal: 1, Type: "date", NormalizedType: "date",
		Default: &literal, DefaultIsExpression: false, Generated: GeneratedNone,
	})
	specB := specWithColumns(ColumnSpec{
		Name: "d", Ordinal: 1, Type: "date", NormalizedType: "date",
		Default: &expression, DefaultIsExpression: true, Generated: GeneratedNone,
	})

	diffs := DiffSpecs(specA, specB)
	if len(diffs) != 1 || diffs[0].Kind != ColumnDefaultMismatch {
		t.Fatalf("a literal default of %q against an expression default of the same "+
			"text produced %+v, want one ColumnDefaultMismatch; only DEFAULT_GENERATED "+
			"distinguishes them", literal, diffs)
	}
	want := SpecDiff{
		Kind: ColumnDefaultMismatch, Side: SideBoth, Column: "d",
		A: literal, B: expression, BIsExpression: true,
	}
	if diffs[0] != want {
		t.Errorf("default diff = %+v, want %+v", diffs[0], want)
	}
}

func TestDiffSpecsDefaultAbsenceNamesTheSideThatLacksIt(t *testing.T) {
	t.Parallel()

	empty := ""
	value := "x"

	tests := []struct {
		name     string
		a, b     *string
		wantSide DiffSide
	}{
		{name: "no default vs empty string", a: nil, b: &empty, wantSide: SideA},
		{name: "empty string vs no default", a: &empty, b: nil, wantSide: SideB},
		{name: "no default vs non-empty", a: nil, b: &value, wantSide: SideA},
		{name: "non-empty vs no default", a: &value, b: nil, wantSide: SideB},
		{name: "empty string vs non-empty", a: &empty, b: &value, wantSide: SideBoth},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			base := ColumnSpec{
				Name: "c", Ordinal: 1, Type: "varchar(10)",
				NormalizedType: "varchar(10)", Nullable: true,
				Generated: GeneratedNone,
			}
			columnA, columnB := base, base
			columnA.Default = test.a
			columnB.Default = test.b

			diffs := DiffSpecs(specWithColumns(columnA), specWithColumns(columnB))
			if len(diffs) != 1 || diffs[0].Kind != ColumnDefaultMismatch {
				t.Fatalf("DiffSpecs returned %+v, want one ColumnDefaultMismatch",
					diffs)
			}
			if diffs[0].Side != test.wantSide {
				t.Errorf("Side = %v, want %v; SideA and SideB name the spec "+
					"that has no default at all", diffs[0].Side, test.wantSide)
			}
		})
	}
}

func TestDiffSpecsNilDefaultsAreEqualRegardlessOfExpressionFlag(t *testing.T) {
	t.Parallel()

	base := ColumnSpec{
		Name: "c", Ordinal: 1, Type: "date", NormalizedType: "date",
		Nullable: true, Generated: GeneratedNone,
	}
	flagged := base
	flagged.DefaultIsExpression = true

	if diffs := DiffSpecs(specWithColumns(base), specWithColumns(flagged)); len(diffs) != 0 {
		t.Errorf("DiffSpecs returned %+v, want none; DefaultIsExpression "+
			"qualifies Default's text, so with no text on either side there "+
			"is nothing to differ on", diffs)
	}
}

func TestSpecDiffJSONPreservesDefaultAbsenceOrientation(t *testing.T) {
	t.Parallel()

	empty := ""
	base := ColumnSpec{
		Name: "c", Ordinal: 1, Type: "varchar(10)",
		NormalizedType: "varchar(10)", Nullable: true, Generated: GeneratedNone,
	}
	withEmpty := base
	withEmpty.Default = &empty

	// The numeric literals are the point: this pins the wire format, so a
	// renumbering of the kind or side vocabulary must break it. 11 is
	// ColumnDefaultMismatch; 1 and 2 are SideA and SideB.
	tests := []struct {
		name string
		a, b ColumnSpec
		want string
	}{
		{
			name: "absence on a",
			a:    base, b: withEmpty,
			want: `{"kind":11,"side":1,"column":"c"}`,
		},
		{
			name: "absence on b",
			a:    withEmpty, b: base,
			want: `{"kind":11,"side":2,"column":"c"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			diffs := DiffSpecs(specWithColumns(test.a), specWithColumns(test.b))
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs returned %+v, want one diff", diffs)
			}
			got, err := json.Marshal(diffs[0])
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != test.want {
				t.Errorf("JSON = %s, want %s; the two orientations must stay "+
					"distinguishable after serialization", got, test.want)
			}
		})
	}
}

func TestDiffSpecsColumnOutputOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(
		column("zeta", 1, "int"), column("alpha", 2, "int"), column("only_a", 3, "int"),
	)
	specB := specWithColumns(
		column("zeta", 1, "varchar(1)"), column("alpha", 2, "varchar(1)"),
		column("Zz_only_b", 3, "int"), column("aa_only_b", 4, "int"),
	)

	first := DiffSpecs(specA, specB)
	for range 5 {
		if got := DiffSpecs(specA, specB); !sameDiffs(first, got) {
			t.Fatal("DiffSpecs returned different output for the same input; " +
				"golden comparisons require deterministic order")
		}
	}

	var columnsInOrder []string
	for _, diff := range first {
		if diff.Column != "" {
			columnsInOrder = append(columnsInOrder, diff.Column)
		}
	}
	// a's ordinal order first, then b-only columns sorted by name.
	want := []string{"zeta", "alpha", "only_a", "aa_only_b", "Zz_only_b"}
	if len(columnsInOrder) != len(want) {
		t.Fatalf("column diffs = %v, want %v", columnsInOrder, want)
	}
	for i := range want {
		if columnsInOrder[i] != want[i] {
			t.Fatalf("column diffs = %v, want %v", columnsInOrder, want)
		}
	}
}

func TestDiffSpecsFoldsColumnNameCase(t *testing.T) {
	t.Parallel()
	a := specWithColumns(column("Id", 1, "int"))
	b := specWithColumns(column("id", 1, "bigint"))
	want := []SpecDiff{
		{Kind: ColumnNameCaseMismatch, Side: SideBoth, Column: "Id", A: "Id", B: "id"},
		{Kind: ColumnTypeMismatch, Side: SideBoth, Column: "Id", A: "int", B: "bigint"},
	}
	if got := DiffSpecs(a, b); !sameDiffs(got, want) {
		t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
	}
}

func TestDiffSpecsFoldIsASCIIOnly(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"\u212Aey", "key"}, {"\u0131d", "id"}} {
		t.Run(pair[1], func(t *testing.T) {
			t.Parallel()
			a := specWithColumns(column(pair[0], 1, "int"))
			b := specWithColumns(column(pair[1], 1, "int"))
			want := []SpecDiff{
				{Kind: ColumnAbsent, Side: SideB, Column: pair[0], A: "int"},
				{Kind: ColumnAbsent, Side: SideA, Column: pair[1], B: "int"},
			}
			if got := DiffSpecs(a, b); !sameDiffs(got, want) {
				t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDiffSpecsCaseCollisionMatchesExactly(t *testing.T) {
	t.Parallel()
	collision := specWithColumns(column("Id", 1, "int"), column("id", 1, "int"))
	single := specWithColumns(column("id", 1, "int"))
	for _, tc := range []struct {
		name string
		a, b TableSpec
		want SpecDiff
	}{
		{"collision on a", collision, single, SpecDiff{Kind: ColumnAbsent, Side: SideB, Column: "Id", A: "int"}},
		{"collision on b", single, collision, SpecDiff{Kind: ColumnAbsent, Side: SideA, Column: "Id", B: "int"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DiffSpecs(tc.a, tc.b); !sameDiffs(got, []SpecDiff{tc.want}) {
				t.Fatalf("DiffSpecs = %+v, want only %+v", got, tc.want)
			}
		})
	}
}

func TestDiffSpecsIndexCaseCollisionMatchesExactly(t *testing.T) {
	t.Parallel()
	upper := IndexSpec{Name: "Idx", Parts: []IndexPart{{Column: "x"}}}
	lower := IndexSpec{Name: "idx", Parts: []IndexPart{{Column: "x"}}}
	collision := TableSpec{Indexes: []IndexSpec{upper, lower}, Captured: SectionIndexes}
	single := TableSpec{Indexes: []IndexSpec{lower}, Captured: SectionIndexes}
	for _, tc := range []struct {
		name string
		a, b TableSpec
		side DiffSide
	}{
		{"collision on a", collision, single, SideB},
		{"collision on b", single, collision, SideA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := []SpecDiff{{Kind: IndexAbsent, Side: tc.side, Index: "Idx"}}
			if got := DiffSpecs(tc.a, tc.b); !sameDiffs(got, want) {
				t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDiffSpecsFoldsIndexNameCaseAndParts(t *testing.T) {
	t.Parallel()
	a := specWithColumns(column("id", 1, "int"))
	b := specWithColumns(column("id", 1, "int"))
	a.Captured, b.Captured = SectionIndexes, SectionIndexes
	a.Indexes = []IndexSpec{{Name: "Idx", Parts: []IndexPart{{Column: "Id"}}}}
	b.Indexes = []IndexSpec{{Name: "idx", Parts: []IndexPart{{Column: "id"}}}}
	want := []SpecDiff{{Kind: IndexNameCaseMismatch, Side: SideBoth, Index: "Idx", A: "Idx", B: "idx"}}
	if got := DiffSpecs(a, b); !sameDiffs(got, want) {
		t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
	}
}

func TestDiffSpecsIndexPartsFoldColumnOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		a, b         IndexPart
		textA, textB string
	}{
		{"expression", IndexPart{Expression: "lower(a)"}, IndexPart{Expression: "LOWER(a)"}, "(lower(a))", "(LOWER(a))"},
		{"prefix", IndexPart{Column: "a", SubPart: 10}, IndexPart{Column: "a", SubPart: 20}, "a(10)", "a(20)"},
		{"direction", IndexPart{Column: "a"}, IndexPart{Column: "a", Descending: true}, "a", "a DESC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := TableSpec{Indexes: []IndexSpec{{Name: "idx", Parts: []IndexPart{tc.a}}}, Captured: SectionIndexes}
			b := TableSpec{Indexes: []IndexSpec{{Name: "idx", Parts: []IndexPart{tc.b}}}, Captured: SectionIndexes}
			want := []SpecDiff{{Kind: IndexPartsMismatch, Side: SideBoth, Index: "idx", A: tc.textA, B: tc.textB}}
			if got := DiffSpecs(a, b); !sameDiffs(got, want) {
				t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
			}
		})
	}
}

func defaultColumn(value *string, expression bool) ColumnSpec {
	return ColumnSpec{Name: "d", Default: value, DefaultIsExpression: expression}
}

func TestDiffSpecsDefaultTextIsNotDecorated(t *testing.T) {
	t.Parallel()
	literal, expression := "(curdate())", "curdate()"
	a := specWithColumns(defaultColumn(&literal, false))
	b := specWithColumns(defaultColumn(&expression, true))
	want := []SpecDiff{{Kind: ColumnDefaultMismatch, Side: SideBoth, Column: "d",
		A: literal, B: expression, BIsExpression: true}}
	if got := DiffSpecs(a, b); !sameDiffs(got, want) {
		t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
	}
}

func TestDiffSpecsExpressionFlagsFollowTheirSide(t *testing.T) {
	t.Parallel()
	value, literal := "curdate()", "x"
	for _, tc := range []struct {
		name string
		a, b ColumnSpec
		want SpecDiff
	}{
		{"expression on a", defaultColumn(&value, true), defaultColumn(&value, false),
			SpecDiff{Kind: ColumnDefaultMismatch, Side: SideBoth, Column: "d", A: value, B: value, AIsExpression: true}},
		{"flagged nil control", defaultColumn(nil, true), defaultColumn(&literal, false),
			SpecDiff{Kind: ColumnDefaultMismatch, Side: SideA, Column: "d", B: literal}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DiffSpecs(specWithColumns(tc.a), specWithColumns(tc.b)); !sameDiffs(got, []SpecDiff{tc.want}) {
				t.Fatalf("DiffSpecs = %+v, want only %+v", got, tc.want)
			}
		})
	}
}

func TestSpecDiffJSONCarriesExpressionFlags(t *testing.T) {
	t.Parallel()
	literal, expression, x := "(curdate())", "curdate()", "x"
	for _, tc := range []struct {
		name string
		a, b ColumnSpec
		want string
	}{
		{"expression on b", defaultColumn(&literal, false), defaultColumn(&expression, true),
			`{"kind":11,"side":3,"column":"d","a":"(curdate())","b":"curdate()","b_is_expression":true}`},
		{"expression on a", defaultColumn(&expression, true), defaultColumn(&expression, false),
			`{"kind":11,"side":3,"column":"d","a":"curdate()","b":"curdate()","a_is_expression":true}`},
		{"flagged nil control", defaultColumn(nil, true), defaultColumn(&x, false),
			`{"kind":11,"side":1,"column":"d","b":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diffs := DiffSpecs(specWithColumns(tc.a), specWithColumns(tc.b))
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs = %+v, want one diff", diffs)
			}
			got, err := json.Marshal(diffs[0])
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("JSON = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDiffSpecsUnknownConstraintKindsAreUnconfirmed(t *testing.T) {
	t.Parallel()
	a := TableSpec{Constraints: []ConstraintSpec{{Name: "c"}}, Captured: SectionConstraints}
	b := TableSpec{Constraints: []ConstraintSpec{{Name: "c"}}, Captured: SectionConstraints}
	want := []SpecDiff{{Kind: ConstraintKindUnconfirmed, Side: SideBoth, Index: "c"}}
	if got := DiffSpecs(a, b); !sameDiffs(got, want) {
		t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
	}
	b.Constraints[0].Kind = ConstraintCheck
	want = []SpecDiff{{Kind: ConstraintKindMismatch, Side: SideBoth, Index: "c", A: "unknown", B: "check"}}
	if got := DiffSpecs(a, b); !sameDiffs(got, want) {
		t.Fatalf("DiffSpecs = %+v, want %+v", got, want)
	}
}

func sameDiffs(a, b []SpecDiff) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestDiffSpecsIndexesRequireBothSidesToOptIn(t *testing.T) {
	t.Parallel()

	index := IndexSpec{
		Name: "idx_email", Parts: []IndexPart{{Column: "email"}}, Unique: true,
		Type: "BTREE", Visible: true,
	}

	specA := TableSpec{Indexes: []IndexSpec{index}, Captured: SectionIndexes}
	specB := TableSpec{}

	diffs := DiffSpecs(specA, specB)
	if len(diffs) != 1 {
		t.Fatalf("DiffSpecs returned %d diffs, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].Kind != IndexUnconfirmed || diffs[0].Side != SideB {
		t.Errorf("(Kind, Side) = (%v, %v), want (IndexUnconfirmed, SideB); "+
			"a spec captured without WithIndexes cannot prove index agreement",
			diffs[0].Kind, diffs[0].Side)
	}
}

func TestDiffSpecsIndexesUncapturedOnBothSidesIsSilent(t *testing.T) {
	t.Parallel()

	if diffs := DiffSpecs(TableSpec{}, TableSpec{}); len(diffs) != 0 {
		t.Errorf("neither side captured indexes and DiffSpecs returned %+v, want none; "+
			"a question nobody asked is not a gap", diffs)
	}
}

func TestDiffSpecsIndexDifferences(t *testing.T) {
	t.Parallel()

	base := IndexSpec{
		Name:   "idx",
		Parts:  []IndexPart{{Column: "a"}, {Column: "b"}},
		Unique: true, Type: "BTREE", Visible: true,
	}

	tests := []struct {
		name     string
		mutate   func(*IndexSpec)
		wantKind SpecDiffKind
	}{
		{
			name: "part order",
			mutate: func(i *IndexSpec) {
				i.Parts = []IndexPart{{Column: "b"}, {Column: "a"}}
			},
			wantKind: IndexPartsMismatch,
		},
		{
			name: "prefix length",
			mutate: func(i *IndexSpec) {
				i.Parts = []IndexPart{{Column: "a", SubPart: 10}, {Column: "b"}}
			},
			wantKind: IndexPartsMismatch,
		},
		{
			name: "direction",
			mutate: func(i *IndexSpec) {
				i.Parts = []IndexPart{{Column: "a"}, {Column: "b", Descending: true}}
			},
			wantKind: IndexPartsMismatch,
		},
		{
			name: "functional expression",
			mutate: func(i *IndexSpec) {
				i.Parts = []IndexPart{{Expression: "(`a` + 1)"}, {Column: "b"}}
			},
			wantKind: IndexPartsMismatch,
		},
		{
			name:     "uniqueness",
			mutate:   func(i *IndexSpec) { i.Unique = false },
			wantKind: IndexUniquenessMismatch,
		},
		{
			name:     "type",
			mutate:   func(i *IndexSpec) { i.Type = "HASH" },
			wantKind: IndexTypeMismatch,
		},
		{
			name:     "visibility",
			mutate:   func(i *IndexSpec) { i.Visible = false },
			wantKind: IndexVisibilityMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			changed := base
			changed.Parts = append([]IndexPart(nil), base.Parts...)
			test.mutate(&changed)

			specA := TableSpec{Indexes: []IndexSpec{base}, Captured: SectionIndexes}
			specB := TableSpec{Indexes: []IndexSpec{changed}, Captured: SectionIndexes}

			diffs := DiffSpecs(specA, specB)
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs returned %d diffs, want 1: %+v", len(diffs), diffs)
			}
			if diffs[0].Kind != test.wantKind {
				t.Errorf("Kind = %v, want %v", diffs[0].Kind, test.wantKind)
			}
			if diffs[0].Index != "idx" {
				t.Errorf("Index = %q, want \"idx\"", diffs[0].Index)
			}
		})
	}
}

func TestDiffSpecsIndexAbsenceNamesTheSideThatLacksIt(t *testing.T) {
	t.Parallel()

	specA := TableSpec{
		Indexes: []IndexSpec{{
			Name: "only_a", Parts: []IndexPart{{Column: "x"}},
			Type: "BTREE", Visible: true,
		}},
		Captured: SectionIndexes,
	}
	specB := TableSpec{
		Indexes: []IndexSpec{{
			Name: "only_b", Parts: []IndexPart{{Column: "x"}},
			Type: "BTREE", Visible: true,
		}},
		Captured: SectionIndexes,
	}

	found := map[string]DiffSide{}
	for _, diff := range DiffSpecs(specA, specB) {
		if diff.Kind == IndexAbsent {
			found[diff.Index] = diff.Side
		}
	}
	if found["only_a"] != SideB || found["only_b"] != SideA {
		t.Errorf("index absence sides = %v, want only_a→SideB and only_b→SideA", found)
	}
}

func TestDiffSpecsConstraintDifferences(t *testing.T) {
	t.Parallel()

	check := ConstraintSpec{
		Name: "chk_age", Kind: ConstraintCheck, CheckClause: "(`age` >= 16)",
		Enforced: true,
	}
	foreignKey := ConstraintSpec{
		Name: "fk_course", Kind: ConstraintForeignKey,
		Columns: []string{"course_id"}, RefSchema: "s", RefTable: "courses",
		RefColumns: []string{"id"}, UpdateRule: "CASCADE", DeleteRule: "SET NULL",
		Enforced: true,
	}

	tests := []struct {
		name     string
		a, b     ConstraintSpec
		wantKind SpecDiffKind
	}{
		{
			name: "check clause",
			a:    check,
			b: ConstraintSpec{
				Name: "chk_age", Kind: ConstraintCheck, CheckClause: "(`age` >= 18)",
				Enforced: true,
			},
			wantKind: CheckClauseMismatch,
		},
		{
			// Identical clause, opposite enforcement: one server validates the
			// data and the other does not.
			name: "check enforcement",
			a:    check,
			b: ConstraintSpec{
				Name: "chk_age", Kind: ConstraintCheck, CheckClause: "(`age` >= 16)",
				Enforced: false,
			},
			wantKind: CheckEnforcementMismatch,
		},
		{
			name: "foreign key columns",
			a:    foreignKey,
			b: func() ConstraintSpec {
				changed := foreignKey
				changed.Columns = []string{"other_id"}
				return changed
			}(),
			wantKind: ForeignKeyColumnsMismatch,
		},
		{
			name: "foreign key reference",
			a:    foreignKey,
			b: func() ConstraintSpec {
				changed := foreignKey
				changed.RefTable = "programs"
				return changed
			}(),
			wantKind: ForeignKeyReferenceMismatch,
		},
		{
			name: "foreign key rule",
			a:    foreignKey,
			b: func() ConstraintSpec {
				changed := foreignKey
				changed.DeleteRule = "CASCADE"
				return changed
			}(),
			wantKind: ForeignKeyRuleMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			specA := TableSpec{
				Constraints: []ConstraintSpec{test.a}, Captured: SectionConstraints,
			}
			specB := TableSpec{
				Constraints: []ConstraintSpec{test.b}, Captured: SectionConstraints,
			}

			diffs := DiffSpecs(specA, specB)
			if len(diffs) != 1 {
				t.Fatalf("DiffSpecs returned %d diffs, want 1: %+v", len(diffs), diffs)
			}
			if diffs[0].Kind != test.wantKind {
				t.Errorf("Kind = %v, want %v", diffs[0].Kind, test.wantKind)
			}
		})
	}
}

func TestDiffSpecsConstraintKindChangeIsReportedAlone(t *testing.T) {
	t.Parallel()

	// The same name is a CHECK on one server and a FOREIGN KEY on the other.
	check := ConstraintSpec{
		Name: "c_orders", Kind: ConstraintCheck, CheckClause: "(`total` >= 0)",
	}
	foreignKey := ConstraintSpec{
		Name: "c_orders", Kind: ConstraintForeignKey,
		Columns: []string{"customer_id"}, RefSchema: "s", RefTable: "customers",
		RefColumns: []string{"id"}, UpdateRule: "CASCADE", DeleteRule: "RESTRICT",
	}

	specA := TableSpec{
		Constraints: []ConstraintSpec{check}, Captured: SectionConstraints,
	}
	specB := TableSpec{
		Constraints: []ConstraintSpec{foreignKey}, Captured: SectionConstraints,
	}

	diffs := DiffSpecs(specA, specB)
	if len(diffs) != 1 {
		t.Fatalf("DiffSpecs returned %d diffs, want exactly 1: %+v; comparing a "+
			"CHECK against a FOREIGN KEY field by field reports differences in "+
			"attributes neither side shares", len(diffs), diffs)
	}
	if diffs[0].Kind != ConstraintKindMismatch {
		t.Errorf("Kind = %v, want ConstraintKindMismatch", diffs[0].Kind)
	}
	if diffs[0].A != "check" || diffs[0].B != "foreign_key" {
		t.Errorf("(A, B) = (%q, %q), want (\"check\", \"foreign_key\")",
			diffs[0].A, diffs[0].B)
	}
}

func TestDiffSpecsIgnoresFieldsIrrelevantToConstraintKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b ConstraintSpec
	}{
		{
			name: "check ignores foreign-key payload",
			a: ConstraintSpec{
				Name: "c", Kind: ConstraintCheck,
				CheckClause: "(`a` > 0)", Enforced: true,
			},
			b: ConstraintSpec{
				Name: "c", Kind: ConstraintCheck,
				CheckClause: "(`a` > 0)", Enforced: true,
				Columns: []string{"other_id"}, RefSchema: "s",
				RefTable: "other", RefColumns: []string{"id"},
				UpdateRule: "CASCADE", DeleteRule: "RESTRICT",
			},
		},
		{
			name: "foreign key ignores check payload",
			a: ConstraintSpec{
				Name: "c", Kind: ConstraintForeignKey,
				Columns: []string{"other_id"}, RefSchema: "s",
				RefTable: "other", RefColumns: []string{"id"},
				UpdateRule: "CASCADE", DeleteRule: "RESTRICT",
				Enforced: true,
			},
			b: ConstraintSpec{
				Name: "c", Kind: ConstraintForeignKey,
				CheckClause: "(`a` > 0)",
				Columns:     []string{"other_id"}, RefSchema: "s",
				RefTable: "other", RefColumns: []string{"id"},
				UpdateRule: "CASCADE", DeleteRule: "RESTRICT",
				Enforced: false,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			specA := TableSpec{
				Constraints: []ConstraintSpec{test.a}, Captured: SectionConstraints,
			}
			specB := TableSpec{
				Constraints: []ConstraintSpec{test.b}, Captured: SectionConstraints,
			}
			if diffs := DiffSpecs(specA, specB); len(diffs) != 0 {
				t.Errorf("DiffSpecs returned irrelevant payload diffs: %+v", diffs)
			}
		})
	}
}

func TestDiffSpecsConstraintsRequireBothSidesToOptIn(t *testing.T) {
	t.Parallel()

	specA := TableSpec{
		Constraints: []ConstraintSpec{{Name: "chk", Kind: ConstraintCheck}},
		Captured:    SectionConstraints,
	}

	diffs := DiffSpecs(specA, TableSpec{})
	if len(diffs) != 1 ||
		diffs[0].Kind != ConstraintUnconfirmed || diffs[0].Side != SideB {
		t.Fatalf("DiffSpecs returned %+v, want one ConstraintUnconfirmed on SideB", diffs)
	}
}

// wantAllSpecDiffKinds is transcribed from the iota block at spec_diff.go:57-90,
// in declaration order. It is the independent witness for
// TestAllSpecDiffKindsMatchesTheDeclaredVocabulary: a test derived from the
// sentinel cannot prove the sentinel is right, so this list is hand-maintained
// on purpose. A stale copy fails the build on the commit that adds a kind,
// which is the point.
var wantAllSpecDiffKinds = []SpecDiffKind{
	EngineMismatch, CharsetMismatch, CollationMismatch, CommentMismatch,
	CommentUnconfirmed,

	ColumnAbsent, ColumnTypeMismatch, ColumnNullabilityMismatch,
	ColumnCharsetMismatch, ColumnCollationMismatch, ColumnDefaultMismatch,
	ColumnOrderMismatch, ColumnVisibilityMismatch, ColumnGeneratedMismatch,
	ColumnGenerationExprMismatch, ColumnAutoIncrementMismatch,
	ColumnOnUpdateMismatch,

	IndexUnconfirmed, IndexAbsent, IndexPartsMismatch, IndexUniquenessMismatch,
	IndexTypeMismatch, IndexVisibilityMismatch,

	ConstraintUnconfirmed, ConstraintAbsent, ConstraintKindMismatch,
	CheckClauseMismatch, CheckEnforcementMismatch, ForeignKeyColumnsMismatch,
	ForeignKeyReferenceMismatch, ForeignKeyRuleMismatch,
	ColumnNameCaseMismatch, IndexNameCaseMismatch, ConstraintKindUnconfirmed,
}

// TestSpecDiffKindValuesAreStable pins the serialized integer vocabulary.
func TestSpecDiffKindValuesAreStable(t *testing.T) {
	t.Parallel()

	// Numeric values are the wire contract, independent of the iota block.
	want := map[SpecDiffKind]int{
		EngineMismatch: 1, CharsetMismatch: 2, CollationMismatch: 3,
		CommentMismatch: 4, CommentUnconfirmed: 5, ColumnAbsent: 6,
		ColumnTypeMismatch: 7, ColumnNullabilityMismatch: 8,
		ColumnCharsetMismatch: 9, ColumnCollationMismatch: 10,
		ColumnDefaultMismatch: 11, ColumnOrderMismatch: 12,
		ColumnVisibilityMismatch: 13, ColumnGeneratedMismatch: 14,
		ColumnGenerationExprMismatch: 15, ColumnAutoIncrementMismatch: 16,
		ColumnOnUpdateMismatch: 17, IndexUnconfirmed: 18, IndexAbsent: 19,
		IndexPartsMismatch: 20, IndexUniquenessMismatch: 21,
		IndexTypeMismatch: 22, IndexVisibilityMismatch: 23,
		ConstraintUnconfirmed: 24, ConstraintAbsent: 25,
		ConstraintKindMismatch: 26, CheckClauseMismatch: 27,
		CheckEnforcementMismatch: 28, ForeignKeyColumnsMismatch: 29,
		ForeignKeyReferenceMismatch: 30, ForeignKeyRuleMismatch: 31,
		ColumnNameCaseMismatch: 32, IndexNameCaseMismatch: 33,
		ConstraintKindUnconfirmed: 34,
	}
	for kind, value := range want {
		if int(kind) != value {
			t.Errorf("%s = %d, want %d", kind, kind, value)
		}
	}
	kinds := AllSpecDiffKinds()
	if len(kinds) != 34 {
		t.Fatalf("kind count = %d, want 34", len(kinds))
	}
	if !slices.Equal(kinds[31:], []SpecDiffKind{
		ColumnNameCaseMismatch, IndexNameCaseMismatch, ConstraintKindUnconfirmed,
	}) {
		t.Errorf("new kinds = %v, want case-column, case-index, unknown-constraint", kinds[31:])
	}
}

// TestAllSpecDiffKindsMatchesTheDeclaredVocabulary asserts that
// AllSpecDiffKinds returns exactly the 34 published kinds, in declaration
// order. It catches a kind added before the sentinel, removed, or reordered.
func TestAllSpecDiffKindsMatchesTheDeclaredVocabulary(t *testing.T) {
	t.Parallel()

	got := AllSpecDiffKinds()
	if !slices.Equal(got, wantAllSpecDiffKinds) {
		t.Errorf("AllSpecDiffKinds() = %v, want %v", got, wantAllSpecDiffKinds)
	}
}

// TestSpecDiffKindVocabularyIsDeclaredInOneTerminatedBlock type-checks every
// non-test file in this package directory and asserts that (i) every constant
// whose semantic type is SpecDiffKind is declared inside the canonical const
// block — the one declaring SpecDiffUnknown — and (ii) specDiffKindCount is
// that block's last ValueSpec.
//
// A SpecDiffKind constant declared outside the enumerated range — after the
// sentinel, in a second const block, in another file, or with an inferred or
// aliased type — is invisible to AllSpecDiffKinds and to any value-based
// assertion, because its value is never produced and nothing compares against
// it. Only reading declarations can catch it, which is why this test
// type-checks the package instead of comparing constant values.
func TestSpecDiffKindVocabularyIsDeclaredInOneTerminatedBlock(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) did not report a file")
	}
	dir := filepath.Dir(thisFile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q): %v", dir, err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	var canonical *ast.GenDecl

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parser.ParseFile(%q): %v", name, parseErr)
		}
		files = append(files, file)

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, ident := range valueSpec.Names {
					if ident.Name != "SpecDiffUnknown" {
						continue
					}
					if canonical != nil {
						t.Fatalf("more than one const block declares SpecDiffUnknown")
					}
					canonical = genDecl
				}
			}
		}
	}

	if canonical == nil {
		t.Fatal("no const block declares SpecDiffUnknown")
	}

	var lastSpec *ast.ValueSpec
	for _, spec := range canonical.Specs {
		if valueSpec, ok := spec.(*ast.ValueSpec); ok {
			lastSpec = valueSpec
		}
	}
	if lastSpec == nil || len(lastSpec.Names) != 1 ||
		lastSpec.Names[0].Name != "specDiffKindCount" {
		// Errorf, not Fatalf: (i) below is an independent assertion and should
		// report in the same run rather than being hidden by this one.
		t.Errorf("canonical block's last ValueSpec = %v, want specDiffKindCount",
			lastSpec)
	}

	info := &types.Info{Defs: make(map[*ast.Ident]types.Object)}
	cfg := &types.Config{
		Importer:         importer.ForCompiler(fset, "source", nil),
		IgnoreFuncBodies: true,
	}
	pkg, err := cfg.Check(
		"github.com/dbsmedya/dbsgomysql/pkg/validations", fset, files, info,
	)
	if err != nil {
		t.Fatalf("type-checking pkg/validations: %v", err)
	}

	start, end := canonical.Pos(), canonical.End()
	for ident, obj := range info.Defs {
		constObj, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		named, ok := types.Unalias(constObj.Type()).(*types.Named)
		if !ok || named.Obj().Pkg() != pkg || named.Obj().Name() != "SpecDiffKind" {
			continue
		}
		if ident.Pos() < start || ident.Pos() >= end {
			t.Errorf("%s: constant %s has type SpecDiffKind but is declared "+
				"outside the canonical block",
				fset.Position(ident.Pos()), ident.Name)
		}
	}
}

// wantSpecDiffKindStrings is the independent witness for
// TestSpecDiffKindStringMatchesTheDeclaredNames: a test derived from the
// switch cannot prove the switch is right, so this map is hand-maintained on
// purpose. A kind added without a matching entry here, or a name changed on
// only one side, fails the count or the per-kind comparison.
var wantSpecDiffKindStrings = map[SpecDiffKind]string{
	EngineMismatch:               "engine_mismatch",
	CharsetMismatch:              "charset_mismatch",
	CollationMismatch:            "collation_mismatch",
	CommentMismatch:              "comment_mismatch",
	CommentUnconfirmed:           "comment_unconfirmed",
	ColumnAbsent:                 "column_absent",
	ColumnTypeMismatch:           "column_type_mismatch",
	ColumnNullabilityMismatch:    "column_nullability_mismatch",
	ColumnCharsetMismatch:        "column_charset_mismatch",
	ColumnCollationMismatch:      "column_collation_mismatch",
	ColumnDefaultMismatch:        "column_default_mismatch",
	ColumnOrderMismatch:          "column_order_mismatch",
	ColumnVisibilityMismatch:     "column_visibility_mismatch",
	ColumnGeneratedMismatch:      "column_generated_mismatch",
	ColumnGenerationExprMismatch: "column_generation_expr_mismatch",
	ColumnAutoIncrementMismatch:  "column_auto_increment_mismatch",
	ColumnOnUpdateMismatch:       "column_on_update_mismatch",
	IndexUnconfirmed:             "index_unconfirmed",
	IndexAbsent:                  "index_absent",
	IndexPartsMismatch:           "index_parts_mismatch",
	IndexUniquenessMismatch:      "index_uniqueness_mismatch",
	IndexTypeMismatch:            "index_type_mismatch",
	IndexVisibilityMismatch:      "index_visibility_mismatch",
	ConstraintUnconfirmed:        "constraint_unconfirmed",
	ConstraintAbsent:             "constraint_absent",
	ConstraintKindMismatch:       "constraint_kind_mismatch",
	CheckClauseMismatch:          "check_clause_mismatch",
	CheckEnforcementMismatch:     "check_enforcement_mismatch",
	ForeignKeyColumnsMismatch:    "foreign_key_columns_mismatch",
	ForeignKeyReferenceMismatch:  "foreign_key_reference_mismatch",
	ForeignKeyRuleMismatch:       "foreign_key_rule_mismatch",
	ColumnNameCaseMismatch:       "column_name_case_mismatch",
	IndexNameCaseMismatch:        "index_name_case_mismatch",
	ConstraintKindUnconfirmed:    "constraint_kind_unconfirmed",
}

// TestSpecDiffKindStringMatchesTheDeclaredNames asserts that String() matches
// a hand-written map of all 34 published kinds, and that the map itself has
// exactly 34 entries. A kind added without a case falls to the default and
// fails the per-kind comparison; a name changed on only one side fails too.
func TestSpecDiffKindStringMatchesTheDeclaredNames(t *testing.T) {
	t.Parallel()

	if len(wantSpecDiffKindStrings) != 34 {
		t.Fatalf("wantSpecDiffKindStrings has %d entries, want 34",
			len(wantSpecDiffKindStrings))
	}

	for _, kind := range AllSpecDiffKinds() {
		want, ok := wantSpecDiffKindStrings[kind]
		if !ok {
			t.Errorf("SpecDiffKind %d has no entry in wantSpecDiffKindStrings", kind)

			continue
		}
		if got := kind.String(); got != want {
			t.Errorf("SpecDiffKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}

// TestSpecDiffKindStringsAreDistinct asserts that the 34 returned strings are
// pairwise distinct. A hand-written map that agrees with a switch duplicating
// one string on two kinds would still pass
// TestSpecDiffKindStringMatchesTheDeclaredNames, so this is the only case that
// catches that mutation.
func TestSpecDiffKindStringsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]SpecDiffKind, len(wantSpecDiffKindStrings))
	for _, kind := range AllSpecDiffKinds() {
		got := kind.String()
		if other, ok := seen[got]; ok {
			t.Errorf("SpecDiffKind %d and %d both render as %q", other, kind, got)

			continue
		}
		seen[got] = kind
	}
}

// TestSpecDiffKindStringZeroValueIsUnknown asserts that the zero value renders
// as unknownEnum. AllSpecDiffKinds excludes the zero value, so
// TestSpecDiffKindStringMatchesTheDeclaredNames never evaluates it.
func TestSpecDiffKindStringZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	if got := SpecDiffUnknown.String(); got != unknownEnum {
		t.Errorf("SpecDiffUnknown.String() = %q, want %q", got, unknownEnum)
	}
}

// TestSpecDiffKindStringUndeclaredValueIsBoundsChecked asserts that a value
// outside the published vocabulary renders as SpecDiffKind(n) rather than as
// unknownEnum, which would present a garbage value as a declared state. 255 is
// deliberately not specDiffKindCount: it is a fixed literal outside the
// vocabulary, so it stays outside it even under a mutation that inserts a new
// kind and moves the sentinel.
func TestSpecDiffKindStringUndeclaredValueIsBoundsChecked(t *testing.T) {
	t.Parallel()

	const undeclared = SpecDiffKind(255)
	want := "SpecDiffKind(255)"
	if got := undeclared.String(); got != want {
		t.Errorf("SpecDiffKind(255).String() = %q, want %q", got, want)
	}
}

// TestAllSpecDiffKindsReturnsAFreshSlice asserts that corrupting one call's
// result does not affect a later call, which would be observable if
// AllSpecDiffKinds were "optimized" to return a package-level array or a
// cached slice instead of building fresh with make.
//
// Only the element write is a real probe. Reslicing the result would rewrite
// this function's own slice header and could not reach the library, so it is
// not attempted.
func TestAllSpecDiffKindsReturnsAFreshSlice(t *testing.T) {
	t.Parallel()

	first := AllSpecDiffKinds()
	if len(first) == 0 {
		t.Fatal("AllSpecDiffKinds() returned no kinds")
	}
	wantFirstKind := first[0]

	first[0] = SpecDiffUnknown

	second := AllSpecDiffKinds()
	if len(second) == 0 {
		t.Fatal("AllSpecDiffKinds() returned no kinds on a later call")
	}
	if second[0] != wantFirstKind {
		t.Errorf("second call's first kind = %v, want %v; writing through the "+
			"first call's result affected a later call",
			second[0], wantFirstKind)
	}
}
