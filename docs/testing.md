# Testing

`dbsgomysql` has four test layers. Unit tests require no database; unit and
MySQL 8.4 smoke tests gate every commit.

| Layer | Database | Command | Scope |
|---|---|---|---|
| Unit | none | `make test` | Pure logic — type normalization, spec diffing, grantee formatting, finding assembly |
| Smoke | MySQL 8.4 | `make test-smoke` | Every fact and check runs once against a seeded fixture and a live source-replica trio. Fast go/no-go |
| Integration | 8.0 / 8.4 / 9.7 | `make test-integration` | Per-version behavior, including every quirk pinned in [COMPAT.md](COMPAT.md) |
| E2E | 8.0 / 8.4 / 9.7 | `make test-e2e` | Defect-schema scenarios compared against golden findings |

## Running without a database

```sh
make test
```

Database-backed tests sit behind build tags (`integration`, `e2e`), so a plain
`go test ./...` passes with no MySQL present. This is deliberate: it keeps the
per-commit CI job honest and fast, and it means a contributor can work on the
pure logic without Docker.

## Running the matrix locally

Compose definitions for the Oracle MySQL and Percona Server for MySQL matrices
live in `tests/docker/`.

```sh
# Bring up the version you want to test against
docker compose -f tests/docker/compose.yaml up -d mysql84

# Point the tests at it
export DBSGOMYSQL_TEST_DSN='root:root@tcp(127.0.0.1:3384)/'

make test-integration
```

The Oracle MySQL matrix uses:

| Version | Service | Port |
|---|---|---|
| 8.0 | `mysql80` | 3380 |
| 8.4 | `mysql84` | 3384 |
| 9.7 | `mysql97` | 3397 |

The Percona matrix uses the official moving `8.0`, `8.4`, and `9.7` image
tags:

```sh
docker compose -p dbsgomysql-percona \
  -f tests/docker/compose_percona.yml up -d

export DBSGOMYSQL_TEST_DSN='root:root@tcp(127.0.0.1:3484)/'
make test-integration
make test-e2e
```

| Version | Service | Port |
|---|---|---|
| 8.0 | `percona80` | 3480 |
| 8.4 | `percona84` | 3484 |
| 9.7 | `percona97` | 3497 |

The same three lines also run the replication trios, in a service-for-service
mirror of the Oracle topology described below:

```sh
docker compose -p dbsgomysql-percona-repl \
  -f tests/docker/compose_percona_replication.yml up -d --wait
```

| Version | Source | Replica (reporting) | Silent replica |
|---|---|---|---|
| 8.0 | `percona-repl80-source` · 3880 | `percona-repl80-replica` · 3980 | `percona-repl80-silent` · 4080 |
| 8.4 | `percona-repl84-source` · 3884 | `percona-repl84-replica` · 3984 | `percona-repl84-silent` · 4084 |
| 9.7 | `percona-repl97-source` · 3897 | `percona-repl97-replica` · 3997 | `percona-repl97-silent` · 4097 |

The environment recipe is the replication-topology section's below, with these
ports and `DBSGOMYSQL_TEST_REPL_SOURCE_HOST=percona-repl<v>-source`.

**Run one matrix at a time.** The Oracle and Percona matrices must not be
co-resident on an 8 GB Docker VM: the servers exhaust it, and the kernel OOM
killer takes casualties across stack boundaries — it has killed containers
belonging to the *other* matrix mid-run. Stop one before starting the other,
or give the VM about 16 GB.

Record `SELECT VERSION()` when a matrix runs: moving image tags are not
evidence of an exact server release. On 2026-08-10 they resolved to Percona
Server 8.0.46-37, 8.4.10-10, and 9.7.1-1; the complete integration and E2E
suites passed on all three. On 2026-08-19 all twelve servers of both Percona
matrices reported those same three releases, and the complete suites passed
again on every version — the `pkg/replication` source-replica layers included.
Percona is a supported distribution, but this local matrix is not currently
part of the GitHub Actions gate. See [LIMITATIONS.md](LIMITATIONS.md) for the
complete distribution-support scope.

Tests skip rather than fail when `DBSGOMYSQL_TEST_DSN` is unset, so an
accidental `go test -tags=integration ./...` without a server does not produce
misleading failures. The replication layers are the one exception, described
below.

## The replication topology

`pkg/replication` cannot be tested against a single container: it needs a
source, a replica with explicit `report_host`, and a replica without it. The
third still registers, listed with an empty `Host` — that divergence between
live behavior and the manual is what the topology makes observable. All three
per version live in `tests/docker/compose_replication.yaml`, on host ports
disjoint from the standalone matrix above, so both stacks run at once.

```sh
# The standalone container the existing suites use, plus this version's trio
docker compose -f tests/docker/compose.yaml up -d --wait mysql84
docker compose -f tests/docker/compose_replication.yaml up -d --wait \
  repl84-source repl84-replica repl84-silent
```

| Version | Source | Replica (reporting) | Silent replica | server-ids |
|---|---|---|---|---|
| 8.0 | `repl80-source` · 3580 | `repl80-replica` · 3680 | `repl80-silent` · 3780 | 1 / 2 / 3 |
| 8.4 | `repl84-source` · 3584 | `repl84-replica` · 3684 | `repl84-silent` · 3784 | 1 / 2 / 3 |
| 9.7 | `repl97-source` · 3597 | `repl97-replica` · 3697 | `repl97-silent` · 3797 | 1 / 2 / 3 |

The tests find the topology through five variables, alongside the
`DBSGOMYSQL_TEST_DSN` and `DBSGOMYSQL_TEST_MYSQL_VERSION` the other layers
read:

