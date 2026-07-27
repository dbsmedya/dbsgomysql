package validations

import "strconv"

// DiffSide names which side of a comparison a difference concerns. Its zero
// value is SideUnknown.
//
// DiffSide is a plain value and is safe for concurrent use.
type DiffSide uint8

// Comparison sides. SideA and SideB name the spec that lacks something;
// SideBoth means both supplied a value and the values differ.
const (
	SideUnknown DiffSide = iota
	SideA
	SideB
	SideBoth
)

// String returns the side as a lowercase word.
//
// String is safe for concurrent use.
func (s DiffSide) String() string {
	switch s {
	case SideUnknown:
		return unknownEnum
	case SideA:
		return "a"
	case SideB:
		return "b"
	case SideBoth:
		return "both"
	default:
		return "DiffSide(" + strconv.Itoa(int(s)) + ")"
	}
}

// SpecDiffKind identifies one kind of difference between two table
// specifications. Its zero value is SpecDiffUnknown.
//
// SpecDiffKind is a plain value and is safe for concurrent use.
type SpecDiffKind uint8

// Kinds of difference DiffSpecs reports. Kinds ending in Unconfirmed mean a
// section only one side captured, so the question was never asked rather than
// answered in the negative.
const (
	SpecDiffUnknown SpecDiffKind = iota

	EngineMismatch
	CharsetMismatch
	CollationMismatch
	CommentMismatch
	CommentUnconfirmed

	ColumnAbsent
	ColumnTypeMismatch
	ColumnNullabilityMismatch
	ColumnCharsetMismatch
	ColumnCollationMismatch
	ColumnDefaultMismatch
	ColumnOrderMismatch
	ColumnVisibilityMismatch
	ColumnGeneratedMismatch
	ColumnGenerationExprMismatch
	ColumnAutoIncrementMismatch
	ColumnOnUpdateMismatch

	IndexUnconfirmed
	IndexAbsent
	IndexPartsMismatch
	IndexUniquenessMismatch
	IndexTypeMismatch
	IndexVisibilityMismatch

	ConstraintUnconfirmed
	ConstraintAbsent
	ConstraintKindMismatch
	CheckClauseMismatch
	CheckEnforcementMismatch
	ForeignKeyColumnsMismatch
	ForeignKeyReferenceMismatch
	ForeignKeyRuleMismatch
)

// SpecDiff is one difference between two table specifications.
//
// It carries no severity, not even a remappable default. Whether a difference
// matters is the consumer's decision, exactly as with Finding.
//
// SpecDiff is safe for concurrent reads. Callers must synchronize mutation.
type SpecDiff struct {
	// Kind identifies the difference.
	Kind SpecDiffKind `json:"kind"`
	// Side names the spec that lacks something, or SideBoth when both supplied
	// differing values.
	Side DiffSide `json:"side"`
	// Column is the column the difference concerns, empty otherwise.
	Column string `json:"column,omitempty"`
	// Index is the index or constraint the difference concerns, empty
	// otherwise.
	Index string `json:"index,omitempty"`
	// A and B are the two values, empty where a side has none.
	A string `json:"a,omitempty"`
	B string `json:"b,omitempty"`
}

// DiffSpecs reports every difference between two table specifications.
//
// It is a pure function over two values: it opens no connection, issues no
// query, and returns no error, because it inspects nothing and so has nothing
// to fail at. The two specs normally come from different Inspectors on
// different servers.
//
// Output order is deterministic — table-level differences, then columns in a's
// ordinal order with b-only columns last by name, then indexes, then
// constraints — so golden comparisons are stable.
//
// An empty result means no differences were found in the sections both sides
// captured. Where only one side captured a section, DiffSpecs emits an
// Unconfirmed diff naming the side that did not look, so an empty result can
// never mean "nobody checked".
//
// DiffSpecs judges nothing. Mapping a difference to fatal, warn, or ignore is
// consumer policy.
//
// DiffSpecs is safe for concurrent use when neither spec is mutated
// concurrently.
func DiffSpecs(a, b TableSpec) []SpecDiff {
	var diffs []SpecDiff
	diffs = append(diffs, diffTableLevel(a, b)...)

	return diffs
}

func diffTableLevel(a, b TableSpec) []SpecDiff {
	var diffs []SpecDiff

	if a.Engine != b.Engine {
		diffs = append(diffs, SpecDiff{
			Kind: EngineMismatch, Side: SideBoth, A: a.Engine, B: b.Engine,
		})
	}
	if a.Charset != b.Charset {
		diffs = append(diffs, SpecDiff{
			Kind: CharsetMismatch, Side: SideBoth, A: a.Charset, B: b.Charset,
		})
	}
	if a.Collation != b.Collation {
		diffs = append(diffs, SpecDiff{
			Kind: CollationMismatch, Side: SideBoth, A: a.Collation, B: b.Collation,
		})
	}

	switch side, both := sectionAgreement(a, b, SectionComment); {
	case both && a.Comment != b.Comment:
		diffs = append(diffs, SpecDiff{
			Kind: CommentMismatch, Side: SideBoth, A: a.Comment, B: b.Comment,
		})
	case side != SideUnknown:
		diffs = append(diffs, SpecDiff{Kind: CommentUnconfirmed, Side: side})
	}

	return diffs
}

// sectionAgreement reports how two specs agree about one optional section.
// It returns both=true when both captured it, and otherwise names the side that
// did not — SideUnknown when neither did, since a question nobody asked is not
// a gap.
func sectionAgreement(a, b TableSpec, section SpecSections) (side DiffSide, both bool) {
	hasA := a.Captured.Has(section)
	hasB := b.Captured.Has(section)

	switch {
	case hasA && hasB:
		return SideUnknown, true
	case hasA:
		return SideB, false
	case hasB:
		return SideA, false
	default:
		return SideUnknown, false
	}
}
