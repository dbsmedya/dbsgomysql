---
name: release
description: Use when cutting a dbsgomysql release, tagging a version, deciding which version a change belongs in, or asking whether work should ship now. Owns the version rule, the release order, and the CHANGELOG discipline.
---

# Releasing dbsgomysql

**Sinan owns releases. Do not cut one unasked.** Propose it, prepare it, check
it — but a tag is the one step here that cannot be taken back: Go's module proxy
records it on first fetch, so a mistake costs a new version rather than a fix.
Wait to be told.

## 1. Decide the version — before implementing, not at release time

**Read `.ayder/versions/ROADMAP.md` first.** It is the version authority: which
release carries which issue, and what is on hold. Take the next unclaimed
version from its table.

| Change | Bump |
|---|---|
| New public API, new package, or any breaking change | **minor** — `0.N.0` |
| Bug fix, doc correction, test-only work, CI | **patch** — `0.N.M` |

Under `v0.x` the **minor** is the compatibility unit — it is what a consumer
pins and what Go tooling treats as the boundary.

Two rules that are not negotiable:

- **A consumer report that can only be answered by changing the public API is a
  minor**, whatever its bug report looked like.
- **If the work turns out to land new public API and the ROADMAP row said patch,
  change the row before implementing** — not at release time.

A GoDoc-only change is a patch, but it still needs a tag: consumers read
pkg.go.dev, which renders tags, not `main`.

## 2. Cut it

**Every line is marked with who performs it. The two marked `SINAN` are his
decisions, not yours — do not perform them, and do not read the lines around
them as permission to. A merge can be reverted; the tag that follows cannot.**

```
you    branch release/vX.Y.Z from main
you    move [Unreleased] under `## [X.Y.Z] - YYYY-MM-DD`, add the compare link
you    commit `chore(release): vX.Y.Z`, push, open the PR
you    report that it is ready, then STOP

SINAN  reviews the PR and merges it on GitHub

you    git checkout main && git pull --ff-only
you    dispatch integration.yml against main, confirm green on every version
you    report green, then STOP

SINAN  says to tag

you    tag vX.Y.Z on the merge commit, push the tag
you    write .ayder/versions/vX.Y.Z.md
you    make -C .ayder post-merge
```

Both stops are real stops. A green matrix is not permission to tag, and an
approved PR is not permission to merge.

**This skill also fires on questions** — "which version does this take?", "should
this ship now?". Answering one is not starting a release. Answer, and stay
stopped.

## 3. The four things that have actually gone wrong

**Move `[Unreleased]` in the release commit.** It lagged the tag on five
consecutive releases (`v0.6.0`–`v0.6.4`), each backfilled afterwards by a
separate PR. This is the single most-repeated defect in the repo's history.

**Merge before tagging.** The tag names a commit on `main`, never one that only
exists on a branch.

**Dispatch `integration.yml` against `main` and confirm green before tagging.**
The tag push also triggers it, but that is after the fact. Dispatching first
verifies the exact commit about to be tagged.

**Cut the tag from the last merge in the set.** `v0.6.3` was tagged the moment
the first of two PRs merged, so the second PR's work could never reach it and
had to be retargeted to `v0.6.4`. If a release spans two PRs, say when the tag
is cut relative to the second merge — or give each PR its own version.

**A published tag is never moved.** Go's module proxy and checksum database
record it on first fetch; rewriting breaks every consumer that already resolved
it. A mistake costs a new version, not a retag.

## 4. Verify, then claim

```sh
make check                                   # must pass
gh pr checks <n>                             # all green
go list -m -versions github.com/dbsmedya/dbsgomysql   # proxy sees the tag
```

For a release whose payload is documentation, confirm the rendered result from
the published module rather than the working tree — fetch it and run `go doc`.
