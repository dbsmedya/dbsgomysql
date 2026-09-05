# pkg/validations — Consumer Guide

> **Status:** the facts layer, all 15 catalog checks, `TableSpec`, and
> `DiffSpecs` are implemented. Track shipped changes in
> [CHANGELOG.md](../CHANGELOG.md).

`pkg/validations` has two layers built on one connection:

- a **facts layer** that answers typed questions about a MySQL schema, and
- a **checks layer** that turns those facts into named findings with a
  documented rationale.

## Facts, not policy

The library never decides whether something is a problem. `PK_SINGLE_COLUMN`
reports that a table has a composite primary key; whether that is fatal, a
warning, or irrelevant is your policy. **Findings carry no severity** — not even
a default for you to remap, because a default is a decision, and this one is
yours. What each check ships instead is a rationale: the failure mode it
protects against, in its doc comment, so you can judge it rather than inherit a
number.

The contract is four lines:

- a **fact** describes the schema;
- a **check** returns findings when its predicate is not satisfied;
- **no findings** means the check passed for the objects inspected;
- an **error** means the inspection could not be completed.

This is why checks do not disappear when one consumer outgrows them. A tool
that gains composite-primary-key support stops treating that finding as fatal,
but the classification stays in the library, fully tested, because the next
consumer still needs the question answered.

## Getting an Inspector

You supply the connection. The library never opens, configures, or closes one,
and never imports a driver.

```go
import (
    "database/sql"

    _ "github.com/go-sql-driver/mysql" // your choice of driver
    "github.com/dbsmedya/dbsgomysql/pkg/validations"
)

db, err := sql.Open("mysql", dsn)
if err != nil {
    return err
}
defer db.Close()

insp := validations.NewInspector(db, "sakila")
```

`NewInspector` accepts anything with `QueryContext` and `QueryRowContext`, so a
`*sql.DB`, `*sql.Conn`, and `*sql.Tx` all work. Passing a transaction is how you
make inspection observe uncommitted schema changes. A pinned connection matters
for role-sensitive privilege facts, described below.

## Asking factual questions

Every call takes a `context.Context` first.

```go
tables := []string{"film", "film_actor"}

tableFacts, err := insp.Tables(ctx, tables)
columns, err := insp.Columns(ctx, tables)
pks, err := insp.PrimaryKeys(ctx, tables)
invisible, err := insp.InvisibleColumns(ctx, tables)
deleteTriggers, err := insp.Triggers(ctx, tables, validations.TriggerDelete)
incoming, err := insp.ForeignKeys(ctx, validations.IncomingTo(tables...))
grants, err := insp.Grants(ctx)
```

Each fact returns a slice in requested table order. Missing or invisible
objects are absent rather than errors; compare `tableFacts` with `tables` using
`CheckTablesExist` when absence is the question. `PrimaryKeys` answers only for
base tables; a requested view is absent from its result, so it never reaches
`CheckPKExists`. `PKInfo.Columns` retains exact case and primary-key order, and
a single-column key reports its `DataType`, `IsInteger`, and `Unsigned` facts.

Names come back in the server's exact case, and the library compares names in
Go rather than relying on SQL predicates. `information_schema` name collations
vary by category and configuration: some are binary while others fold case.
See [COMPAT.md §2](COMPAT.md).

## General column facts

`Columns` returns every column for each requested table or view:

```go
facts, err := insp.Columns(ctx, []string{"film", "film_list"})
for _, object := range facts {
    for _, column := range object.Columns {
        // column.Name is exact server spelling.
        // Ordinal is one-based; DataType is COLUMNS.DATA_TYPE verbatim.
        // Unsigned reports MySQL's UNSIGNED column attribute.
        // Invisible and Generated are independent facts.
    }
}
```

Results preserve requested-object order, including duplicate requests, and
columns follow `ORDINAL_POSITION`. Missing or metadata-invisible objects are
absent. Requested object identity is matched by exact Go string equality, so a
case-only table variant cannot contribute columns to the requested object.

`Columns` includes views and performs no table-level resolution. It is
deliberately separate from `TableSpec`, which supports base tables only and
captures one table deeply enough for schema comparison. Use `Columns` when the
question is whether an exact, case-only, or differently purposed column exists
without changing the surrounding inspection order.

The `Generated` flag covers virtual and stored generated columns.
`DEFAULT_GENERATED` describes an expression default and does not set it.
`DataType` contains only the type name, so signed and unsigned integers have
the same value there. `Unsigned` is derived from `COLUMNS.COLUMN_TYPE` and lets
callers distinguish their value ranges without parsing MySQL type syntax.

