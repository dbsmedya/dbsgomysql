# Limitations

> **Applies to:** v1.0.0. These boundaries are the v1 consumer contract unless
> a later release documents an additive expansion or correction.

`dbsgomysql` is a reference library of MySQL schema facts and validations. It
does not attempt to be a complete database catalog, authorization engine, DDL
round-tripper, or operational framework. This document states the boundaries a
consumer must account for when deciding whether a result is sufficient for an
operation.

These are support and design boundaries, not necessarily defects or future
commitments. The changelog records when a later release changes one of them.

## Supported database distributions

The supported distributions are deliberately narrow:

| Distribution | Status | Current evidence |
|---|---|---|
| Oracle MySQL, self-managed or official container images | Supported from 8.0.40 | This repository directly tests the current 8.0, 8.4, and 9.7 lines. Exact resolved versions are recorded in [COMPAT.md](COMPAT.md)'s introduction. |
| Percona Server for MySQL | Supported | This repository directly passes its integration and E2E suites on the official 8.0, 8.4, and 9.7 image lines. On 2026-08-10 those tags resolved to Percona Server 8.0.46-37, 8.4.10-10, and 9.7.1-1. The reproducible local matrix is [`tests/docker/compose_percona.yml`](../tests/docker/compose_percona.yml). |
| Google Cloud SQL for MySQL | **Not yet tested; support not claimed** | Google documents `PROCESS` as available through its MySQL privilege model, but this repository has not run its integration or E2E suites against Cloud SQL. |
| Amazon RDS for MySQL | **Not yet tested; support not claimed** | AWS documents `PROCESS` for RDS for MySQL administrative users and roles, but this repository has not run its integration or E2E suites against RDS. |
| MariaDB | **Not supported** | MariaDB compatibility is neither claimed nor tested. |
| Other MySQL-compatible servers and forks | **Not supported unless named above** | Similar SQL syntax or protocol compatibility is not evidence that their metadata and privilege behavior satisfy this library's contracts. |

