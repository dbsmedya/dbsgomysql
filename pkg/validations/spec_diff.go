package validations

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

const checkEnforcedName = "ENFORCED"

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
func DiffSpecs(a, b TableSpec) []SpecDiff { //nolint:gocritic // Value semantics are the public contract.
	var diffs []SpecDiff
	diffs = append(diffs, diffTableLevel(&a, &b)...)
	diffs = append(diffs, diffColumns(a.Columns, b.Columns)...)
	diffs = append(diffs, diffIndexes(&a, &b)...)
	diffs = append(diffs, diffConstraints(&a, &b)...)

	return diffs
}

func diffTableLevel(a, b *TableSpec) []SpecDiff {
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
func sectionAgreement(a, b *TableSpec, section SpecSections) (side DiffSide, both bool) {
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
	byNameB := make(map[string]int, len(b))
	for index := range b {
		byNameB[b[index].Name] = index
	}

	var diffs []SpecDiff
	matched := make(map[string]struct{}, len(a))
	for indexA := range a {
		columnA := &a[indexA]
		indexB, ok := byNameB[columnA.Name]
		if !ok {
			diffs = append(diffs, SpecDiff{
				Kind: ColumnAbsent, Side: SideB, Column: columnA.Name,
				A: columnA.Type,
			})

			continue
		}
		matched[columnA.Name] = struct{}{}
		diffs = append(diffs, diffColumnPair(columnA, &b[indexB])...)
	}

	var onlyInB []int
	for indexB := range b {
		if _, ok := matched[b[indexB].Name]; !ok {
			onlyInB = append(onlyInB, indexB)
		}
	}
	sort.Slice(onlyInB, func(i, j int) bool {
		return b[onlyInB[i]].Name < b[onlyInB[j]].Name
	})
	for _, indexB := range onlyInB {
		columnB := &b[indexB]
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
func diffColumnPair(a, b *ColumnSpec) []SpecDiff {
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
func sameDefault(a, b *ColumnSpec) bool {
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

func defaultText(c *ColumnSpec) string {
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

// diffIndexes compares indexes only when both specs captured them. Where one
// did not, it emits IndexUnconfirmed for that side and compares nothing —
// reporting agreement about a section one side never read would be a false
// all-clear.
func diffIndexes(a, b *TableSpec) []SpecDiff {
	side, both := sectionAgreement(a, b, SectionIndexes)
	if !both {
		if side == SideUnknown {
			return nil
		}

		return []SpecDiff{{Kind: IndexUnconfirmed, Side: side}}
	}

	byNameB := make(map[string]IndexSpec, len(b.Indexes))
	for _, index := range b.Indexes {
		byNameB[index.Name] = index
	}

	var diffs []SpecDiff
	matched := make(map[string]struct{}, len(a.Indexes))
	for _, indexA := range a.Indexes {
		indexB, ok := byNameB[indexA.Name]
		if !ok {
			diffs = append(diffs, SpecDiff{
				Kind: IndexAbsent, Side: SideB, Index: indexA.Name,
			})

			continue
		}
		matched[indexA.Name] = struct{}{}

		emit := func(kind SpecDiffKind, valueA, valueB string) {
			diffs = append(diffs, SpecDiff{
				Kind: kind, Side: SideBoth, Index: indexA.Name, A: valueA, B: valueB,
			})
		}
		if !slices.Equal(indexA.Parts, indexB.Parts) {
			emit(IndexPartsMismatch, partsText(indexA.Parts), partsText(indexB.Parts))
		}
		if indexA.Unique != indexB.Unique {
			emit(IndexUniquenessMismatch,
				boolText(indexA.Unique), boolText(indexB.Unique))
		}
		if indexA.Type != indexB.Type {
			emit(IndexTypeMismatch, indexA.Type, indexB.Type)
		}
		if indexA.Visible != indexB.Visible {
			emit(IndexVisibilityMismatch,
				boolText(indexA.Visible), boolText(indexB.Visible))
		}
	}

	for _, indexB := range b.Indexes {
		if _, ok := matched[indexB.Name]; !ok {
			diffs = append(diffs, SpecDiff{
				Kind: IndexAbsent, Side: SideA, Index: indexB.Name,
			})
		}
	}

	return diffs
}

// diffConstraints compares CHECK and FOREIGN KEY constraints, gated on both
// sides having captured them for the same reason as diffIndexes.
func diffConstraints(a, b *TableSpec) []SpecDiff {
	side, both := sectionAgreement(a, b, SectionConstraints)
	if !both {
		if side == SideUnknown {
			return nil
		}

		return []SpecDiff{{Kind: ConstraintUnconfirmed, Side: side}}
	}

	byNameB := make(map[string]int, len(b.Constraints))
	for index := range b.Constraints {
		byNameB[b.Constraints[index].Name] = index
	}

	var diffs []SpecDiff
	matched := make(map[string]struct{}, len(a.Constraints))
	for indexA := range a.Constraints {
		constraintA := &a.Constraints[indexA]
		indexB, ok := byNameB[constraintA.Name]
		if !ok {
			diffs = append(diffs, SpecDiff{
				Kind: ConstraintAbsent, Side: SideB, Index: constraintA.Name,
			})

			continue
		}
		matched[constraintA.Name] = struct{}{}
		diffs = append(diffs, diffConstraintPair(constraintA, &b.Constraints[indexB])...)
	}

	for indexB := range b.Constraints {
		constraintB := &b.Constraints[indexB]
		if _, ok := matched[constraintB.Name]; !ok {
			diffs = append(diffs, SpecDiff{
				Kind: ConstraintAbsent, Side: SideA, Index: constraintB.Name,
			})
		}
	}

	return diffs
}

// diffConstraintPair compares two constraints sharing a name.
//
// A kind change is reported alone. A CHECK and a FOREIGN KEY have no fields in
// common, so comparing them field by field would emit four diffs — clause,
// columns, reference, rules — each describing an attribute the other side does
// not have, and inviting a consumer to fix things that do not exist.
func diffConstraintPair(a, b *ConstraintSpec) []SpecDiff {
	if a.Kind != b.Kind {
		return []SpecDiff{{
			Kind: ConstraintKindMismatch, Side: SideBoth, Index: a.Name,
			A: a.Kind.String(), B: b.Kind.String(),
		}}
	}

	var diffs []SpecDiff

	emit := func(kind SpecDiffKind, valueA, valueB string) {
		diffs = append(diffs, SpecDiff{
			Kind: kind, Side: SideBoth, Index: a.Name, A: valueA, B: valueB,
		})
	}

	switch a.Kind {
	case ConstraintCheck:
		if a.CheckClause != b.CheckClause {
			emit(CheckClauseMismatch, a.CheckClause, b.CheckClause)
		}
		if a.Enforced != b.Enforced {
			emit(CheckEnforcementMismatch,
				enforcedText(a.Enforced), enforcedText(b.Enforced))
		}
	case ConstraintForeignKey:
		if !slices.Equal(a.Columns, b.Columns) {
			emit(ForeignKeyColumnsMismatch,
				joinColumns(a.Columns), joinColumns(b.Columns))
		}
		if a.RefSchema != b.RefSchema || a.RefTable != b.RefTable ||
			!slices.Equal(a.RefColumns, b.RefColumns) {
			emit(ForeignKeyReferenceMismatch, referenceText(a), referenceText(b))
		}
		if a.UpdateRule != b.UpdateRule || a.DeleteRule != b.DeleteRule {
			emit(ForeignKeyRuleMismatch, ruleText(a), ruleText(b))
		}
	}

	return diffs
}

func referenceText(c *ConstraintSpec) string {
	if c.RefTable == "" {
		return ""
	}

	return c.RefSchema + "." + c.RefTable + "(" + joinColumns(c.RefColumns) + ")"
}

// enforcedText renders enforcement as MySQL spells it, so a diff reads as the
// DDL it came from rather than as a Go boolean.
func enforcedText(enforced bool) string {
	if enforced {
		return checkEnforcedName
	}

	return "NOT " + checkEnforcedName
}

func ruleText(c *ConstraintSpec) string {
	if c.Kind != ConstraintForeignKey {
		return ""
	}

	return "ON UPDATE " + c.UpdateRule + " ON DELETE " + c.DeleteRule
}

func joinColumns(columns []string) string {
	return strings.Join(columns, ", ")
}

// partsText renders key parts the way MySQL's index grammar writes them, so a
// diff reads as the DDL difference it is: "name(10), sku DESC".
func partsText(parts []IndexPart) string {
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		text := part.Column
		if part.Expression != "" {
			text = "(" + part.Expression + ")"
		}
		if part.SubPart != 0 {
			text += "(" + strconv.Itoa(part.SubPart) + ")"
		}
		if part.Descending {
			text += " DESC"
		}
		rendered = append(rendered, text)
	}

	return strings.Join(rendered, ", ")
}
