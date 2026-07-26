# Changelog

All notable changes to this project are documented in this file.

This file is the **sole owner of version history** for `dbsgomysql`. Neither
`README.md` nor `AGENTS.md` carries a history section; both point here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `pkg/validations`: the check catalog — stable check identifiers, `CheckInfo`,
  `Catalog()`, and `LookupCheck`. The catalog covers the whole published check
  vocabulary, so the six checks reserved for a later slice appear too, carrying
  `StatusDeferred` and the phase that delivers them.
- `pkg/validations`: `PKKind` and `TriggerEvent`. Both reserve their zero value
  for "not populated", so an unset value is detectable rather than silently
  meaning `PKNone` or `INSERT`. `TriggerEvent.String` returns the event as
  `information_schema.TRIGGERS.EVENT_MANIPULATION` spells it.
- `pkg/validations`: immutable `Inspector`, inspectable `ObjectError`, and the
  `Tables`, `PrimaryKeys`, `InvisibleColumns`, and `Triggers` facts. Facts issue
  one metadata query per call, preserve requested table order and exact server
  spelling, and distinguish inspection failures from missing or invisible
  objects.
- Nine pure checks over those facts: `TABLES_EXIST`, `STORAGE_ENGINE`,
  `INVISIBLE_COLUMNS`, `TRIGGERS_PRESENT`, `PK_EXISTS`, `PK_SINGLE_COLUMN`,
  `PK_MATCHES_EXPECTED`, `PK_NAME_CASE`, and `PK_INTEGER_TYPE`.
- Repository-owned MySQL fixture, unit and smoke coverage, 8.0 / 8.4 / 9.7
  integration pins, and nine E2E golden-finding scenarios. The per-commit
  workflow now runs the MySQL 8.4 smoke suite alongside `make check`.
- `docs/COMPAT.md` entry 10: `information_schema.TRIGGERS.ACTION_TIMING` is an
  `ENUM`, so MySQL orders it by declaration index and `Inspector.Triggers`
  reports BEFORE ahead of AFTER. The reliance is now recorded and pinned by
  `TestTriggerTimingEnumOrderIntegration`.

The new `pkg/validations` API is part of the `v0.x` line and may change before
`v1.0.0`.

### Changed

- Clarified the planned `pkg/validations` contract: checks are pure functions
  over facts, and findings carry no severity; consumers own policy. A check
  returns `[]Finding` and no error — it inspects nothing, so it has nothing to
  fail at, and errors belong to the facts layer.

## [0.1.0] - 2026-07-26

### Added

- Repository scaffold: `go.mod` (module `github.com/dbsmedya/dbsgomysql`, Go
  1.24), `Makefile` with `make check` as the verification gate, `.golangci.yml`,
  `.editorconfig`, `.gitignore`, MIT `LICENSE`.
- `AGENTS.md`, the operating contract, imported by `CLAUDE.md`.
- `docs/`: `COMPAT.md` (MySQL version-quirk registry),
  `mysql-version-specific-compatibility.md` (version divergence register),
  `validations.md`, `sqlutil.md`, `testing.md`.
- CI: `ci.yml` runs `make check` on every push and pull request;
  `integration.yml` runs the MySQL 8.0 / 8.4 / 9.7 matrix on a version tag, a
  manual dispatch, or the `run-integration` label. The gate lints the
  `integration` and `e2e` build-tagged files explicitly.
- Test-only module requirements: `github.com/go-sql-driver/mysql` v1.10.0 and
  indirect `filippo.io/edwards25519` v1.2.0. Adding the driver normalized the
  module directive from Go 1.24 to 1.24.0; the requirements appear in consumer
  module graphs but remain unreachable from library code.
- `pkg/sqlutil`: `QuoteIdentifier`, `QuoteQualified`, `IsSimpleIdentifier`,
  `ValidateIdentifier`, and inspectable identifier-validation errors, with
  unit tests and MySQL 8.0 / 8.4 / 9.7 integration pins. `ValidateIdentifier`
  rejects every trailing space character MySQL rejects — `U+0009` through
  `U+000D` and `U+0020` — and accepts NBSP, `U+3000`, and leading space
  characters, which MySQL preserves.

[Unreleased]: https://github.com/dbsmedya/dbsgomysql/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dbsmedya/dbsgomysql/releases/tag/v0.1.0
