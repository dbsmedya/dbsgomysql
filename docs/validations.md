# pkg/validations — Consumer Guide

> **Status: design phase.** The package is not implemented yet. This document
> describes the intended shape so consumers can plan against it. Signatures are
> indicative and will be confirmed against the released API. Track progress in
> [CHANGELOG.md](../CHANGELOG.md).

`pkg/validations` has two layers built on one connection:

- a **facts layer** that answers typed questions about a MySQL schema, and
- a **checks layer** that turns those facts into named findings with a
  documented rationale.

## Facts, not policy

The library never decides whether something is a problem. `PK_SINGLE_COLUMN`
reports that a table has a composite primary key; whether that is fatal, a
warning, or irrelevant is your policy. Every `Finding` carries a *reference*
severity that you are expected to remap.

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
pk, err := insp.PKInfo(ctx, "film_actor")
// pk.Kind    → PKSingle | PKComposite | PKNone
// pk.Columns → exact-case column names, in key order
// pk.Type    → column type of a single-column PK (with IsInteger, Unsigned)
```

Names come back in the server's exact case, and the library compares names in
Go rather than in SQL — `information_schema` collates case-insensitively, so a
SQL-side comparison would match `LOG_ID` against `log_id`. See
[COMPAT.md §2](COMPAT.md).

Other facts include table existence, invisible columns, triggers by event,
foreign keys (with an `Indexed` flag computed for every key), and the effective
privileges of the connected account.

## Table specifications and diffs

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
    Check    string   // stable ID, e.g. "PK_SINGLE_COLUMN"
    Severity Severity // reference severity — remap to your policy
    Message  string   // human-readable, including the rationale
    Tables   []string
    Facts    any      // typed payload — never parse the message
}
```

Branch on `Check` and read `Facts`. Message text is for humans and is not part
of the compatibility contract.

## Errors versus findings

The distinction is strict:

- A **finding** describes the schema. A composite primary key is a finding.
- An **error** describes the inspection failing. An unreachable server, a
  permission denial, or a malformed query is an error.

Fact functions return `(facts, error)`; checks return `([]Finding, error)`.
Errors wrap with `%w` and name the schema and table concerned, so
`errors.Is` and `errors.As` work through the whole call stack.

The library never panics and never logs.

## Building your own statements

`pkg/validations` returns facts; it does not build SQL for you. When you act on
what it reports, table and column names must be interpolated into the statement
text — values can be bound as parameters, identifiers cannot. Use
[`pkg/sqlutil`](sqlutil.md) for that rather than quoting by hand.

## Concurrency

Thread-safety is documented on every exported type. `Inspector` is intended to
be safe for concurrent use by multiple goroutines, bounded by the connection
limits of the `*sql.DB` you supply.

## MySQL versions

Supported floor is MySQL 8.0; tested against 8.0, 8.4, and 9.7. Version-specific
behavior the library absorbs on your behalf is catalogued in
[COMPAT.md](COMPAT.md) — worth reading before you debug a surprising result.