Cloud SQL and RDS therefore have no known `PROCESS`-privilege blocker for the
authoritative foreign-key source: Google grants all MySQL static privileges
except `SUPER` and `FILE` through `cloudsqlsuperuser`, and AWS includes
`PROCESS` in the RDS master privilege set and `rds_superuser_role`. See the
[Cloud SQL MySQL user privilege model](https://docs.cloud.google.com/sql/docs/mysql/users)
and [RDS master-user privilege model](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/UsingWithRDS.MasterAccounts.html).

That privilege availability is feasibility evidence only. It does not prove
that either managed service exposes every required metadata table with the
same contents, errors, performance, and session behavior as the directly
tested distributions. Do not represent Cloud SQL or RDS as supported until the
full integration and E2E suites pass on live instances and their exact engine
versions are recorded.

The MySQL 26.x development line is watched but is not supported until its
behavior stabilizes and it joins the required test matrix. The Go support floor
is 1.24.

Foreign-key completeness is specifically about registered InnoDB constraints.
MyISAM does not enforce foreign keys, and NDB Cluster uses different metadata
and is outside the supported and tested foreign-key matrix. See
[COMPAT.md](COMPAT.md), entry 5.

The library does not query the server version at runtime and does not select
behavior by parsing `VERSION()`. A server being reachable does not upgrade it
into the supported matrix.

## Metadata visibility and absence

Most facts are built from `information_schema`, whose rows can depend on the
connected account's metadata visibility. For the multi-object fact methods,
an object that is missing and an object that exists but is invisible can both
be absent from the returned slice.

Consequences:

- absence is not a universal proof that an object does not exist;
- `CheckTablesExist` reports which requested objects had no returned fact, but
  it cannot distinguish nonexistence from invisibility;
- a successful empty result proves only what that fact's documented source and
  visibility permit it to prove;
- names are compared by exact Go string equality after retrieval, so a
  case-only variant does not satisfy a requested identity.

Consumers performing destructive or security-sensitive work must supply an
account whose metadata visibility is sufficient for that operation and must
interpret the fact's explicit visibility state where one exists.

## Foreign-key completeness and cost

`Inspector.ForeignKeys` first reads the `PROCESS`-gated
`INNODB_FOREIGN`/`INNODB_FOREIGN_COLS` registry. Success returns
`VisibilityComplete` for registered InnoDB constraints. If that source fails,
the library tries standard metadata and returns `VisibilityUnconfirmed` on
fallback success, retaining the primary failure and downgrade stage.

An unconfirmed empty result must not be interpreted as proof that no incoming
or outgoing foreign key exists. Consumers that require closure should treat
`VisibilityUnconfirmed` according to their own fail, warn, or defer policy. See
[the foreign-key consumer contract](validations.md#foreign-keys-and-completeness).

The authoritative source has a known scale characteristic: a narrow selector
can still cause work proportional to the server-wide registered foreign-key
population. Selecting one table reduces returned rows and Go-side processing,
but does not make the server operation proportional only to that table. Asking
for incoming and outgoing keys with two calls can pay this cost twice.

The library currently offers eager foreign-key results, not streaming or
pagination. This preserves atomic source fallback, validation, and visibility
semantics: no partial authoritative result is exposed before the source has
finished successfully.

## Privilege facts are conservative, not an authorization engine

`Inspector.Grants` models only these static privileges:

- `SELECT`;
- `INSERT`;
- `UPDATE`;
- `DELETE`;
- `CREATE`.

It returns `GrantUnconfirmed` whenever the available metadata cannot prove
presence or absence safely. Important uncertainty sources include:

- a pooled `*sql.DB`, which does not establish one session for a multi-statement
  fact;
- an opaque custom `Querier`, whose session affinity cannot be established;
- privileges that depend on an enabled role;
- nested role closure, which the library does not resolve;
- partial revokes stored outside the ordinary privilege tables;
- schema grants represented by wildcard patterns.

After `SET ROLE`, use the exact `*sql.Conn` or `*sql.Tx` on which the role was
enabled. Even on a pinned session, role-dependent answers can remain
unconfirmed because nested role closure is outside the implementation. See
[the privilege guide](validations.md#privileges-and-session-affinity) and
[COMPAT.md](COMPAT.md), entries 4, 11, and 12.

`Grants` is a method-based fact and has no useful direct JSON representation.
Consumers that need a serialized record should store the individual requested
privilege answers and their `GrantState`, not marshal `Grants` itself.

No result from this package replaces authorization by the server at execution
time.

## Consistency across statements and calls

The library never opens a transaction, pins a connection, retries a statement,
or establishes an isolation level. The consumer supplies the `Querier` and
therefore owns those choices.

There is no library-level point-in-time snapshot guarantee across separate fact
calls. Some individual operations, including `Grants` and optional `TableSpec`
capture, also use multiple statements. Concurrent DDL, grant changes, or
session-state changes can therefore produce observations from different
moments unless the consumer provides an execution context with stronger
guarantees.

Using `*sql.Conn` or `*sql.Tx` can provide connection affinity, but the library
does not claim guarantees beyond those supplied by the driver and server.
Contexts, deadlines, retry policy, credentials, least privilege, pool sizing,
and connection lifetime remain consumer responsibilities.

`Inspector` is immutable and can be shared only when its supplied `Querier` is
safe for concurrent use. Returned slices are consumer-owned and may be mutated,
but concurrent mutation still requires caller synchronization.

## Large requests, memory, and query shape

There is no public maximum supported table count. There is also no promise that
time or memory remains constant as the schema, requested set, or returned fact
set grows.

- Fact methods materialize their complete result in memory before returning.
- There is no general streaming or pagination API in v1.0.0.
- Requested order and duplicate positions are preserved. Facts at duplicate
  positions own independent backing slices, so duplicate-heavy requests consume
  corresponding memory.
- Several multi-object facts use bounded point-lookup query shapes for narrow
  requests and may deliberately switch to a schema scan for a large requested
  set. The current internal crossover bound is 2,048 distinct tables; it is an
  implementation choice, not a public API constant.
- Internal prepared-statement parameter bounds select a behavior-preserving
  fallback query shape rather than rejecting large input as a library error.
- Server load, metadata size, network latency, driver behavior, result width,
  and available client memory determine the practical ceiling.

Consumers with exceptionally large requested sets should set an appropriate
context deadline, measure against a representative database, and may batch
ordinary table facts themselves when memory or latency requires it. Batching
is not automatically faster: dividing a near-complete schema request into many
point queries can multiply round trips, and dividing foreign-key discovery can
repeat the global registry work.

v1.0.0 API stability is not a performance service-level agreement.

## `TableSpec` and `DiffSpecs` are not DDL round-tripping

`TableSpec` captures a deliberately selected comparison model. It is not a
complete representation of `CREATE TABLE`, and `DiffSpecs` cannot prove that
two original DDL statements were textually or semantically identical.

Current boundaries include:

- only base tables are supported; a view returns `ErrUnsupportedTableType`;
- view definitions are not captured;
- partition definitions are not captured;
- the table's current `AUTO_INCREMENT` counter is deliberately omitted because
  it is mutable runtime state rather than stable schema;
- indexes, constraints, and comments participate only when their corresponding
  `With...` option was requested;
- server-normalized type, default, generated-expression, and CHECK-clause forms
  are compared rather than the original DDL spelling;
- expressions are compared as captured text; the library does not prove
  logical equivalence;
- optional sections omitted on both sides are outside the comparison and
  produce no difference.

An empty `DiffSpecs` result therefore means that no difference was found in the
sections captured on both sides. It does not mean that every possible table
property or the original DDL is identical. See
[the table specification guide](validations.md#table-specifications-and-diffs).

## Identifier scope

The library treats identifiers as exact strings. It does not normalize case,
Unicode, or server naming configuration into a portable identity.

Relevant limits include:

- `information_schema` name columns cannot faithfully represent every
  supplementary Unicode identifier; affected fact requests use documented
  absence or fallback behavior rather than pretending the name round-tripped;
- `sqlutil.ValidateIdentifier` covers ordinary MySQL schema/table/column-style
  identifiers with the 64-character limit, not every identifier category;
- validation does not predict storage-engine or filesystem-dependent naming
  failures;
- `IsSimpleIdentifier` is a conservative ASCII allowlist and deliberately
  rejects many valid MySQL identifiers;
- `QuoteIdentifier` makes one identifier component safe for interpolation into
  a trusted statement template; it does not validate arbitrary SQL, bind
  values, authorize access, or check object existence.

See [the identifier safety guide](sqlutil.md) and
[COMPAT.md](COMPAT.md), entries 2, 8, and 9.

## Facts, checks, and consumer policy

Checks are pure functions over caller-supplied facts. They cannot establish
that the caller supplied an unmodified result from the correct server, schema,
selector, or moment in time. For example, a clean `CheckFKClosure` result is
trustworthy only when it receives the matching, unmodified incoming
`ForeignKeyResult`; the function cannot authenticate that provenance.

No findings means only that the check's predicate passed for the facts and
objects supplied. It does not mean that every relevant object was visible,
every possible fact was inspected, or an operation is safe.

Findings and specification differences intentionally carry no severity.
Consumers own the mapping to fatal, warning, ignore, or another policy. The
library does not generate migrations, archive operations, remediation SQL, or
execution plans.

Consumers should branch on catalog check IDs, enums, and typed fact payloads.
Human-readable `Finding.Message` wording is not part of the compatibility
contract.

## Connection and driver scope

Library packages import only the Go standard library. They do not:

- import or configure a MySQL driver;
- open, close, or health-check a connection;
- classify concrete driver errors by MySQL error number or SQLSTATE;
- configure TLS, authentication plugins, timeouts, or pool behavior;
- log, retry, or recover an operation automatically.

Errors retain wrapped causes so consumers can use `errors.Is` and `errors.As`
with their selected driver when driver-specific handling is required.
