# pkg/validations — Consumer Guide

> **Status:** the package foundation, metadata facts, and nine checks described
> below are implemented. Foreign-key and privilege facts plus `TableSpec` and
> `DiffSpecs` remain in design; those sections are marked accordingly. Track
> shipped changes in [CHANGELOG.md](../CHANGELOG.md).

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
`*sql.DB` and a `*sql.Tx` both work. Passing a transaction is how you make the
inspection observe uncommitted schema changes.

## Asking factual questions

Every call takes a `context.Context` first.

```go
tables := []string{"film", "film_actor"}

tableFacts, err := insp.Tables(ctx, tables)
pks, err := insp.PrimaryKeys(ctx, tables)
invisible, err := insp.InvisibleColumns(ctx, tables)
deleteTriggers, err := insp.Triggers(ctx, tables, validations.TriggerDelete)
```

Each fact returns a slice in requested table order. Missing or invisible
objects are absent rather than errors; compare `tableFacts` with `tables` using
`CheckTablesExist` when absence is the question. `PKInfo.Columns` retains exact
case and primary-key order, and a single-column key reports its `DataType`,
`IsInteger`, and `Unsigned` facts.

Names come back in the server's exact case, and the library compares names in
Go rather than in SQL — `information_schema` collates case-insensitively, so a
SQL-side comparison would match `LOG_ID` against `log_id`. See
[COMPAT.md §2](COMPAT.md).

Foreign-key and effective-privilege facts are planned for the next slice.

## Table specifications and diffs

> **Pending:** `TableSpec`, `Ref`, the option functions, and `DiffSpecs` land in
> phase 1d and are not part of the current package.

`TableSpec` describes a table completely enough to compare it with any other
table, on any server:

```go
spec, err := insp.TableSpec(ctx, validations.Ref("sakila", "payment"),
    validations.WithIndexes(),
    validations.WithConstraints(),
    validations.WithPartitions(),
)

diffs := validations.DiffSpecs(specA, specB)
```

`DiffSpecs` returns typed differences and attaches **no severity**. Whether a
collation mismatch blocks your operation is your decision.

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

Supported floor is MySQL 8.0; tested against 8.0, 8.4, and 9.7. Version-specific
behavior the library absorbs on your behalf is catalogued in
[COMPAT.md](COMPAT.md) — worth reading before you debug a surprising result.
