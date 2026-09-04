package validations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// MetadataVisibility reports whether foreign-key discovery is complete for
// registered InnoDB constraints.
//
// The zero value means the fact was not populated. MetadataVisibility is a
// plain value and is safe for concurrent use.
type MetadataVisibility uint8

const (
	// VisibilityUnknown means no metadata-completeness proof was populated.
	VisibilityUnknown MetadataVisibility = iota
	// VisibilityComplete means the PROCESS-gated InnoDB query succeeded.
	VisibilityComplete
	// VisibilityUnconfirmed means the visibility-filtered fallback was used.
	VisibilityUnconfirmed
)

// String returns a stable human spelling for the visibility state.
//
// String is safe for concurrent use.
func (v MetadataVisibility) String() string {
	switch v {
	case VisibilityUnknown:
		return unknownEnum
	case VisibilityComplete:
		return "complete"
	case VisibilityUnconfirmed:
		return grantStateUnconfirmedName
	default:
		return "MetadataVisibility(" + strconv.Itoa(int(v)) + ")"
	}
}

// ForeignKeyDowngradeReason reports the primary-source stage that made
// ForeignKeys use its standard metadata fallback.
//
// The zero value means no primary-source downgrade occurred.
// ForeignKeyDowngradeReason is a plain value and is safe for concurrent use.
type ForeignKeyDowngradeReason uint8

const (
	// ForeignKeyDowngradeNone means no primary-source downgrade occurred.
	ForeignKeyDowngradeNone ForeignKeyDowngradeReason = iota
	// ForeignKeyDowngradePrimaryQueryError means the primary query returned an
	// error before rows were available.
	ForeignKeyDowngradePrimaryQueryError
	// ForeignKeyDowngradePrimaryReadError means the primary query succeeded, but
	// its rows could not be scanned, iterated, grouped, or decoded.
	ForeignKeyDowngradePrimaryReadError
)

// String returns a stable human spelling for the downgrade reason.
//
// String is safe for concurrent use.
func (r ForeignKeyDowngradeReason) String() string {
	switch r {
	case ForeignKeyDowngradeNone:
		return enumNoneName
	case ForeignKeyDowngradePrimaryQueryError:
		return "primary_query_error"
	case ForeignKeyDowngradePrimaryReadError:
		return "primary_read_error"
	default:
		return "ForeignKeyDowngradeReason(" + strconv.Itoa(int(r)) + ")"
	}
}

const (
	fkRuleCascade  = "CASCADE"
	fkRuleNoAction = "NO ACTION"
	fkRuleRestrict = "RESTRICT"
	fkRuleSetNull  = "SET NULL"
)

type fkSelectorKind uint8

const (
	fkSelectorInvalid fkSelectorKind = iota
	fkSelectorIncoming
	fkSelectorOutgoing
	fkSelectorWithin
)

// FKSelector identifies one foreign-key direction and an ordered table set.
// Its zero value is invalid; construct one with IncomingTo, OutgoingFrom, or
// Within. Constructors copy their input, and duplicate names remain
// significant.
//
// FKSelector owns its table slice and is safe for concurrent use.
type FKSelector struct {
	kind   fkSelectorKind
	tables []string
}

// IncomingTo selects constraints whose parent belongs to tables in the
// Inspector's schema. The input is copied and duplicates are preserved.
//
// IncomingTo is safe for concurrent use.
func IncomingTo(tables ...string) FKSelector {
	return newFKSelector(fkSelectorIncoming, tables)
}

// OutgoingFrom selects constraints whose child belongs to tables in the
// Inspector's schema. The input is copied and duplicates are preserved.
//
// OutgoingFrom is safe for concurrent use.
func OutgoingFrom(tables ...string) FKSelector {
	return newFKSelector(fkSelectorOutgoing, tables)
}

// Within selects constraints whose child and parent both belong to tables in
// the Inspector's schema. The input is copied and duplicates are preserved.
//
// Within is safe for concurrent use.
func Within(tables ...string) FKSelector {
	return newFKSelector(fkSelectorWithin, tables)
}

func newFKSelector(kind fkSelectorKind, tables []string) FKSelector {
	return FKSelector{kind: kind, tables: append([]string(nil), tables...)}
}

