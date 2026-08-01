package validations

import "strconv"

const constraintKindCheckName = "check"

// TableRef names one table by schema and table, both case-exact. Its zero value
// is invalid; construct one with Ref.
//
// TableRef is a plain value and is safe for concurrent use.
type TableRef struct {
	schema string
	table  string
}

// Ref names a table for TableSpec. Both names are stored exactly as given.
// information_schema name collations vary by category and configuration, so
// TableSpec verifies the returned spelling in Go rather than resting its
// case-exact contract on a predicate.
//
// Ref is safe for concurrent use.
func Ref(schema, table string) TableRef {
	return TableRef{schema: schema, table: table}
}

func (r TableRef) valid() bool {
	return r.schema != "" && r.table != ""
}

// SpecSections is the set of optional sections a TableSpec captured. Its zero
// value means none were requested, which is a valid outcome rather than an
// unpopulated one — TableSpec accepts zero options.
//
// SpecSections is a plain value and is safe for concurrent use.
type SpecSections uint8

// Optional TableSpec sections, each selected by its With function.
const (
	SectionIndexes SpecSections = 1 << iota
	SectionConstraints
	SectionComment
)

// Has reports whether every bit in section is present.
//
// Has is safe for concurrent use.
func (s SpecSections) Has(section SpecSections) bool {
	return s&section == section
}

type specRequest struct {
	sections SpecSections
}

// SpecOption selects an optional section of a TableSpec. Options are
// idempotent, and passing none is valid.
//
// SpecOption is safe for concurrent use.
type SpecOption func(*specRequest)

// WithIndexes captures the table's indexes from information_schema.STATISTICS,
// including primary and unique keys — in MySQL a UNIQUE constraint is a unique
// index, and this package reports it once, here.
//
// WithIndexes is safe for concurrent use.
func WithIndexes() SpecOption {
	return func(r *specRequest) { r.sections |= SectionIndexes }
}

// WithConstraints captures CHECK and FOREIGN KEY constraints, including the
// referential UPDATE and DELETE rules. Primary and unique keys are indexes and
// belong to WithIndexes; the foreign-key graph over a target set is a different
// question, answered by Inspector.ForeignKeys.
//
// WithConstraints is safe for concurrent use.
func WithConstraints() SpecOption {
	return func(r *specRequest) { r.sections |= SectionConstraints }
}

// WithComment brings the table comment into scope so DiffSpecs compares it.
//
// Unlike the other options this one costs no additional query — the comment is
// already in the information_schema.TABLES row TableSpec reads regardless. Its
// only effect is to declare that comments are part of what the caller is
// comparing.
//
// WithComment is safe for concurrent use.
func WithComment() SpecOption {
	return func(r *specRequest) { r.sections |= SectionComment }
}

// ConstraintKind distinguishes the constraint types TableSpec captures. Its
// zero value is ConstraintUnknown, reserved for "not populated".
//
// ConstraintKind is a plain value and is safe for concurrent use.
type ConstraintKind uint8

// Constraint kinds. ConstraintUnknown is the zero value.
const (
	ConstraintUnknown ConstraintKind = iota
	ConstraintCheck
	ConstraintForeignKey
)

