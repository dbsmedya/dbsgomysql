# Testing

`dbsgomysql` has four test layers. Unit tests require no database; unit and
MySQL 8.4 smoke tests gate every commit.

| Layer | Database | Command | Scope |
|---|---|---|---|
| Unit | none | `make test` | Pure logic — type normalization, spec diffing, grantee formatting, finding assembly |
| Smoke | MySQL 8.4 | `make test-smoke` | Every fact and check runs once against a seeded fixture. Fast go/no-go |
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

Compose definitions for all three tested versions live in `tests/docker/`.

```sh
# Bring up the version you want to test against
docker compose -f tests/docker/compose.yaml up -d mysql84

# Point the tests at it
export DBSGOMYSQL_TEST_DSN='root:root@tcp(127.0.0.1:3384)/'

make test-integration
```

Each version listens on its own port so several can run at once:

| Version | Service | Port |
|---|---|---|
| 8.0 | `mysql80` | 3380 |
| 8.4 | `mysql84` | 3384 |
| 9.7 | `mysql97` | 3397 |

Tests skip rather than fail when `DBSGOMYSQL_TEST_DSN` is unset, so an
accidental `go test -tags=integration ./...` without a server does not produce
misleading failures.

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
| `ci.yml` | every push and pull request | `make check` without a database, plus MySQL 8.4 smoke |
| `integration.yml` | version tags, manual dispatch, or the `run-integration` PR label | the full 8.0 / 8.4 / 9.7 matrix |

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
