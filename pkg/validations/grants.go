package validations

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	grantStatePresentName = "present"
	privilegeCreateName   = "CREATE"
	privilegeSelectName   = "SELECT"
)

type querierAffinity uint8

const (
	affinityOpaque querierAffinity = iota
	affinityPool
	affinityPinned
)

type grantSources uint8

const (
	grantSourceAccount grantSources = 1 << iota
	grantSourceRole
)

type schemaPrivilegeKey struct {
	schema    string
	privilege Privilege
}

type tablePrivilegeKey struct {
	schema    string
	table     string
	privilege Privilege
}

// GrantState reports whether a requested privilege is established.
//
// The zero value means the fact was not populated. GrantState is a plain value
// and is safe for concurrent use.
type GrantState uint8

const (
	// GrantUnknown means the fact or requested privilege was not populated.
	GrantUnknown GrantState = iota
	// GrantPresent means a qualifying grant is established for this session.
	GrantPresent
	// GrantAbsent means a pinned, role-free session has no qualifying grant.
	GrantAbsent
	// GrantUnconfirmed means session, role, partial-revoke, or wildcard-grant
	// uncertainty prevents either a present or absent conclusion.
	GrantUnconfirmed
)

// String returns a stable human spelling for the grant state.
//
// String is safe for concurrent use.
func (s GrantState) String() string {
	switch s {
	case GrantUnknown:
		return unknownEnum
	case GrantPresent:
		return grantStatePresentName
	case GrantAbsent:
		return "absent"
	case GrantUnconfirmed:
		return grantStateUnconfirmedName
	default:
		return "GrantState(" + strconv.Itoa(int(s)) + ")"
	}
}

// Privilege is one static MySQL privilege supported by the validation checks.
//
// The zero value names no privilege. Privilege is a plain value and is safe
// for concurrent use.
type Privilege uint8

const (
	// PrivilegeUnknown names no privilege.
	PrivilegeUnknown Privilege = iota
	// PrivilegeSelect is MySQL's SELECT privilege.
	PrivilegeSelect
	// PrivilegeInsert is MySQL's INSERT privilege.
	PrivilegeInsert
	// PrivilegeUpdate is MySQL's UPDATE privilege.
	PrivilegeUpdate
	// PrivilegeDelete is MySQL's DELETE privilege.
	PrivilegeDelete
	// PrivilegeCreate is MySQL's CREATE privilege.
	PrivilegeCreate
)

// String returns the upper-case MySQL spelling of the privilege.
//
// String is safe for concurrent use.
func (p Privilege) String() string {
	switch p {
	case PrivilegeUnknown:
		return unknownEnum
	case PrivilegeSelect:
		return privilegeSelectName
	case PrivilegeInsert:
		return triggerEventInsert
	case PrivilegeUpdate:
		return triggerEventUpdate
	case PrivilegeDelete:
		return triggerEventDelete
	case PrivilegeCreate:
		return privilegeCreateName
	default:
		return "Privilege(" + strconv.Itoa(int(p)) + ")"
	}
}

func (p Privilege) valid() bool {
	return p >= PrivilegeSelect && p <= PrivilegeCreate
}

func privilegeFromString(value string) (Privilege, bool) {
	switch value {
	case privilegeSelectName:
		return PrivilegeSelect, true
	case triggerEventInsert:
		return PrivilegeInsert, true
	case triggerEventUpdate:
		return PrivilegeUpdate, true
	case triggerEventDelete:
		return PrivilegeDelete, true
	case privilegeCreateName:
		return PrivilegeCreate, true
	default:
		return PrivilegeUnknown, false
	}
}

// PrivilegeFact is one privilege answer used as a finding payload. Table is
// empty for a schema-scoped answer.
//
// PrivilegeFact is a plain value and is safe for concurrent use.
type PrivilegeFact struct {
	// Schema is the schema whose privilege was requested.
	Schema string `json:"schema"`
	// Table is the requested table, or empty for a schema-scoped answer.
	Table string `json:"table"`
	// Privilege is the requested privilege.
	Privilege Privilege `json:"privilege"`
	// State is the resolved grant state.
	State GrantState `json:"state"`
}