## Foreign keys and completeness

An `Inspector` is bound to the target-set schema. Foreign-key selectors copy
their arguments and use exact qualified `(schema, table)` identity:

```go
incoming, err := insp.ForeignKeys(ctx, validations.IncomingTo(tables...))
outgoing, err := insp.ForeignKeys(ctx, validations.OutgoingFrom(tables...))
within, err := insp.ForeignKeys(ctx, validations.Within(tables...))
```

- `IncomingTo` selects parent-in-set constraints, including same-schema and
  cross-schema children.
- `OutgoingFrom` selects child-in-set constraints.
- `Within` requires both endpoints to belong to the target set.

The zero `FKSelector` is invalid; use a constructor even for an empty set.
Constructors copy the input, preserve duplicates, and an empty constructed
selector returns a zero result without querying. Duplicate requested tables
repeat matching constraints at that requested position. Composite constraints
remain one `ForeignKey` with ordered child and parent column slices.

`ForeignKeys` first executes one
`INNODB_FOREIGN JOIN INNODB_FOREIGN_COLS` statement. Both metadata tables are
gated directly by MySQL's `PROCESS` privilege, so statement success proves that
the returned registered InnoDB constraints are complete and sets
`VisibilityComplete` — including for an empty result. **`PROCESS` is the only
grant required for this complete source.** Table `SELECT`, partial revokes,
active roles, and whether the `Querier` is pooled do not upgrade or downgrade a
successful primary query.

If that statement fails for any reason, the library tries the standard
`KEY_COLUMN_USAGE JOIN REFERENTIAL_CONSTRAINTS` source. Its rows are filtered by
the account's object visibility, so fallback success is always
`VisibilityUnconfirmed`. It then reads `information_schema.STATISTICS` for each
child table to decide `Indexed`; if that read fails, or an index's
`SEQ_IN_INDEX` is not dense, the whole fallback fails and the result carries
both causes. The successful result also carries the wrapped primary failure in
`PrimaryError` and classifies its source stage in `DowngradeReason`:

- `ForeignKeyDowngradePrimaryQueryError` means the primary query returned an
  error before rows were available. It does not necessarily mean missing
  `PROCESS`; cancellation, transport, driver, and custom-`Querier` errors share
  this stage.
- `ForeignKeyDowngradePrimaryReadError` means the primary query succeeded, but
  its rows could not be scanned, iterated, grouped, or decoded.

Use the enum for stable control flow and inspect `PrimaryError` with
`errors.Is` or `errors.As` for the concrete cause:

```go
if incoming.Visibility == validations.VisibilityUnconfirmed {
    switch incoming.DowngradeReason {
    case validations.ForeignKeyDowngradePrimaryQueryError:
        // Inspect incoming.PrimaryError for a driver/server or context cause.
    case validations.ForeignKeyDowngradePrimaryReadError:
        // Report malformed or unreadable authoritative metadata upstream.
    }
}
```

A primary success, including an empty one, carries
`ForeignKeyDowngradeNone` and a nil `PrimaryError`; so does the zero result from
a constructed empty selector. If both sources fail, the returned result remains
zero and the `ObjectError` preserves both causes instead.

`DowngradeReason` is an always-present numeric `downgrade_reason` member when a
`ForeignKeyResult` is encoded as JSON. `PrimaryError` is excluded because an
arbitrary concrete error has no stable cross-driver JSON representation.

Completeness is explicitly InnoDB-scoped. MyISAM parses and ignores foreign-key
declarations, so it has no enforced constraints to discover. NDB Cluster uses a
different registry and is outside the supported and tested server matrix.

Closure keeps the facts and their proof coupled:

```go
incoming, err := insp.ForeignKeys(ctx, validations.IncomingTo(tables...))
if err != nil {
    return err
}
closure := validations.CheckFKClosure(incoming, "sakila", tables)
visibility := validations.CheckFKMetadataVisibility(incoming.Visibility)

within, err := insp.ForeignKeys(ctx, validations.Within(tables...))
cascades := validations.CheckCascadeRules(within.Keys)
```