// ForeignKey is one constraint, with the child side named first.
//
// Each returned value owns its ChildColumns and ParentColumns slices; see the
// package documentation on ownership of returned slices.
//
// ForeignKey is safe for concurrent reads. Callers must synchronize mutation
// of ChildColumns or ParentColumns.
type ForeignKey struct {
	// ConstraintName is the exact server-side constraint spelling.
	ConstraintName string `json:"constraint_name"`
	// ChildSchema is the exact child schema spelling.
	ChildSchema string `json:"child_schema"`
	// ChildTable is the exact child table spelling.
	ChildTable string `json:"child_table"`
	// ChildColumns contains child columns in constraint order.
	ChildColumns []string `json:"child_columns"`
	// ParentSchema is the exact parent schema spelling.
	ParentSchema string `json:"parent_schema"`
	// ParentTable is the exact parent table spelling.
	ParentTable string `json:"parent_table"`
	// ParentColumns contains parent columns in constraint order.
	ParentColumns []string `json:"parent_columns"`
	// OnDelete is MySQL's normalized referential action spelling.
	OnDelete string `json:"on_delete"`
	// OnUpdate is MySQL's normalized referential action spelling.
	OnUpdate string `json:"on_update"`
	// Indexed reports whether the ordered child columns are a leftmost prefix
	// of an index. It is true by construction for authoritative InnoDB facts.
	Indexed bool `json:"indexed"`
}

// ForeignKeyResult is a selected foreign-key fact set coupled to its
// completeness proof.
//
// ForeignKeyResult is safe for concurrent reads. Callers must synchronize
// mutation of Keys, nested column slices, DowngradeReason, or PrimaryError.
// The package does not mutate PrimaryError after return; a concrete error
// supplied by a custom Querier retains its own concurrency guarantees.
type ForeignKeyResult struct {
	// Keys contains selected constraints in deterministic selector order.
	Keys []ForeignKey `json:"keys"`
	// Visibility reports whether discovery is complete for registered InnoDB
	// constraints or came from the visibility-filtered fallback.
	Visibility MetadataVisibility `json:"visibility"`
	// DowngradeReason reports whether the primary query failed or its returned
	// metadata could not be read. It is ForeignKeyDowngradeNone unless fallback
	// succeeded after a primary-source error.
	DowngradeReason ForeignKeyDowngradeReason `json:"downgrade_reason"`
	// PrimaryError is the wrapped primary-source error retained after a
	// successful fallback. Inspect it with errors.Is or errors.As. It is nil
	// when no fallback succeeded and is deliberately excluded from JSON.
	PrimaryError error `json:"-"`
}

// ForeignKeys returns constraints matching sel. It first queries the
// PROCESS-gated InnoDB registry; success proves VisibilityComplete. Any
// primary-source error selects the standard information_schema fallback,
// whose success is VisibilityUnconfirmed and carries the wrapped primary error
// plus its query or read stage. A query-stage reason does not classify the
// concrete cause as a permission failure; inspect PrimaryError with errors.Is
// or errors.As. Failure of both sources returns a zero result and preserves
// both causes in the returned error.
//
// IncomingTo follows requested parent-table order. OutgoingFrom and Within
// follow requested child-table order. Ties sort by exact child schema, child
// table, and constraint name. A duplicate requested table repeats matching
// constraints. A constructor-produced empty selector returns a zero result
// without querying; the zero selector is invalid.
//
// ForeignKeys is safe for concurrent use when the Inspector's Querier is safe
// for concurrent use.
func (i *Inspector) ForeignKeys(
	ctx context.Context,
	sel FKSelector,
) (ForeignKeyResult, error) {
	if err := i.validate(opForeignKeys, sel.tables); err != nil {
		return ForeignKeyResult{}, err
	}
	if !sel.kind.valid() {
		return ForeignKeyResult{}, newObjectError(
			opForeignKeys,
			i.schema,
			"",
			fmt.Errorf("%w: kind %d", ErrInvalidFKSelector, sel.kind),
		)
	}
	if len(sel.tables) == 0 {
		return ForeignKeyResult{}, nil
	}

	keys, downgradeReason, primaryErr := i.foreignKeysInnoDB(ctx, sel)
	if primaryErr == nil {
		return ForeignKeyResult{
			Keys:       selectForeignKeys(keys, i.schema, sel),
			Visibility: VisibilityComplete,
		}, nil
	}

	keys, fallbackErr := i.foreignKeysStandard(ctx, sel)
	if fallbackErr == nil {
		return ForeignKeyResult{
			Keys:            selectForeignKeys(keys, i.schema, sel),
			Visibility:      VisibilityUnconfirmed,
			DowngradeReason: downgradeReason,
			PrimaryError:    primaryErr,
		}, nil
	}

	return ForeignKeyResult{}, newObjectError(
		opForeignKeys,
		i.schema,
		"",
		errors.Join(primaryErr, fallbackErr),
	)
}

