package validations

import "testing"

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
}

func TestDiffSpecsColumnOutputOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	specA := specWithColumns(
		column("zeta", 1, "int"), column("alpha", 2, "int"), column("only_a", 3, "int"),
	)
	specB := specWithColumns(
		column("zeta", 1, "varchar(1)"), column("alpha", 2, "varchar(1)"),
		column("zz_only_b", 3, "int"), column("aa_only_b", 4, "int"),
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
	want := []string{"zeta", "alpha", "only_a", "aa_only_b", "zz_only_b"}
	if len(columnsInOrder) != len(want) {
		t.Fatalf("column diffs = %v, want %v", columnsInOrder, want)
	}
	for i := range want {
		if columnsInOrder[i] != want[i] {
			t.Fatalf("column diffs = %v, want %v", columnsInOrder, want)
		}
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
