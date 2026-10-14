# tests/ — testing rules

Applies to `tests/` and every `*_test.go` in the repo. Repo-wide contract:
[`../AGENTS.md`](../AGENTS.md).

## Your loop

You are in **BUILD**. Nothing here requires the spec/plan revision protocol.

1. **Pick the layer** from the table below — the cheapest one that can actually
   catch the defect.
2. **Work test-driven** — red, green, refactor. A test added for behavior that
   already exists asserts that behavior; nothing in the library is broken to
   make it fail.
3. Run that layer's target, then `make check`. Paste the output.
4. If a consumer could notice the change, record it under `CHANGELOG.md
   ## [Unreleased]` **in the same commit**. New coverage counts: `v0.7.1` was
   test-only and still carried an entry, because a consumer deleting their own
   equivalent test needs to know this one exists.
5. Commit, push, open the PR, **stop**.

Do not pick the version, write the plan, or merge.

## The four layers

| Layer | Database | Scope | Tag |
|---|---|---|---|
| Unit | none | pure logic: type normalization, spec diffing, finding assembly | — |
| Smoke | one 8.4 container | every fact and check once against a seeded fixture | — |
| Integration | 8.0 / 8.4 / 9.7 | per-version behavior, including every `docs/COMPAT.md` quirk | `integration` |
| E2E | 8.0 / 8.4 / 9.7 | defect schemas against golden findings | `e2e` |

`make help` shows which target runs which layer.

## Rules

- **Use test-driven development.**
- **Never mutate library code to prove a test.** No reverting a mechanism, no
  injected bug, no "prove the test bites" step, no mutation evidence in a plan
  or PR body. Which code to mutate, and what a red run then proves, has no
  answer, so it is not evidence.
- **Drive failure paths from the server, not the code.** To show a fact or
  check handles a hostile environment, change the database the test runs
  against — a session or global variable (`SET SESSION autocommit = 0`,
  `sql_mode`), a privilege the account lacks, a stopped replication thread or
  group member, a plugin state — and assert the library detects it and reports
  what its documentation promises. Restore the setting in cleanup registered
  before the change. A unit test's scripted querier stands in for that server
  state: it is test input, not a mutation.
- **Keep DB-backed tests behind the `integration` and `e2e` build tags**, so
  `go test ./...` passes with no database present.
- **Skip, do not fail, when the DSN is unset.** The harness conventions — DSN
  variable, compose services, ports — are published in
  [`../docs/testing.md`](../docs/testing.md), which is the only copy. Do not
  restate them here.
- **A target reporting `skipped` is not evidence of anything.** Do not quote one
  as a passing result.
- **Handle version drift by normalizing both forms or falling back — never by
  branching on `@@version`.**
- **Where the account cannot see all metadata, report that fact.** Never a false
  all-clear.
- **Pin every `docs/COMPAT.md` quirk with a named test**, and cite the test from
  the entry.
- This repo owns its own fixtures and containers. Do not reach into another
  repository's harness.

## Versions

Supported floor is MySQL 8.0.40. The 26.x line is watch-only: allowed to fail,
never depended on.

## Running

```sh
make check              # the gate — unit, lint, vet, build
make test-smoke         # requires MySQL; one 8.4 container
make test-integration   # requires MySQL; see ../docs/testing.md
make test-e2e           # requires MySQL
```

Run everything through `make`, never a bare `go test ./...` — the Makefile pins
the toolchain and a bare invocation does not. See
[`../AGENTS.md`](../AGENTS.md), "The gate".
