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
// SideBoth means both supplied a value and the values differ — or, for
// ConstraintKindUnconfirmed, that neither supplied a comparable value.
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
// answered in the negative — with one documented exception:
// ConstraintKindUnconfirmed means a constraint matched by name carries
// ConstraintUnknown on both sides, so nothing could be compared.
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

	// Added in v1.2.0 after every earlier kind so serialized values stay stable.
	ColumnNameCaseMismatch
	IndexNameCaseMismatch
	ConstraintKindUnconfirmed

	// specDiffKindCount must remain the last constant in this block;
	// TestSpecDiffKindVocabularyIsDeclaredInOneTerminatedBlock fails if it does
	// not. It is unexported because it is not part of the published vocabulary,
	// and AllSpecDiffKinds derives its result from it so that adding a kind
	// above this line needs no other edit.
	specDiffKindCount
)

// String returns the kind as a lowercase word. An undeclared value renders as
// SpecDiffKind(n) rather than as "unknown", so a garbage value is
// distinguishable from the zero value in a message.
//
// String is safe for concurrent use.
func (k SpecDiffKind) String() string {
	switch k {
	case SpecDiffUnknown:
		return unknownEnum
	case EngineMismatch:
		return "engine_mismatch"
	case CharsetMismatch:
		return "charset_mismatch"
	case CollationMismatch:
		return "collation_mismatch"
	case CommentMismatch:
		return "comment_mismatch"
	case CommentUnconfirmed:
		return "comment_unconfirmed"
	case ColumnAbsent:
		return "column_absent"
	case ColumnTypeMismatch:
		return "column_type_mismatch"
	case ColumnNullabilityMismatch:
		return "column_nullability_mismatch"
	case ColumnCharsetMismatch:
		return "column_charset_mismatch"
	case ColumnCollationMismatch:
		return "column_collation_mismatch"
	case ColumnDefaultMismatch:
		return "column_default_mismatch"
	case ColumnOrderMismatch:
		return "column_order_mismatch"
	case ColumnVisibilityMismatch:
		return "column_visibility_mismatch"
	case ColumnGeneratedMismatch:
		return "column_generated_mismatch"
	case ColumnGenerationExprMismatch:
		return "column_generation_expr_mismatch"
	case ColumnAutoIncrementMismatch:
		return "column_auto_increment_mismatch"
	case ColumnOnUpdateMismatch:
		return "column_on_update_mismatch"
	case IndexUnconfirmed:
		return "index_unconfirmed"
	case IndexAbsent:
		return "index_absent"
	case IndexPartsMismatch:
		return "index_parts_mismatch"
	case IndexUniquenessMismatch:
		return "index_uniqueness_mismatch"
	case IndexTypeMismatch:
		return "index_type_mismatch"
	case IndexVisibilityMismatch:
		return "index_visibility_mismatch"
	case ConstraintUnconfirmed:
		return "constraint_unconfirmed"
	case ConstraintAbsent:
		return "constraint_absent"
	case ConstraintKindMismatch:
		return "constraint_kind_mismatch"
	case CheckClauseMismatch:
		return "check_clause_mismatch"
	case CheckEnforcementMismatch:
		return "check_enforcement_mismatch"
	case ForeignKeyColumnsMismatch:
		return "foreign_key_columns_mismatch"
	case ForeignKeyReferenceMismatch:
		return "foreign_key_reference_mismatch"
	case ForeignKeyRuleMismatch:
		return "foreign_key_rule_mismatch"
	case ColumnNameCaseMismatch:
		return "column_name_case_mismatch"
	case IndexNameCaseMismatch:
		return "index_name_case_mismatch"
	case ConstraintKindUnconfirmed:
		return "constraint_kind_unconfirmed"
	default:
		return "SpecDiffKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// AllSpecDiffKinds returns every nonzero SpecDiffKind DiffSpecs may emit, in
// declaration order. The vocabulary is stable within a v0.N.x release line:
// kinds are added only in a new minor, and none is renumbered or removed.
//
// SpecDiffUnknown is excluded. It is the zero value, DiffSpecs never emits it,
// and it is exactly what a consumer's fail-closed default arm should keep
// rejecting — so including it would make the natural loop over this result
// assert that the zero value is classified. That exclusion is part of the
// contract and will not change silently.
//
// The result is built fresh on each call: a caller may keep or modify it
// without affecting any other caller. AllSpecDiffKinds is safe for concurrent
// use.
func AllSpecDiffKinds() []SpecDiffKind {
	kinds := make([]SpecDiffKind, 0, int(specDiffKindCount)-1)
	for kind := SpecDiffUnknown + 1; kind < specDiffKindCount; kind++ {
		kinds = append(kinds, kind)
	}

	return kinds
}

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
	// differing values — or, for ConstraintKindUnconfirmed, when neither
	// supplied a comparable kind.
	Side DiffSide `json:"side"`
	// Column is the column the difference concerns, empty otherwise.
	Column string `json:"column,omitempty"`
	// Index is the index or constraint the difference concerns, empty
	// otherwise.
	Index string `json:"index,omitempty"`
	// A and B are the two values, empty where a side has none. For
	// ColumnDefaultMismatch, Side disambiguates the empty string: SideA or
	// SideB names the spec with no default at all, so an empty value on the
	// named side means absence, and an empty value on the other side is the
	// literal empty-string default.
	A string `json:"a,omitempty"`
	B string `json:"b,omitempty"`
	// AIsExpression qualifies A for ColumnDefaultMismatch: true when A's
	// default is an expression rather than a literal (docs/COMPAT.md entry 14).
	// It is false for an absent default and every other kind, and omitted from
	// JSON when false. Expression qualifiers never decorate the default text.
	AIsExpression bool `json:"a_is_expression,omitempty"`
	// BIsExpression qualifies B on the same terms as AIsExpression.
	BIsExpression bool `json:"b_is_expression,omitempty"`
}

// DiffSpecs reports every difference between two table specifications.
//
// It is a pure function over two values: it opens no connection, issues no
// query, and returns no error, because it inspects nothing and so has nothing
// to fail at. The two specs normally come from different Inspectors on
// different servers.
//
// Output order is deterministic — table-level differences, then columns in a's
// ordinal order with b-only columns last by folded name, then indexes, then
// constraints — so golden comparisons are stable.
//
// Columns and indexes are matched by ASCII-folded name, and a case-only
// spelling difference is reported once as ColumnNameCaseMismatch or
// IndexNameCaseMismatch; constraint names compare exactly.
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

// foldKey returns a name's ASCII-folded comparison key. Non-ASCII bytes stay
// exact; COMPAT entry 28 records why column and index names can be folded.
func foldKey(name string) string {
	for index := range len(name) {
		if c := name[index]; c >= 'A' && c <= 'Z' {
			folded := []byte(name)
			for rest := index; rest < len(folded); rest++ {
				if folded[rest] >= 'A' && folded[rest] <= 'Z' {
					folded[rest] += 'a' - 'A'
				}
			}

			return string(folded)
		}
	}

	return name
}

// foldCollisions collects keys shared by multiple names on one side. Both
// sides contribute before matching, so caller-built specs with case-distinct
// objects cannot match several objects to one. Servers disallow these
// collisions (COMPAT entry 28); only the colliding keys stay byte-exact.
func foldCollisions(count int, nameAt func(int) string, into map[string]struct{}) {
	seen := make(map[string]struct{}, count)
	for position := range count {
		key := foldKey(nameAt(position))
		if _, dup := seen[key]; dup {
			into[key] = struct{}{}
		}
		seen[key] = struct{}{}
	}
}

// foldedIndex tries exact spelling first, then an unambiguous folded key.
type foldedIndex struct {
	exact    map[string]int
	folded   map[string]int
	excluded map[string]struct{}
}

func newFoldedIndex(count int, nameAt func(int) string, excluded map[string]struct{}) foldedIndex {
	index := foldedIndex{
		exact:    make(map[string]int, count),
		folded:   make(map[string]int, count),
		excluded: excluded,
	}
	for position := range count {
		name := nameAt(position)
		index.exact[name] = position
		if key := foldKey(name); !index.isExcluded(key) {
			index.folded[key] = position
		}
	}

	return index
}

func (index foldedIndex) isExcluded(key string) bool {
	_, excluded := index.excluded[key]

	return excluded
}

func (index foldedIndex) lookup(name string) (int, bool) {
	if position, ok := index.exact[name]; ok {
		return position, true
	}
	key := foldKey(name)
	if index.isExcluded(key) {
		return 0, false
	}
	position, ok := index.folded[key]

	return position, ok
}

// diffColumns matches columns by folded name. Positional matching would compare
// unrelated columns against each other and report type mismatches that name the
// wrong problem; a differing ordinal is reported as ColumnOrderMismatch
// instead, because column order still matters to a caller issuing
// INSERT INTO dest SELECT * FROM src.
func diffColumns(a, b []ColumnSpec) []SpecDiff {
	excluded := make(map[string]struct{})
	foldCollisions(len(a), func(i int) string { return a[i].Name }, excluded)
	foldCollisions(len(b), func(i int) string { return b[i].Name }, excluded)
	byNameB := newFoldedIndex(len(b), func(i int) string { return b[i].Name }, excluded)

	var diffs []SpecDiff
	matched := make(map[int]struct{}, len(a))
	for indexA := range a {
		columnA := &a[indexA]
		indexB, ok := byNameB.lookup(columnA.Name)
		if !ok {
			diffs = append(diffs, SpecDiff{
				Kind: ColumnAbsent, Side: SideB, Column: columnA.Name,
				A: columnA.Type,
			})

			continue
		}
		matched[indexB] = struct{}{}
		if columnA.Name != b[indexB].Name {
			diffs = append(diffs, SpecDiff{
				Kind: ColumnNameCaseMismatch, Side: SideBoth, Column: columnA.Name,
				A: columnA.Name, B: b[indexB].Name,
			})
		}
		diffs = append(diffs, diffColumnPair(columnA, &b[indexB])...)
	}

	var onlyInB []int
	for indexB := range b {
		if _, ok := matched[indexB]; !ok {
			onlyInB = append(onlyInB, indexB)
		}
	}
	sort.Slice(onlyInB, func(i, j int) bool {
		left, right := foldKey(b[onlyInB[i]].Name), foldKey(b[onlyInB[j]].Name)
		if left != right {
			return left < right
		}
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

// diffColumnPair compares two columns matched by name. Comparison uses
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
		diffs = append(diffs, SpecDiff{
			Kind: ColumnDefaultMismatch, Side: defaultSide(a, b), Column: a.Name,
			A: defaultValue(a), B: defaultValue(b),
			AIsExpression: a.Default != nil && a.DefaultIsExpression,
			BIsExpression: b.Default != nil && b.DefaultIsExpression,
		})
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

// defaultSide names the side that has no default at all, honoring DiffSide's
// contract: SideA and SideB name the spec that lacks something, SideBoth means
// both supplied a value. A pointer to the empty string is a supplied value —
// the literal empty-string default — which is exactly the distinction
// defaultValue alone cannot carry, since it renders absence and the empty
// string identically. sameDefault treats two nil defaults as equal, so by the
// time this runs, a nil pointer sits opposite a non-nil one or both are
// non-nil.
func defaultSide(a, b *ColumnSpec) DiffSide {
	switch {
	case a.Default == nil && b.Default != nil:
		return SideA
	case a.Default != nil && b.Default == nil:
		return SideB
	default:
		return SideBoth
	}
}

// sameDefault compares defaults including whether each is an expression. A
// literal default of the text "curdate()" and an expression default that the
// server rewrote to "curdate()" hold the same string and are different
// defaults; see docs/COMPAT.md entry 14. The flag qualifies Default's text,
// so it counts only when both sides have one: two nil defaults are equal
// regardless of DefaultIsExpression, because DEFAULT_GENERATED marks columns
// that have an expression default value, and a column with a nil Default
// has none for the flag to describe.
func sameDefault(a, b *ColumnSpec) bool {
	switch {
	case a.Default == nil && b.Default == nil:
		return true
	case a.Default == nil || b.Default == nil:
		return false
	default:
		return *a.Default == *b.Default &&
			a.DefaultIsExpression == b.DefaultIsExpression
	}
}

func defaultValue(c *ColumnSpec) string {
	if c.Default == nil {
		return ""
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

	excluded := make(map[string]struct{})
	foldCollisions(len(a.Indexes), func(i int) string { return a.Indexes[i].Name }, excluded)
	foldCollisions(len(b.Indexes), func(i int) string { return b.Indexes[i].Name }, excluded)
	byNameB := newFoldedIndex(len(b.Indexes), func(i int) string { return b.Indexes[i].Name }, excluded)

	var diffs []SpecDiff
	matched := make(map[int]struct{}, len(a.Indexes))
	for _, indexA := range a.Indexes {
		positionB, ok := byNameB.lookup(indexA.Name)
		if !ok {
			diffs = append(diffs, SpecDiff{
				Kind: IndexAbsent, Side: SideB, Index: indexA.Name,
			})

			continue
		}
		matched[positionB] = struct{}{}
		indexB := b.Indexes[positionB]

		emit := func(kind SpecDiffKind, valueA, valueB string) {
			diffs = append(diffs, SpecDiff{
				Kind: kind, Side: SideBoth, Index: indexA.Name, A: valueA, B: valueB,
			})
		}
		if indexA.Name != indexB.Name {
			emit(IndexNameCaseMismatch, indexA.Name, indexB.Name)
		}
		if !partsEqual(indexA.Parts, indexB.Parts) {
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

	for positionB, indexB := range b.Indexes {
		if _, ok := matched[positionB]; !ok {
			diffs = append(diffs, SpecDiff{
				Kind: IndexAbsent, Side: SideA, Index: indexB.Name,
			})
		}
	}

	return diffs
}

// partsEqual folds column names but preserves expressions, prefix lengths,
// directions, and part order. COMPAT entry 28 pins identifier case behavior.
func partsEqual(a, b []IndexPart) bool {
	return slices.EqualFunc(a, b, func(left, right IndexPart) bool {
		return asciiFoldEqual(left.Column, right.Column) &&
			left.Expression == right.Expression &&
			left.SubPart == right.SubPart &&
			left.Descending == right.Descending
	})
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
	if a.Kind == ConstraintUnknown {
		return []SpecDiff{{Kind: ConstraintKindUnconfirmed, Side: SideBoth, Index: a.Name}}
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