`CheckFKClosure` reports same-schema children outside `tables` and every
cross-schema child as external. It emits one finding per external key in the
order the result carries them, so a table listed twice in `tables` yields its
external keys twice, as the fact does. For an external key, `Finding.Tables`
names the child table unqualified and `Facts` is the `ForeignKey`, whose
`ChildSchema` is the child's schema. The unconfirmed-closure finding has a
different shape — `Tables` is the requested list and `Facts` is the
`MetadataVisibility` — so switch on the type of `Facts` rather than asserting
`ForeignKey` unconditionally. For a non-empty set it also reports closure as
unconfirmed unless visibility is complete. A `nil` closure result is trustworthy
only when the argument is the matching, unmodified complete result of
`ForeignKeys(ctx, IncomingTo(tables...))` from the Inspector bound to the same
schema. The pure check cannot detect a caller-authored, truncated, substituted,
or wrong-selector result.

## Privileges and session affinity

`Grants` resolves the current account and structured rows from
`information_schema.ENABLED_ROLES`, then records supported privileges at global,
schema, and table scope:

```go
grants, err := insp.Grants(ctx)
read := grants.Table("sakila", "film", validations.PrivilegeSelect)
create := grants.Schema("jobs", validations.PrivilegeCreate)

tableFindings := validations.CheckTablePrivileges(
    grants, "sakila", tables, validations.PrivilegeDelete,
)
schemaFindings := validations.CheckSchemaPrivileges(
    grants,
    "jobs",
    []validations.Privilege{
        validations.PrivilegeCreate,
        validations.PrivilegeSelect,
        validations.PrivilegeInsert,
        validations.PrivilegeUpdate,
    },
)
```

Answers are `GrantPresent`, `GrantAbsent`, `GrantUnconfirmed`, or
`GrantUnknown`. The source connection determines how strong an answer can be:

- exact `*sql.Conn` and `*sql.Tx` values are pinned;
- exact `*sql.DB` values are known pools: a direct current-account positive may
  remain present, but a role-only positive and every negative are unconfirmed;
- wrappers and custom `Querier` implementations are opaque, so even a
  direct-account positive is unconfirmed.

After `SET ROLE`, call `Grants` through the exact pinned `*sql.Conn` or
`*sql.Tx` on which the role was enabled. Enabled roles are session-scoped, and
the fact requires several statements. MySQL does not expose an enabled role's
grant rows through the ordinary privilege tables visible to that account, so
every answer depending only on a role is unconfirmed even on the pinned
session. Nested role closure is also deliberately not resolved: any enabled
role makes an otherwise-negative answer unconfirmed.

While `@@global.partial_revokes` is enabled, a global grant alone proves
nothing: schema and table answers are unconfirmed until a direct matching
schema or table grant proves that object, and `Global` is unconfirmed as well,
because the restriction list is not something this library reads. Under
partial revokes, a privilege with no grant row at any scope is still reported
absent only on a pinned, role-free session that holds a direct schema-level
SELECT on the mysql schema; a global SELECT does not count while partial
revokes are enabled. Otherwise it is GrantUnconfirmed.

A schema grant may also be stored as a wildcard pattern, such as `shop%` or
`my_db`, while partial revokes are disabled; once they are enabled the server
reads the stored name literally and so does the fact. Where an exact lookup
finds nothing and a stored pattern matches the requested schema, the answer is
unconfirmed rather than absent. A pattern never proves a privilege. The same
weakening rule applies when a column grant covers the requested table, when a
stored schema or table name matches the request only case-insensitively, and
when a visible anonymous-account database grant covers the requested scope. A
column grant proves nothing at schema scope, and none of these sources can
produce `GrantPresent`.

Because MySQL applies an anonymous database grant that a named account may be
unable to see, `GrantAbsent` requires the account's own direct `SELECT` on the
`mysql` schema: schema-level, or global while partial revokes are disabled.
Every otherwise-negative answer is `GrantUnconfirmed` without that sufficient
visibility condition. See [COMPAT.md](COMPAT.md) entries 11, 12, and 25-27.

## Table specifications and diffs

Capture and comparison are separate layers. `Inspector.TableSpec` reads one
base table and returns `(TableSpec, error)`. `DiffSpecs` is a pure function over
two captured values: it opens no connection, issues no query, and returns no
error because it inspects nothing. The specs normally come from two
`Inspector`s on two servers:

```go
specA, err := sourceInspector.TableSpec(
    ctx,
    validations.Ref("sakila", "payment"),
    validations.WithIndexes(),
    validations.WithConstraints(),
    validations.WithComment(),
)
if err != nil {
    return err
}

specB, err := destinationInspector.TableSpec(
    ctx,
    validations.Ref("archive", "payment"),
    validations.WithIndexes(),
    validations.WithConstraints(),
    validations.WithComment(),
)
if err != nil {
    return err
}

diffs := validations.DiffSpecs(specA, specB)
```