// Grants is the assembled current-account privilege fact. It is immutable
// after construction.
//
// Exact *sql.Conn and *sql.Tx values are pinned; an exact *sql.DB is a known
// pool; every wrapper or custom Querier is opaque. A pool may confirm a
// direct-account positive but degrades every negative. An opaque Querier
// degrades all otherwise-present and absent answers. Every role-dependent
// answer is unconfirmed because ordinary privilege tables do not expose the
// enabled role's grant rows to that account, and nested role closure is not
// resolved. While partial revokes are enabled, a global privilege row proves
// nothing on its own: schema and table answers stay unconfirmed until a direct
// schema or table grant proves the object, and the global answer stays
// unconfirmed because this package does not read the restriction list. A
// privilege with no grant row at any scope is still reported absent. A wildcard
// schema grant matching the requested schema downgrades an otherwise-absent
// schema or table answer to unconfirmed, and never proves one.
//
// Declining to consult SHOW GRANTS is a deliberate choice rather than an
// oversight, and the unconfirmed role answers above should be read as "not
// proven by this fact", not as "unprovable by any means". SHOW GRANTS merges
// the session's currently active roles and, for the current user, needs no
// SELECT privilege on the mysql schema, so it can show a role-derived privilege
// to be effective where the privilege tables cannot. It is declined because it
// answers a different question: it returns version-sensitive text about the
// roles active on one session, where this fact reports relational rows about
// the account. See docs/COMPAT.md entry 4.
//
// Grants is safe for concurrent use.
type Grants struct {
	populated      bool
	affinity       querierAffinity
	partialRevokes bool
	accountGrantee string
	roleGrantees   map[string]struct{}
	global         map[Privilege]grantSources
	schema         map[schemaPrivilegeKey]grantSources
	table          map[tablePrivilegeKey]grantSources
}

// Grants assembles privileges for CURRENT_USER() and the session's structured
// ENABLED_ROLES identities from the global, schema, and table privilege
// metadata tables. It also records @@global.partial_revokes.
//
// The several statements are not an atomic snapshot. Callers that use SET ROLE
// must bind the Inspector to the same *sql.Conn or *sql.Tx that will perform the
// protected work. Grants is safe for concurrent use when the Inspector's
// Querier is safe for concurrent use.
func (i *Inspector) Grants(ctx context.Context) (Grants, error) {
	if err := i.validate(opGrants, nil); err != nil {
		return Grants{}, err
	}

	fact := Grants{
		affinity:     classifyQuerier(i.q),
		roleGrantees: make(map[string]struct{}),
		global:       make(map[Privilege]grantSources),
		schema:       make(map[schemaPrivilegeKey]grantSources),
		table:        make(map[tablePrivilegeKey]grantSources),
	}

	var currentUser string
	if err := i.q.QueryRowContext(ctx, "SELECT CURRENT_USER()").Scan(&currentUser); err != nil {
		return Grants{}, newObjectError(
			opGrants,
			i.schema,
			"",
			fmt.Errorf("resolve CURRENT_USER(): %w", err),
		)
	}
	fact.accountGrantee = formatCurrentUserGrantee(currentUser)

	if err := i.readEnabledRoles(ctx, &fact); err != nil {
		return Grants{}, newObjectError(opGrants, i.schema, "", err)
	}

	var partialRevokes int
	if err := i.q.QueryRowContext(ctx, "SELECT @@global.partial_revokes").Scan(
		&partialRevokes,
	); err != nil {
		return Grants{}, newObjectError(
			opGrants,
			i.schema,
			"",
			fmt.Errorf("read @@global.partial_revokes: %w", err),
		)
	}
	fact.partialRevokes = partialRevokes != 0

	grantees := make([]string, 0, len(fact.roleGrantees)+1)
	grantees = append(grantees, fact.accountGrantee)
	for grantee := range fact.roleGrantees {
		grantees = append(grantees, grantee)
	}
	sort.Strings(grantees[1:])
	if err := i.readGlobalGrants(ctx, &fact, grantees); err != nil {
		return Grants{}, newObjectError(opGrants, i.schema, "", err)
	}
	if err := i.readSchemaGrants(ctx, &fact, grantees); err != nil {
		return Grants{}, newObjectError(opGrants, i.schema, "", err)
	}
	if err := i.readTableGrants(ctx, &fact, grantees); err != nil {
		return Grants{}, newObjectError(opGrants, i.schema, "", err)
	}

	fact.populated = true

	return fact, nil
}

func classifyQuerier(q Querier) querierAffinity {
	switch q.(type) {
	case *sql.Conn, *sql.Tx:
		return affinityPinned
	case *sql.DB:
		return affinityPool
	default:
		return affinityOpaque
	}
}

