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

## Reference validation

Every entry closes with a **Reference** line naming where the claim comes from.
Three kinds appear, and the difference matters when an entry is challenged:

- **Documented** — the MySQL manual, a release note, or the error reference
  states the behavior. The entry paraphrases the source; the source wins.
- **Documented in part** — the manual states the surrounding rule but not the
  specific consequence recorded here. The gap is named explicitly.
- **No supporting statement found** — searching the corpus did not turn up
  documentation, so the behavior rests on the pinning test alone. Read this as a
  statement about the search, **not** a claim that MySQL documents nothing: the
  reference manual is roughly 13 MB per version and the search is a hybrid
  semantic one whose own contract says an empty result is a low-confidence
  signal, not proof of absence. Each such line records what was searched so the
  next reader can go further rather than repeat it. These are also the entries
  that most need their test, because nothing upstream will warn us if the server
  changes.

References cite the manual's **section title**. Section *numbers* are unstable
across versions and are given for 8.4 only as a convenience — the `TRIGGERS`
table is §28.3.44 in the 8.4 manual and §28.3.50 in 9.7, and CHECK Constraints
moved from §15.1.20.6 to §15.1.25.6 over the same range. Resolve any reference
by its title. Release notes cite the version, its date, and the worklog number
where one is given.

Where a reference has a stable manual URL, cite it alongside the title. The
`dev.mysql.com` slug is normally identical across versions — only the version
segment changes — which makes a per-version URL set the one form of citation
that survives the renumbering described above. The title still leads; the URL
is there so a reader can reach the page without searching for it.

**Open the URL before writing it down.** Slugs are not derivable: the page
holding §17.15's foreign-key example is not at the address its section title
suggests, and a guess there returns 404. A dead link is worse than the
title-only citation this file used everywhere before, because it reads as
verification that never happened. These URLs are navigation; the corpus named
in AGENTS.md section 3 is what settles a claim.

**Validation coverage.** Every claim below was checked on **2026-08-01** against
the corpus described in AGENTS.md section 3. Entries carrying a version
threshold, an error number, or an "all supported versions" claim were queried
once per version file and the answers diffed — the method that turned up the
three corrections recorded in `CHANGELOG.md`. Two limits worth stating plainly:
a documented behavior confirmed in all three manuals is still only evidence
about the *documentation*, and the integration matrix remains what pins the
server; and the corpus is a point-in-time snapshot of the 8.0, 8.4, and 9.7
publications, so an entry can go stale without any line here changing.

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

**Affected:** display widths were **deprecated in 8.0.17** but kept appearing in
output until **8.0.19**, which stopped showing them for integer types. All
supported versions can still expose legacy widths, because 8.0.19 changed only
what new statements emit — data dictionary entries written by an earlier 8.0
release are left as they are. An upgrade from 5.7 is the opposite case: it
re-creates all dictionary information, so widths are dropped.

**Symptom:** `information_schema.COLUMNS.COLUMN_TYPE` returns `bigint(20)` on
an older server and `bigint` on a current one. A naive string comparison
between a schema captured before 8.0.17 and one captured after reports a false
type mismatch on every integer column.

**Handling:** `ColumnSpec.NormalizedType` strips the display width from
`smallint`, `mediumint`, `int`, `integer`, and `bigint`, and from `tinyint`
except for **`tinyint(1)`**. `BOOLEAN` is an alias for `TINYINT(1)`, and MySQL
preserves that width where it strips every other; erasing it would report a
boolean and a plain `TINYINT` as identical. This is not an accident of the
implementation — Oracle carved out the exception deliberately, because "MySQL
Connectors make the assumption that `TINYINT(1)` columns originated as `BOOLEAN`
columns". A column carrying `ZEROFILL` keeps its width too — that is MySQL's
second documented exception, and the width is genuinely semantic there, because
retrieved values are zero-padded to it: `int(5) zerofill` yields `00042` where
`int(10) zerofill` yields `0000000042`. Trailing attributes such as `unsigned`
are preserved because they change the value range.

