package validations

import (
	"sort"
	"strconv"
)

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
	diffs = append(diffs, diffColumns(a.Columns, b.Columns)...)

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

// diffColumns matches columns by name. Positional matching would compare
// unrelated columns against each other and report type mismatches that name the
// wrong problem; a differing ordinal is reported as ColumnOrderMismatch
// instead, because column order still matters to a caller issuing
// INSERT INTO dest SELECT * FROM src.
func diffColumns(a, b []ColumnSpec) []SpecDiff {
	byNameB := make(map[string]ColumnSpec, len(b))
	for _, column := range b {
		byNameB[column.Name] = column
	}

	var diffs []SpecDiff
	matched := make(map[string]struct{}, len(a))
	for _, columnA := range a {
		columnB, ok := byNameB[columnA.Name]
		if !ok {
			diffs = append(diffs, SpecDiff{
				Kind: ColumnAbsent, Side: SideB, Column: columnA.Name,
				A: columnA.Type,
			})

			continue
		}
		matched[columnA.Name] = struct{}{}
		diffs = append(diffs, diffColumnPair(columnA, columnB)...)
	}

	var onlyInB []ColumnSpec
	for _, columnB := range b {
		if _, ok := matched[columnB.Name]; !ok {
			onlyInB = append(onlyInB, columnB)
		}
	}
	sort.Slice(onlyInB, func(i, j int) bool {
		return onlyInB[i].Name < onlyInB[j].Name
	})
	for _, columnB := range onlyInB {
		diffs = append(diffs, SpecDiff{
			Kind: ColumnAbsent, Side: SideA, Column: columnB.Name, B: columnB.Type,
		})
	}

	return diffs
}

// diffColumnPair compares two columns of the same name. Comparison uses
// NormalizedType while A and B carry the raw COLUMN_TYPE, so a diff shows what
// the servers actually said rather than what this package normalized it to.
//
// ColumnSpec.Extra is deliberately not compared: it is a composite string whose
// facts are compared individually through the typed fields, and diffing it too
// would report every one of them twice.
func diffColumnPair(a, b ColumnSpec) []SpecDiff {
	var diffs []SpecDiff

	emit := func(kind SpecDiffKind, valueA, valueB string) {
		diffs = append(diffs, SpecDiff{
			Kind: kind, Side: SideBoth, Column: a.Name, A: valueA, B: valueB,
		})
	}

	if a.NormalizedType != b.NormalizedType {
		emit(ColumnTypeMismatch, a.Type, b.Type)
	}
	if a.Nullable != b.Nullable {
		emit(ColumnNullabilityMismatch, boolText(a.Nullable), boolText(b.Nullable))
	}
	if a.Charset != b.Charset {
		emit(ColumnCharsetMismatch, a.Charset, b.Charset)
	}
	if a.Collation != b.Collation {
		emit(ColumnCollationMismatch, a.Collation, b.Collation)
	}
	if !sameDefault(a, b) {
		emit(ColumnDefaultMismatch, defaultText(a), defaultText(b))
	}
	if a.Ordinal != b.Ordinal {
		emit(ColumnOrderMismatch, strconv.Itoa(a.Ordinal), strconv.Itoa(b.Ordinal))
	}
	if a.Invisible != b.Invisible {
		emit(ColumnVisibilityMismatch, boolText(a.Invisible), boolText(b.Invisible))
	}
	if a.Generated != b.Generated {
		emit(ColumnGeneratedMismatch, a.Generated.String(), b.Generated.String())
	}
	if a.GenerationExpr != b.GenerationExpr {
		emit(ColumnGenerationExprMismatch, a.GenerationExpr, b.GenerationExpr)
	}
	if a.AutoIncrement != b.AutoIncrement {
		emit(ColumnAutoIncrementMismatch,
			boolText(a.AutoIncrement), boolText(b.AutoIncrement))
	}
	if a.OnUpdate != b.OnUpdate {
		emit(ColumnOnUpdateMismatch, boolText(a.OnUpdate), boolText(b.OnUpdate))
	}

	return diffs
}

// sameDefault compares defaults including whether each is an expression. A
// literal default of the text "curdate()" and an expression default that the
// server rewrote to "curdate()" hold the same string and are different
// defaults; see docs/COMPAT.md entry 14.
func sameDefault(a, b ColumnSpec) bool {
	if a.DefaultIsExpression != b.DefaultIsExpression {
		return false
	}
	switch {
	case a.Default == nil && b.Default == nil:
		return true
	case a.Default == nil || b.Default == nil:
		return false
	default:
		return *a.Default == *b.Default
	}
}

func defaultText(c ColumnSpec) string {
	if c.Default == nil {
		return ""
	}
	if c.DefaultIsExpression {
		return "(" + *c.Default + ")"
	}

	return *c.Default
}

func boolText(v bool) string {
	if v {
		return "true"
	}

	return "false"
}