func (i *Inspector) readEnabledRoles(ctx context.Context, fact *Grants) error {
	const query = `
		SELECT ROLE_NAME, ROLE_HOST
		FROM information_schema.ENABLED_ROLES
		ORDER BY ROLE_NAME, ROLE_HOST`
	rows, err := i.q.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query ENABLED_ROLES: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, host string
		if err := rows.Scan(&name, &host); err != nil {
			return fmt.Errorf("scan ENABLED_ROLES: %w", err)
		}
		fact.roleGrantees[formatRoleGrantee(name, host)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ENABLED_ROLES: %w", err)
	}

	return nil
}

func (i *Inspector) readGlobalGrants(
	ctx context.Context,
	fact *Grants,
	grantees []string,
) error {
	query := `
		SELECT GRANTEE, PRIVILEGE_TYPE
		FROM information_schema.USER_PRIVILEGES
		WHERE GRANTEE IN (` + sqlPlaceholders(len(grantees)) + `)`
	rows, err := i.q.QueryContext(ctx, query, stringsToAny(grantees)...)
	if err != nil {
		return fmt.Errorf("query USER_PRIVILEGES: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var grantee, privilegeName string
		if err := rows.Scan(&grantee, &privilegeName); err != nil {
			return fmt.Errorf("scan USER_PRIVILEGES: %w", err)
		}
		privilege, ok := privilegeFromString(privilegeName)
		if !ok {
			continue
		}
		fact.global[privilege] |= fact.sourceFor(grantee)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate USER_PRIVILEGES: %w", err)
	}

	return nil
}

func (i *Inspector) readSchemaGrants(
	ctx context.Context,
	fact *Grants,
	grantees []string,
) error {
	query := `
		SELECT GRANTEE, TABLE_SCHEMA, PRIVILEGE_TYPE
		FROM information_schema.SCHEMA_PRIVILEGES
		WHERE GRANTEE IN (` + sqlPlaceholders(len(grantees)) + `)`
	rows, err := i.q.QueryContext(ctx, query, stringsToAny(grantees)...)
	if err != nil {
		return fmt.Errorf("query SCHEMA_PRIVILEGES: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var grantee, schema, privilegeName string
		if err := rows.Scan(&grantee, &schema, &privilegeName); err != nil {
			return fmt.Errorf("scan SCHEMA_PRIVILEGES: %w", err)
		}
		privilege, ok := privilegeFromString(privilegeName)
		if !ok {
			continue
		}
		key := schemaPrivilegeKey{schema: schema, privilege: privilege}
		fact.schema[key] |= fact.sourceFor(grantee)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SCHEMA_PRIVILEGES: %w", err)
	}

	return nil
}

func (i *Inspector) readTableGrants(
	ctx context.Context,
	fact *Grants,
	grantees []string,
) error {
	query := `
		SELECT GRANTEE, TABLE_SCHEMA, TABLE_NAME, PRIVILEGE_TYPE
		FROM information_schema.TABLE_PRIVILEGES
		WHERE GRANTEE IN (` + sqlPlaceholders(len(grantees)) + `)`
	rows, err := i.q.QueryContext(ctx, query, stringsToAny(grantees)...)
	if err != nil {
		return fmt.Errorf("query TABLE_PRIVILEGES: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var grantee, schema, table, privilegeName string
		if err := rows.Scan(&grantee, &schema, &table, &privilegeName); err != nil {
			return fmt.Errorf("scan TABLE_PRIVILEGES: %w", err)
		}
		privilege, ok := privilegeFromString(privilegeName)
		if !ok {
			continue
		}
		key := tablePrivilegeKey{
			schema: schema, table: table, privilege: privilege,
		}
		fact.table[key] |= fact.sourceFor(grantee)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate TABLE_PRIVILEGES: %w", err)
	}

	return nil
}

func (g Grants) sourceFor(grantee string) grantSources {
	var source grantSources
	if grantee == g.accountGrantee {
		source |= grantSourceAccount
	}
	if _, ok := g.roleGrantees[grantee]; ok {
		source |= grantSourceRole
	}

	return source
}

// Global reports whether priv is established at global scope.
//
// Global is safe for concurrent use.
func (g Grants) Global(priv Privilege) GrantState {
	return g.resolve(priv, nil, g.global[priv])
}

// Schema reports whether priv is established for schema through a schema or
// global grant.
//
// Schema is safe for concurrent use.
func (g Grants) Schema(schema string, priv Privilege) GrantState {
	specific := []grantSources{
		g.schema[schemaPrivilegeKey{schema: schema, privilege: priv}],
	}
	state := g.resolve(priv, specific, g.global[priv])

	return g.downgradeForSchemaPattern(state, schema, priv)
}

// Table reports whether priv is established for schema.table through a table,
// schema, or global grant.
//
// Table is safe for concurrent use.
func (g Grants) Table(schema, table string, priv Privilege) GrantState {
	specific := []grantSources{
		g.table[tablePrivilegeKey{schema: schema, table: table, privilege: priv}],
		g.schema[schemaPrivilegeKey{schema: schema, privilege: priv}],
	}
	state := g.resolve(priv, specific, g.global[priv])

	return g.downgradeForSchemaPattern(state, schema, priv)
}

func (g Grants) resolve(
	priv Privilege,
	specific []grantSources,
	global grantSources,
) GrantState {
	if !priv.valid() || !g.populated {
		return GrantUnknown
	}

	unconfirmed := false
	for _, sources := range specific {
		state := g.stateForSources(sources)
		if state == GrantPresent {
			return GrantPresent
		}
		if state == GrantUnconfirmed {
			unconfirmed = true
		}
	}

	if global != 0 {
		if g.partialRevokes {
			unconfirmed = true
		} else {
			state := g.stateForSources(global)
			if state == GrantPresent {
				return GrantPresent
			}
			if state == GrantUnconfirmed {
				unconfirmed = true
			}
		}
	}
	if unconfirmed ||
		len(g.roleGrantees) > 0 ||
		g.affinity != affinityPinned {
		return GrantUnconfirmed
	}

	return GrantAbsent
}

// downgradeForSchemaPattern turns a provable absence into uncertainty when a
// wildcard schema grant covers the requested schema. It only ever weakens an
// answer: a pattern cannot prove an object, because deciding which of several
// matching mysql.db rows MySQL applies is not something this package models.
func (g Grants) downgradeForSchemaPattern(
	state GrantState,
	schema string,
	priv Privilege,
) GrantState {
	if state != GrantAbsent {
		return state
	}
	for key, sources := range g.schema {
		if key.privilege != priv || sources == 0 || key.schema == schema {
			continue
		}
		if likePatternMatches(key.schema, schema) {
			return GrantUnconfirmed
		}
	}

	return state
}

func (g Grants) stateForSources(sources grantSources) GrantState {
	if sources&grantSourceAccount != 0 {
		switch g.affinity {
		case affinityPinned, affinityPool:
			return GrantPresent
		case affinityOpaque:
			return GrantUnconfirmed
		}
	}
	if sources&grantSourceRole != 0 {
		return GrantUnconfirmed
	}

	return GrantAbsent
}

// formatCurrentUserGrantee reproduces MySQL's own GRANTEE concatenation, which
// does not escape a quote embedded in the user name: `o'brien` becomes the
// literal `'o'brien'@'%'`. See docs/COMPAT.md entry 3 — this is deliberate and
// must not be "fixed" into well-formed SQL, because the column it is matched
// against is built the same way.
func formatCurrentUserGrantee(currentUser string) string {
	index := strings.LastIndexByte(currentUser, '@')
	if index < 0 {
		return "'" + currentUser + "'@'%'"
	}

	return "'" + currentUser[:index] + "'@'" + currentUser[index+1:] + "'"
}

func formatRoleGrantee(name, host string) string {
	return "'" + name + "'@'" + host + "'"
}

func stringsToAny(values []string) []any {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}

	return args
}

// likePatternMatches reports whether pattern matches name under the wildcard
// syntax MySQL applies to the schema column of mysql.db: % matches any run of
// characters, _ matches exactly one, and \ escapes either. Matching is
// case-exact, as everywhere else in this package.
//
// Only schema-level grants carry patterns. MySQL requires a literal schema name
// in mysql.tables_priv, so table-level rows are matched exactly.
func likePatternMatches(pattern, name string) bool {
	patternRunes, nameRunes := []rune(pattern), []rune(name)
	patternIndex, nameIndex := 0, 0
	star, starNext := -1, 0

	for nameIndex < len(nameRunes) {
		matched := false
		if patternIndex < len(patternRunes) {
			switch current := patternRunes[patternIndex]; {
			case current == '%':
				star, starNext = patternIndex, nameIndex
				patternIndex++

				continue
			case current == '\\' && patternIndex+1 < len(patternRunes):
				if patternRunes[patternIndex+1] == nameRunes[nameIndex] {
					patternIndex += 2
					nameIndex++
					matched = true
				}
			case current == '_' || current == nameRunes[nameIndex]:
				patternIndex++
				nameIndex++
				matched = true
			}
		}
		if matched {
			continue
		}
		if star < 0 {
			return false
		}
		starNext++
		patternIndex, nameIndex = star+1, starNext
	}

	for patternIndex < len(patternRunes) && patternRunes[patternIndex] == '%' {
		patternIndex++
	}

	return patternIndex == len(patternRunes)
}