func (k fkSelectorKind) valid() bool {
	return k >= fkSelectorIncoming && k <= fkSelectorWithin
}

func (i *Inspector) foreignKeysInnoDB(
	ctx context.Context,
	sel FKSelector,
) ([]ForeignKey, ForeignKeyDowngradeReason, error) {
	column := "f.FOR_NAME"
	if sel.kind == fkSelectorIncoming {
		column = "f.REF_NAME"
	}
	// INNODB_FOREIGN names a table as "schema/table" in one column, so the
	// bound parameters are composed values rather than the caller's table
	// names. narrowNames must see exactly what is bound: the schema reaches
	// the wire here, and NewInspector validates nothing.
	composed := make([]string, 0, len(sel.tables))
	for _, table := range sel.tables {
		composed = append(composed, i.schema+"/"+table)
	}

	narrowed := `
		SELECT
			f.ID,
			f.FOR_NAME,
			f.REF_NAME,
			f.N_COLS,
			f.TYPE,
			c.FOR_COL_NAME,
			c.REF_COL_NAME,
			c.POS
		FROM information_schema.INNODB_FOREIGN AS f
		JOIN information_schema.INNODB_FOREIGN_COLS AS c
		  ON c.ID = f.ID`

	// This table carries no schema column — the schema is embedded in the
	// name — so there is no narrower predicate to keep, and dropping the IN
	// clause widens the read from one schema to the whole server. That is
	// deliberate. The alternative, a LIKE 'schema/%' pattern, needs % and _
	// escaped out of the schema name, and a defect of exactly that shape
	// already reached review once (see the v0.6.4 record). selectForeignKeys
	// compares schemas in Go, so the wider read cannot widen the answer.
	//
	// It can widen the *error* surface: scanInnoDBForeignKeys validates every
	// row it reads, so a malformed constraint in a schema this call never
	// named can fail it. That is the accepted cost of a fallback that only
	// runs for a request no narrowed statement could represent.
	var args []any
	if params, ok := narrowNames(composed, 0); ok {
		narrowed += `
		WHERE ` + column + ` IN (` + sqlPlaceholders(len(params)) + `)`
		args = make([]any, 0, len(params))
		for _, name := range params {
			args = append(args, name)
		}
	}
	query := narrowed + `
		ORDER BY f.ID, c.POS`

	// Deliberately unguarded, unlike foreignKeysStandard below. VisibilityComplete
	// means "the PROCESS-gated InnoDB query succeeded" — see the constant's own
	// doc — so a skipped query could not honestly claim it: completeness is a
	// property of having asked, not of the answer being predictable in advance.
	//
	// This path already answers an unrepresentable schema correctly without a
	// guard: composed above carries the schema, so narrowNames rejects it,
	// the predicate above is dropped, the unnarrowed read returns rows from the
	// whole server, and selectForeignKeys' Go comparison matches none of them.
	// The whole-server read is the price of an honest completeness proof, and
	// it is already paid. See representable and docs/COMPAT.md entry 8 for the
	// guard this call does not need.
	rows, err := i.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, ForeignKeyDowngradePrimaryQueryError,
			fmt.Errorf("query InnoDB foreign-key metadata: %w", err)
	}
	defer rows.Close()

	keys, err := scanInnoDBForeignKeys(rows)
	if err != nil {
		return nil, ForeignKeyDowngradePrimaryReadError,
			fmt.Errorf("read InnoDB foreign-key metadata: %w", err)
	}

	return keys, ForeignKeyDowngradeNone, nil
}

type innoDBForeignKeyGroup struct {
	id            string
	forName       string
	refName       string
	columnCount   uint64
	flags         uint64
	childColumns  []string
	parentColumns []string
	nextPosition  uint64
}

