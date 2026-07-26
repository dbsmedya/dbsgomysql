# Changelog

All notable changes to this project are documented in this file.

This file is the **sole owner of version history** for `dbsgomysql`. Neither
`README.md` nor `AGENTS.md` carries a history section; both point here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Clarified the planned `pkg/validations` contract: checks are pure functions
  over facts, and findings carry no severity; consumers own policy. A check
  returns `[]Finding` and no error — it inspects nothing, so it has nothing to
  fail at, and errors belong to the facts layer. No code is affected:
  `pkg/validations` is not implemented yet, so this changes published
  documentation and the operating contract only.

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
