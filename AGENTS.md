# AGENTS.md — dbsgomysql

Operating contract for any agent or human working in this repository, and the
only normative document. `.ayder/specs/` holds design records and reasoning, not
rules; the code is the truth. Where a document disagrees with reality, fix the
document.

---

## 1. Project

| | |
|---|---|
| **Module** | `github.com/dbsmedya/dbsgomysql` |
| **Go floor** | 1.24 — consumers must be on 1.24 or newer |
| **Development toolchain** | pinned by go.mod's `toolchain` directive; see §6 |
| **Purpose** | A reference library of MySQL schema *facts* and *validations* for Go, plus a registry of MySQL version-specific `information_schema` behavior |
| **Current version** | see [CHANGELOG.md](CHANGELOG.md) |
| **Planned consumers** | [goarchive](https://github.com/dbsmedya/goarchive), gocdc |

The organizing idea is **facts versus policy**. The library answers factual
questions (*what kind of primary key does this table have?*) and reports findings
carrying a rationale and **no severity** — not even a remappable default, since a
default is a decision. Deciding whether a finding matters is the consumer's job —
which is why a check stays once written: another consumer still needs that
question answered.

## 2. Layout

```
dbsgomysql/
├─ CHANGELOG.md         sole owner of version history
├─ README.md            public front door
├─ go.mod               go 1.24
├─ Makefile             `make check` is the gate
├─ .golangci.yml        the enforced half of the library rules
├─ docs/                public product documentation — audience: consumers
├─ pkg/                 public API                             (phase 1)
├─ internal/            private; shared test support           (phase 1)
├─ tests/               docker/ · fixtures/ · e2e/             (phase 1)
├─ .github/workflows/   ci.yml · integration.yml
└─ .ayder/              GITIGNORED — internal docs: specs/ · plans/ ·
                        notes/ · archived/{specs,plans}
```

`docs/` is written for consumers of the library; nothing whose audience is "us"
belongs there. Everything internal — designs, plans, scratch — goes in `.ayder/`,
which is gitignored and never ships. When unsure whether code should be public,
put it in `internal/`: moving outward later is additive, inward is breaking.

## 3. Workflow

```
read .ayder/versions/ROADMAP.md  ->  branch from main
  ->  read the highest -rN spec in .ayder/specs/
  ->  plan in .ayder/plans/  ->  implement (test first, watch it fail, then code)
  ->  make check  ->  CHANGELOG.md [Unreleased]  ->  commit  ->  PR  ->  merge
  ->  make -C .ayder post-merge
```

**The first and last steps are the ones that get skipped, because nothing fails
when they are.** `ROADMAP.md` is the version authority — which release carries
which issue, and what is on hold — and reading it after a branch exists is how
work gets scoped into a held release. `post-merge` archives superseded
revisions and reports what drifted; skipping it is how `plans/` accumulated
seven documents for shipped work, two still reading "planned; not started".
Both are one command. The protocol behind them is
[`.ayder/SPEC_AND_PLAN_REVISION_GUIDE.md`](.ayder/SPEC_AND_PLAN_REVISION_GUIDE.md) §5.

### One document, one branch

**Every spec and every plan runs on its own git branch, and nothing is ever
committed directly to `main`.** `main` only ever advances by merge.

The documents themselves live in gitignored `.ayder/` and are never committed,
so a branch does not carry its spec or plan — it carries what that document
produces: code, tests, `docs/`, and `CHANGELOG.md`.

| Work | Branch | Example |
|---|---|---|
| Executing a plan | `feat/<topic>-<phase>` | `feat/validations-phase-1b` |
| Spec-driven change to tracked files, no plan | `spec/<topic>` | `spec/validations-library-design` |
| Anything else — repo chores, CI, corrections | `fix/`, `docs/`, `chore/` + a short slug | `chore/bump-golangci` |
| Releasing | `release/vX.Y.Z` | `release/v0.2.0` |

Use the branch prefix that matches the change's Conventional Commit type; the
`feat/` row above is the common case, not the only legal prefix.

A plan is the unit of execution: two plans never share a branch, and one plan
never spreads across two. A spec spanning several plans therefore produces
several branches. Branch from current `main`, and merge back through a pull
request once `make check` passes and `CHANGELOG.md` records anything a consumer
could notice. Delete the branch after merge.

If you find yourself on `main` with uncommitted work, branch first and commit
there — do not commit and then fix it up.

Internal documents in `.ayder/specs|plans/` carry a `-rN` suffix. Read the
**highest `-rN`** of a topic — superseded revisions move to `.ayder/archived/`, so
look there before concluding a topic never existed. **Never edit an `-rN` file in
place**; copy it to `-r(N+1)`. Authoring or revising a spec or plan? Read
[`.ayder/SPEC_AND_PLAN_REVISION_GUIDE.md`](.ayder/SPEC_AND_PLAN_REVISION_GUIDE.md)
first — it has the tooling, and regenerating a document instead of copy-then-edit
destroys the only history this gitignored tree has.

Topics: `validations-library-design` (design, API shape, check catalog),
`goarchive-extraction-inventory`, `repo-scaffold-and-agents-design`.

### goarchive is read-only reference material

`/Users/sinanalyuruk/Vscode/goarchive`, pinned at `d48152c`. **Copy and
reconstruct; never move, never edit.** Read it without touching the working tree:
`git -C /Users/sinanalyuruk/Vscode/goarchive show d48152c:<path>`. Run nothing
there that can write. The `goarchive-extraction-inventory` spec records what to
copy, what to rebuild differently, and what to leave behind. goarchive is ported
to consume this library later, as its own effort in its own repo.

### MySQL documentation is a lookup, not a recollection

The `dbs-vector` MCP indexes the reference manuals, release notes, and error
references for 8.0, 8.4, and 9.7 as one sanitized corpus. **Before asserting any
MySQL behavior — a version threshold, an error number, an `information_schema`
column, a type or storage rule — look it up there.** Training data on MySQL is
old enough to be wrong, and a remembered fact is not a fact. This corpus is the
first source of truth, and the manual's own wording is what a check's rationale
or a `docs/COMPAT.md` entry should quote.

Search `search_md_mysql_product_documentation`, scoped `clean/<file>`:

| Question | File |
|---|---|
| What does the server do, and what does `information_schema` expose? | `refman-{8.0,8.4,9.7}-en.a4.md` |
| When did this change, and under which worklog? | `mysql-{8.0,8.4,9.7}-relnotes-en.a4.md` |
| What is this error number, symbol, or SQLSTATE? | `mysql-errors-{8.0,8.4,9.7}-en.a4.md` |

Establish a `docs/COMPAT.md` quirk by running one query per version file and
diffing the answers; cite the manual's own section title or the release note's
dated heading. `mysqld-version-reference-en.a4.md` is the exception — its prose
chapters are sound, but its version matrices are stale and unreadable **in the
source PDF itself**, so no reconversion will rescue them. Scope to that file
deliberately or leave it out.

Two reading rules. The first breadcrumb node of a refman result is a false
heading harvested from a nearby code block — trust the path from `Chapter N`
onward, and never quote the root. When a result stops mid-list or mid-table, walk
it with the chunk cursor rather than re-querying; a second search returns the same
neighborhood reranked, the cursor returns the actual next text.

The corpus settles what the documentation claims. Section 5 still requires the
test that pins what the server does.

### Our own documents are a lookup too

`dbs-vector` indexes a **second, separate** corpus: this repository's Markdown —
`.ayder/` (`specs/`, `plans/`, `notes/`, `versions/`, `releases/`), `docs/`, and
the root files — searchable through `search_md_dbsgomysql_knowledge_vault`. The
MySQL corpus above answers *what does the server do*; this one answers *what did
we already decide, and where*.

Query it **through the MCP tool**. That server is the authoritative interface;
it is what the corpus config and its file watcher are wired to. Do not shell out
to a `dbs-vector` binary on `PATH` — a local build can disagree with the config,
and nothing tells you when it does.

**When a question spans documents rather than files, search it. Do not sweep it
with greps.** The trigger is concrete: if answering would take a handful of
`grep` passes over `.ayder/`, `docs/`, and the code — checking a spec against a
plan, finding whether a decision was already made, sweeping a claim for
consistency before a release — one semantic query replaces them and finds
phrasings a keyword never would. `grep` is still right for a known symbol in a
known file; it is the wrong tool for *"has anyone settled this?"*.

Two properties make this more than a convenience:

- **A recorded decision outranks a re-derived one.** Reasoning your way to a
  conclusion that already exists in writing wastes the effort and risks quietly
  contradicting the original, whose reasons you never read.
- **It finds phrasings a keyword never would.** One query found the same defect
  written three ways — "PR open, awaiting merge", "awaiting merge", and "PR #11
  open, release owned by user". A grep needs every phrasing guessed in advance.

**`.ayder/archived/` is deliberately NOT indexed, and this is load-bearing.** A
superseded revision is not old-but-true the way MySQL 8.0 is true of 8.0 — it is
**false now**: an `-r5` describing a field `-r6` deleted describes something that
never shipped. Similarity cannot rank the current revision above it, so it would
compete on equal footing and sometimes win. Archiving is therefore what removes a
document from search: the watcher evicts a moved file within seconds.

So the vault answers *"has anyone settled this?"*. It does not answer *"did this
topic ever exist?"* — for that, read `.ayder/archived/` directly, which is why
Section 3 tells you to look there before concluding a topic never existed.

Cite what you find, the way a `COMPAT.md` entry cites the manual. Ignore hits
from documents authored in the current session — the index includes them, and
they are your own words coming back.

Both corpora share one rule: **look before asserting.** A remembered MySQL fact
is not a fact, and a remembered decision is not a decision.

## 4. Library rules

`.golangci.yml` is the canonical statement of the mechanical rules — no `panic`,
no logging, no `init()`, no global mutable state, wrapped errors, GoDoc on every
export, stdlib-only `pkg/`. Read it there, not here, and loosen it only with the
reason in the commit message. Standard Go practice applies without being restated.
What no tool checks:

- **Library code imports stdlib only**, and `pkg/` is driver-agnostic: it never
  imports a MySQL driver and never opens, configures, or closes a connection. The
  consumer supplies the connection, so accept the smallest `database/sql`
  interface the call needs — one a `*sql.DB` and a `*sql.Tx` both satisfy.
- **Facts and findings are not errors.** A composite primary key is a finding; an
  unreachable server is an error. Facts return `(facts, error)`; checks are pure
  predicates over facts and return `[]Finding` alone — a check inspects nothing,
  so it has nothing to fail at. Errors name the object they concern, and claim no
  attribution they do not have.
- **Every check documents the failure mode it protects against.** The rationale is
  the product — it is what makes this a reference library and not a bag of
  predicates.
- **Every exported type states whether it is safe for concurrent use.**
- **Every MySQL version quirk the code accommodates** gets a `docs/COMPAT.md`
  entry and a test pinning the behavior.
- **`v0.x`: anything may break.** Mark it `!` on the commit type and say so in
  `CHANGELOG.md`. Nothing is frozen until `v1.0.0` is actually on the table —
  revisit compatibility rules then, not now.

## 5. Testing

| Layer | Database | Scope |
|---|---|---|
| Unit | none | pure logic: type normalization, spec diffing, finding assembly |
| Smoke | one 8.4 container | every fact and check once against a seeded fixture |
| Integration | 8.0 / 8.4 / 9.7 | per-version behavior, including every `docs/COMPAT.md` quirk |
| E2E | 8.0 / 8.4 / 9.7 | defect schemas against golden findings |

DB-backed tests sit behind the `integration` and `e2e` build tags, so `go test
./...` passes with no database present. The harness conventions — DSN variable,
compose services, ports, and the rule that tests **skip rather than fail** when
the DSN is unset — are published in [`docs/testing.md`](docs/testing.md), which is
the only copy. `make help` shows which target runs which layer.

Supported floor is MySQL 8.0; the 26.x line is watch-only, allowed to fail and
never depended on. This repo owns its own fixtures and containers. Write the test
first and watch it fail for the right reason. Handle version drift by normalizing
both forms or falling back, never by branching on `@@version`; where the account
cannot see all metadata, report that fact rather than a false all-clear.

## 6. Gate

```sh
make check      # run it, read the output, paste the output
```

Nothing is done until it passes — not "done except for", not "done, just a lint
nit". A completion claim without pasted output is not a completion claim, and a
target reporting `skipped` is not evidence of anything. `make help` lists every
target; `ci.yml` runs `make check` verbatim.

### One toolchain, and the gate enforces it

**Run the gate through `make`. Never substitute a bare `go test ./...` for it.**
The Makefile exports `GOTOOLCHAIN` from go.mod's `toolchain` directive, so every
`go` invocation it makes runs that exact toolchain, fetched on first use and
overriding whatever is on `PATH`. Outside `make` you get your own Go, and the
two do not have to agree.

This applies to agents exactly as it does to humans, and it is not a style
preference. `go 1.24.0` in go.mod is a **floor** — 1.24, 1.25, and 1.26 all
satisfy it — so without the pin every contributor compiles against a different
release. Vet and lint findings differ across them, which is how a failure
reaches code review as a surprise instead of arriving as a red check. It has
already cost one review cycle.

Two directives, two jobs. `go` is the consumer floor and the compatibility
unit. `toolchain` is the development platform, and is ignored when this module
is somebody's dependency, so pinning it costs consumers nothing. Bump it in
go.mod alone: the Makefile reads it, and `ci.yml` reads the Makefile.

The linter is pinned the same way, and separately. Run `make tools` once: it
builds the pinned `golangci-lint` with *its* pinned Go into a gitignored
`./bin`, and the gate calls that copy by absolute path. A PATH install is not
used even at the identical version, because golangci-lint embeds the
`go/types` of whatever compiled it, so the same release reports differently
depending on how it was built — Homebrew and `go install` do not agree. Its own
module floor sits above this one's, which is why it cannot share `GO_VERSION`.

`tools-check` prints both resolved versions, including the Go that built the
linter, so what produced a result is in the pasted output rather than assumed.

The pinned toolchain is currently past upstream end-of-life. That is deliberate:
the floor is this library's contract, and the gate is what proves it still
compiles there. Revisit at `v1.0.0` with the compatibility rules.

## 7. Commits & releases

[Conventional Commits](https://www.conventionalcommits.org/), scoped to the
package or area: `feat(validations): add FK_CLOSURE check`. Breaking changes take
`!` and a `BREAKING CHANGE:` footer.

`CHANGELOG.md` is the sole owner of history — no history section in this file or
`README.md`. If a consumer could notice the change, it belongs under
`## [Unreleased]` in the same commit, per
[Keep a Changelog](https://keepachangelog.com/).

**Releasing:** on a `release/vX.Y.Z` branch, move `[Unreleased]` under a dated
version heading and commit as `chore(release): vX.Y.Z`. Merge that branch to
`main` first — the tag names a commit on `main`, never one that only exists on a
branch — then tag `vX.Y.Z` on the merge result and push the tag. The tag push is
what triggers `integration.yml`, so dispatch that workflow manually and confirm
green before tagging.