Columns and table facts are always captured. Optional sections declare what the
comparison is allowed to claim:

- `WithIndexes()` captures primary, unique, ordinary, and MySQL-created
  supporting indexes as ordered `IndexPart` values.
- `WithConstraints()` captures CHECK and FOREIGN KEY constraints, including
  CHECK enforcement and referential rules.
- `WithComment()` declares comments in scope. It costs no query because the
  table row already contains the comment.

Indexes retain the server's case-insensitive `INDEX_NAME` order, while
constraints are ordered in Go by exact-byte name and then kind. `DiffSpecs` is
unaffected by that difference because it matches both sections by name rather
than position.

`TableSpec.Captured` records those choices. If only one side captured an
optional section, `DiffSpecs` emits `IndexUnconfirmed`,
`ConstraintUnconfirmed`, or `CommentUnconfirmed` naming the side that did not
look. If neither side opted in, the section is outside the comparison and is
silent. This makes an empty diff list trustworthy: every in-scope question was
asked on both sides.

`ConstraintKindUnconfirmed` is the one `Unconfirmed` kind that does not mean
one-sided capture: a constraint matched by name has an unset kind on both
sides, so nothing could be compared. It carries `SideBoth`, the constraint
name in `Index`, and empty `A`/`B`. Capture always sets the kind; only
caller-built specs reach it. One unknown kind opposite a known kind still
reports `ConstraintKindMismatch`.

Columns match by name, not position, and column and index names fold ASCII
case ([COMPAT](COMPAT.md) entry 28). A pair differing only by ASCII case is
reported once as `ColumnNameCaseMismatch` or `IndexNameCaseMismatch`, carrying
both spellings, then compared attribute by attribute. Every diff of the pair
names side A's spelling; `b`-only columns are listed last in folded-name order,
with exact-byte order breaking ties. Non-ASCII bytes must match exactly.
Caller-built names that collide under folding on either side keep byte-exact
name matching for that key on both sides, as every release before 1.2.0 did:
the unmatched spelling surfaces as `ColumnAbsent` or `IndexAbsent`, no case
kind is emitted for that key, and nothing else signals the fallback.
Column-valued key parts and the `b`-only column order still fold. Capture
never produces such a spec ([COMPAT](COMPAT.md) entry 28). Constraint names
and their column lists continue to compare exactly.

A reordering therefore produces
`ColumnOrderMismatch` instead of comparing unrelated columns and cascading
spurious type differences. Integer display widths are normalized for
comparison while the raw server values remain available in the diff. Index
parts preserve prefix length, direction, and functional expression. A column
part's name folds like a column name; expression, prefix length, and direction
compare exactly.

Only base tables are supported. A view returns
`ErrUnsupportedTableType` rather than a partial spec:
`information_schema.COLUMNS` exposes view columns but the table metadata query
does not describe the defining query, so two different views could otherwise
compare equal.

`TableSpec` deliberately omits `information_schema.TABLES.AUTO_INCREMENT`.
That value is the next counter, not stable schema: it advances with inserts and
is approximate for InnoDB. Partition capture and `WithPartitions()` are not
part of this API.

`SpecDiff` carries **no severity**, not even a default. Whether a collation,
index, or constraint difference blocks an operation is consumer policy. See
[COMPAT.md](COMPAT.md) entries 1 and 13–17 for the MySQL behavior that shapes
capture and comparison.

`ColumnDefaultMismatch` distinguishes a column with no default from one whose
default is the literal empty string. `SpecDiff.Side` carries the distinction:
`SideA` or `SideB` names the spec that has no default at all, while `SideBoth`
means both sides supplied defaults that differ. An empty `A` or `B` on the
side `Side` names is absence; an empty value on the other side is
`DEFAULT ''`. The distinction survives JSON, where `a` and `b` are omitted
when empty: `side` is always present, so absence on A
(`{"kind":11,"side":1,"column":"c"}`) never collides with absence on B
(`{"kind":11,"side":2,"column":"c"}`), and no sentinel text is introduced
that a real default value could collide with. Expression defaults (COMPAT
entry 14) are marked by `a_is_expression`/`b_is_expression`, omitted when
false, never by decorating the text: `a` and `b` carry raw default text.
The flags are false for absent defaults and for every other diff kind.
Two columns that both lack a
default compare equal regardless of `DefaultIsExpression`: the flag qualifies
the default's text, so with no text on either side there is nothing for it to
distinguish, and no diff is emitted.