func scanInnoDBForeignKeys(rows *sql.Rows) ([]ForeignKey, error) {
	var (
		keys    []ForeignKey
		current *innoDBForeignKeyGroup
	)
	for rows.Next() {
		var (
			id, forName, refName, childColumn, parentColumn string
			columnCount, flags, position                    uint64
		)
		if err := rows.Scan(
			&id,
			&forName,
			&refName,
			&columnCount,
			&flags,
			&childColumn,
			&parentColumn,
			&position,
		); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		if current == nil || current.id != id {
			if current != nil {
				key, err := current.foreignKey()
				if err != nil {
					return nil, err
				}
				keys = append(keys, key)
			}
			current = &innoDBForeignKeyGroup{
				id: id, forName: forName, refName: refName,
				columnCount: columnCount, flags: flags, nextPosition: 1,
			}
		}
		if current.forName != forName ||
			current.refName != refName ||
			current.columnCount != columnCount ||
			current.flags != flags {
			return nil, fmt.Errorf("constraint %q has inconsistent scalar fields", id)
		}
		if position != current.nextPosition {
			return nil, fmt.Errorf(
				"constraint %q column position is %d, want %d",
				id,
				position,
				current.nextPosition,
			)
		}
		current.childColumns = append(current.childColumns, childColumn)
		current.parentColumns = append(current.parentColumns, parentColumn)
		current.nextPosition++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	if current != nil {
		key, err := current.foreignKey()
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}

	return keys, nil
}

func (g *innoDBForeignKeyGroup) foreignKey() (ForeignKey, error) {
	constraintSchema, constraintName, err := splitInnoDBName(g.id)
	if err != nil {
		return ForeignKey{}, fmt.Errorf("constraint ID %q: %w", g.id, err)
	}
	childSchema, childTable, err := splitInnoDBName(g.forName)
	if err != nil {
		return ForeignKey{}, fmt.Errorf("constraint %q child name: %w", g.id, err)
	}
	parentSchema, parentTable, err := splitInnoDBName(g.refName)
	if err != nil {
		return ForeignKey{}, fmt.Errorf("constraint %q parent name: %w", g.id, err)
	}
	if constraintSchema != childSchema {
		return ForeignKey{}, fmt.Errorf(
			"constraint %q schema %q differs from child schema %q",
			g.id,
			constraintSchema,
			childSchema,
		)
	}
	if g.columnCount == 0 || uint64(len(g.childColumns)) != g.columnCount {
		return ForeignKey{}, fmt.Errorf(
			"constraint %q has %d column rows, want %d",
			g.id,
			len(g.childColumns),
			g.columnCount,
		)
	}
	deleteRule, updateRule, err := decodeInnoDBForeignKeyRules(g.flags)
	if err != nil {
		return ForeignKey{}, fmt.Errorf("constraint %q TYPE %d: %w", g.id, g.flags, err)
	}

	return ForeignKey{
		ConstraintName: constraintName,
		ChildSchema:    childSchema,
		ChildTable:     childTable,
		ChildColumns:   g.childColumns,
		ParentSchema:   parentSchema,
		ParentTable:    parentTable,
		ParentColumns:  g.parentColumns,
		OnDelete:       deleteRule,
		OnUpdate:       updateRule,
		Indexed:        true,
	}, nil
}

func splitInnoDBName(value string) (left, right string, err error) {
	index := strings.IndexByte(value, '/')
	if index <= 0 || index == len(value)-1 {
		return "", "", fmt.Errorf("name %q has no non-empty schema/name separator", value)
	}

	return value[:index], value[index+1:], nil
}

func decodeInnoDBForeignKeyRules(
	flags uint64,
) (deleteRule, updateRule string, err error) {
	const knownFlags uint64 = 1 | 2 | 4 | 8 | 16 | 32
	if flags&^knownFlags != 0 {
		return "", "", fmt.Errorf("unknown flag bits %d", flags&^knownFlags)
	}

	deleteRule, err = decodeForeignKeyRule(flags, []ruleFlag{
		{flag: 1, rule: fkRuleCascade},
		{flag: 2, rule: fkRuleSetNull},
		{flag: 16, rule: fkRuleNoAction},
	})
	if err != nil {
		return "", "", fmt.Errorf("delete rule: %w", err)
	}
	updateRule, err = decodeForeignKeyRule(flags, []ruleFlag{
		{flag: 4, rule: fkRuleCascade},
		{flag: 8, rule: fkRuleSetNull},
		{flag: 32, rule: fkRuleNoAction},
	})
	if err != nil {
		return "", "", fmt.Errorf("update rule: %w", err)
	}

	return deleteRule, updateRule, nil
}

type ruleFlag struct {
	flag uint64
	rule string
}

func decodeForeignKeyRule(flags uint64, choices []ruleFlag) (string, error) {
	rule := fkRuleRestrict
	found := false
	for _, choice := range choices {
		if flags&choice.flag == 0 {
			continue
		}
		if found {
			return "", errors.New("mutually exclusive flags are both set")
		}
		rule = choice.rule
		found = true
	}

	return rule, nil
}

func (i *Inspector) foreignKeysStandard(
	ctx context.Context,
	sel FKSelector,
) ([]ForeignKey, error) {
	columnSchema := "kcu.TABLE_SCHEMA"
	columnTable := "kcu.TABLE_NAME"
	if sel.kind == fkSelectorIncoming {
		columnSchema = "kcu.REFERENCED_TABLE_SCHEMA"
		columnTable = "kcu.REFERENCED_TABLE_NAME"
	}
	// A schema rejected by representable cannot be the exact spelling of anything
	// information_schema reports, so the standard fallback is answerable without
	// asking. A supplementary character would make this fixed comparison fail
	// with ER_IMPOSSIBLE_STRING_CONVERSION (3988); invalid UTF-8 already matches
	// nothing. See representable and docs/COMPAT.md entry 8. Skipping this source
	// preserves its empty result for both cases and repairs the supplementary one;
	// the InnoDB source above remains deliberately queried.
	if !representable(i.schema) {
		return nil, nil
	}

	// Unlike the InnoDB source, this one has a schema column, so the
	// unnarrowed form keeps its schema equality and only drops the table list.
	narrowed := `
		SELECT
			kcu.TABLE_SCHEMA,
			kcu.TABLE_NAME,
			kcu.CONSTRAINT_NAME,
			kcu.COLUMN_NAME,
			kcu.REFERENCED_TABLE_SCHEMA,
			kcu.REFERENCED_TABLE_NAME,
			kcu.REFERENCED_COLUMN_NAME,
			rc.DELETE_RULE,
			rc.UPDATE_RULE,
			kcu.ORDINAL_POSITION
		FROM information_schema.KEY_COLUMN_USAGE AS kcu
		JOIN information_schema.REFERENTIAL_CONSTRAINTS AS rc
		  ON rc.CONSTRAINT_SCHEMA = kcu.CONSTRAINT_SCHEMA
		 AND rc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
		WHERE kcu.REFERENCED_TABLE_NAME IS NOT NULL
		  AND ` + columnSchema + ` = ?`
	args := make([]any, 0, len(sel.tables)+1)
	args = append(args, i.schema)
	if params, ok := narrowNames(sel.tables, len(args)); ok {
		narrowed += `
		  AND ` + columnTable + ` IN (` + sqlPlaceholders(len(params)) + `)`
		for _, table := range params {
			args = append(args, table)
		}
	}
	query := narrowed + `
		ORDER BY
			kcu.TABLE_SCHEMA,
			kcu.TABLE_NAME,
			kcu.CONSTRAINT_NAME,
			kcu.ORDINAL_POSITION`

	rows, err := i.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query standard foreign-key metadata: %w", err)
	}
	defer rows.Close()

	keys, err := scanStandardForeignKeys(rows)
	if err != nil {
		return nil, fmt.Errorf("read standard foreign-key metadata: %w", err)
	}
	if err := i.populateFallbackIndexes(ctx, keys); err != nil {
		return nil, err
	}

	return keys, nil
}

