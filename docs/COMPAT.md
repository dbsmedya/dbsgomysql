# MySQL Compatibility & Quirk Registry

`dbsgomysql` supports **MySQL 8.0 and newer**. It is tested against **8.0,
8.4, and 9.7**. The 26.x development line is watched but not supported: its CI
job is allowed to fail and no code accommodates it until it stabilizes.

This document is the registry of MySQL behaviors that differ across versions or
that surprise callers of `information_schema`. Each entry states the affected
versions, the observable symptom, and how the library handles it.

Most entries here describe behavior MySQL exhibits *identically* on every
supported version. Where a behavior genuinely differs between versions, it is
also listed in the
[version divergence register](mysql-version-specific-compatibility.md), which
records nothing else and is currently empty.

> **Status legend** — ✅ handled and pinned by a test · ⚠️ bounded and pinned
> limitation (safe behavior exists; the underlying server gap is not solved) ·
> 🔜 registered, handling lands with the package that needs it · 👁 operator
> guidance only, no library code involved.
>
> An entry becomes ✅ when its handling lands with a linked pinning test.

## Strategy

Four principles govern every entry here. They are deliberately conservative:

1. **Normalize both forms rather than sniff versions.** Where output shape
   drifted, the library normalizes old and new into one comparable form.
2. **Try, then fall back.** Where statement syntax was renamed, try the current
   form and fall back to the legacy one on a syntax error.
3. **Fail closed on visibility gaps.** If the connected account provably cannot
   see all relevant metadata, the library reports that fact rather than a false
   "all clear".
4. **No runtime version branching.** The library never issues `SELECT
   VERSION()` or reads `@@version` to choose a code path. Version drift is
   absorbed by (1) and (2), which are testable without a version matrix.

---

## 1. Integer display widths dropped ✅

**Affected:** all supported versions can expose legacy widths after an in-place
upgrade; 8.0.17 and newer stop recording widths on newly created integer
columns.

**Symptom:** `information_schema.COLUMNS.COLUMN_TYPE` returns `bigint(20)` on
an older server and `bigint` on a current one. A naive string comparison
between a schema captured before 8.0.17 and one captured after reports a false
type mismatch on every integer column.

**Handling:** `ColumnSpec.NormalizedType` strips the display width from
`smallint`, `mediumint`, `int`, `integer`, and `bigint`, and from `tinyint`
except for **`tinyint(1)`**. `BOOLEAN` is an alias for `TINYINT(1)`, and MySQL
preserves that width where it strips every other; erasing it would report a
boolean and a plain `TINYINT` as identical. The `unsigned` and `zerofill`
attributes are preserved because they change the value range.

A fresh current server cannot reproduce `int(11)`, so legacy-form
normalization is pinned synthetically by
[`TestNormalizeColumnType`](../pkg/validations/spec_normalize_test.go). The
matrix pins that new integers are bare, `tinyint(1)` survives, and decimal
precision is untouched in
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go),
verified on 8.0.46, 8.4.9, and 9.7.1.

## 2. `information_schema` name collations vary by category ✅

**Affected:** all supported versions.

**Symptom:** name columns in `information_schema` do not share one collation.
`COLUMN_NAME` and `CONSTRAINT_NAME` use `utf8mb3_tolower_ci`,
`TRIGGER_NAME` uses `utf8mb3_general_ci`, and `TABLE_NAME` and `SCHEMA_NAME`
use `utf8mb3_bin` on the tested 8.0 / 8.4 / 9.7 matrix. A query like

```sql
SELECT ... FROM information_schema.COLUMNS WHERE COLUMN_NAME = 'log_id'
```

also matches a column actually named `LOG_ID`. Code that trusts such a lookup
to confirm exact naming silently accepts the wrong case, which then fails later
against a case-sensitive consumer or a differently configured server.
Conversely, assuming that every metadata name comparison is case-insensitive
is also wrong.

