# AGENTS.md — dbsgomysql

Operating contract for any agent or human working in this repository. The code
is the truth; where a document disagrees with reality, fix the document.

## Read this too

Everything below applies to everyone. Anything narrower lives with the work:

| Doing this | Read |
|---|---|
| Writing library code | [`pkg/AGENTS.md`](pkg/AGENTS.md) |
| Writing or running tests | [`tests/AGENTS.md`](tests/AGENTS.md) |
| Cutting a release, or choosing a version | the `release` skill |
| Authoring or revising a spec or plan | [`.ayder/SPEC_AND_PLAN_REVISION_GUIDE.md`](.ayder/SPEC_AND_PLAN_REVISION_GUIDE.md) |

## 1. Project

| | |
|---|---|
| **Module** | `github.com/dbsmedya/dbsgomysql` |
| **Go floor** | 1.24 — consumers must be on 1.24 or newer |
| **Purpose** | A reference library of MySQL schema *facts* and *validations* for Go, plus a registry of MySQL version-specific `information_schema` behavior |
| **Current version** | see [CHANGELOG.md](CHANGELOG.md) |
| **Consumers** | [goarchive](https://github.com/dbsmedya/goarchive), gocdc |

The organizing idea is **facts versus policy**. The library answers factual
questions and reports findings carrying a rationale and **no severity**.
Deciding whether a finding matters is the consumer's job — which is why a check
stays once written: another consumer still needs that question answered.

## 2. Layout

```
CHANGELOG.md         sole owner of version history
.golangci.yml        the enforced half of the library rules
docs/                consumer documentation — audience: consumers, never us
pkg/  internal/      public API · private
tests/               docker/ · fixtures/ · e2e/
.github/workflows/   ci.yml · integration.yml
.ayder/              GITIGNORED internal docs — specs/ plans/ notes/
                     versions/ releases/ archived/
```

Put anything whose audience is "us" in `.ayder/`, never `docs/`. When unsure
whether code should be public, put it in `internal/`.

## 3. Workflow

```
read .ayder/versions/ROADMAP.md  ->  branch from main
  ->  read the highest -rN spec in .ayder/specs/
  ->  implement (test first, watch it fail, then code)
  ->  make check  ->  CHANGELOG.md [Unreleased]  ->  commit  ->  PR  ->  merge
  ->  make -C .ayder post-merge
```

**The first and last steps get skipped, because nothing fails when they are.**
Reading `ROADMAP.md` after a branch exists is how work gets scoped into a held
release. `post-merge` archives superseded revisions and reports what drifted;
skipping it is how seven plans for shipped work piled up, two still reading
"planned; not started".

- **Never commit to `main`.** It advances only by merge. Found yourself on it
  with uncommitted work? Branch first, then commit — do not commit and fix up.
- **Name the branch for the change's Conventional Commit type:** `feat/`,
  `fix/`, `docs/`, `chore/`, `spec/<topic>`, `release/vX.Y.Z`.
- **Delete the branch after merge.**
- **Commit as [Conventional Commits](https://www.conventionalcommits.org/),
  scoped:** `feat(validations): add FK_CLOSURE check`. Breaking takes `!` and a
  `BREAKING CHANGE:` footer.
- **Record anything a consumer could notice under `## [Unreleased]` in the same
  commit.** `CHANGELOG.md` is the sole owner of history — no history section
  here or in `README.md`.

### goarchive is read-only reference material

`/Users/sinanalyuruk/Vscode/goarchive`, pinned at `d48152c`. **Copy and
reconstruct; never move, never edit.** Read it without touching the working
tree: `git -C /Users/sinanalyuruk/Vscode/goarchive show d48152c:<path>`. Run
nothing there that can write.

## 4. Look before asserting

**A remembered MySQL fact is not a fact, and a remembered decision is not a
decision.** Two `dbs-vector` corpora exist so neither has to be remembered.
Query both **through the MCP tools** — never a `dbs-vector` binary on `PATH`,
which can disagree with the config with nothing reporting that it has.

**Before asserting any MySQL behavior** — a version threshold, an error number,
an `information_schema` column, a type or storage rule — look it up in
`search_md_mysql_product_documentation`, scoped `clean/<file>`:

| Question | File |
|---|---|
| What does the server do, and what does `information_schema` expose? | `refman-{8.0,8.4,9.7}-en.a4.md` |
| When did this change, and under which worklog? | `mysql-{8.0,8.4,9.7}-relnotes-en.a4.md` |
| What is this error number, symbol, or SQLSTATE? | `mysql-errors-{8.0,8.4,9.7}-en.a4.md` |

Establish a `docs/COMPAT.md` quirk by running one query per version file and
diffing; cite the manual's own section title. Ignore the first breadcrumb node
of a refman result — it is a false heading — and walk a truncated result with
the chunk cursor rather than re-querying. The corpus settles what the
documentation *claims*; a test still has to pin what the server *does*.

**Before deciding something the project may already have decided**, search
`search_md_dbsgomysql_knowledge_vault`. If answering would take several `grep`
passes over `.ayder/`, `docs/`, and the code, one semantic query replaces them
and finds phrasings a keyword never would. `grep` is still right for a known
symbol in a known file.

- **Cite what you find**, the way a `COMPAT.md` entry cites the manual.
- **Ignore hits from documents authored in the current session** — they are your
  own words coming back.
- **`.ayder/archived/` is deliberately not indexed.** A superseded revision is
  not old-but-true; it is *false now*. So the vault answers "has anyone settled
  this?" — for "did this topic ever exist?", read `.ayder/archived/` directly.

## 5. The gate

```sh
make check      # run it, read the output, paste the output
```

Nothing is done until it passes — not "done except for", not "done, just a lint
nit". **A completion claim without pasted output is not a completion claim**,
and a target reporting `skipped` is not evidence of anything.

**Run the gate through `make`. Never substitute a bare `go test ./...`.** The
Makefile exports `GOTOOLCHAIN` from go.mod's `toolchain` directive, so every
`go` it invokes runs that exact toolchain. Outside `make` you get your own Go,
and the two need not agree — `go 1.24.0` in go.mod is a *floor*, so without the
pin every contributor compiles against a different release and lint findings
differ. That has already cost one review cycle.

Run `make tools` once: it builds the pinned `golangci-lint` with *its* pinned Go
into `./bin`, which the gate calls by absolute path. A PATH install is not used
even at the identical version — golangci-lint embeds the `go/types` of whatever
compiled it. `make tools-check` prints both resolved versions.

The pinned toolchain is past upstream end-of-life. **That is deliberate:** the
floor is this library's contract, and the gate is what proves it still compiles
there. Revisit at `v1.0.0`.
