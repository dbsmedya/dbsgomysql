package validations

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
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

// Grants is the assembled current-account privilege fact. It is immutable
// after construction.
//
// Exact *sql.Conn and *sql.Tx values are pinned; an exact *sql.DB is a known
// pool; every wrapper or custom Querier is opaque. A pool may confirm a
// direct-account positive but degrades every negative. An opaque Querier
// degrades all otherwise-present and absent answers. Every role-dependent
// answer is unconfirmed because ordinary privilege tables do not expose the
// enabled role's grant rows to that account, and nested role closure is not
// resolved. While partial revokes are enabled, global grants are unconfirmed
// for object-scoped answers until a more-specific direct-account grant proves
// the object.
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

	return g.resolve(priv, specific, g.global[priv])
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

	return g.resolve(priv, specific, g.global[priv])
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
		g.partialRevokes ||
		len(g.roleGrantees) > 0 ||
		g.affinity != affinityPinned {
		return GrantUnconfirmed
	}

	return GrantAbsent
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