type standardForeignKeyGroup struct {
	key          string
	fact         ForeignKey
	nextPosition uint64
}

func scanStandardForeignKeys(rows *sql.Rows) ([]ForeignKey, error) {
	var (
		keys    []ForeignKey
		current *standardForeignKeyGroup
	)
	for rows.Next() {
		var (
			fact      ForeignKey
			childCol  string
			parentCol string
			position  uint64
		)
		if err := rows.Scan(
			&fact.ChildSchema,
			&fact.ChildTable,
			&fact.ConstraintName,
			&childCol,
			&fact.ParentSchema,
			&fact.ParentTable,
			&parentCol,
			&fact.OnDelete,
			&fact.OnUpdate,
			&position,
		); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		groupKey := fact.ChildSchema + "\x00" + fact.ConstraintName
		if current == nil || current.key != groupKey {
			if current != nil {
				keys = append(keys, current.fact)
			}
			current = &standardForeignKeyGroup{key: groupKey, fact: fact, nextPosition: 1}
		}
		if current.fact.ChildSchema != fact.ChildSchema ||
			current.fact.ChildTable != fact.ChildTable ||
			current.fact.ConstraintName != fact.ConstraintName ||
			current.fact.ParentSchema != fact.ParentSchema ||
			current.fact.ParentTable != fact.ParentTable ||
			current.fact.OnDelete != fact.OnDelete ||
			current.fact.OnUpdate != fact.OnUpdate {
			return nil, fmt.Errorf("constraint %q has inconsistent scalar fields", fact.ConstraintName)
		}
		if position != current.nextPosition {
			return nil, fmt.Errorf(
				"constraint %q column position is %d, want %d",
				fact.ConstraintName,
				position,
				current.nextPosition,
			)
		}
		current.fact.ChildColumns = append(current.fact.ChildColumns, childCol)
		current.fact.ParentColumns = append(current.fact.ParentColumns, parentCol)
		current.nextPosition++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	if current != nil {
		keys = append(keys, current.fact)
	}

	return keys, nil
}

func (i *Inspector) populateFallbackIndexes(
	ctx context.Context,
	keys []ForeignKey,
) error {
	if len(keys) == 0 {
		return nil
	}

	pairs := make([]tableIdentity, 0, len(keys))
	seen := make(map[tableIdentity]struct{}, len(keys))
	for index := range keys {
		key := &keys[index]
		pair := tableIdentity{schema: key.ChildSchema, table: key.ChildTable}
		if _, ok := seen[pair]; ok {
			continue
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}

	// This predicate binds two parameters per pair and is built by hand rather
	// than through sqlPlaceholders, so it needs the same ceiling by its own
	// arithmetic. It batches instead of falling back to an unnarrowed query:
	// the scan below density-checks every row it reads before anything is
	// selected, so reading the whole server's index metadata would validate
	// tables this call never named, and one non-dense index anywhere would
	// fail it.
	//
	// Batches partition whole pairs. That is load-bearing rather than tidy —
	// the density check counts what has accumulated for an (identity, index)
	// so far, so a pair split across two statements would fail it spuriously.
	//
	// Merging is safe here in a way it is not for the IN (...) sites: this
	// accumulates into a lookup map, and every caller-visible ordering
	// decision is made afterwards by selectForeignKeys.
	indexes := make(map[tableIdentity]map[string][]string)
	for start := 0; start < len(pairs); start += maxStatementParameters / 2 {
		end := min(start+maxStatementParameters/2, len(pairs))
		if err := i.readFallbackIndexes(ctx, pairs[start:end], indexes); err != nil {
			return err
		}
	}

	candidatesByTable := make(map[tableIdentity][][]string, len(indexes))
	for identity, byName := range indexes {
		candidates := make([][]string, 0, len(byName))
		for _, columns := range byName {
			candidates = append(candidates, columns)
		}
		candidatesByTable[identity] = candidates
	}

	for index := range keys {
		identity := tableIdentity{
			schema: keys[index].ChildSchema,
			table:  keys[index].ChildTable,
		}
		keys[index].Indexed = foreignKeyColumnsIndexed(
			keys[index].ChildColumns, candidatesByTable[identity],
		)
	}

	return nil
}

// readFallbackIndexes reads one batch of pairs into indexes, which spans every
// batch of one call.
func (i *Inspector) readFallbackIndexes(
	ctx context.Context,
	pairs []tableIdentity,
	indexes map[tableIdentity]map[string][]string,
) error {
	clauses := make([]string, 0, len(pairs))
	args := make([]any, 0, len(pairs)*2)
	for _, pair := range pairs {
		clauses = append(clauses, "(TABLE_SCHEMA = ? AND TABLE_NAME = ?)")
		args = append(args, pair.schema, pair.table)
	}
	query := `
		SELECT TABLE_SCHEMA, TABLE_NAME, INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX
		FROM information_schema.STATISTICS
		WHERE ` + strings.Join(clauses, " OR ") + `
		ORDER BY TABLE_SCHEMA, TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`

	rows, err := i.q.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query fallback supporting indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			schema, table, index string
			// COLUMN_NAME is NULL for a functional key part; the expression
			// lives in EXPRESSION. See docs/COMPAT.md entry 18.
			column   sql.NullString
			position uint64
		)
		if err := rows.Scan(&schema, &table, &index, &column, &position); err != nil {
			return fmt.Errorf("scan fallback supporting index: %w", err)
		}
		identity := tableIdentity{schema: schema, table: table}
		if indexes[identity] == nil {
			indexes[identity] = make(map[string][]string)
		}
		columns := indexes[identity][index]
		if position != uint64(len(columns)+1) {
			return fmt.Errorf(
				"index %q on %s/%s position is %d, want %d",
				index,
				schema,
				table,
				position,
				len(columns)+1,
			)
		}
		// A NULL part is recorded as "" rather than dropped, for two reasons.
		// It keeps SEQ_IN_INDEX dense, so the check above still holds. And it
		// keeps the part in the position it occupies: dropping it would let a
		// later column stand in a leftmost slot it does not hold, so
		// INDEX((f(a)), a, b) would look like support for FOREIGN KEY (a, b),
		// which MySQL requires be the index's first columns in order. "" cannot
		// collide with a real name — an identifier is never empty, and the
		// columns compared against come from KEY_COLUMN_USAGE, which reports
		// only columns because functional parts cannot appear in a foreign key.
		indexes[identity][index] = append(columns, column.String)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate fallback supporting indexes: %w", err)
	}

	return nil
}

