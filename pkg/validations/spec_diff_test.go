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
			name:   "engine",
			mutate: func(s *TableSpec) { s.Engine = "MyISAM" },
			wantKind: EngineMismatch, wantA: "InnoDB", wantB: "MyISAM",
		},
		{
			name:   "charset",
			mutate: func(s *TableSpec) { s.Charset = "latin1" },
			wantKind: CharsetMismatch, wantA: "utf8mb4", wantB: "latin1",
		},
		{
			name:   "collation",
			mutate: func(s *TableSpec) { s.Collation = "utf8mb4_bin" },
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
