# Changelog

All notable changes to this project are documented in this file.

This file is the **sole owner of version history** for `dbsgomysql`. Neither
`README.md` nor `AGENTS.md` carries a history section; both point here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `Grants.Schema` and `Grants.Table` no longer downgrade a provable
  `GrantAbsent` to `GrantUnconfirmed` because a stored schema grant contains
  `_` or `%` while `@@global.partial_revokes` is enabled; the server reads
  those characters literally in that mode and so does the fact (#73).
- `ColumnSpec.NormalizedType` now strips the legacy display width from `YEAR`
  as it does from integer types, so `year(4)` on an in-place-upgraded server
  compares equal to `year` (#75).
- `PrimaryKeys` no longer returns a `PKNone` fact for a view, so
  `CheckPKExists` no longer reports `PK_EXISTS` for one; a requested view is
  absent from the result, matching `TableSpec`'s refusal of views (#76).
- `Triggers` now orders each table's triggers in Go — by firing order and
  then by exact byte-order name, the comparator `CheckTriggersPresent` already
  used — so a fact and its finding no longer disagree for names differing in
  case, and the returned order no longer depends on the server's sort; and
  `CheckTriggersPresent` given `TriggerEventUnknown` or an undeclared event
  now returns one finding carrying the event instead of `nil` (#78).
- `pkg/replication` decoders now accept a boolean delivered by a database
  driver as `bool` and an integer delivered as an integral `float64`, the
  remaining representations `database/sql/driver.Value` permits; a fractional,
  NaN, or infinite float is rejected as an unrecognized value (#79).
- `Inspector.BinaryLogStatus` no longer issues the `SHOW MASTER STATUS`
  fallback when the first statement failed because the context was cancelled
  or its deadline passed, and an error carrying both statements' causes now
  separates them with `; ` instead of a newline (#80).
- `CheckFKClosure` no longer multiplies `FK_CLOSURE` findings when the
  requested table list contains a duplicate; it emits one finding per external
  key in the order the `ForeignKeys` result carries them (#74).
- The `v1.1.0` entry for `Inspector.ReplicaStatus` attributed the `User` and
  `Password` columns to `--show-replica-auth-info`; that option affects
  `SHOW REPLICAS`, not `SHOW REPLICA STATUS`. The entry is corrected, and
  `Inspector.RegisteredReplicas` now has a test pinning that those columns
  are ignored (#81).
- `Finding.Channels` in `pkg/replication` is documented as nil, encoded as
  JSON `null`, for server-scoped checks; the JSON contract test now pins a
  shape the checks emit (#81).

### Changed

- `Finding.Tables` documents that an `FK_CLOSURE` finding for an external key
  names the child table unqualified and carries the child schema in `Facts`
  as `ForeignKey.ChildSchema`, and that the finding for incomplete visibility
  lists the requested targets with the `MetadataVisibility` in `Facts` (#74).

### Added

- Tests pinning `ReplicaParallelWorkers` on MySQL 8.0 and 8.4 against the
  server's own value, and `internal/testsupport` hardening: the scripted
  driver rejects a row whose width differs from its column list, and the
  account helper escapes backslashes in generated literals (#81, #82).

## [1.1.2] - 2026-09-02

### Fixed

- `TableSpec` with `WithConstraints()` no longer reports a CHECK constraint
  that belongs to another table sharing a name with a key on the inspected
  table, and no longer duplicates a foreign key's key parts when a unique key
  shares the constraint's name. Constraints sharing a name across kinds now
  order stably: CHECK before FOREIGN KEY (#71).
- `Grants.Table` no longer reports `GrantAbsent` when the account holds a
  column-level grant on the table; the fact now reads
  `information_schema.COLUMN_PRIVILEGES` and answers `GrantUnconfirmed`.
  `Grants.Schema` and `Grants.Table` also answer `GrantUnconfirmed` instead of
  `GrantAbsent` when a stored grant matches the requested name only
  case-insensitively, and when an anonymous-account database grant covers the
  requested scope. When the account cannot see other accounts' privilege rows,
  every otherwise-absent answer is now `GrantUnconfirmed`; `GrantAbsent`
  therefore requires the account's own direct `SELECT` on the `mysql` schema,
  schema-level or global with partial revokes off (#72).

### Changed

- Under partial revokes, a privilege with no grant row at any scope is now
  `GrantUnconfirmed` unless the account holds a direct schema-level `SELECT`
  on the `mysql` schema; `docs/COMPAT.md` entry 11 and the privilege guide no
  longer promise `GrantAbsent` for a global-`SELECT`-only account (#72).
- `docs/LIMITATIONS.md` records the new visibility requirement for
  `GrantAbsent` and the one privilege identity this fact cannot split: an
  account whose host part contains `@`.

## [1.1.1] - 2026-08-28

### Fixed

- `pkg/replication` now fails with column attribution if a row decoder and its
  promised-column list drift instead of silently reading column zero, returns
  no partial channel snapshot when closing `SHOW REPLICA STATUS` fails, and
  accepts boolean 0/1 values delivered by a database driver as `uint64` while
  continuing to reject every other numeric value.

## [1.1.0] - 2026-08-19

### Added

- `pkg/replication` package skeleton: the package-local `Querier` interface,
  the server-scoped `Inspector` with its infallible `NewInspector` constructor,
  the `ErrNilQuerier` sentinel, and the `OpError` type reporting a failed fact
  read with operation, channel, and column attribution. The package declares
  its own `Querier` instead of importing `pkg/validations`, so neither package
  depends on the other. Fact and check surfaces follow.
- `pkg/replication` variable facts: `Inspector.BinaryLogEnabled`,
  `Inspector.GTIDStatus`, and `Inspector.ReplicationConfig`, with the
  `GTIDStatus` and `Config` fact types and their JSON contracts.
  Each fact issues exactly one `SELECT` of the system variables it reports.
  GTID sets are returned as opaque strings and never parsed, so a tagged
  `UUID:TAG:NUMBER` set from MySQL 8.4 or later survives intact
  (`docs/COMPAT.md` entry 21). Only the `replica_*` variable spellings valid
  on every supported version are read, so no version branch is involved
  (`docs/COMPAT.md` entry 23). A value the server sends as SQL `NULL` or in an
  undecodable form is reported as an error naming the variable, never as a
  silently zeroed field.
- `pkg/replication` replica channel status fact: `Inspector.ReplicaStatus`
  reports one `ChannelStatus` per replication channel, in the order the server
  returned them, and an empty slice when the server is not a replica. Columns
  are read by name, so a column a future server version adds is ignored rather
  than misread, while a missing promised column fails the fact and names itself.
  `SecondsBehindSource` is invalid if and only if the server sent SQL `NULL`;
  any other undecodable value is an error naming the channel and column that
  caused it.
- `pkg/replication` binary log status fact: `Inspector.BinaryLogStatus` reports
  the server's binary log coordinates as a `*BinaryLogStatus`, and `nil` with
  no error when the server returns no row — provable absence, meaning binary
  logging is disabled. It issues `SHOW BINARY LOG STATUS` and falls back to
  `SHOW MASTER STATUS`, the one statement pair that genuinely differs across
  the supported range (`docs/COMPAT.md` entry 20); the fallback is issued only
  when the first statement fails, and it is bound to the transitional MySQL
  8.0 support window. When both statements fail, both causes are preserved in
  the returned error, each named by the statement that produced it, so either
  one remains reachable through `errors.Is` and `errors.As`.
- `pkg/replication` registered replicas fact: `Inspector.RegisteredReplicas`
  reports one `RegisteredReplica` per replica registered with this server, in
  the order the server returned them. Its GoDoc states the contract that gives
  the fact its name: the list is never proof of absence. The rows cover
  replicas that are or have been connected, a replica without explicit
  `report_host` still registers and is listed with an empty `Host`, and
  `Host` and `Port` are self-reported and unverified (`docs/COMPAT.md`
  entry 22), so an empty slice must not be read as "this server has no
  replicas". `Port` is the port the replica reported: omitting
  `report_port` normally yields the replica's actual listening port, and
  zero means only that the server returned zero.
- `pkg/replication` checks catalog: `Finding`, `CheckInfo`, `CheckStatus`,
  `Catalog`, and `LookupCheck`, mirroring `pkg/validations`, plus five pure
  check functions carrying no severity — `BINARY_LOG_ENABLED`,
  `GTID_MODE_ON`, `REPLICATION_CHANNELS_RUNNING`, `REPLICATION_CONFIGURED`,
  and `SECONDS_BEHIND_SOURCE_WITHIN`. The checks fail closed: a channel passes
  only when both threads report the exact value `Yes`, and GTID mode passes
  only on the exact value `ON`, so an unrecognized server value becomes a
  visible finding rather than a silent pass. `SECONDS_BEHIND_SOURCE_WITHIN`
  takes the caller's bound, so no threshold policy enters the library; it
  fails a channel whose lag is `NULL` (unknowable) or above the bound, and a
  negative bound produces a finding for every supplied channel rather than
  being clamped. This package reserves no check identifiers, so `CheckStatus`
  declares only `StatusImplemented`.
- Source-replica test topology and live smoke coverage for `pkg/replication`:
  `tests/docker/compose_replication.yaml` starts a source, a self-reporting
  replica, and a silent replica for each supported MySQL version, and
  `internal/testsupport` gains the helpers that open the trio, converge it on
  running replication, wait for a replica to catch up, and stop and restore an
  applier. Every wait polls a real observation under a bound; there are no
  fixed sleeps. The new `TestSmokeReplication` exercises all six facts and all
  five checks once against that live topology, and a missing topology DSN
  fails rather than skips when `DBSGOMYSQL_TEST_REQUIRE_REPLICATION=1`, so a
  skipped replication test cannot pass for evidence.
- `docs/replication.md`, the `pkg/replication` consumer guide: every fact with
  the privilege it needs and what an empty result means, why GTID sets are
  returned as opaque strings, the limits that make `RegisteredReplicas` a
  registration history rather than a topology, what
  `SECONDS_BEHIND_SOURCE_WITHIN` bounds and the four documented limits of the
  estimate it bounds, the five checks with their rationales, and a job-loop
  recipe that runs one snapshot through three checks with
  `REPLICATION_CONFIGURED` first — so neither an unconfigured server nor a
  channel-name typo can pass the gate. `README.md` and `docs/testing.md` gain
  the package and its test topology.
- Live matrix pins for every replication behavior `docs/COMPAT.md` records:
  `TestCompat6SecondsBehindIntegration`,
  `TestCompat20BinaryLogStatusIntegration`,
  `TestCompat21TaggedGTIDIntegration`,
  `TestCompat22RegisteredReplicasIntegration`, and
  `TestCompat23ReplicationConfigIntegration` run against a source-replica trio
  on MySQL 8.0, 8.4, and 9.7. They pin the `Seconds_Behind_Source` `NULL` rule
  on a deliberately stopped applier, the statement each version accepts for the
  source status (asserting that the other one is rejected, so success proves
  which ran), a GTID tag generated fresh per run surviving intact from source
  to replica, the source listing a replica that reports nothing as well as one
  that does, and one variable spelling serving all three versions. Entries 6
  and 20–23 in `docs/COMPAT.md` move from declared limitation to handled and
  pinned, each naming its test.
- Replication stop-start end-to-end scenario: `TestReplicationScenarioE2E`
  drives one incident against a live source-replica pair on MySQL 8.0, 8.4, and
  9.7 — a healthy replica passes the three-check gate with no findings, a
  deliberately stopped applier produces exactly two, and the cleanup restores
  the topology and proves it running again. The findings are compared against
  goldens (`tests/e2e/testdata/replication_running.json`,
  `replication_sql_stopped.json`) with the GTID sets and source coordinates
  normalized, so what the goldens pin is what the state means:
  `REPLICATION_CHANNELS_RUNNING` carrying an empty last-error pair, because a
  deliberate stop is not an error, and `SECONDS_BEHIND_SOURCE_WITHIN` carrying
  an invalid `Seconds_Behind_Source` rather than a zero.
- `docs/COMPAT.md` entries 20–23, recording the MySQL 8.0/8.4/9.7 replication
  observability sweep that scopes `pkg/replication` (v1.1.0): the
  `SHOW MASTER STATUS` → `SHOW BINARY LOG STATUS` divergence and its
  error-1064 fallback, tagged GTIDs making GTID sets opaque strings,
  `SHOW REPLICAS`' `report_host` visibility limits, and the 8.0.26 `replica_*`
  variable renames with their 9.3.0 and 9.5.0 prunings.
- Percona Server for MySQL support evidence now covers the replication layers:
  `tests/docker/compose_percona_replication.yml` mirrors the Oracle trios on
  the official Percona image lines, and on 2026-08-19 the complete integration
  and E2E suites — the `pkg/replication` source-replica layers included —
  passed on all three, which resolved to Percona Server 8.0.46-37, 8.4.10-10,
  and 9.7.1-1. `docs/LIMITATIONS.md` records the run against the Percona row,
  whose evidence had until now been standalone-only and predated the package.

### Fixed

- `pkg/replication.RegisteredReplicas` now reads what `SHOW REPLICAS` actually
  returns. It promised the column names the MySQL manual prints in its own
  example — `Server_id` and `Source_id` — which no supported server sends, so
  against a real server the fact failed with a missing-column error instead of
  reporting a replica. The promised columns are now `Server_Id` and
  `Source_Id`. Two `RegisteredReplica` contract statements were wrong for the
  same reason and are corrected: `Host` may be empty for a listed replica,
  because a replica started without `report_host` registers anyway rather than
  staying invisible, and `Port` zero means only that the server returned
  zero — an unset `report_port` normally reports the replica's actual
  listening port. The list is still never proof of absence, now grounded on
  stale rows and replicas that never connected rather than on a registration
  opt-out that does not exist. `docs/COMPAT.md` entry 22 records the
  manual/live disagreement, verified on MySQL 8.0.46, 8.4.9, and 9.7.1.
- `docs/COMPAT.md` entry 6 no longer claims MySQL 8.4 narrowed the
  `Seconds_Behind_Source` `NULL` rule: the 8.0 and 8.4 manuals state the
  identical rule, and the narrowing both contrast against is pre-8.0. The
  entry now also records that at the 8.0.40 floor the `REPLICA` statement
  spellings and output column names exist on every supported version, so no
  `SLAVE` fallback is needed.

## [1.0.0] - 2026-08-11

### Added

- A focused catalog contract test now pins every field of a representative
  `CheckInfo`, supplementing the existing `Catalog` and `LookupCheck` coverage
  before the v1.0.0 API freeze.

### Changed

- v1.0.0 declares the existing `pkg/sqlutil` and `pkg/validations` surfaces to
  be the stable v1 API. Exported Go APIs, check identifiers, and documented JSON
  contracts now follow Semantic Versioning; breaking changes wait for v2. This
  is a stability promotion with no runtime library behavior change from v0.9.0.
  The v1 support contract retains Go 1.24+, Oracle MySQL 8.0.40+ with
  transitional EOL 8.0 support, and the tested 8.4 and 9.7 lines.
- `Grants` GoDoc now states that the method-based fact deliberately has no
  general JSON representation and that generic JSON encoding produces an empty
  object. Slice-bearing `TableSpec`, `IndexSpec`, and `ConstraintSpec` GoDoc now
  links explicitly to the package ownership contract.
- Make targets now distinguish a genuinely empty package set from a failed
  `go list`. Package and dependency discovery errors fail the target instead of
  being reported as successful skips.
- `docs/COMPAT.md` entry 6 (replication statement and column renames) is now
  an explicitly declared known limitation whose handling `pkg/replication`
  delivers in v1.1.0, and the status legend defines 🔜 as a declared
  limitation naming its delivery release. The introduction now carries the
  no-divergence claim with the exact verified server versions. Entries 3
  and 10 record completed 8.0/8.4/9.7 manual searches, and stale `AGENTS.md`
  section references were corrected.

### Removed

- `docs/mysql-version-specific-compatibility.md`, the version divergence
  register. It recorded no divergence for the supported baseline; that claim
  and the exact verified server versions now live in `docs/COMPAT.md`'s
  introduction, which `docs/LIMITATIONS.md` and the README point to instead.

## [0.9.0] - 2026-08-11

### Added

- `make bench` and representative allocation-reporting benchmarks now cover
  identifier utilities, validation checks, foreign-key selection and closure,
  grant lookup, catalog lookup, schema diffs, and metadata predicate builders.
- A consumer limitations contract now centralizes the supported database
  distributions and the library's metadata-visibility, consistency, eager
  memory, large-schema, foreign-key-cost, privilege, identifier, and
  `TableSpec` boundaries. The README links it directly. Google Cloud SQL for
  MySQL and Amazon RDS for MySQL document the required `PROCESS` privilege but
  remain explicitly untested and unsupported until the live suites pass.
- A repository-owned Percona Server for MySQL Compose matrix now covers the
  official 8.0, 8.4, and 9.7 image lines. The complete integration and E2E
  suites passed on Percona Server 8.0.46-37, 8.4.10-10, and 9.7.1-1.

### Changed

- `Tables`, `Columns`, `InvisibleColumns`, `PrimaryKeys`, and `Triggers` now
  join up to 2,048 distinct requested schema/table pairs to MySQL's data
  dictionary instead of scanning the requested schema. Requested order,
  duplicates, absence behavior, and the oversized/unrepresentable-name
  fallbacks are unchanged. With three requested objects, handler reads stayed
  constant as the fixture grew from 500 to 5,000 tables on MySQL 8.0, 8.4, and
  9.7; the previous queries grew with the schema. This resolves #19 and also
  covers `Columns`, which had the same unreported scaling shape.
- `TableSpec` foreign-key capture now pins both sides of its metadata join to
  the resolved table. On 5,000-table foreign-key-heavy fixtures this reduced
  median query time by 6.5x to 23x across MySQL 8.0, 8.4, and 9.7 without
  changing captured constraints.
- Foreign-key closure checks and selector reconstruction now group facts once
  instead of repeatedly scanning the complete key set for every requested
  table. Their 1,000-table benchmark cases improved by approximately 15x and
  21x respectively while retaining requested order, duplicate positions, and
  slice ownership.
- Finding construction now looks up check metadata without rebuilding the
  public catalog. `LookupCheck` performs no allocations, cutting representative
  10,000-finding workloads by up to roughly half in time and by 49% to 60% in
  allocated bytes; `Catalog` still returns a fresh caller-owned slice.

## [0.8.1] - 2026-08-10

### Fixed

- `DiffSpecs` no longer loses which side of a `ColumnDefaultMismatch` has no
  default when the other side's default is the empty string (`DEFAULT ''`).
  `SpecDiff.Side` now honors its documented contract for defaults: `SideA` or
  `SideB` names the spec with no default at all, and `SideBoth` means both
  sides supplied defaults that differ. Previously every
  `ColumnDefaultMismatch` carried `SideBoth`, so "no default" versus
  `DEFAULT ''` produced byte-identical payloads in both orientations, in
  memory and in JSON. Consumers that read `Side` on default mismatches will
  now see `SideA`/`SideB` where one side is absent. Additionally, two columns
  that both lack a default now compare equal regardless of
  `DefaultIsExpression`: the flag qualifies the default's text, so with no
  text on either side there is nothing for it to distinguish, and no
  `ColumnDefaultMismatch` is emitted. (#56)

## [0.8.0] - 2026-08-08

### Changed

- **Breaking:** `ForeignKeyResult` now retains why its authoritative InnoDB
  source was downgraded when the standard metadata fallback succeeds.
  `DowngradeReason` distinguishes a primary query error from a primary
  read/decode error, while `PrimaryError` preserves the existing wrapped cause
  for `errors.Is` and `errors.As`. Query-stage errors are not assumed to be
  privilege failures. Complete and zero results carry no downgrade; if both
  sources fail, the existing joined function error and zero result remain.
  `PrimaryError` is excluded from JSON, while `downgrade_reason` is a new
  always-present numeric member. Consumers using unkeyed `ForeignKeyResult`
  literals must migrate to keyed fields and should inspect the new diagnostics
  whenever `Visibility` is `VisibilityUnconfirmed`.

## [0.7.5] - 2026-08-08

### Fixed

- `Columns`, `InvisibleColumns`, `Tables`, `PrimaryKeys`, and `Triggers` now
  report absence when the Inspector schema contains a character above
  `U+FFFF`, instead of failing with MySQL error 3988
  (`ER_IMPOSSIBLE_STRING_CONVERSION`). `TableSpec` likewise returns its
  existing `ErrTableNotFound` when either the requested schema or table has
  that shape. These fixed-identity reads are now answered before issuing SQL,
  because MySQL cannot store the requested spelling. Verified on MySQL 8.0,
  8.4, and 9.7.
- When `ForeignKeys` cannot use its `PROCESS`-gated InnoDB source, an
  unrepresentable Inspector schema no longer makes the standard fallback fail
  with error 3988. It now returns an empty result with
  `VisibilityUnconfirmed`; a successful InnoDB read remains unchanged and
  continues to report `VisibilityComplete`.
- A fixed-identity read short-circuited for an unrepresentable schema or table
  no longer observes an already-cancelled context or an error a custom
  `Querier` would have returned, because no query is issued. Argument
  validation and empty-input precedence are unchanged.

## [0.7.4] - 2026-08-08

### Fixed

- Requesting a table or view whose name contains a character above `U+FFFF` now
  reports absence, as `Columns` and `ForeignKeys` document, instead of failing
  with MySQL error 3988 (`ER_IMPOSSIBLE_STRING_CONVERSION`). Such a name cannot
  be compared against an `information_schema` name column, so the affected
  queries drop their name predicate and filter in Go, which is what they already
  do for every other name. Verified on MySQL 8.0, 8.4, and 9.7.
- Predicates built from a requested table list emitted one placeholder per
  requested element rather than per distinct name, so the same table named `n`
  times cost `n` parameters. They now bind one parameter per distinct name.
  Requested order and duplicate positions are unchanged: they are reconstructed
  from the caller's slice, not from the returned rows.
- Every dynamically built predicate is now bounded, and a request exceeding the
  bound falls back to an unfiltered query instead of producing a statement the
  server rejects with error 1390 (`ER_PS_MANY_PARAM`, "Prepared statement
  contains too many placeholders"). The supporting-index lookup used by the
  foreign-key fallback binds two parameters per table and is batched rather than
  unfiltered, because it validates every row it reads.

## [0.7.3] - 2026-08-08

### Added

- A live pin for `TableSpec`'s captured index-part shapes — `SUB_PART`,
  `COLLATION='D'`, and `EXPRESSION` — read from a real server across an index
  mixing a prefixed column, a `DESC` column, and a functional key part.
  Without it, two indexes that differ only in a prefix length, a sort
  direction, or an expression could be captured identically and `DiffSpecs`
  would call them equal.

### Changed

- The supported MySQL floor is now explicitly fixed at 8.0.40. Earlier
  pre-1.0 releases described support as MySQL 8.0 generally; compatibility
  below 8.0.40 is no longer claimed.

## [0.7.2] - 2026-08-07

### Fixed

- Every fact now owns the slices it returns. `InvisibleColumns`, `PrimaryKeys`,
  and the `ForeignKeys` selector previously returned facts sharing one backing
  array when the same table was requested twice, so sorting or editing one
  fact's `Columns`, `ChildColumns`, or `ParentColumns` silently changed
  another's. `Columns` already cloned, so the package followed two conventions;
  it now states one, in the package documentation under "Ownership of returned
  slices". `PrimaryKeys` was not named in the original report — it aliases the
  same way and is fixed here too.
- `ObjectError.Op` documentation now lists `"columns"`. The enumeration named
  seven of the eight ops and had omitted `opColumns` since `Inspector.Columns`
  shipped.
- `README.md` no longer describes general column facts as "in development for
  `v0.5.0`" — they shipped in `v0.5.0`, and signedness followed in `v0.6.0`. The
  Status section now defers to this file for release history rather than naming
  a version that goes stale again.

## [0.7.1] - 2026-08-03

### Added

- Coverage for the positive half of privilege resolution, which no server-backed
  test previously asserted: `GrantPresent` is now pinned against a real server
  for a global-backed grant with partial revokes disabled, and for a direct
  schema grant with `partial_revokes` enabled. The latter pins the clause of
  `docs/COMPAT.md` entry 11 that its own named test did not reach — a global row
  degrades while a direct schema or table grant still proves its object.
  Verified on 8.0, 8.4, and 9.7.
- `TestRoleHeldProcessCompletesFKVisibilityIntegration`, pinning that a `PROCESS`
  held only through an activated role still yields `VisibilityComplete` from
  `ForeignKeys`. It is the deliberate counterpart to the role-held `Grants`
  answers reported `GrantUnconfirmed`: the asymmetry turns on what constitutes
  evidence, not on the privilege type, and both halves are now pinned so neither
  reads as an inconsistency to be harmonized away. Includes a negative control
  proving the completeness comes from the activated role.
- Unit cases for two previously untested paths in grantee handling: the
  `CURRENT_USER()` user/host split taken at the last `@` rather than the first,
  which a wrong split would degrade silently into an expected absent answer, and
  a trailing lone backslash in a wildcard schema pattern.

### Changed

- `Grants` GoDoc now records that declining to consult `SHOW GRANTS` is a
  deliberate choice, and that its unconfirmed role answers mean "not proven by
  this fact" rather than "unprovable by any means". `SHOW GRANTS` merges the
  session's active roles and needs no `SELECT` on the `mysql` schema for the
  current user, so an operator whose working account fails a consumer's
  preflight can now find the reason in the API documentation rather than only in
  `docs/COMPAT.md` entry 4. No behavior change.

## [0.7.0] - 2026-08-01

The `SpecDiffKind` vocabulary becomes discoverable and printable. Purely
additive: no identifier changes meaning, signature, or numeric value, and the
JSON representation of `SpecDiff` is byte-identical.

### Added

- `validations.AllSpecDiffKinds()` publishes the full `SpecDiffKind` vocabulary
  as data, in declaration order, so a consumer can prove its policy switch over
  `SpecDiff.Kind` is exhaustive at review time instead of discovering a new kind
  through a fail-closed `default` at run time. Reported as #14.
- `SpecDiffKind.String()` and `CheckStatus.String()`, so both types join the
  rest of the package's enum-like types in rendering as a lowercase word (e.g.
  `column_visibility_mismatch`) instead of a bare integer. An undeclared
  `SpecDiffKind` renders as `SpecDiffKind(n)` rather than `"unknown"`, keeping a
  garbage value distinguishable from the zero value. `CheckStatus` has no
  declared zero-value member, so `CheckStatus(0)` renders as `CheckStatus(0)`,
  not `"unknown"` — that spelling is reserved for types with a declared unknown
  member, and using it here would present an unpopulated `CheckInfo` as a real,
  nameable status. **The JSON wire format is unchanged**: `SpecDiff.Kind`
  continues to marshal as a number, since `encoding/json` never consults
  `fmt.Stringer`. Reported as #32.

## [0.6.4] - 2026-08-01

### Added

- `docs/COMPAT.md` entry 19 records that `INNODB_FOREIGN_COLS.POS` counts from 1
  although every supported manual documents it as counting from 0, twice each.
  No behavior changes — `Inspector.ForeignKeys` was already correct. The entry
  exists because "correcting" the code to match the manual would not fail
  visibly: any primary-source error falls back to the standard
  `information_schema` query, so every call that matched at least one foreign
  key would silently degrade to `MetadataVisibility` `unconfirmed`. A selector
  matching none would still report `complete`, which is what makes the
  regression easy to miss. Now pinned directly on the raw column. Reported as
  #17.
- `docs/COMPAT.md` gains a fourth Reference kind, *documented and contradicted
  by the server*, for entries where the manual is not the tiebreaker and the
  pinning test is.
- Entry 19 carries a runnable SQL reproduction, so a reader can check a claim
  that contradicts the manual without running this library's test suite.

## [0.6.3] - 2026-08-01

### Fixed

- `Inspector.ForeignKeys` no longer fails when a child table carries a
  functional index. The fallback source reads
  `information_schema.STATISTICS.COLUMN_NAME`, which MySQL reports as NULL for a
  functional key part, and scanning it into a plain string aborted the query. An
  account without `PROCESS` — the ordinary shape of an inspection account, and
  the only path that reaches the fallback — therefore got an error instead of
  facts whenever a child table of one of the matched constraints carried such an
  index. Supporting indexes are looked up only for those tables, so a functional
  index elsewhere in the schema never had any effect. Reported as #16.

  The fix records a NULL part as an empty name rather than dropping the row,
  which also closes a latent false positive: a dropped part would let a later
  column stand in a leftmost slot it does not occupy, so an index on
  `((amount * 2)), tenant_id, parent_id` would have been reported as supporting
  `FOREIGN KEY (tenant_id, parent_id)`. `ForeignKey.Indexed` is now correct for
  every mixed index shape.

### Added

- `docs/COMPAT.md` entry 18 records the NULL `COLUMN_NAME` behavior and both
  failure modes above.
- `docs/COMPAT.md` reference lines may now carry per-version manual URLs
  alongside the section title, with the rule that a slug is opened before it is
  cited. Entry 18 is the first to use them.

## [0.6.2] - 2026-08-01

Build and CI only. The public surface is identical to `v0.6.1`, and the `go`
directive — the floor a consumer's build honors — is unchanged at `1.24.0`.

### Changed

- Build: the development toolchain is pinned. go.mod gains
  `toolchain go1.24.13`, and the Makefile exports it as `GOTOOLCHAIN`, so
  `make` targets run that exact toolchain regardless of the Go on `PATH`.
  `ci.yml` and `integration.yml` provision it by reading
  `make print-go-version`, so there is one place to bump.

  **Consumers are unaffected.** The `go` directive — the floor, and the thing a
  consumer's build actually honors — stays at `1.24.0`, and a `toolchain`
  directive is ignored in a module consumed as a dependency. Nothing about the
  supported Go range changes.

  Before this, go.mod declared only a floor, which 1.24, 1.25, and 1.26 all
  satisfy, so contributors and CI could each compile against a different
  release. CI resolved the floor literally and ran Go 1.24.0 while development
  happened on 1.26.5.
- Build: the linter is pinned as an artifact rather than as a version string.
  `make tools` builds the pinned `golangci-lint` with its own pinned Go
  (`GOLANGCI_GO_VERSION`) into a gitignored `./bin`, the gate invokes that copy
  by absolute path, and `ci.yml` runs the same target. `tools-check` verifies
  the Go release that built it, not just its version.

  golangci-lint embeds the `go/types` of the toolchain that compiled it, so one
  release can report differently depending on how it was built. Local Homebrew
  binaries were built with go1.26.2 while CI's `go install` produced go1.25.12.
  Contributors need to run `make tools` once; a PATH install is no longer used.

## [0.6.1] - 2026-08-01

### Added

- Coverage pinning the `ZEROFILL` display-width carve-out: unit cases for
  normalization and for the `DiffSpecs` mismatch it protects, plus a
  `TestTableSpecCompatPinsIntegration` pin creating `INT(5) ZEROFILL` and
  `INT(10) ZEROFILL` columns, verified on 8.0.46, 8.4.9, and 9.7.1.

### Changed

- `docs/COMPAT.md`: every entry now closes with a **Reference** line recording
  whether the behavior is documented by MySQL, documented only in part, or has
  no supporting statement that a search could find, and citing the manual
  section title, release note, or error-reference record it came from. Claims
  carrying a version threshold, an error number, or an "all supported versions"
  scope were queried once per version file and the answers diffed.
- `docs/COMPAT.md`: references cite section *titles*, because section numbers
  move between versions — `TRIGGERS` is §28.3.44 in the 8.4 manual and §28.3.50
  in 9.7, and CHECK Constraints moved from §15.1.20.6 to §15.1.25.6.
- `docs/COMPAT.md`: entries 2, 3, 5, 12, 13, 14, 15, and 16 gained supporting
  detail from the sources. Notably, entry 16 now records that the automatic
  foreign-key index "might be silently dropped later", so its disappearance from
  a captured schema is not a defect; entry 14 records that `COLUMNS.EXTRA` is an
  open value set that gained `MASKING POLICY` in 9.x; entry 12 records that
  wildcards in grants are deprecated as of 9.x; and entry 8 records that error
  3988 was only added in 8.0.22.

### Fixed

- `pkg/validations`: `ColumnSpec.NormalizedType` no longer strips the display
  width from a column carrying `ZEROFILL`. MySQL's carve-out from the 8.0.19
  display-width removal has **two** members, not one — `TINYINT(1)` and any type
  with `ZEROFILL` — and the width is semantic under `ZEROFILL`, because
  retrieved values are zero-padded to it. `int(5) zerofill` renders `00042`
  where `int(10) zerofill` renders `0000000042`.

  Consumers can notice: `DiffSpecs` compares `NormalizedType`, so two servers
  holding the same column at different zerofill widths previously produced **no
  `ColumnTypeMismatch`** — a false all-clear on a real schema difference. They
  now diff as a mismatch. A schema with no `ZEROFILL` column is unaffected, and
  `INT ZEROFILL` declared without a width still reports `int(10) unsigned
  zerofill`, so it continues to compare equal to an explicit `INT(10) ZEROFILL`.
  ([#15](https://github.com/dbsmedya/dbsgomysql/issues/15))
- `pkg/validations`: the `normalizeColumnType` and `hasDisplayWidth` doc
  comments gave 8.0.17 as the release that stopped emitting display widths.
  8.0.17 deprecated the attribute; **8.0.19** stopped showing it — the same
  correction already applied to `docs/COMPAT.md` entry 1 below.
- `docs/COMPAT.md`: three factual errors, each found by checking the entry
  against the MySQL documentation rather than against memory.
  - Entry 1 gave 8.0.17 as the release that stopped recording integer display
    widths. 8.0.17 deprecated the attribute; **8.0.19** is where output stopped
    showing it, and dictionary entries written by earlier 8.0 releases are left
    unchanged.
  - Entry 7 stated that `mysql_native_password` was removed in 8.4. It was
    deprecated in 8.0.34, **disabled by default** in 8.4 — where it can still be
    re-enabled with `--mysql-native-password=ON` — and **removed in 9.0.0**, and
    then only from the server; the client-side plugin remains available.
  - Entry 8 gave error 3988 the symbol `ER_CANNOT_CONVERT_STRING`. The number is
    correct; the symbol is **`ER_IMPOSSIBLE_STRING_CONVERSION`**.

  No library code depends on any of the three, so behavior is unchanged.

## [0.6.0] - 2026-07-29

### Added

- `pkg/validations`: `ColumnInfo.Unsigned`, derived from
  `information_schema.COLUMNS.COLUMN_TYPE` in the existing `Inspector.Columns`
  metadata query. Callers can now distinguish signed and unsigned configured
  integer columns without substituting actual primary-key metadata or parsing
  MySQL type syntax.
- Unit and MySQL 8.0 / 8.4 / 9.7 integration coverage for signed and unsigned
  `TINYINT`, `SMALLINT`, `MEDIUMINT`, `INT`, `INTEGER`, and `BIGINT`, including
  the legacy-form synthetic value `int(10) unsigned zerofill`.

### Notes

- Adding an exported field can break unkeyed `ColumnInfo` struct literals.
  This pre-1.0 API change took this compatibility unit; consumers should
  continue to pin exact tags.

## [0.5.0] - 2026-07-28

### Added

- `pkg/validations`: `Inspector.Columns`, `TableColumns`, and `ColumnInfo`.
  One metadata query returns every column for requested tables or views,
  preserving requested-object and ordinal order and exact server-side names.
  Each column reports `ORDINAL_POSITION`, `DATA_TYPE`, visibility, and whether
  it is generated. Missing or metadata-invisible objects are absent rather
  than errors.
- Unit, smoke, and MySQL 8.0 / 8.4 / 9.7 integration coverage for general
  columns, including views, generated and invisible columns, duplicate
  requests, missing objects, and exact-case table separation.

### Notes

- The general column fact is deliberately separate from `TableSpec`, which
  supports base tables only and performs table-level resolution. This lets a
  consumer distinguish a missing configured column, a case-only match on any
  column, and an existing non-primary column without changing its inspection
  failure order.
- This public API addition takes the next pre-1.0 compatibility unit,
  `v0.5.0`. Existing exported APIs are unchanged, but pre-1.0 consumers should
  continue to pin exact tags.

## [0.4.1] - 2026-07-28

Documentation only. The public surface is identical to `v0.4.0`.

### Changed

- `README.md`: added a pre-1.0 disclaimer stating that exported types, function
  signatures, check identifiers, and finding payloads may all change without a
  deprecation period before `v1.0.0`. The repository is public from this tag on,
  so the warning has to be the first thing a reader sees. It is removed on the
  commit that becomes `v1.0.0`.

## [0.4.0] - 2026-07-27

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

[Unreleased]: https://github.com/dbsmedya/dbsgomysql/compare/v1.1.2...HEAD
[1.1.2]: https://github.com/dbsmedya/dbsgomysql/compare/v1.1.1...v1.1.2
[1.1.1]: https://github.com/dbsmedya/dbsgomysql/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/dbsmedya/dbsgomysql/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.9.0...v1.0.0
[0.9.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/dbsmedya/dbsgomysql/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.7.5...v0.8.0
[0.7.5]: https://github.com/dbsmedya/dbsgomysql/compare/v0.7.4...v0.7.5
[0.7.4]: https://github.com/dbsmedya/dbsgomysql/compare/v0.7.3...v0.7.4
[0.7.3]: https://github.com/dbsmedya/dbsgomysql/compare/v0.7.2...v0.7.3
[0.7.2]: https://github.com/dbsmedya/dbsgomysql/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/dbsmedya/dbsgomysql/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.6.4...v0.7.0
[0.6.4]: https://github.com/dbsmedya/dbsgomysql/compare/v0.6.3...v0.6.4
[0.6.3]: https://github.com/dbsmedya/dbsgomysql/compare/v0.6.2...v0.6.3
[0.6.2]: https://github.com/dbsmedya/dbsgomysql/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/dbsmedya/dbsgomysql/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/dbsmedya/dbsgomysql/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dbsmedya/dbsgomysql/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dbsmedya/dbsgomysql/releases/tag/v0.1.0