**Handling:** library-wide rule — **fetch the real name and compare it in Go**.
SQL predicates may narrow a metadata result set, but the returned spelling is
the server's actual value and any acceptance decision is made by exact
comparison in Go. This is core behavior, not a workaround limited to one
check. The category collations and both sides of the case-exact comparison are
pinned by
[`TestMetadataNameCollationsIntegration`](../pkg/validations/validations_integration_test.go),
[`TestColumnNameCaseInsensitivityIntegration`](../pkg/validations/validations_integration_test.go),
and
[`TestTableNameCaseSensitivityIntegration`](../pkg/validations/validations_integration_test.go).

## 3. `GRANTEE` does not escape embedded quotes ✅

**Affected:** all supported versions.

**Symptom:** MySQL builds the `GRANTEE` column of the `*_PRIVILEGES` tables by
naive concatenation. A user named `o'brien` yields the literal string
`'o'brien'@'%'` — the embedded quote is **not** doubled or escaped. Code that
constructs a grantee string with correct SQL escaping will never match.

**Handling:** the library reproduces MySQL's concatenation exactly, including
the missing escape, rather than producing well-formed SQL. Matching the
server's actual behavior is the requirement here. Pure formatting is pinned by
[`TestFormatGranteeEmbeddedQuote`](../pkg/validations/grants_test.go), and the
server output is pinned across the matrix by
[`TestGranteeAndRolePrivilegesIntegration`](../pkg/validations/validations_integration_test.go).

## 4. Privileges held through nested roles are not resolved ⚠️

**Affected:** all supported versions. **This is a bounded limitation, not a
claim that role closure is solved.**

**Symptom:** the library resolves the current account plus structured
`ROLE_NAME` / `ROLE_HOST` rows from `information_schema.ENABLED_ROLES`. It does
not walk the role graph. A privilege held only through a role granted *to
another role* therefore does not appear under any grantee the fact queries.

**Handling:** the library reports every role-dependent answer as
`GrantUnconfirmed`, even on a pinned session, and never reports `GrantAbsent`
while any role is enabled. This is necessary because the ordinary
`*_PRIVILEGES` tables visible to the account do not expose the enabled role's
grant rows; `SHOW GRANTS` proves the privilege is effective, but parsing that
statement and walking role-specific metadata are outside this slice. The pure
state table and live direct plus role-granted-to-role cases are pinned by
[`TestGrantResolutionNegativesAndPartialRevokes`](../pkg/validations/grants_test.go)
and
[`TestGranteeAndRolePrivilegesIntegration`](../pkg/validations/validations_integration_test.go).

## 5. Cross-schema foreign key metadata can be invisible ✅

**Affected:** all supported versions.

**Symptom:** a foreign key constraint is exposed in `information_schema` only
to accounts privileged on the **child** table. An account without privileges on
some schema cannot see that schema at all — not even in `SCHEMATA`. A query for
"which foreign keys point into these tables?" therefore returns an
under-count with no error and no warning.

**Handling:** `Inspector.ForeignKeys` first queries the server-wide
`INNODB_FOREIGN JOIN INNODB_FOREIGN_COLS` registry. Both tables require
`PROCESS`, so a successful statement proves complete registered InnoDB FK
discovery and returns `VisibilityComplete`, even when the account has no
`SELECT` privilege on either schema. `PROCESS` is the only grant required for
that source.

Any primary-source failure tries the standard
`KEY_COLUMN_USAGE JOIN REFERENTIAL_CONSTRAINTS` query. A successful standard
query remains useful but returns `VisibilityUnconfirmed`, because its rows are
filtered by privileges on the child table. Closure then emits its own
unconfirmed finding, and `CheckFKMetadataVisibility` emits the catalog-level
finding; an empty fallback result is never mistaken for proof that no incoming
key exists. Partial revokes, active roles, and pooled-session affinity do not
participate in this FK source proof.

The completeness claim is InnoDB-scoped. MyISAM ignores foreign-key
declarations and has no enforced keys to discover. NDB Cluster uses different
metadata and is outside this repository's supported/tested matrix. Primary,
fallback, same- and cross-schema, `PROCESS`-only, and MyISAM behavior are
pinned by
[`TestForeignKeysIntegration`](../pkg/validations/validations_integration_test.go),
[`TestForeignKeyVisibilityAccountsIntegration`](../pkg/validations/validations_integration_test.go),
and the phase-1c E2E goldens in
[`TestPhase1cFindingsE2E`](../tests/e2e/e2e_test.go).