A fresh current server cannot reproduce `int(11)`, so legacy-form
normalization is pinned synthetically by
[`TestNormalizeColumnType`](../pkg/validations/spec_normalize_test.go). The
matrix pins that new integers are bare, `tinyint(1)` survives, both zerofill
widths survive and diff as a `ColumnTypeMismatch`, and decimal precision is
untouched in
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go),
verified on 8.0.46, 8.4.9, and 9.7.1. `INT ZEROFILL` declared without a width
reports `int(10) unsigned zerofill` on all three, so preserving the width does
not make the bare declaration compare unequal to an explicit `INT(10)`.

**Reference:** documented. Refman §13.1.6, "Numeric Type Attributes" and
§13.1.1, "Numeric Data Type Syntax", for the deprecation. MySQL 8.0 Release
Notes, 8.0.17 (2019-07-22), Deprecation and Removal Notes (WL #13127) deprecates
the attribute; 8.0.19 (2020-01-13), Deprecation and Removal Notes (WL #13528,
Bug #30556657) is where output stops showing it, and states both exceptions and
the data-dictionary retention rule quoted above. All three manuals still read
"you should expect support … to be removed in a future version of MySQL", so the
attribute is deprecated but **not removed as of 9.7** — this entry does not
become moot when the 8.0 line is dropped.

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

The manual describes this from the other direction, and doing so makes the rule
above stronger rather than weaker. It states that `information_schema` string
columns have a collation of `utf8mb3_general_ci`, but that for values naming
objects *represented in the file system* — databases and tables — a search "can
be case-sensitive or case-insensitive, depending on the characteristics of the
underlying file system and the `lower_case_table_names` system variable
setting". So the per-category collations observed on the test matrix are an
effect of that environment, not a fixed property: the same query changes answer
on a case-insensitive file system, or when `lower_case_table_names` is 1 or 2.
There is therefore no server-side comparison the library could rely on across
deployments, which is exactly why the decision is made in Go.

**Reference:** documented in part. Refman §12.8.7, "Using Collation in
INFORMATION_SCHEMA Searches", documents the mechanism and the
`lower_case_table_names` dependency, worded identically in the 8.0, 8.4, and 9.7
manuals. The specific per-column collations recorded above are not stated there;
they come from the pinning tests.

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

**Reference:** no supporting statement found. Searched the 8.4 manual for the
`*_PRIVILEGES` `GRANTEE` column and for account-name quoting rules. What the
manual gives is the *format* — "the name of the account to which the privilege
is granted, in `'user_name'@'host_name'` format" — with nothing on what happens
when either part contains a quote. Refman §28.3.10, "The INFORMATION_SCHEMA
COLUMN_PRIVILEGES Table". One structural detail does corroborate the
concatenation: §28.3.27, "The INFORMATION_SCHEMA ROLE_COLUMN_GRANTS Table",
exposes `GRANTEE` and `GRANTEE_HOST` as *separate* columns and so has no
escaping problem at all, which is only possible because the `*_PRIVILEGES`
tables join the two into one string. Not searched: the 8.0 and 9.7 manuals, and
the `GRANT` statement reference, where an escaping rule could plausibly live.

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

**Reference:** not applicable — this is a scope boundary of this library, not a
MySQL behavior. MySQL resolves role closure correctly; the entry records that
this package does not follow it there.

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

**Reference:** documented. Refman §28.4.12, "The INFORMATION_SCHEMA
INNODB_FOREIGN Table", and §28.4.13, "The INFORMATION_SCHEMA INNODB_FOREIGN_COLS
Table", each state the requirement this entry's completeness claim rests on:
"You must have the `PROCESS` privilege to query this table." Confirmed in the
8.0, 8.4, and 9.7 manuals — the sentence is identical in all three, which
matters because `VisibilityComplete` is asserted on that one privilege. Worked
examples of both tables appear in §17.15, "InnoDB INFORMATION_SCHEMA Tables"
(Example 17.3). That the standard `KEY_COLUMN_USAGE` route is filtered by
child-table privileges is documented per-table under §28.3, "INFORMATION_SCHEMA
General Tables"; that the two sources therefore disagree is ours.

## 6. Replication status statements and columns were renamed 🔜

**Affected:** the `REPLICA` spellings were added in **8.0.22**, which
simultaneously deprecated the `SLAVE` ones; the `SLAVE` statements were
**removed in 8.4**. The column `Seconds_Behind_Master` became
`Seconds_Behind_Source` in the same 8.0.22 terminology change.

**Symptom:** a single hard-coded statement or column name fails on one end of
the supported range. Additionally, 8.4 returns the seconds-behind column with a
different Go driver type, which breaks naive type switches that worked on 8.0.

**Handling:** try the current form, fall back to the legacy one on a syntax
error; accept both column names; convert the value defensively rather than
type-switching on a single expected type. The defensive conversion covers a
second drift the rename hides: 8.4 also narrowed when the column is `NULL`, so
a value that was previously non-`NULL` on a half-running replica may now arrive
as `NULL`. Reserved for `pkg/replication` (phase 2).

**Reference:** documented. MySQL 8.0 Release Notes, 8.0.22 (2020-10-19),
Deprecation and Removal Notes (WL #14171), deprecates `START SLAVE`, `STOP
SLAVE`, `SHOW SLAVE STATUS`, `SHOW SLAVE HOSTS`, and `RESET SLAVE` and names the
replacement for each. Refman §1.4, "What Is New in MySQL 8.4 since MySQL 8.0"
(Features Removed in MySQL 8.4), confirms removal and lists the `MASTER`
statements removed alongside them. Refman §19.1, "Configuring Replication",
documents `Seconds_Behind_Source` including the 8.4 `NULL` rule. The rename ran
in waves rather than one release — the MySQL 8.4 Release Notes, Changes in MySQL
8.2.0 (2023-10-25), SQL Syntax Notes (WL #14190), deprecate a further set
(`RESET MASTER`, `SHOW MASTER STATUS`, `SHOW MASTER LOGS`, `PURGE MASTER LOGS`)
— so a phase-2 implementation should treat this as a family of renames with
several thresholds, not a single 8.0.22/8.4 cut.

## 7. `mysql_native_password` disabled in 8.4, removed in 9.0 👁

**Affected:** 8.0.34 deprecated the plugin; **8.4 disables it by default but
still ships it**; **9.0.0 removed it from the server**. The removal is
server-side only — the client-side plugin remains available, converted into a
dynamically loadable plugin — so "removed in 9.0" describes what the server will
authenticate, not what a client can still offer.

**Symptom:** on 8.4, accounts authenticating with `mysql_native_password` fail
to connect with `ERROR 1045 (28000): Access denied`, and `CREATE USER ...
IDENTIFIED WITH 'mysql_native_password'` fails with `ERROR 1524 (HY000): Plugin
'mysql_native_password' is not loaded`. The distinction from removal is
operationally real: an 8.4 server can be started with
`--mysql-native-password=ON` to restore the accounts, which buys time to
migrate. On 9.0 and newer there is no such option, and `caching_sha2_password`
is the only supported path.

**Handling:** **none — this is outside the library.** `dbsgomysql` accepts a
`*sql.DB` and never manages connections or authentication. The entry exists
here as operator guidance: migrate affected accounts to
`caching_sha2_password` before upgrading, and do not read an 8.4 authentication
failure as evidence the plugin is gone.

**Reference:** documented. Refman §8.4.1, "Security Components and Plugins"
(Native Pluggable Authentication), states the sequence exactly: "The
`mysql_native_password` authentication plugin is deprecated as of MySQL 8.0.34,
disabled by default in MySQL 8.4, and removed as of MySQL 9.0.0", with the
disable-and-re-enable procedure and both error numbers under "Disabling Native
Pluggable Authentication". The 9.7 manual, §8.4.1.2, "SHA-256 Pluggable
Authentication", confirms the end state: "In MySQL 9.7, `caching_sha2_password`
is the default authentication plugin; `mysql_native_password` is no longer
available." The MySQL 9.7 Release Notes, Changes in MySQL 9.0.0 (2024-07-01),
Deprecation and Removal Notes (WL #15930), give the removal itself and its
scope: the plugin "has been removed, and the server now rejects mysql_native
authentication requests from older client programs which do not have
`CLIENT_PLUGIN_AUTH` capability. For backward compatibility,
`mysql_native_password` remains available on the client". The same note records
that 9.0 also removed the `--mysql-native-password` and
`--mysql-native-password-proxy-users` options and the
`default_authentication_plugin` system variable — which is why the 8.4 escape
hatch above has no 9.x equivalent.

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
(`ER_IMPOSSIBLE_STRING_CONVERSION`), because MySQL cannot convert the `utf8mb4`
parameter into the metadata column's `utf8mb3` collation. Code must not read
that error as "the table does not exist". The message template is `Conversion
from collation %s into %s impossible for %s`, so both collation names are
substituted from the session — the error *number* is the stable part, and the
message text is not.

**Handling:** `sqlutil.ValidateIdentifier` returns
`ErrIdentifierSupplementary` before SQL is executed, which keeps callers away
from both the silent replacement and the metadata error. `QuoteIdentifier`
remains total and safe for interpolation because validity and quoting safety
are separate contracts. The replacement and the 3988 lookup failure are both
pinned by
[`TestIdentifierCharacterSetIntegration`](../pkg/sqlutil/sqlutil_integration_test.go)
on MySQL 8.0, 8.4, and 9.7; the validator result is pinned independently by
[`TestValidateIdentifier`](../pkg/sqlutil/sqlutil_test.go).

**Reference:** documented in part. The 8.0, 8.4, and 9.7 Error Message
References, Chapter 2, "Server Error Message Reference", all give error 3988 as
symbol `ER_IMPOSSIBLE_STRING_CONVERSION`, SQLSTATE `HY000`, with the message
template quoted above. The 8.0 reference adds a threshold the newer ones drop:
"`ER_IMPOSSIBLE_STRING_CONVERSION` was added in 8.0.22." That is below the
effective 8.0.4x floor and so does not affect the supported range, but it does
mean the error did not exist for the first half of the 8.0 line — worth knowing
before this entry is cited for anything older. That the error is what an
`information_schema` name lookup produces for a supplementary-character
parameter, rather than an empty result, is not documented; it comes from the
pinning test.

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

**Reference:** documented in part. All three error numbers and symbols are
confirmed verbatim in the 8.4 and 9.7 Error Message References, Chapter 2,
"Server Error Message Reference" — 1102 `ER_WRONG_DB_NAME` ("Incorrect database
name '%s'"), 1103 `ER_WRONG_TABLE_NAME` ("Incorrect table name '%s'"), and 1166
`ER_WRONG_COLUMN_NAME` ("Incorrect column name '%s'"), each SQLSTATE `42000`,
with no drift between the two. Which characters trigger them, and that position
rather than identity decides it, is not documented; that mapping comes from the
pinning test.

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

**Reference:** no supporting statement found — and that is the point of the
entry. Refman §28.3.44 in the 8.4 manual and §28.3.50 in 9.7, both "The
INFORMATION_SCHEMA TRIGGERS Table", document only the permitted *values*:
`ACTION_TIMING` is "whether the trigger activates before or after the triggering
event. The value is `BEFORE` or `AFTER`", and `EVENT_MANIPULATION` is "`INSERT`,
`DELETE`, or `UPDATE`". Neither version says the columns are `ENUM`, so the
ordering the query depends on has no documented basis in either. Nothing
upstream will announce a change to it; the pinning test is the only guarantee,
which is why it asserts the column type and not just the order. Not searched:
the data dictionary chapter, where the column definitions may be given.

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

**Reference:** documented. MySQL 8.0 Release Notes, 8.0.16 (2019-04-25), Account
Management Notes (WL #12098, WL #12364, WL #12820), introduces `partial_revokes`
and states that "the server records partial revokes by adding a `Restrictions`
attribute to the `User_attributes` column of the `mysql.user` system table".
Refman §8.2.12, "Privilege Restriction Using Partial Revokes", adds the
constraint that matters for the degradation rule: "Partial revokes apply at the
schema level only. You cannot use partial revokes for privileges that apply only
globally … or for table, column, or routine privileges." That the
`*_PRIVILEGES` tables keep reporting the unrestricted global row follows from
the restriction living in `mysql.user`, and is pinned by test rather than stated.

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

**Reference:** documented. Refman §8.2.12, "Privilege Restriction Using Partial
Revokes", states the interaction this entry turns on: "enabling
`partial_revokes` causes MySQL to interpret occurrences of unescaped `_` and `%`
SQL wildcard characters in schema names as literal characters, just as if they
had been escaped as `\_` and `\%`", and advises avoiding unescaped wildcards for
that reason. The same wording appears in §15.7.1.6, "GRANT Statement", and in
the 8.0.16 release note. That a stored pattern therefore defeats an exact-name
lookup against `SCHEMA_PRIVILEGES` is the consequence recorded here.

The 9.7 manual adds a sentence the 8.0 one does not carry, and it points at this
entry's eventual retirement: "use of `_` and `%` as wildcard characters in grants
is deprecated, and you should expect support for them to be removed in a future
version of MySQL." The hazard is therefore on its way out, but is not gone in
any supported version, and a schema captured from an older server can still hold
a pattern. Keep the downgrade.

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

**Reference:** documented. Refman §15.1.20, "Indexes, Foreign Keys, and CHECK
Constraints", states it twice: "The name of a `PRIMARY KEY` is always `PRIMARY`,
which thus cannot be used as the name for any other kind of index", and "In
MySQL, the name of a `PRIMARY KEY` is `PRIMARY`." Both sentences appear
unchanged in the 8.0, 8.4, and 9.7 manuals. The same section explains why the
other names survive — "each constraint type has its own namespace per schema",
so a declared `CONSTRAINT symbol` is retained and must be unique per schema per
type. One 8.0-only wrinkle, below our floor and recorded so it is not
rediscovered: for an unnamed foreign key, "MySQL uses the foreign key index name
up to MySQL 8.0.15, and automatically generates a constraint name thereafter."

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

**Reference:** documented in part. Refman §28.3.8, "The INFORMATION_SCHEMA
COLUMNS Table", lists `DEFAULT_GENERATED` among the `EXTRA` values, "for columns
that have an expression default value" — which establishes that `EXTRA` is the
marker, worded identically in 8.4 and 9.7. Refman §13.6, "Data Type Default
Values", covers expression defaults generally. That `COLUMN_DEFAULT` holds the
server's *rewritten* form rather than the source text, so the two are
indistinguishable without `EXTRA`, is not documented and comes from the pinning
test.

The `EXTRA` value set is **not** closed and has grown: the 9.7 manual lists
`MASKING POLICY`, which the 8.0 and 8.4 manuals do not. That particular value
belongs to MySQL Enterprise Data Masking and so never appears on a community
build, which is why no check reads it. The general point stands regardless:
the library tests for `DEFAULT_GENERATED` rather than enumerating `EXTRA`, and
any future code that switches exhaustively on that column must tolerate a value
it has never seen.

## 15. `CHECK_CLAUSE` is server-normalized ✅

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** `information_schema.CHECK_CONSTRAINTS.CHECK_CLAUSE` is not the
source text. MySQL backticks identifiers and rewrites keyword case; for
example, `CHECK (gpa BETWEEN 0.00 AND 4.00)` becomes
`` (`gpa` between 0.00 and 4.00) ``.

**Handling:** `ConstraintSpec.CheckClause` preserves the normalized server
form and `DiffSpecs` compares it verbatim. The exact rewrites are pinned by
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go).

**Reference:** documented in part. Refman "CHECK Constraints" (§15.1.20.6 in the
8.0 and 8.4 manuals, §15.1.25.6 in 9.7) never states that `CHECK_CLAUSE` is
normalized, but its own worked example shows the rewrite: `CHECK (i1 <> 0)`
comes back from `SHOW CREATE TABLE` as ``CONSTRAINT `t1_chk_1` CHECK ((`i1` <>
0))`` — identifiers backticked and the expression re-parenthesized. The same
section documents the generated-name pattern (`_chk_` plus an ordinal) and that
constraint names are "case-sensitive, but not accent-sensitive". The rewrite
rules themselves are not specified anywhere, so they are pinned rather than
cited. The 8.0 manual also marks the floor: "Prior to MySQL 8.0.16, `CREATE
TABLE` permits only the following limited version of table `CHECK` constraint
syntax, which is parsed and ignored." Below 8.0.16 there is no clause to
normalize, and no constraint either.

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

There is a second half to this the entry previously missed: the index MySQL
creates can also **disappear on its own**. The manual warns that it "might be
silently dropped later if you create another index that can be used to enforce
the foreign key constraint". So a captured schema can lose an index it once
reported without any statement having dropped it, and a diff that treats index
disappearance as a defect will produce a false finding. The foreign key is the
durable fact; its supporting index is not.

**Reference:** documented. Refman §15.1.20.5, "FOREIGN KEY Constraints"
(Conditions and Restrictions): "MySQL requires indexes on foreign keys and
referenced keys so that foreign key checks can be fast and not require a table
scan. In the referencing table, there must be an index where the foreign key
columns are listed as the first columns in the same order. Such an index is
created on the referencing table automatically if it does not exist. This index
might be silently dropped later if you create another index that can be used to
enforce the foreign key constraint." Confirmed word for word in the 8.0, 8.4,
and 9.7 manuals. That the automatic index takes the constraint's name is shown
by the `INNODB_FOREIGN` example in §17.15.

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

**Reference:** documented. Refman "CHECK Constraints" (§15.1.20.6 in 8.0 and
8.4, §15.1.25.6 in 9.7) gives the syntax as `[CONSTRAINT [symbol]] CHECK (expr)
[[NOT] ENFORCED]` and states the consequence plainly in all three: "If omitted
or specified as `ENFORCED`, the constraint is created and enforced. If specified
as `NOT ENFORCED`, the constraint is created but not enforced." The same section
lists the statements an enforced constraint is evaluated for — `INSERT`,
`UPDATE`, `REPLACE`, `LOAD DATA`, and `LOAD XML` — which is the set a `NOT
ENFORCED` constraint silently sits out. The manual's `SHOW CREATE TABLE` example
also shows how the flag surfaces, behind a version gate: ``CONSTRAINT `t1_chk_3`
CHECK ((`i2` <> 0)) /*!80016 NOT ENFORCED */``. Reading the clause text alone
therefore misses it in DDL exactly as it does in metadata.

## 18. A functional index part reports `COLUMN_NAME` as NULL ✅

**Affected:** all supported versions; verified on 8.0.46, 8.4.9, and 9.7.1.

**Symptom:** two symptoms from one cause. `information_schema.STATISTICS`
describes an index as key *parts*, and a functional part — `INDEX ((amount *
2))` — indexes an expression rather than a column, so its `COLUMN_NAME` is NULL
and `EXPRESSION` carries the text. Reading `COLUMN_NAME` into a plain string
therefore fails outright, and because the failure is a scan error rather than an
empty result, one such index on any child table the query touches aborts the
whole query rather than that one row.

The second symptom is quieter and worse. A functional part still *occupies* its
position in the index. An index whose parts are `((amount * 2)), tenant_id,
parent_id` does not begin with `tenant_id`, so it cannot support `FOREIGN KEY
(tenant_id, parent_id)` — but a reader that skips NULL-named parts sees exactly
the column list the constraint wants and reports the index as supporting. The
sharpest form puts the expression *between* the two columns, where skipping it
yields an exact match rather than merely a prefix.

**Handling:** every reader of this column scans `sql.NullString`.
`TableSpec`'s `IndexPart` keeps `Column` empty and puts the text in
`Expression`, so `INDEX(name)` and `INDEX((f(name)))` never compare equal. The
foreign-key fallback records a NULL part as `""`, which preserves both the
position and the density of `SEQ_IN_INDEX`, and cannot be mistaken for a real
column: an identifier is never empty, and the names it is compared against come
from `KEY_COLUMN_USAGE`, which reports only columns.

That last point is a documented guarantee rather than a convenient accident —
the manual states both that `KEY_COLUMN_USAGE` "provides no information about
functional key parts because they are expressions and the table provides
information only about columns", and that "functional key parts are not
permitted in foreign key specifications". A functional part can never *be* a
foreign-key column, so standing in a leading slot it correctly disqualifies the
index.

Pinned at the unit layer by
[`TestForeignKeysFallbackToleratesFunctionalIndexPart`](../pkg/validations/foreign_keys_source_test.go),
its leading-part and interior-part siblings, and
[`TestTableSpecCapturesIndexParts`](../pkg/validations/spec_capture_test.go);
live by the `compat 18` subtest of
[`TestForeignKeyVisibilityAccountsIntegration`](../pkg/validations/validations_integration_test.go),
which runs through an account holding schema-wide `SELECT` and no `PROCESS` —
the shape of a real inspection account, and the only path where this defect was
reachable.

**Reference:** documented. Refman §28.3.x, "The INFORMATION_SCHEMA STATISTICS
Table" (Notes): "For a nonfunctional key part, COLUMN_NAME indicates the column
indexed by the key part and EXPRESSION is NULL. For a functional key part,
COLUMN_NAME column is NULL and EXPRESSION indicates the expression for the key
part." Confirmed word for word in all three manuals —
[8.0](https://dev.mysql.com/doc/refman/8.0/en/information-schema-statistics-table.html) ·
[8.4](https://dev.mysql.com/doc/refman/8.4/en/information-schema-statistics-table.html) ·
[9.7](https://dev.mysql.com/doc/refman/9.7/en/information-schema-statistics-table.html).
The foreign-key exclusions are in §28.3.16, "The INFORMATION_SCHEMA
KEY_COLUMN_USAGE Table"
([8.0](https://dev.mysql.com/doc/refman/8.0/en/information-schema-key-column-usage-table.html) ·
[8.4](https://dev.mysql.com/doc/refman/8.4/en/information-schema-key-column-usage-table.html) ·
[9.7](https://dev.mysql.com/doc/refman/9.7/en/information-schema-key-column-usage-table.html))
and in "Functional Key Parts", carried on the `CREATE INDEX` page
([8.0](https://dev.mysql.com/doc/refman/8.0/en/create-index.html) ·
[8.4](https://dev.mysql.com/doc/refman/8.4/en/create-index.html) ·
[9.7](https://dev.mysql.com/doc/refman/9.7/en/create-index.html)), which also
establishes that the mixed shape above is legal: "An index with multiple key
parts can mix nonfunctional and functional key parts." That the foreign key's
columns must lead the index is §15.1.20.5, quoted in full under entry 16.

The 8.0 manual alone dates the feature — "MySQL 8.0.13 and higher supports
functional key parts" — which is below this library's 8.0.4x support floor, so
no version branch is warranted.

---

## Adding an entry

Every version-specific behavior the code accommodates gets an entry here **and**
a test that pins it in the integration matrix. A quirk handled in code but not
recorded here is a quirk that will be "fixed" by someone who does not know why
the code is shaped that way.

Each entry also gets a **Reference** line. Look the claim up before writing it
down — a version threshold, an error number, or an `information_schema` column
recalled from memory is not a fact, and three of the entries above were wrong
until they were checked against the source. See AGENTS.md section 3, "MySQL
documentation is a lookup, not a recollection", for where to look and how to
cite. When the manual turns out to be silent, say so: "not documented" is a
useful finding, because it tells the next reader that the pinning test is the
only thing standing between us and a silent behavior change.
