# dbsgomysql

> ## ⚠️ Under heavy development — use at your own risk
>
> This library is pre-1.0. **Anything may break before v1.0.0**: exported types,
> function signatures, check identifiers, and finding payloads are all subject to
> change without a deprecation period. Pin an exact tag and read the changelog
> before every bump.
>
> `v1.0.0` is the stability announcement. Until then, treat this repository as a
> preview.

**A reference library of MySQL schema facts and validations for Go.**

Module: `github.com/dbsmedya/dbsgomysql` · Go 1.24+ · Status: **phase 1
implementation** · Version: see [CHANGELOG.md](CHANGELOG.md)

`dbsgomysql` gives Go projects a single, well-tested answer to questions like
*"does this table have a single, composite, or no primary key?"*, *"which
foreign keys point into this set of tables?"*, or *"does the connected account
hold DELETE on these tables?"* — plus named validation checks with documented
rationale built on those facts. It also tracks MySQL version-specific
`information_schema` quirks (8.0 / 8.4 / 9.7, with 26.x watched) so consumers
don't have to rediscover them.

## Facts, not policy

The library answers factual questions and reports findings with a documented
rationale. It never decides whether a finding *matters* — mapping a finding to
fatal, warning, or ignore is the consumer's policy. That separation is why a
check survives one consumer outgrowing it: the question stays answerable for
everyone else.

## Design principles

- **Zero dependencies.** Library code imports the Go standard library only.
- **Driver-agnostic.** You supply the connection — a `*sql.DB`, `*sql.Conn`, or
  `*sql.Tx`; the library never opens, configures, or closes one, and never
  imports a driver.
- **Never panics, never logs.** It returns data and errors. You decide what
  reaches a terminal.
- **Context-first.** Every call that touches the database takes a
  `context.Context` as its first argument.

## Installation

```sh
go get github.com/dbsmedya/dbsgomysql
```

## Status

Available now: `pkg/sqlutil` for MySQL identifier validation and quoting, plus
the `pkg/validations` metadata, column, foreign-key, and privilege facts and all
15 pure catalog checks, including table specification and diff facts.
`pkg/replication` follows later.

Which release added what is recorded in [CHANGELOG.md](CHANGELOG.md), the sole
owner of version history.

Planned consumers: [goarchive](https://github.com/dbsmedya/goarchive), gocdc.

## Documentation

| | |
|---|---|
| [docs/validations.md](docs/validations.md) | `pkg/validations` consumer guide |
| [docs/sqlutil.md](docs/sqlutil.md) | `pkg/sqlutil` — identifier safety |
| [docs/LIMITATIONS.md](docs/LIMITATIONS.md) | Supported distributions and known correctness, visibility, consistency, and scale boundaries |
| [docs/COMPAT.md](docs/COMPAT.md) | MySQL version-quirk registry |
| [docs/testing.md](docs/testing.md) | Running the test matrix |
| [CHANGELOG.md](CHANGELOG.md) | Version history |
| [AGENTS.md](AGENTS.md) | Contributor and agent operating rules |

## License

[MIT](LICENSE)
