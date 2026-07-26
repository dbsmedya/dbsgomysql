# pkg/sqlutil — Identifier Safety

> **Status: design phase.** The package is not implemented yet. This document
> describes the intended shape and the guarantees it will carry. Signatures are
> indicative and will be confirmed against the released API. Track progress in
> [CHANGELOG.md](../CHANGELOG.md).

`pkg/sqlutil` makes MySQL identifiers safe to interpolate into SQL.

It is small on purpose and public on purpose. Values can be bound as
parameters; **identifiers cannot** — a table or column name has to be
interpolated into the statement text. That makes identifier quoting the one
place where string handling and SQL injection meet, in every tool that builds
queries dynamically.

The alternative to one hardened, tested implementation is each consumer writing
its own. Identifier quoting is exactly the kind of code that looks trivial,
gets written from memory, and is wrong in the cases that matter. This package
exists so that does not happen.

## Quoting

```go
import "github.com/dbsmedya/dbsgomysql/pkg/sqlutil"

sqlutil.QuoteIdentifier("payment")       // `payment`
sqlutil.QuoteIdentifier("wei``rd")       // `wei````rd`   — backticks doubled
```

Backtick quoting is correct under both the default SQL mode and `ANSI_QUOTES`,
so no mode detection is required.

### Qualified names

**`QuoteIdentifier` takes one identifier, not a qualified reference.**

```go
sqlutil.QuoteIdentifier("sakila.payment")   // `sakila.payment`  ← WRONG
```

That is a single identifier literally named `sakila.payment` — not the
`payment` table in the `sakila` schema. Use `QuoteQualified`:

```go
sqlutil.QuoteQualified("sakila", "payment")  // `sakila`.`payment`
```

This distinction is the most common way identifier quoting is misused, and
hand-joining the parts at every call site is how the mistake spreads. Reach for
`QuoteQualified` whenever more than one part is involved.

## Validation

```go
sqlutil.IsSimpleIdentifier("order_items")   // true
sqlutil.IsSimpleIdentifier("order-items")   // false
```

`IsSimpleIdentifier` reports whether a name matches a deliberately **narrow
allowlist** — ASCII letters, digits, and underscore.

It is **not** a MySQL validity check, and it is named to say so. MySQL's real
rules are much broader: `$` and unicode letters are permitted unquoted, almost
anything is permitted when quoted, and the limit is 64 characters. A name this
function rejects may be perfectly legal.

Use it to gate names arriving from configuration or user input, where a
conservative allowlist is what you want. Do not use it to decide whether MySQL
would accept an identifier — it will produce false negatives.

## Threat model

**What this package does:** makes an identifier safe to interpolate into a
statement, so a hostile or merely awkward table name cannot terminate the
quoting context and inject SQL.

**What it does not do:**

- **It is not a substitute for bound parameters.** Values belong in
  placeholders. `WHERE id = ?`, never `WHERE id = ` + quoted input.
- **It authorizes nothing.** Quoting a name says nothing about whether the
  connected account may read or write that object. For privilege facts, use
  [`pkg/validations`](validations.md).
- **It does not check existence.** A well-quoted identifier for a table that
  does not exist is still well-quoted.

## Relationship to pkg/validations

The two are independent — `pkg/sqlutil` has no knowledge of schemas and
`pkg/validations` exposes facts, not SQL text. They are commonly used together:
validations tells you what is safe to act on, `sqlutil` helps you write the
statement that acts on it.

Both are stdlib-only and driver-agnostic.