The text in `a` and `b` is contract: raw `COLUMN_TYPE` for
`ColumnTypeMismatch`, `true`/`false` for boolean kinds,
`ENFORCED`/`NOT ENFORCED` for `CheckEnforcementMismatch`, decimal ordinals,
enum string forms, DDL-style key parts, comma-joined column lists,
`schema.table(columns)` references, and `ON UPDATE`/`ON DELETE` rule text.
Only the default rendering changes in v1.2.0. A new qualifier arrives as a
typed field, never as a mark added to existing text, so consumers may parse
these strings.

`AllSpecDiffKinds()` returns every nonzero `SpecDiffKind` `DiffSpecs` may emit,
in declaration order, so a consumer can prove a policy switch over `SpecDiff.Kind`
is exhaustive at review time instead of discovering a new kind through a
fail-closed `default` at run time. `SpecDiffUnknown` is excluded: it is the zero
value, `DiffSpecs` never emits it, and it is exactly what a fail-closed `default`
arm should keep rejecting. The three kinds added in v1.2.0 are declared last,
so every earlier kind keeps its integer value. Exhaustive policy switches
need cases for `ColumnNameCaseMismatch`, `IndexNameCaseMismatch`, and
`ConstraintKindUnconfirmed`.

`SpecDiffKind.String()` renders a kind as a lowercase word, e.g.
`column_visibility_mismatch`, so a log line or error message names the kind
instead of printing its number; the JSON representation of `SpecDiff.Kind` is
unchanged, since `encoding/json` never consults `fmt.Stringer`.

## Findings

```go
type Finding struct {
    Check   string   // stable ID, e.g. "PK_SINGLE_COLUMN"
    Message string   // human-readable, including the rationale
    Tables  []string
    Facts   any      // typed payload — never parse the message
}
```

Branch on `Check` and read `Facts`. Message text is for humans and is not part
of the compatibility contract.

Checks are pure functions over facts, so you fetch once and run as many as you
like without touching the server again:

```go
var findings []validations.Finding
findings = append(findings, validations.CheckTablesExist(tables, tableFacts)...)
findings = append(findings, validations.CheckStorageEngine(tableFacts, "")...)
findings = append(findings, validations.CheckInvisibleColumns(invisible)...)
findings = append(findings,
    validations.CheckTriggersPresent(deleteTriggers, validations.TriggerDelete)...)
findings = append(findings, validations.CheckPKExists(pks)...)
findings = append(findings, validations.CheckPKSingleColumn(pks)...)
findings = append(findings, validations.CheckPKMatchesExpected(pks, expected)...)
findings = append(findings, validations.CheckPKNameCase(pks, expected)...)
findings = append(findings, validations.CheckPKIntegerType(pks)...)
findings = append(findings, validations.CheckFKIndexed(incoming.Keys)...)
findings = append(findings,
    validations.CheckFKClosure(incoming, "sakila", tables)...)
findings = append(findings,
    validations.CheckFKMetadataVisibility(incoming.Visibility)...)
findings = append(findings,
    validations.CheckTablePrivileges(
        grants, "sakila", tables, validations.PrivilegeDelete,
    )...)
```

A check returns `[]Finding` and no error — it inspects nothing, so there is
nothing for it to fail at.

## Errors versus findings

The distinction is strict:

- A **finding** describes the schema. A composite primary key is a finding.
- An **error** describes the inspection failing. An unreachable server, a
  permission denial, or a malformed query is an error.

Fact functions return `(facts, error)`; checks return `[]Finding`. Errors wrap
with `%w` and name the schema — and the table, when the failure is attributable
to one — so `errors.Is` and `errors.As` work through the whole call stack.

The library never panics and never logs.

## Building your own statements

`pkg/validations` returns facts; it does not build SQL for you. When you act on
what it reports, table and column names must be interpolated into the statement
text — values can be bound as parameters, identifiers cannot. Use
[`pkg/sqlutil`](sqlutil.md) for that rather than quoting by hand.

## Concurrency

Thread-safety is documented on every exported type. `Inspector` is immutable
and safe for concurrent use when the `Querier` you supply is safe for concurrent
use. Mutable slices in returned facts and findings require the usual caller
synchronization.

## MySQL versions

Supported floor is MySQL 8.0.40; tested against 8.0, 8.4, and 9.7. Version-specific
behavior the library absorbs on your behalf is catalogued in
[COMPAT.md](COMPAT.md) — worth reading before you debug a surprising result.
