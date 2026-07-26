# Changelog

All notable changes to this project are documented in this file.

This file is the **sole owner of version history** for `dbsgomysql`. Neither
`README.md` nor `AGENTS.md` carries a history section; both point here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Repository scaffold: `go.mod` (module `github.com/dbsmedya/dbsgomysql`, Go
  1.24), `Makefile` with `make check` as the verification gate, `.golangci.yml`,
  `.editorconfig`, `.gitignore`, MIT `LICENSE`.
- `AGENTS.md`, the operating contract, imported by `CLAUDE.md`.
- `docs/`: `COMPAT.md` (MySQL version-quirk registry), `validations.md`,
  `sqlutil.md`, `testing.md`.
- CI: `ci.yml` runs `make check` on every push and pull request;
  `integration.yml` runs the MySQL 8.0 / 8.4 / 9.7 matrix on a version tag, a
  manual dispatch, or the `run-integration` label.
- `pkg/sqlutil`: `QuoteIdentifier`, `QuoteQualified`, `IsSimpleIdentifier`,
  `ValidateIdentifier`, and inspectable identifier-validation errors, with
  unit tests and MySQL 8.0 / 8.4 / 9.7 integration pins.

[Unreleased]: https://github.com/dbsmedya/dbsgomysql/commits/main