## 6. Replication status statements and columns were renamed 🔜

**Affected:** `SHOW REPLICA STATUS` was added in 8.0.22; `SHOW SLAVE STATUS`
was removed in 8.4. The column `Seconds_Behind_Master` became
`Seconds_Behind_Source` in 8.4.

**Symptom:** a single hard-coded statement or column name fails on one end of
the supported range. Additionally, 8.4 returns the seconds-behind column with a
different Go driver type, which breaks naive type switches that worked on 8.0.

**Handling:** try the current form, fall back to the legacy one on a syntax
error; accept both column names; convert the value defensively rather than
type-switching on a single expected type. Reserved for `pkg/replication`
(phase 2).

## 7. `mysql_native_password` removed in 8.4 👁

**Affected:** 8.4 and newer.

**Symptom:** accounts created with `mysql_native_password` cannot authenticate
after an upgrade to 8.4.

**Handling:** **none — this is outside the library.** `dbsgomysql` accepts a
`*sql.DB` and never manages connections or authentication. The entry exists
here as operator guidance: migrate affected accounts to
`caching_sha2_password` before upgrading.

## 8. Supplementary identifier characters are replaced ✅

**Affected:** all supported versions.

**Symptom:** MySQL accepts a quoted identifier containing a Unicode character
above `U+FFFF`, but does not preserve it. For example, a table requested as
`supp_𐀀` is stored and reported by `information_schema` as `supp_?`. Reusing
the original SQL text can appear to work because the same replacement happens
again, but the configured name does not round-trip and can collide with a
literal question mark. Looking the original name up afterwards does not simply
fail to match: comparing `information_schema.TABLES.TABLE_NAME` against the
original supplementary-character parameter **raises error 3988**
(`ER_CANNOT_CONVERT_STRING`), because MySQL cannot convert the `utf8mb4`
parameter into the metadata column's `utf8mb3` collation. Code must not read
that error as "the table does not exist". The collation named in the message
text follows the connection's collation, so the error *number* is the stable
part.

**Handling:** `sqlutil.ValidateIdentifier` returns
`ErrIdentifierSupplementary` before SQL is executed, which keeps callers away
from both the silent replacement and the metadata error. `QuoteIdentifier`
remains total and safe for interpolation because validity and quoting safety
are separate contracts. The replacement and the 3988 lookup failure are both
pinned by
[`TestIdentifierCharacterSetIntegration`](../pkg/sqlutil/sqlutil_integration_test.go)
on MySQL 8.0, 8.4, and 9.7; the validator result is pinned independently by
[`TestValidateIdentifier`](../pkg/sqlutil/sqlutil_test.go).

## 9. Trailing ASCII space characters are rejected ✅

**Affected:** all supported versions.

**Symptom:** MySQL rejects database, table, and column identifiers whose final
character is TAB, LF, VT, FF, CR (`U+0009`–`U+000D`), or SPACE (`U+0020`).
Databases fail with error 1102, tables with 1103, and columns with 1166.
Position is what decides it: the same six characters remain legal in the
leading position, as do NBSP (`U+00A0`) and ideographic space (`U+3000`)
anywhere.

**Handling:** `sqlutil.ValidateIdentifier` returns
`ErrIdentifierTrailingSpace` for the six rejected final characters. Server
behavior is pinned by
[`TestIdentifierCharacterSetIntegration`](../pkg/sqlutil/sqlutil_integration_test.go),
which asserts the specific error number for each of the three object kinds
rather than merely that the statement failed; the validator and error
precedence are pinned by
[`TestValidateIdentifier`](../pkg/sqlutil/sqlutil_test.go).

## 10. `ACTION_TIMING` sorts by ENUM index, not alphabetically ✅

**Affected:** all supported versions.