type tableIdentity struct {
	schema string
	table  string
}

func foreignKeyColumnsIndexed(columns []string, indexes [][]string) bool {
	if len(columns) == 0 {
		return false
	}
	for _, index := range indexes {
		if len(index) < len(columns) {
			continue
		}
		matches := true
		for position := range columns {
			if index[position] != columns[position] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}

	return false
}

func selectForeignKeys(keys []ForeignKey, schema string, sel FKSelector) []ForeignKey {
	tableSet := make(map[string]struct{}, len(sel.tables))
	for _, table := range sel.tables {
		tableSet[table] = struct{}{}
	}

	byRequested := make(map[string][]int, len(tableSet))
	for index := range keys {
		requested, ok := foreignKeySelectorTable(&keys[index], schema, tableSet, sel.kind)
		if !ok {
			continue
		}
		byRequested[requested] = append(byRequested[requested], index)
	}
	for _, indexes := range byRequested {
		sort.Slice(indexes, func(left, right int) bool {
			leftKey := &keys[indexes[left]]
			rightKey := &keys[indexes[right]]
			if leftKey.ChildSchema != rightKey.ChildSchema {
				return leftKey.ChildSchema < rightKey.ChildSchema
			}
			if leftKey.ChildTable != rightKey.ChildTable {
				return leftKey.ChildTable < rightKey.ChildTable
			}

			return leftKey.ConstraintName < rightKey.ConstraintName
		})
	}

	var selected []ForeignKey
	for _, requested := range sel.tables {
		for _, index := range byRequested[requested] {
			match := keys[index]
			match.ChildColumns = slices.Clone(match.ChildColumns)
			match.ParentColumns = slices.Clone(match.ParentColumns)
			selected = append(selected, match)
		}
	}

	return selected
}

func foreignKeySelectorTable(
	key *ForeignKey,
	schema string,
	tableSet map[string]struct{},
	kind fkSelectorKind,
) (string, bool) {
	switch kind {
	case fkSelectorIncoming:
		_, requested := tableSet[key.ParentTable]
		return key.ParentTable, key.ParentSchema == schema && requested
	case fkSelectorOutgoing:
		_, requested := tableSet[key.ChildTable]
		return key.ChildTable, key.ChildSchema == schema && requested
	case fkSelectorWithin:
		if key.ChildSchema != schema || key.ParentSchema != schema {
			return "", false
		}
		_, childRequested := tableSet[key.ChildTable]
		_, parentRequested := tableSet[key.ParentTable]
		return key.ChildTable, childRequested && parentRequested
	default:
		return "", false
	}
}
