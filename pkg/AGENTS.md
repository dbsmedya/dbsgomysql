# pkg/ — library rules

Applies to `pkg/` and `internal/`. Repo-wide contract: [`../AGENTS.md`](../AGENTS.md).

`.golangci.yml` is the canonical statement of the mechanical rules — no `panic`,
no logging, no `init()`, no global mutable state, wrapped errors, GoDoc on every
export, stdlib-only `pkg/`. Read it there, not here. Loosen it only with the
reason in the commit message. Standard Go practice applies without being
restated.

What no tool checks:

- **Import stdlib only.** `pkg/` never imports a MySQL driver and never opens,
  configures, or closes a connection. The consumer supplies it.
- **Accept the smallest `database/sql` interface the call needs** — one that a
  `*sql.DB` and a `*sql.Tx` both satisfy.
- **Return facts as `(facts, error)`. Return checks as `[]Finding` alone.** A
  check is a pure predicate over facts: it inspects nothing, so it has nothing
  to fail at. Give it no `error` return.
- **Never report a finding as an error, or an error as a finding.** A composite
  primary key is a finding; an unreachable server is an error.
- **Name the object in every error, and claim no attribution you do not have.**
- **Document the failure mode every check protects against.** The rationale is
  the product — it is what makes this a reference library and not a bag of
  predicates.
- **State on every exported type whether it is safe for concurrent use.**
- **Give every accommodated MySQL quirk a `docs/COMPAT.md` entry *and* a test
  pinning the behavior.** Both, or neither is done.
- **Findings carry no severity** — not even a remappable default, because a
  default is a decision and the decision is the consumer's.
- **`v0.x`: anything may break.** Mark `!` on the commit type and say so in
  `CHANGELOG.md`.

Before asserting any MySQL behavior in code, a rationale, or a COMPAT entry,
look it up — see [`../AGENTS.md`](../AGENTS.md) §4. A remembered MySQL fact is
not a fact.

When unsure whether something should be public, put it in `internal/`. Moving
outward later is additive; moving inward is breaking.