**Symptom:** `information_schema.TRIGGERS.ACTION_TIMING` is declared
`ENUM('BEFORE','AFTER')`, not a string column. MySQL orders an `ENUM` by its
declaration index, so `ORDER BY ACTION_TIMING` yields `BEFORE` before `AFTER` —
firing order, which is what a caller wants. Read as text the pair inverts,
since `'AFTER' < 'BEFORE'`. The hazard is that the SQL *looks* like a string
sort that is obviously wrong and invites a "correction" into a `CASE`
expression, which would change nothing on a real server while implying the
original was broken. The same trap applies to `EVENT_MANIPULATION`, declared
`ENUM('INSERT','UPDATE','DELETE')`.

**Handling:** `Inspector.Triggers` orders by `ACTION_TIMING` in SQL and relies
on the ENUM index deliberately; the reliance is called out at the query. The
pure check `CheckTriggersPresent` cannot depend on it — it sorts facts already
in memory, with no server involved — so it reproduces the same order in Go
through `triggerTimingOrder`. The two agree by construction rather than by
luck, and
[`TestTriggerTimingEnumOrderIntegration`](../pkg/validations/validations_integration_test.go)
pins both halves: that the column is still an `ENUM` with `BEFORE` declared
first, and that the fact method returns BEFORE-timed triggers first.
[`TestTriggersIntegration`](../pkg/validations/validations_integration_test.go)
additionally asserts the `Timing` values themselves rather than only the
resulting name order.

---

## 11. Partial revokes hide restrictions from the privilege tables ⚠️

**Affected:** 8.0.16 and newer, whenever `@@global.partial_revokes` is enabled.
**This is a bounded limitation, not a claim that restrictions are resolved.**

**Symptom:** `REVOKE SELECT ON db.* FROM u` after `GRANT SELECT ON *.* TO u`
stores the restriction in `mysql.user.User_attributes`, not in the
`*_PRIVILEGES` tables. `information_schema.USER_PRIVILEGES` keeps reporting the
unrestricted global `SELECT`, so a global row can coexist with a schema the
account cannot read at all.

**Handling:** while partial revokes are enabled, a global privilege row proves
nothing on its own. Schema and table answers are `GrantUnconfirmed` until a
direct schema or table grant proves the requested object, and the global answer
is `GrantUnconfirmed` too, because this package deliberately does not read
`mysql.user` or parse `SHOW GRANTS`, and no more-specific row can prove a
global-scope question.

The degradation stops there. Restrictions only ever subtract from an existing
grant, so a privilege with **no** row at any scope is still reported
`GrantAbsent` on a pinned, role-free session — enabling partial revokes
instance-wide must not make every negative answer unprovable. The pure state
table is pinned by
[`TestPartialRevokesDegradeEveryAnswerBackedByGlobalRow`](../pkg/validations/grants_test.go)
and
[`TestPartialRevokesDoNotHideProvableAbsence`](../pkg/validations/grants_test.go),
and the live behavior by
[`TestPartialRevokesPrivilegeResolutionIntegration`](../pkg/validations/validations_integration_test.go).

## 12. Schema grants may be stored as wildcard patterns ⚠️

**Affected:** all supported versions, while `@@global.partial_revokes` is
disabled. **This is a bounded limitation: patterns downgrade an answer, they
never resolve one.**

**Symptom:** the schema column of `mysql.db` may hold SQL pattern characters —
`GRANT SELECT ON \`shop%\`.* TO u` is legal, and MySQL matches real database
names against that pattern. `information_schema.SCHEMA_PRIVILEGES.TABLE_SCHEMA`
surfaces the pattern literally, so an exact-name lookup misses a grant the
account genuinely holds. Note that `_` is a wildcard too: a grant recorded on
`my_db` also covers `myXdb`. Enabling `partial_revokes` makes MySQL treat both
characters literally, which closes the gap. Table-level rows are unaffected:
MySQL requires a literal schema name in `mysql.tables_priv`.

