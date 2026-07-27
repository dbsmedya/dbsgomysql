# Changelog

All notable changes to this project are documented in this file.

This file is the **sole owner of version history** for `dbsgomysql`. Neither
`README.md` nor `AGENTS.md` carries a history section; both point here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `pkg/validations`: `TableSpec`, `Ref`, `SpecOption` with `WithIndexes`,
  `WithConstraints`, and `WithComment`, and `Inspector.TableSpec`. A spec
  records which optional sections were captured, so an empty section is
  distinguishable from a question nobody asked.
- `pkg/validations`: `DiffSpecs`, `SpecDiff`, `SpecDiffKind`, and `DiffSide`.
  Comparison is a pure function over two specs — no connection, no query, no
  error — and reports differences symmetrically, naming the side that lacks
  something rather than treating either as authoritative. Where only one side
  captured a section, the result carries `IndexUnconfirmed`,
  `ConstraintUnconfirmed`, or `CommentUnconfirmed` instead of silently
  reporting agreement.
- `pkg/validations`: `GeneratedKind`; columns are matched by name, so a
  reordering reports `ColumnOrderMismatch` rather than a cascade of spurious
  type mismatches. `COLUMNS.EXTRA` is decomposed into typed `Invisible`,
  `Generated`, `AutoIncrement`, and `OnUpdate` facts, each compared
  independently.
- `pkg/validations`: an index is captured as an ordered list of `IndexPart`
  values carrying prefix length, direction, and functional expression, so
  `INDEX(name)` and `INDEX(name(10))` are not reported as identical.
  Constraints are matched on name and kind, and carry `Enforced`.
- `pkg/validations`: `TableSpec` describes base tables only. A view is reported
  as `ErrUnsupportedTableType` rather than described partially —
  `information_schema` exposes a view's columns but not its defining query, so
  a view spec would compare equal to any other view over the same columns.
- `docs/COMPAT.md` entries 13-17: discarded PRIMARY KEY constraint names,
  expression defaults distinguishable only by `DEFAULT_GENERATED`,
  server-normalized `CHECK_CLAUSE`, the supporting index MySQL creates for a
  foreign key, and `NOT ENFORCED` CHECK constraints, which the server records
  but never evaluates. All five verified identical on 8.0, 8.4, and 9.7.
- Two-schema E2E fixtures and golden diffs, plus matrix pins for every new
  COMPAT entry.

### Changed

- `docs/COMPAT.md` entry 1 is handled and pinned, with a carve-out: integer
  display-width normalization **preserves `tinyint(1)`**, because `BOOLEAN` is
  an alias for it and MySQL keeps that width where it strips every other.
  Normalizing it away would report a `BOOLEAN` and a plain `TINYINT` as
  identical.

### Notes

- `TableSpec` does not capture `information_schema.TABLES.AUTO_INCREMENT`. It
  is the next counter value rather than a schema property, so two otherwise
  identical tables always differ on it.
- Partition capture is not part of this release.

## [0.3.0] - 2026-07-27

### Added

- `pkg/validations`: `ForeignKey`, `ForeignKeyResult`,
  `MetadataVisibility`, copied opaque selectors (`IncomingTo`,
  `OutgoingFrom`, `Within`), and `Inspector.ForeignKeys`. Discovery first uses
  the `PROCESS`-gated `INNODB_FOREIGN*` registry for complete registered
  InnoDB facts, then falls back to visibility-filtered standard metadata and
  marks that result unconfirmed.
- `pkg/validations`: `Grants`, `GrantState`, `Privilege`, and
  `PrivilegeFact`. Resolution accounts for enabled-role uncertainty and
  partial revokes, and mechanically degrades answers from pooled or opaque
  `Querier` implementations that cannot substantiate session state. While
  partial revokes are enabled, a global privilege row proves nothing on its own
  at any scope, including `Global`; a privilege with no grant row at any scope
  is still reported absent.
- `pkg/validations`: wildcard schema grants such as `` `shop%` `` downgrade an
  otherwise-absent `Schema` or `Table` answer to `GrantUnconfirmed` instead of
  producing a spurious "privilege is absent" finding. A pattern never proves a
  privilege.
- The six remaining catalog checks: `FK_INDEXED`, `FK_CLOSURE`,
  `FK_METADATA_VISIBILITY`, `CASCADE_RULES`, `TABLE_PRIVILEGES`, and
  `SCHEMA_PRIVILEGES`. Every published catalog ID is now implemented.
- Unit source-orchestration coverage, MySQL 8.0 / 8.4 / 9.7 integration pins,
  and seven phase-1c E2E golden scenarios covering same- and cross-schema
  referrers, cascade rules, privilege states, `PROCESS`-only completeness, and
  no-`PROCESS` fallback visibility.

### Changed

- `FK_INDEXED` now documents MySQL's supporting-index invariant: a finding
  indicates nonconforming or synthetic facts, not a reproducible slow-query
  state on a registered InnoDB foreign key.
- `docs/COMPAT.md` entries 3 and 5 are handled and pinned. Entry 4 is now an
  explicit bounded limitation: nested role closure is not resolved, and an
  otherwise-negative answer with enabled roles is unconfirmed. Entries 11
  (partial revokes) and 12 (wildcard schema grants) are new bounded
  limitations, both pinned.

## [0.2.0] - 2026-07-26

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

[Unreleased]: https://github.com/dbsmedya/dbsgomysql/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dbsmedya/dbsgomysql/releases/tag/v0.1.0