| Variable | Meaning | 8.4 local value |
|---|---|---|
| `DBSGOMYSQL_TEST_SOURCE_DSN` | source, host-mapped | `root:root@tcp(127.0.0.1:3584)/` |
| `DBSGOMYSQL_TEST_REPLICA_DSN` | replica with explicit `report_host` | `root:root@tcp(127.0.0.1:3684)/` |
| `DBSGOMYSQL_TEST_SILENT_REPLICA_DSN` | replica without explicit `report_host` | `root:root@tcp(127.0.0.1:3784)/` |
| `DBSGOMYSQL_TEST_REPL_SOURCE_HOST` | the source's hostname **as the replicas reach it** — its compose service name, not the host-mapped address above | `repl84-source` |
| `DBSGOMYSQL_TEST_REQUIRE_REPLICATION` | `1` in CI: a missing DSN **fails** instead of skipping | `1` |

```sh
export DBSGOMYSQL_TEST_DSN='root:root@tcp(127.0.0.1:3384)/'
export DBSGOMYSQL_TEST_MYSQL_VERSION=8.4
export DBSGOMYSQL_TEST_SOURCE_DSN='root:root@tcp(127.0.0.1:3584)/'
export DBSGOMYSQL_TEST_REPLICA_DSN='root:root@tcp(127.0.0.1:3684)/'
export DBSGOMYSQL_TEST_SILENT_REPLICA_DSN='root:root@tcp(127.0.0.1:3784)/'
export DBSGOMYSQL_TEST_REPL_SOURCE_HOST=repl84-source

make test-smoke
make test-integration
make test-e2e
```

`DBSGOMYSQL_TEST_REQUIRE_REPLICATION=1` inverts the skip rule for these
variables only, and both workflows set it. A skipped replication test is not
evidence: without the flag a mistyped variable produces a green run that
proved nothing, which is indistinguishable from a pass in a summary. Set it
locally too whenever you intend to quote a run as evidence.

Bootstrap is **convergent**, not one-shot. Every call reads each replica's
current state and acts on it — configure and start when there are no channels,
restart when a thread is stopped, do nothing when both are running — and every
path ends by proving the channels running. A replica an earlier test left
stopped is repaired rather than waited on, so tests do not have to run in a
particular order, and a failed run does not poison the next one.

**No fixed sleeps anywhere.** Every wait polls a real observation under a
bounded deadline and reports the last thing it saw on timeout. A test that
stops a replication thread registers its restart *before* stopping, and waits
for one single snapshot showing the thread stopped and the lag `NULL` together
— `SHOW REPLICA STATUS` is nonblocking, so reading the stopped thread from one
query and the `NULL` from a later one would be a race rather than a proof. No
test that mutates replication state runs in parallel.

The topology is disposable. Reset it with:

```sh
docker compose -f tests/docker/compose_replication.yaml down -v
```

## Fixtures

`tests/fixtures/` holds repository-owned seed schemas. The validations fixture
covers clean and missing tables, views, InnoDB and MyISAM, absent/single/
composite/non-integer primary keys, exact-name mismatches, invisible and
generated columns, DELETE/INSERT/UPDATE triggers, composite and cascade foreign
keys, MySQL's automatically created supporting FK index, and a MyISAM
declaration that is parsed and ignored.

Privilege and visibility scenarios create namespaced accounts and roles through
the configured administrative handle. Each successful create registers
teardown immediately. Tests authenticate fixture accounts by parsing
`DBSGOMYSQL_TEST_DSN`, copying its driver configuration, replacing the
credentials, clearing the default database, and formatting it again; credentials
are never spliced into a DSN string.

Role-sensitive assertions use a pinned `*sql.Conn`, enable the role on that
connection, and bind the Inspector to the same connection. Connection and pool
cleanup is registered after account cleanup, so LIFO test cleanup closes client
handles before dropping their account.

The FK completeness fixture account receives exactly `PROCESS`: no global,
schema, or table `SELECT`. That proves the complete `INNODB_FOREIGN*` source
does not rely on ordinary table visibility. The no-`PROCESS` account exercises
the visibility-filtered standard fallback.

The partial-revoke integration test is serial. It reads and restores the
original `@@global.partial_revokes` value with `SET GLOBAL` (never
`SET PERSIST`) even after an assertion failure. No account test runs in
parallel with it.

Phase-1c E2E projection copies `ForeignKey` and `PrivilegeFact` payloads before
replacing the exact generated target and external schema identities with
`{{target_schema}}` and `{{external_schema}}`. It rewrites no other string, so
goldens still distinguish same-schema from cross-schema edges and retain exact
table, column, and constraint names.

This repository owns its fixtures and containers outright. It does not reuse
another project's test infrastructure in place.

## CI

| Workflow | Trigger | What runs |
|---|---|---|
| `ci.yml` | every push and pull request | `make check` without a database, plus MySQL 8.4 smoke against a standalone server and the 8.4 replication trio |
| `integration.yml` | version tags, manual dispatch, or the `run-integration` PR label | the full Oracle MySQL 8.0 / 8.4 / 9.7 matrix, each version with its own replication trio |

Smoke runs on every push so every fact and check gets a real-server go/no-go.
The full three-version integration and E2E matrix remains off the per-push path
by design; it runs on tags, manual dispatch, and labeled pull requests.

The 26.x development line runs as an allowed-to-fail watch job when practical
images exist.

## Writing tests

Write the test first, watch it fail for the right reason, then implement. A
test that has never failed has never been shown to test anything.

Every entry in [COMPAT.md](COMPAT.md) needs a test that pins the documented
behavior on every affected version. That test is what stops a future
contributor from "simplifying" a version accommodation they do not recognize.
