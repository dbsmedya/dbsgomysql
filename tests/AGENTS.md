# tests/ — testing rules

Applies to `tests/` and every `*_test.go` in the repo. Repo-wide contract:
[`../AGENTS.md`](../AGENTS.md).

## Your loop

You are in **BUILD**. Nothing here requires the spec/plan revision protocol.

1. **Pick the layer** from the table below — the cheapest one that can actually
   catch the defect.
2. **Write the test and watch it fail for the right reason.** For a test added
   to cover existing behavior, remove the thing it depends on, confirm it fails,
   and put it back. Green on first run proves nothing.
3. Run that layer's target, then `make check`. Paste the output.
4. Commit, push, open the PR, **stop**.

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

- **Write the test first and watch it fail for the right reason.** A test that
  has never failed has not been shown to test anything.
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

Supported floor is MySQL 8.0. The 26.x line is watch-only: allowed to fail,
never depended on.

## Running

```sh
make check              # the gate — unit, lint, vet, build
make test-integration   # requires MySQL; see ../docs/testing.md
make test-e2e           # requires MySQL
```

Run everything through `make`, never a bare `go test ./...` — the Makefile pins
the toolchain and a bare invocation does not. See [`../AGENTS.md`](../AGENTS.md) §5.
