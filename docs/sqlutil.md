# pkg/sqlutil — Identifier Safety

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
sqlutil.QuoteIdentifier("wei`rd")        // `wei``rd`   — backticks doubled
sqlutil.QuoteIdentifier("")              // ``
```

Backtick quoting is correct under both the default SQL mode and `ANSI_QUOTES`,
so no mode detection is required. `QuoteIdentifier` is total: it never fails
or rejects input. The empty output shown above cannot escape its identifier
context, but MySQL will reject it as an invalid name. Use `ValidateIdentifier`
at trust boundaries to distinguish server validity from interpolation safety.

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

With no parts, `QuoteQualified()` returns `""`. Empty parts are not discarded:

```go
sqlutil.QuoteQualified("", "payment") // ``.`payment`
```

Silently dropping an empty schema would make the statement use the connection's
default schema and could address the wrong object.

## Validation

### MySQL validity

`ValidateIdentifier` checks the rules common to the 64-character database,
table, and column identifiers this package is intended to interpolate:

```go
if err := sqlutil.ValidateIdentifier(configuredTable); err != nil {
    return fmt.Errorf("table name %q: %w", configuredTable, err)
}
```

It reports these sentinel errors, which callers can inspect with `errors.Is`:

| Error | Condition |
|---|---|
| `ErrIdentifierInvalidUTF8` | the string is not valid UTF-8 |
| `ErrIdentifierEmpty` | the name is empty |
| `ErrIdentifierNulByte` | the name contains `U+0000` |
| `ErrIdentifierSupplementary` | the name contains a character above `U+FFFF` |
| `ErrIdentifierTrailingSpace` | the name ends with TAB–CR (`U+0009`–`U+000D`) or SPACE (`U+0020`) |
| `ErrIdentifierTooLong` | the name exceeds 64 Unicode characters |

The length is counted in characters, not bytes, so a 64-character multibyte
BMP name is accepted. MySQL 8.0, 8.4, and 9.7 accept a statement containing a
supplementary character above `U+FFFF`, but silently store that character as
`?`; the name therefore does not round-trip and validation rejects it.
Validation uses the table order above; when a name has several defects, the
first matching error is returned deterministically.

This contract does not cover MySQL identifier categories with other limits,
such as 256-character aliases or 16-character compound-statement labels.
It also does not predict storage-engine or filesystem-encoding limits: a
64-rune CJK table name satisfies MySQL's identifier-character limit but may
still fail with an environment-dependent InnoDB filename error. Punctuation,
NBSP (`U+00A0`), `U+3000`, and leading space characters that are legal inside
a quoted identifier are accepted; validation does not impose the conservative
ASCII allowlist described below.

### Conservative allowlist

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

Consumers construct trusted statement templates. Configuration or another
untrusted input may supply schema, table, and column names and bound values,
but it is not accepted as an arbitrary SQL program.

**What this package does:** validates identifier parts at the trust boundary
and makes each part safe to interpolate into a trusted statement template, so
a hostile or merely awkward name cannot terminate the quoting context and
inject SQL.

**What it does not do:**

- **It is not a substitute for bound parameters.** Values belong in
  placeholders. `WHERE id = ?`, never `WHERE id = ` + quoted input.
- **It does not parse, sanitize, or classify arbitrary query text.** The caller
  owns the trusted SQL template.
- **It authorizes nothing.** Quoting a name says nothing about whether the
  connected account may read or write that object. For privilege facts, use
  [`pkg/validations`](validations.md).
- **It does not check existence.** A well-quoted identifier for a table that
  does not exist is still well-quoted.
- **It does not manage execution controls.** Credentials, least-privilege
  policy, contexts, timeouts, and transactions remain the consumer's
  responsibility.

## Relationship to pkg/validations

The two are independent — `pkg/sqlutil` has no knowledge of schemas and
`pkg/validations` exposes facts, not SQL text. They are commonly used together:
validations tells you what is safe to act on, `sqlutil` helps you write the
statement that acts on it.

Both library packages are stdlib-only and driver-agnostic. This repository
declares `github.com/go-sql-driver/mysql` and its indirect dependency for
integration tests, so those requirements appear in the module graph even
though neither is reachable from library code.