// String returns the kind as a lowercase word. An undeclared value renders as
// ConstraintKind(N).
//
// String is safe for concurrent use.
func (k ConstraintKind) String() string {
	switch k {
	case ConstraintUnknown:
		return unknownEnum
	case ConstraintCheck:
		return constraintKindCheckName
	case ConstraintForeignKey:
		return "foreign_key"
	default:
		return "ConstraintKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// ColumnSpec describes one column completely enough to compare it with a column
// on any other server.
//
// ColumnSpec is safe for concurrent reads. Callers must synchronize mutation.
type ColumnSpec struct {
	// Name is the column's exact server-side spelling.
	Name string `json:"name"`
	// Ordinal is ORDINAL_POSITION, one-based.
	Ordinal int `json:"ordinal"`
	// Type is COLUMN_TYPE verbatim.
	Type string `json:"type"`
	// NormalizedType is Type with the deprecated integer display width removed,
	// except for the two widths MySQL itself still emits: tinyint(1), which
	// BOOLEAN is an alias for, and any type carrying ZEROFILL, whose retrieved
	// values are zero-padded to the width. Both are real differences between
	// schemas rather than formatting noise, so both survive here; see
	// docs/COMPAT.md entry 1. Comparison uses this; Type is what the server said.
	NormalizedType string `json:"normalized_type"`
	// Nullable reports IS_NULLABLE.
	Nullable bool `json:"nullable"`
	// Charset is CHARACTER_SET_NAME, empty where MySQL reports NULL.
	Charset string `json:"charset"`
	// Collation is COLLATION_NAME, empty where MySQL reports NULL.
	Collation string `json:"collation"`
	// Default is COLUMN_DEFAULT, nil when MySQL reports NULL. MySQL does not
	// distinguish "no default" from "DEFAULT NULL" on a nullable column.
	Default *string `json:"default"`
	// DefaultIsExpression reports DEFAULT_GENERATED in EXTRA. Without it an
	// expression default is indistinguishable from a literal of the same text;
	// see docs/COMPAT.md entry 14.
	DefaultIsExpression bool `json:"default_is_expression"`
	// Extra is EXTRA verbatim, kept for fidelity. DiffSpecs never compares it —
	// the facts it packs together are compared through the typed fields below.
	Extra string `json:"extra"`
	// Invisible reports whether SELECT * omits this column.
	Invisible bool `json:"invisible"`
	// Generated reports how the value is produced.
	Generated GeneratedKind `json:"generated"`
	// GenerationExpr is GENERATION_EXPRESSION, as the server rewrote it.
	GenerationExpr string `json:"generation_expr"`
	// AutoIncrement reports the auto_increment attribute on this column. It is
	// a schema property, unlike the table's current counter value, which
	// TableSpec deliberately does not capture.
	AutoIncrement bool `json:"auto_increment"`
	// OnUpdate reports an ON UPDATE CURRENT_TIMESTAMP clause.
	OnUpdate bool `json:"on_update"`
}

// IndexPart is one key part of an index. A part indexes either a column or an
// expression, never both.
//
// IndexPart is a plain value and is safe for concurrent use.
type IndexPart struct {
	// Column is the indexed column's exact server-side spelling, empty for a
	// functional part.
	Column string `json:"column,omitempty"`
	// Expression is STATISTICS.EXPRESSION for a functional key part, empty for
	// a column part.
	Expression string `json:"expression,omitempty"`
	// SubPart is the indexed prefix length. Zero means the whole value is
	// indexed: INDEX(name) and INDEX(name(10)) are different indexes.
	SubPart int `json:"sub_part,omitempty"`
	// Descending reports a DESC key part, which MySQL records as
	// STATISTICS.COLLATION = 'D'.
	Descending bool `json:"descending,omitempty"`
}

// IndexSpec describes one index as an ordered list of key parts.
//
// Parts rather than column names, because a column name alone does not identify
// a key part: a prefix length, a direction, and a functional expression are all
// schema properties that change which queries the index can serve.
//
// IndexSpec is safe for concurrent reads. Callers must synchronize mutation of
// Parts.
type IndexSpec struct {
	// Name is the index's exact server-side spelling. A primary key is always
	// named PRIMARY; see docs/COMPAT.md entry 13.
	Name string `json:"name"`
	// Parts are the key parts in SEQ_IN_INDEX order.
	Parts []IndexPart `json:"parts"`
	// Unique reports whether duplicate keys are rejected.
	Unique bool `json:"unique"`
	// Type is INDEX_TYPE: BTREE, HASH, FULLTEXT, or SPATIAL.
	Type string `json:"type"`
	// Visible reports IS_VISIBLE. An invisible index is not used by the
	// optimizer.
	Visible bool `json:"visible"`
}

// ConstraintSpec describes one CHECK or FOREIGN KEY constraint. Which fields
// are populated depends on Kind.
//
// ConstraintSpec is safe for concurrent reads. Callers must synchronize
// mutation of Columns or RefColumns.
type ConstraintSpec struct {
	// Name is the constraint's exact server-side spelling.
	Name string `json:"name"`
	// Kind selects which of the fields below are populated.
	Kind ConstraintKind `json:"kind"`
	// CheckClause is CHECK_CONSTRAINTS.CHECK_CLAUSE as the server normalized
	// it; see docs/COMPAT.md entry 15. Populated for ConstraintCheck.
	CheckClause string `json:"check_clause,omitempty"`
	// Enforced reports TABLE_CONSTRAINTS.ENFORCED. A CHECK declared NOT
	// ENFORCED is recorded but never evaluated, so the clause says what would
	// be checked and this says whether anything checks it.
	Enforced bool `json:"enforced"`
	// Columns are the child columns in key order. Populated for
	// ConstraintForeignKey.
	Columns []string `json:"columns,omitempty"`
	// RefSchema, RefTable, and RefColumns name the parent side. Populated for
	// ConstraintForeignKey.
	RefSchema  string   `json:"ref_schema,omitempty"`
	RefTable   string   `json:"ref_table,omitempty"`
	RefColumns []string `json:"ref_columns,omitempty"`
	// UpdateRule and DeleteRule are the referential actions, e.g. CASCADE or
	// SET NULL. Populated for ConstraintForeignKey.
	UpdateRule string `json:"update_rule,omitempty"`
	DeleteRule string `json:"delete_rule,omitempty"`
}

// TableSpec describes a table completely enough to compare it with a table on
// any other server. Optional sections are populated only when their With option
// was passed, and Captured records which — without it an empty Indexes could
// not be told from a question nobody asked.
//
// TableSpec deliberately does not capture information_schema.TABLES
// .AUTO_INCREMENT. That is the next counter value, not a schema property: it
// advances on every insert and InnoDB reports it approximately, so two
// otherwise identical tables always differ on it.
//
// TableSpec is safe for concurrent reads. Callers must synchronize mutation of
// its slices.
type TableSpec struct {
	// Schema and Table are the verified server-side spelling. On success they
	// equal exactly what Ref requested.
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// Engine is the storage engine.
	Engine string `json:"engine"`
	// Charset and Collation are the table defaults.
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
	// Comment is populated only under WithComment.
	Comment string `json:"comment,omitempty"`
	// Columns are always populated, in ORDINAL_POSITION order.
	Columns []ColumnSpec `json:"columns"`
	// Indexes is populated only under WithIndexes, ordered by name.
	Indexes []IndexSpec `json:"indexes,omitempty"`
	// Constraints is populated only under WithConstraints, ordered by name.
	Constraints []ConstraintSpec `json:"constraints,omitempty"`
	// Captured records which optional sections were requested.
	Captured SpecSections `json:"captured"`
}