**Handling:** `Grants.Schema` and `Grants.Table` consult stored schema keys as
patterns only to weaken an answer. When the exact lookup finds nothing and a
stored pattern matches the requested schema, the answer becomes
`GrantUnconfirmed` instead of `GrantAbsent`, so the privilege checks report
uncertainty rather than a spurious "privilege is absent" finding. A pattern
never yields `GrantPresent`: choosing which of several matching `mysql.db` rows
MySQL applies is not modeled here. Matching is case-exact, like every other
identifier comparison in the package. Pinned by
[`TestWildcardSchemaGrantDowngradesAbsenceOnly`](../pkg/validations/grants_test.go)
and [`TestLikePatternMatches`](../pkg/validations/grants_test.go).

---

## 13. PRIMARY KEY constraint names are discarded ✅

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** MySQL stores every primary key under the fixed name `PRIMARY`.
Writing `CONSTRAINT pk_orders PRIMARY KEY (id)` does not preserve `pk_orders`,
although names declared for CHECK, FOREIGN KEY, and UNIQUE constraints do
survive.

**Handling:** `WithIndexes()` reports the server fact: the primary index is
named `PRIMARY`. Consumers must not compare or reconstruct a declared
primary-key constraint name that MySQL discarded. The behavior and the
survival of other declared names are pinned by
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go).

## 14. Expression defaults are rewritten and marked only in `EXTRA` ✅

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** a declaration such as `DEFAULT (CURRENT_DATE)` is exposed through
`COLUMN_DEFAULT` as `curdate()`. That value alone is indistinguishable from a
literal default containing the same text; only `DEFAULT_GENERATED` in
`COLUMNS.EXTRA` identifies the expression.

**Handling:** `ColumnSpec.Default` preserves the rewritten server value and
`DefaultIsExpression` records `DEFAULT_GENERATED`. `DiffSpecs` compares both,
so a literal and an expression with equal text do not compare equal. Pinned by
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go)
and
[`TestDiffSpecsExpressionDefaultDiffersFromLiteral`](../pkg/validations/spec_diff_test.go).

## 15. `CHECK_CLAUSE` is server-normalized ✅

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** `information_schema.CHECK_CONSTRAINTS.CHECK_CLAUSE` is not the
source text. MySQL backticks identifiers and rewrites keyword case; for
example, `CHECK (gpa BETWEEN 0.00 AND 4.00)` becomes
`` (`gpa` between 0.00 and 4.00) ``.

**Handling:** `ConstraintSpec.CheckClause` preserves the normalized server
form and `DiffSpecs` compares it verbatim. The exact rewrites are pinned by
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go).

## 16. Foreign keys create a supporting index named after the constraint 👁

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** when no suitable child index exists, MySQL creates one and names
it after the foreign-key constraint. The index is visible in
`information_schema.STATISTICS` even though no separate index appeared in the
DDL.

**Handling:** none is needed: the supporting index is real, and
`WithIndexes()` reports it like every other index. A caller that captures only
indexes can therefore see a foreign-key difference as `IndexAbsent`; capture
constraints too when the relationship itself is in scope. Pinned by
[`TestForeignKeyCreatesSupportingIndexIntegration`](../pkg/validations/validations_integration_test.go).

## 17. A `NOT ENFORCED` CHECK is recorded but never evaluated ✅

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** `CHECK (a > 0) NOT ENFORCED` remains present in metadata with the
same clause as an enforced check, but `TABLE_CONSTRAINTS.ENFORCED` is `NO` and
the server does not validate rows against it. Comparing the clause alone
produces a false all-clear.

**Handling:** `ConstraintSpec.Enforced` records the metadata flag for CHECK
constraints, and `DiffSpecs` emits `CheckEnforcementMismatch` independently of
the clause. Live behavior is pinned by
[`TestTableSpecCompatEnforcementIntegration`](../pkg/validations/validations_integration_test.go);
the pure comparison is pinned by
[`TestDiffSpecsConstraintDifferences`](../pkg/validations/spec_diff_test.go).

---

## Adding an entry

Every version-specific behavior the code accommodates gets an entry here **and**
a test that pins it in the integration matrix. A quirk handled in code but not
recorded here is a quirk that will be "fixed" by someone who does not know why
the code is shaped that way.
