# MySQL Compatibility & Quirk Registry

As of v0.7.3, `dbsgomysql` supports MySQL 8.0.40 and newer. Support for the EOL
MySQL 8.0 line is transitional and intended to assist migrations. It is tested
against **8.0, 8.4, and 9.7**. The 26.x development line is watched but not
supported: its CI job is allowed to fail and no code accommodates it until it
stabilizes.

This document is the registry of MySQL behaviors that differ across versions or
that surprise callers of `information_schema`. Each entry states the affected
versions, the observable symptom, and how the library handles it.

Most entries here describe behavior MySQL exhibits *identically* on every
supported version — no behavior the current library exercises differs between
them. That is a measured claim, not an assumption: the complete integration
and E2E matrix, run as described in [testing.md](testing.md), last verified it
on MySQL 8.0.46, 8.4.11, and 9.7.2 (2026-09-04, workflow run 33861085153),
and each entry below names the test that pins it. Where a behavior genuinely
differs between supported versions, its entry's **Affected** line says so.

> **Status legend** — ✅ handled and pinned by a test · ⚠️ bounded and pinned
> limitation (safe behavior exists; the underlying server gap is not solved) ·
> 🔜 declared known limitation — the facts and references are settled here,
> the library deliberately ships no handling code, and the entry names the
> release that delivers it · 👁 operator guidance only, no library code
> involved.
>
> An entry becomes ✅ when its handling lands with a linked pinning test.

## Reference validation

Every entry closes with a **Reference** line naming where the claim comes from.
Four kinds appear, and the difference matters when an entry is challenged:

- **Documented** — the MySQL manual, a release note, or the error reference
  states the behavior. The entry paraphrases the source; the source wins.
- **Documented in part** — the manual states the surrounding rule but not the
  specific consequence recorded here. The gap is named explicitly.
- **Documented, and contradicted by the server** — the manual states the
  behavior and the server does something else on every version tested. The entry
  says so plainly and names the exact releases measured; here the source does
  **not** win, and the **pinning test is authoritative**. Note the wording: every
  version *tested*, never every version, because the measurement is always a
  finite set of releases and the entry beneath is where that set is named. These
  entries carry the highest risk of a well-meaning "fix" — someone reads the
  manual, corrects the code to match, and breaks it everywhere.
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
in AGENTS.md, "Look before asserting", is what settles a claim.

**Validation coverage.** Every claim below was checked on **2026-08-01** against
the corpus described in AGENTS.md, "Look before asserting"; the replication
entries (6 and 20–23) were checked or re-checked on **2026-08-19** during the
`pkg/replication` scoping sweep, which is also what corrected entry 6's `NULL`
claim. Entries carrying a
version threshold, an error number, or an "all supported versions" claim were
queried once per version file and the answers diffed — the method that turned up
the three corrections recorded in `CHANGELOG.md`. Where an entry's **Reference**
line records a narrower search than that, the entry's line is the accurate
record and the gap is deliberate. Two limits worth stating plainly:
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

## 1. Integer and YEAR display widths dropped ✅

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
The same applies to `YEAR`: `year(4)` is normalized to `year`, with no
carve-out, because YEAR has none.

A fresh current server cannot reproduce `int(11)`, so legacy-form
normalization is pinned synthetically by
[`TestNormalizeColumnType`](../pkg/validations/spec_normalize_test.go). The
server fact that keeps the YEAR case unit-pinned is
[`TestYearDisplayWidthIsBareOnFreshServerIntegration`](../pkg/validations/validations_integration_test.go):
a freshly declared `YEAR(4)` reports raw `COLUMN_TYPE` as `year`. The
matrix pins that new integers are bare, `tinyint(1)` survives, both zerofill
widths survive and diff as a `ColumnTypeMismatch`, and decimal precision is
untouched in
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go),
verified by the matrix run named in the introduction. `INT ZEROFILL` declared without a width
reports `int(10) unsigned zerofill` on all three, so preserving the width does
not make the bare declaration compare unequal to an explicit `INT(10)`.

**Reference:** documented. Refman §13.1.6, "Numeric Type Attributes" and
§13.1.1, "Numeric Data Type Syntax", for the deprecation. MySQL 8.0 Release
Notes, 8.0.17 (2019-07-22), Deprecation and Removal Notes (WL #13127) deprecates
the attribute; 8.0.19 (2020-01-13), Deprecation and Removal Notes (WL #13528,
Bug #30556657) is where output stops showing it, and states both exceptions and
the data-dictionary retention rule quoted above. The same 8.0.19 Deprecation
and Removal Notes entry for YEAR (WL #13537) drops the width from `YEAR(4)`
under the identical data-dictionary retention rule, and states that the
exception does not apply to upgrades from 5.7. Refman 8.0, 8.4, and 9.7
§13.2.4, "The YEAR Type", all still call `YEAR(4)` deprecated and describe it
as equivalent to `YEAR`. All three manuals still read
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

**Reference:** no supporting statement found. Searched the 8.0, 8.4, and 9.7
manuals for the `*_PRIVILEGES` `GRANTEE` column and for account-name quoting
rules. All three give the *format* — "the name of the account to which the
privilege is granted, in `'user_name'@'host_name'` format" — with nothing on
what happens when either part contains a quote, so the live pin remains
authoritative. Refman §28.3.10, "The INFORMATION_SCHEMA
COLUMN_PRIVILEGES Table". One structural detail does corroborate the
concatenation: §28.3.27, "The INFORMATION_SCHEMA ROLE_COLUMN_GRANTS Table",
exposes `GRANTEE` and `GRANTEE_HOST` as *separate* columns and so has no
escaping problem at all, which is only possible because the `*_PRIVILEGES`
tables join the two into one string. Not searched: the `GRANT` statement
reference, where an escaping rule could plausibly live.

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
grant rows; `SHOW GRANTS ... USING` can prove the privilege is effective, but
parsing that statement and walking role-specific metadata are outside this
slice. The pure
state table and live direct plus role-granted-to-role cases are pinned by
[`TestGrantResolutionNegativesAndPartialRevokes`](../pkg/validations/grants_test.go)
and
[`TestGranteeAndRolePrivilegesIntegration`](../pkg/validations/validations_integration_test.go).

**Reference:** documented for the alternative, but the limitation itself is a
scope boundary of this library. Refman §15.7.7.22, "SHOW GRANTS Statement",
states that without `USING`, `SHOW GRANTS` lists granted roles rather than their
privileges; adding `USING` also displays the privileges associated with each
named role. MySQL resolves role closure correctly; the entry records that this
package does not follow it there.

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
filtered by privileges on the child table. The result carries the wrapped
primary failure in `PrimaryError` and a package-owned `DowngradeReason` that
distinguishes a primary query error from a read/decode error. The query stage is
not a permission classification: consumers inspect `PrimaryError` with their
driver type when they need that distinction. Closure then emits its own
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
[`TestForeignKeysPrimaryDecodeErrorFallsBack`](../pkg/validations/foreign_keys_source_test.go),
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

## 6. Replication status statements and columns were renamed ✅

**Affected:** the `REPLICA` spellings were added in **8.0.22**, which
simultaneously deprecated the `SLAVE` ones; the `SLAVE` statements were
**removed in 8.4**. The rename covers each statement *and its output*:
`SHOW REPLICA STATUS` returns the new column names (`Replica_IO_Running`,
`Replica_SQL_Running`, `Seconds_Behind_Source`, …) on every release that
accepts the statement. Because the library floor is 8.0.40, the `REPLICA`
forms exist on every supported version.

**Symptom:** a hard-coded legacy statement or column name fails from 8.4 on —
`SHOW SLAVE STATUS` is a syntax error there (entry 20 pins the error number
for the same removal class). Additionally, 8.4 has been observed to return the
seconds-behind column with a different Go driver type, which breaks naive type
switches that worked on 8.0.

**Handling:** always issue the `REPLICA` spelling and read only the new column
names. At the 8.0.40 floor no `SLAVE` fallback and no dual column spellings
are needed — the try-then-fallback an earlier revision of this entry
prescribed is required only below 8.0.22, which the library does not support.
Convert the seconds-behind value defensively rather than type-switching on a
single expected type, and treat `NULL` as meaningful: the manual defines
`Seconds_Behind_Source` as `NULL` when the applier thread is not running, or
when the applier has consumed the relay log and the receiver is not running,
and `0` when the receiver runs with an exhausted relay log. Delivered by
`pkg/replication` in **v1.1.0**: the fact reports the estimate as an
`sql.NullInt64` that is invalid if and only if the server sent SQL `NULL`,
never a fabricated zero. Pinned by
[`TestCompat6SecondsBehindIntegration`](../pkg/replication/compat_integration_test.go),
which reads a running replica (a reported, non-negative estimate) and then
the same channel once the applier is stopped (`NULL`) — the stopped snapshot
is taken only after one single observation showed the applier stopped and the
estimate `NULL` together, because `SHOW REPLICA STATUS` is nonblocking and a
snapshot taken during `STOP REPLICA` may be stale.

**Reference:** documented. MySQL 8.0 Release Notes, 8.0.22 (2020-10-19),
Deprecation and Removal Notes (WL #14171), deprecates `START SLAVE`, `STOP
SLAVE`, `SHOW SLAVE STATUS`, `SHOW SLAVE HOSTS`, and `RESET SLAVE`, names the
replacement for each, and states that "only the terminology used for each
statement and its output has changed" — the output columns follow the
statement form. Refman §1.4, "What Is New in MySQL 8.4 since MySQL 8.0"
(Features Removed in MySQL 8.4), confirms removal and lists the `MASTER`
statements removed alongside them; the source-side statement family is
entry 20. Refman "SHOW REPLICA STATUS Statement" (8.0 and 8.4) states the
identical `NULL` rule for `Seconds_Behind_Source` in both versions, each
contrasting "older versions of MySQL" — that is, pre-8.0. An earlier revision
of this entry read that contrast as an 8.4-specific narrowing; the 8.0/8.4
manual diff run for the v1.1.0 sweep (2026-08-19) corrected it. The
driver-type observation is not documented and is pinned by
[`TestCompat6SecondsBehindIntegration`](../pkg/replication/compat_integration_test.go)
across the 8.0, 8.4, and 9.7 matrix.

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

**Reference:** documented. Refman §8.4.1.1, "Native Pluggable Authentication",
under §8.4.1, "Authentication Plugins", in §8.4, "Security Components and
Plugins", states the sequence exactly: "The
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

**Symptom:** The manual states that supplementary characters (above `U+FFFF`)
are not permitted in identifiers, quoted or unquoted; the server nevertheless
accepts the statement and stores a replacement. For example, an object
requested as `supp_𐀀` is stored and reported by `information_schema` as
`supp_?`. Reusing the original SQL text can appear to work because the same
replacement happens again, but the configured name does not round-trip and can
collide with a literal question mark. Looking the original name up afterwards
does not simply fail to match: comparing an `information_schema` schema or
table name column against the original supplementary-character parameter
**raises error 3988**
(`ER_IMPOSSIBLE_STRING_CONVERSION`), because MySQL cannot convert the `utf8mb4`
parameter into the metadata column's `utf8mb3` collation. Code must not read
that error as "the object does not exist". The message template is `Conversion
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

The same error reaches the fixed schema parameters used by five facts, both
fixed parameters used to resolve a `TableSpec`, and the standard foreign-key
source. Dynamic requested-name predicates reach seven further name columns
through two guarded helpers, `requestedObjects` and `narrowNames`. The three
marked measured were probed on 8.0, 8.4, and 9.7 with identical results; the
rest share the guard by construction:

| Column | Read by | Pinned by |
|---|---|---|
| `TABLES.TABLE_NAME` | `Tables`, `PrimaryKeys` | by construction |
| `COLUMNS.TABLE_NAME` | `Columns`, `InvisibleColumns` | `TestPredicateGuardReportsAbsenceIntegration`, `TestPredicateFallbackMatchesNarrowedResultIntegration` — measured |
| `TRIGGERS.EVENT_OBJECT_TABLE` | `Triggers` | by construction |
| `KEY_COLUMN_USAGE.TABLE_NAME` | `ForeignKeys`, standard source, outgoing/within | `TestPredicateGuardReportsAbsenceIntegration` — measured |
| `KEY_COLUMN_USAGE.REFERENCED_TABLE_NAME` | `ForeignKeys`, standard source, incoming | by construction |
| `INNODB_FOREIGN.FOR_NAME` | `ForeignKeys`, InnoDB source, outgoing/within | `TestPredicateGuardReportsAbsenceIntegration` — measured |
| `INNODB_FOREIGN.REF_NAME` | `ForeignKeys`, InnoDB source, incoming | by construction |

The same helper guards the grantee lists `Grants` binds to `GRANTEE`; those are
account names rather than object names, and an unrepresentable one falls back
to reading every visible row.

`pkg/validations` uses two mechanisms so these requests report absence rather
than failing:

- **Dynamic table-name lists** in `Tables`, `PrimaryKeys`, `Columns`,
  `InvisibleColumns`, `Triggers`, and both `ForeignKeys` sources omit an
  unrepresentable narrowing predicate and select exact returned spellings in
  Go. These predicates only narrow — they never decide — so dropping one widens
  the read but not the answer, per entry 2. Pinned by
  [`TestPredicateGuardReportsAbsenceIntegration`](../pkg/validations/predicate_integration_test.go)
  and
  [`TestPredicateFallbackMatchesNarrowedResultIntegration`](../pkg/validations/predicate_integration_test.go),
  which cover both foreign-key sources separately, since a connection holding
  `PROCESS` never reaches the standard one.
- **Fixed identities** short-circuit before issuing the affected statement.
  `Columns`, `InvisibleColumns`, `Tables`, `PrimaryKeys`, and `Triggers` return
  their documented empty result for an unrepresentable Inspector schema;
  `TableSpec` returns `ErrTableNotFound` for an unrepresentable schema or table;
  and the standard `ForeignKeys` fallback returns empty with
  `VisibilityUnconfirmed`. The `PROCESS`-gated InnoDB source remains queried,
  because `VisibilityComplete` is evidence that this source succeeded, not an
  inference from a skipped read. Pinned by
  [`TestFactsReportAbsenceForUnrepresentableSchemaIntegration`](../pkg/validations/fixed_parameter_representability_integration_test.go),
  [`TestTableSpecReportsTableNotFoundForUnrepresentableRefIntegration`](../pkg/validations/fixed_parameter_representability_integration_test.go),
  and
  [`TestForeignKeysStandardFallbackReportsUnconfirmedForUnrepresentableSchemaIntegration`](../pkg/validations/fixed_parameter_representability_integration_test.go).

Because a fixed-identity short-circuit issues no query, it does not surface an
already-cancelled context or an error from a caller-supplied `Querier` for that
request. Argument validation and the existing empty-input returns still take
precedence.

A name that is not valid UTF-8 is rejected by the same private representability
predicate, but for a different reason: the server already returns no rows for
it rather than failing. Dynamic predicate omission and fixed-identity
short-circuiting are therefore defensive for invalid UTF-8 rather than a
repair. That half is pinned at unit level only — an integration test would pass
without either guard.

**Reference:** documented, and contradicted by the server for acceptance;
documented in part for the rest. Refman §11.2, "Schema Object Names", states in
all three manuals that supplementary characters are not permitted in quoted or
unquoted identifiers. The 8.0, 8.4, and 9.7 Error Message
References, Chapter 2, "Server Error Message Reference", all give error 3988 as
symbol `ER_IMPOSSIBLE_STRING_CONVERSION`, SQLSTATE `HY000`, with the message
template quoted above. The 8.0 reference adds a threshold the newer ones drop:
"`ER_IMPOSSIBLE_STRING_CONVERSION` was added in 8.0.22." That is below the
effective 8.0.40 floor and so does not affect the supported range, but it does
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
with no drift between the two. That the final position is what matters is
documented: §11.2, "Schema Object Names", states in all three manuals that
database, table, and column names cannot end with space characters. Which six
characters count as space, and that NBSP (`U+00A0`) and ideographic space
(`U+3000`) do not, is not stated; that set comes from the pinning test.

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

**Handling:** `Inspector.Triggers` still issues `ORDER BY ACTION_TIMING,
TRIGGER_NAME`, which fixes the row order the scan sees, but the order the fact
returns is made in Go: `sortTriggers` orders each table's triggers by
`triggerTimingOrder` and then by name compared as bytes, and
`CheckTriggersPresent` uses the same comparator, so the two agree by
construction whether or not the server's sort does. The ENUM order is therefore
a pinned server observation rather than something the result depends on.
[`TestTriggerTimingEnumOrderIntegration`](../pkg/validations/validations_integration_test.go)
pins the observation — the column is still an `ENUM` with `BEFORE` declared
first, and the server's own `ORDER BY` returns BEFORE-timed rows first — so a
server that exposed the column as text is noticed even though the fact's order
would not move. The name half has the same shape for the same reason:
`TRIGGER_NAME` collates case-insensitively (entry 2), and
[`TestTriggerNameOrderIsByteOrderIntegration`](../pkg/validations/validations_integration_test.go)
pins the server's case-insensitive order beside the fact's byte order.
[`TestTriggersIntegration`](../pkg/validations/validations_integration_test.go)
additionally asserts the `Timing` values themselves rather than only the
resulting name order.

**Reference:** no supporting statement found — and that is the point of the
entry. Refman "The INFORMATION_SCHEMA TRIGGERS Table" — §28.3.45 in the 8.0
manual, §28.3.44 in 8.4, §28.3.50 in 9.7 — documents only the permitted
*values* in all three: `ACTION_TIMING` is "whether the trigger activates before
or after the triggering event. The value is `BEFORE` or `AFTER`", and
`EVENT_MANIPULATION` is `INSERT`, `DELETE`, or `UPDATE`. No version says the
columns are `ENUM`, so the ordering the query depends on has no documented
basis in any of them. Nothing
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

Under partial revokes, a privilege with no grant row at any scope is still
reported absent only on a pinned, role-free session that holds a direct
schema-level SELECT on the mysql schema; a global SELECT does not count while
partial revokes are enabled. Otherwise it is GrantUnconfirmed. The broad half
and its narrow counter-pin are both exercised by
[`TestPartialRevokesPrivilegeResolutionIntegration`](../pkg/validations/validations_integration_test.go).
The pure state table is pinned by
[`TestPartialRevokesDegradeEveryAnswerBackedByGlobalRow`](../pkg/validations/grants_test.go)
and
[`TestPartialRevokesDoNotHideProvableAbsence`](../pkg/validations/grants_test.go).

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
and [`TestLikePatternMatches`](../pkg/validations/grants_test.go). While
`partial_revokes` is enabled the fact does not consult stored keys as patterns
at all, because the server does not either; that half is pinned by
[`TestPartialRevokesDoNotHideProvableAbsence`](../pkg/validations/grants_test.go).

**Reference:** documented. Refman §8.2.12, "Privilege Restriction Using Partial
Revokes", states the interaction this entry turns on: "enabling
`partial_revokes` causes MySQL to interpret occurrences of unescaped `_` and `%`
SQL wildcard characters in schema names as literal characters, just as if they
had been escaped as `\_` and `\%`", and advises avoiding unescaped wildcards for
that reason. The same wording appears in §15.7.1.6, "GRANT Statement". The
8.0.16 release note states the rule in different words: it says the server
treats the characters as literal where the manual says it interprets
occurrences of them. That a stored pattern therefore defeats an exact-name
lookup against `SCHEMA_PRIVILEGES` is the consequence recorded here.

All three manuals carry the deprecation, and the 8.0 one dates it: the
`partial_revokes` description says use of `_` and `%` as wildcard characters
in grants "is deprecated as of MySQL 8.0.35" and "you should expect support for
them to be removed in a future version of MySQL". 8.0.35 is below the 8.0.40
floor, so the deprecation is in force on every supported version; it is not a
9.x-forward signal. The hazard is on its way out, but is not gone in any
supported version, and a schema captured from an older server can still hold a
pattern. Keep the downgrade.

---

## 13. PRIMARY KEY constraint names are discarded ✅

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

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

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

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

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

**Symptom:** `information_schema.CHECK_CONSTRAINTS.CHECK_CLAUSE` is not the
source text. MySQL backticks identifiers and rewrites keyword case; for
example, `CHECK (gpa BETWEEN 0.00 AND 4.00)` becomes
`` (`gpa` between 0.00 and 4.00) ``.

**Handling:** `ConstraintSpec.CheckClause` preserves the normalized server
form and `DiffSpecs` compares it verbatim. The exact rewrites are pinned by
[`TestTableSpecCompatPinsIntegration`](../pkg/validations/validations_integration_test.go).

**Reference:** documented in part. Refman "SHOW CREATE TABLE Statement"
(§15.7.7.11) carries the worked example showing the rewrite: `CHECK (i1 <> 0)`
comes back as ``CONSTRAINT `t1_chk_1` CHECK ((`i1` <> 0))`` — identifiers
backticked and the expression re-parenthesized. Refman "CHECK Constraints"
(§15.1.20.6 in the 8.0 and 8.4 manuals, §15.1.25.6 in 9.7) documents the
generated-name pattern (`_chk_` plus an ordinal) and that constraint names are
"case-sensitive, but not accent-sensitive". The rewrite rules themselves are
not specified anywhere, so they are pinned rather than cited. The 8.0 manual
also marks the floor: "Prior to MySQL 8.0.16, `CREATE
TABLE` permits only the following limited version of table `CHECK` constraint
syntax, which is parsed and ignored." Below 8.0.16 there is no clause to
normalize, and no constraint either.

## 16. Foreign keys create a supporting index named after the constraint 👁

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

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

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

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
ENFORCED` constraint silently sits out. Refman §15.7.7.11, "SHOW CREATE TABLE
Statement", carries the example showing how the flag surfaces behind a version
gate: ``CONSTRAINT `t1_chk_3` CHECK ((`i2` <> 0)) /*!80016 NOT ENFORCED */``.
Reading the clause text alone therefore misses it in DDL exactly as it does in
metadata.

## 18. A functional index part reports `COLUMN_NAME` as NULL ✅

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

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
functional key parts" — which is below this library's 8.0.40 support floor, so
no version branch is warranted.

## 19. `INNODB_FOREIGN_COLS.POS` counts from 1, not 0 ✅

**Affected:** all supported versions; verified by the matrix run named in the
introduction.

**Symptom:** the manual says this column is 0-based. The server returns 1-based
values. For a two-column foreign key, `POS` is `1` and `2` on every version
measured; the documented reading predicts `0` and `1`.

**Handling:** `scanInnoDBForeignKeys` requires positions `1, 2, …` and rejects a
group that does not supply them. That is correct and must stay.

The reason this needs a registry entry rather than a code comment is the shape
of the failure if someone "corrects" it. `ForeignKeys` routes **any**
primary-source error to the standard `information_schema` fallback, so a
0-based expectation would not surface as a broken query. It would surface as
nothing at all: every call that matched at least one foreign key would quietly
stop using the authoritative InnoDB source and start returning
`VisibilityUnconfirmed`, which is precisely the "metadata may be incomplete"
signal callers are meant to act on. Tests asserting facts rather than visibility
would keep passing.

The "at least one" is not a quibble — it is what makes the regression hard to
notice. A selector matching no constraints builds no position group, so nothing
compares positions, the primary source succeeds trivially, and the result is
still `VisibilityComplete`. The break therefore appears only where foreign keys
actually exist, which is exactly where the answer is being relied on.

Worth stating alongside: `KEY_COLUMN_USAGE.ORDINAL_POSITION`, the column the
fallback reads for the same purpose, genuinely **is** 1-based, and is documented
as such — "Column positions are numbered beginning with 1." So both sources
using `1` is a fact about the server twice over, not one assumption copied into
two scanners.

Pinned directly, at the fact rather than downstream, by
[`assertInnoDBForeignColsPositionBase`](../pkg/validations/validations_integration_test.go)
via `TestForeignKeysIntegration`. It reads `POS` for a composite constraint, so
one assertion covers the base, the increment, and the ordering.

**Reproducing it.** This entry contradicts the manual, so it should be
checkable without trusting this repository or running its suite. The first two
tables below are the manual's own Example 17.3, copied unchanged; the third and
fourth add a composite key so the increment shows too. Needs `PROCESS`.

```sql
CREATE DATABASE pos_probe;
USE pos_probe;

CREATE TABLE parent (id INT NOT NULL, PRIMARY KEY (id)) ENGINE=InnoDB;
CREATE TABLE child (id INT, parent_id INT,
  INDEX par_ind (parent_id),
  CONSTRAINT fk1 FOREIGN KEY (parent_id) REFERENCES parent(id)
  ON DELETE CASCADE) ENGINE=InnoDB;

CREATE TABLE p3 (a INT NOT NULL, b INT NOT NULL, c INT NOT NULL,
  PRIMARY KEY (a, b, c)) ENGINE=InnoDB;
CREATE TABLE c3 (id INT NOT NULL PRIMARY KEY, a INT, b INT, c INT,
  CONSTRAINT fk3 FOREIGN KEY (a, b, c) REFERENCES p3(a, b, c)) ENGINE=InnoDB;

-- Matched exactly, not with LIKE: '_' is a single-character wildcard, so
-- 'pos_probe/%' would also match a constraint in a schema named posXprobe
-- and could add rows to the output below.
SELECT ID, FOR_COL_NAME, POS
FROM information_schema.INNODB_FOREIGN_COLS
WHERE ID IN ('pos_probe/fk1', 'pos_probe/fk3')
ORDER BY ID, POS;

DROP DATABASE pos_probe;
```

Byte-identical output on the matrix run named in the introduction:

```
+---------------+--------------+-----+
| ID            | FOR_COL_NAME | POS |
+---------------+--------------+-----+
| pos_probe/fk1 | parent_id    |   1 |
| pos_probe/fk3 | a            |   1 |
| pos_probe/fk3 | b            |   2 |
| pos_probe/fk3 | c            |   3 |
+---------------+--------------+-----+
```

The first row is the decisive one: it is the manual's example, and the manual
prints `POS: 0` for it. `fk3` shows the count continuing `1, 2, 3` where the
documented reading predicts `0, 1, 2`. Swapping the query to
`KEY_COLUMN_USAGE.ORDINAL_POSITION` for `fk3` returns `1, 2, 3` as well — which
that table's documentation correctly predicts, so the two sources agree with
each other and only one manual page is out of step.

**Reference:** documented, and contradicted by the server. All three manuals
state the 0-based rule **twice**, and all three servers disagree with it.

The column definition, §28.4.13, "The INFORMATION_SCHEMA INNODB_FOREIGN_COLS
Table": "**POS** The ordinal position of this key field within the foreign key
index, starting from 0." —
[8.0](https://dev.mysql.com/doc/refman/8.0/en/information-schema-innodb-foreign-cols-table.html) ·
[8.4](https://dev.mysql.com/doc/refman/8.4/en/information-schema-innodb-foreign-cols-table.html) ·
[9.7](https://dev.mysql.com/doc/refman/9.7/en/information-schema-innodb-foreign-cols-table.html).
The same page carries the `PROCESS` requirement that makes the fallback
necessary at all: "You must have the PROCESS privilege to query this table."

The worked example repeats it, §17.15.3, "InnoDB INFORMATION_SCHEMA Schema
Object Tables", Example 17.3 — a single-column key printing `POS: 0`, closing
"The POS value is the ordinal position of the key field within the foreign key
index, starting at zero." —
[8.0](https://dev.mysql.com/doc/refman/8.0/en/innodb-information-schema-system-tables.html) ·
[8.4](https://dev.mysql.com/doc/refman/8.4/en/innodb-information-schema-system-tables.html) ·
[9.7](https://dev.mysql.com/doc/refman/9.7/en/innodb-information-schema-system-tables.html).
That page's slug still carries its MySQL 5.7 title, "System Tables", which is
why it cannot be derived from the current section title — the case that produced
the rule above about opening a URL before citing it.

The 1-based claim for `ORDINAL_POSITION` is §28.3.16, "The INFORMATION_SCHEMA
KEY_COLUMN_USAGE Table"
([8.0](https://dev.mysql.com/doc/refman/8.0/en/information-schema-key-column-usage-table.html) ·
[8.4](https://dev.mysql.com/doc/refman/8.4/en/information-schema-key-column-usage-table.html) ·
[9.7](https://dev.mysql.com/doc/refman/9.7/en/information-schema-key-column-usage-table.html)),
and there the documentation and the server agree.

Why the documentation is wrong is not established here. A plausible account is
that the text predates the 8.0 rename from `INNODB_SYS_FOREIGN_COLS`, but the
corpus this repository validates against covers 8.0, 8.4, and 9.7 only, so
nothing available supports it and it is left out rather than guessed.

## 20. Source status statements diverge: `SHOW MASTER STATUS` vs `SHOW BINARY LOG STATUS` ✅

**Affected:** genuinely differs between supported versions. `SHOW BINARY LOG
STATUS` (and `RESET BINARY LOGS AND GTIDS`) were **added in 8.2.0**, which
deprecated the `MASTER` spellings; **8.4 removed** the `MASTER` spellings. The
additions were never backported to the 8.0.x line, so on 8.0 only `SHOW
MASTER STATUS` exists. Statements that already had binary-log spellings
before the wave (`SHOW BINARY LOGS`, `PURGE BINARY LOGS`) exist on all
supported versions and are unaffected.

**Symptom:** each end of the range rejects the other end's statement with
`ER_PARSE_ERROR` (1064, SQLSTATE 42000): `SHOW BINARY LOG STATUS` on 8.0,
`SHOW MASTER STATUS` on 8.4 and 9.7. Everything else about the pair is
identical — output columns (`File`, `Position`, `Binlog_Do_DB`,
`Binlog_Ignore_DB`, `Executed_Gtid_Set`), the `REPLICATION CLIENT` privilege
(or the deprecated `SUPER`), and the guarantee that `Executed_Gtid_Set`
equals the server's `gtid_executed`.

**Handling:** strategy principle 2 — try `SHOW BINARY LOG STATUS`, fall back
to `SHOW MASTER STATUS`. Error 1064 is the documented cause, not the
detection mechanism: the stdlib-only library does not inspect driver error
numbers, so the fallback triggers on any error from the first form. When
both forms fail, both errors are preserved in the returned error, each named
by the statement that produced it — either one can be the decisive cause,
because on 8.0 the first failure is the expected syntax error and the second
is the operational one, while on 8.4+ the roles reverse. This is the only
version-divergent statement pair `pkg/replication` needs — every other
statement it issues has one spelling valid across the whole range (entry 6) —
and it is the package's entire accommodation of the EOL 8.0 line: the
fallback is bound to the transitional 8.0 support window and is deleted with
it. Delivered by `pkg/replication` in **v1.1.0** and pinned by
[`TestCompat20BinaryLogStatusIntegration`](../pkg/replication/compat_integration_test.go).
Success alone is the proof of which statement ran, because on each version the
*other* statement cannot succeed — so the test also asserts that rejection
directly, keeping the inference valid if a future server ever accepts both.

**Reference:** documented. MySQL 8.4 Release Notes, Changes in MySQL 8.2.0
(2023-10-25), SQL Syntax Notes (WL #14190), deprecates the `MASTER` set and
names each replacement. Refman 8.4 §15.7.7.1, "SHOW BINARY LOG STATUS
Statement", and §1.4, "What Is New in MySQL 8.4 since MySQL 8.0" (Features
Removed: attempting a removed statement "now produces a syntax error").
Refman 8.0, "SHOW MASTER STATUS Statement", documents the 8.0 form; the 8.0
manual's "RESET MASTER Statement" note — "replaced in later versions of
MySQL … See … the MySQL 8.4 Manual" — is the 8.0 line documenting that the
replacements are not available in 8.0. MySQL 8.0 Error Message Reference:
error 1064, symbol `ER_PARSE_ERROR`, SQLSTATE 42000.

## 21. GTID sets may contain tagged GTIDs from 8.4 ✅

**Affected:** 8.4 and 9.7. MySQL 8.3.0 introduced tagged GTIDs — a three-part
`UUID:TAG:NUMBER` format alongside the original two-part `UUID:NUMBER`, which
continues unchanged — so every supported 8.4 and 9.x release carries them and
no 8.0 release does.

**Symptom:** any GTID-set parser written to the two-part shape mis-reads a
set containing tags. Every GTID-set surface the library touches can carry
them on 8.4+: `gtid_executed`, `gtid_purged`, `Executed_Gtid_Set` and
`Retrieved_Gtid_Set` in `SHOW REPLICA STATUS`, and `Executed_Gtid_Set` in
`SHOW BINARY LOG STATUS`. The 8.4 manual is also internally inconsistent
about the tag's maximum length — §1.4 says "up to 8 characters" while the
`gtid_next` description's regular expression permits up to 32 — so a parser
would have to pick a side the documentation does not settle.

**Handling:** `pkg/replication` returns every GTID set as an opaque string
and never parses one; interpreting or comparing sets is the consumer's
affair. The `TRANSACTION_GTID_TAG` privilege was introduced with tagged GTIDs
in 8.3.0, so it is present on the first supported 8.4 release; on every
supported 8.4 and 9.x release, setting `gtid_purged` requires it, and the 8.4
and 9.7 manuals say so identically. The library only reads, so no privilege
beyond `REPLICATION CLIENT` is involved. Delivered by `pkg/replication` in
**v1.1.0** and pinned by
[`TestCompat21TaggedGTIDIntegration`](../pkg/replication/compat_integration_test.go),
which on 8.4 and 9.7 commits one transaction under a tag generated fresh for
that run and then finds that tag intact in the source's `gtid_executed` and,
after the replica catches up, in its `Retrieved_Gtid_Set` — by substring, never
by parsing. The tag is fresh per run deliberately: `gtid_executed` accumulates
for the container's lifetime, so a fixed tag would let the assertion pass on a
run that created nothing.

**Reference:** documented. MySQL 8.4 Release Notes, Changes in MySQL 8.3.0
(2024-01-16), Replication with GTIDs (WL #15294), introduces tagged GTIDs and
the `TRANSACTION_GTID_TAG` privilege. Refman 8.4 §1.4, "What Is New in MySQL
8.4 since MySQL 8.0" (Features Added: the `UUID:TAG:NUMBER` format, `gtid_next =
AUTOMATIC:TAG`, the privilege; "up to 8 characters"). Refman 8.4 §19.1.6.5,
"Global Transaction ID System Variables", and its "Dynamic Privilege
Descriptions" entry state that setting `gtid_purged` requires the privilege;
the 9.7 manual states the same. The `gtid_next` description gives the tag
regular expression `[a-zA-Z_][a-zA-Z0-9_]{0,31}`.

## 22. `SHOW REPLICAS`: three documented behaviors the server does not have ✅

**Affected:** all supported versions, identically. The statement exists from
8.0.22 (`SHOW SLAVE HOSTS` before it; removed in 8.4).

**Symptom:** the manual's `SHOW REPLICAS` page gets three things wrong about
its own output, and a client that trusts them reads the wrong topology — or
no topology at all.

1. **Column spelling.** The manual's example output prints `Server_id` and
   `Source_id`. Every supported server sends `Server_Id` and `Source_Id`,
   with a capital `I`. A client that maps result columns by name — which is
   the only way to survive columns being added — finds no such column and
   reports a missing column instead of a replica.
2. **Registration is not opt-out.** The `report_host` description says
   "Leave the value unset if you do not want the replica to register itself
   with the source." A replica started without `--report-host` registers all
   the same and is listed, with an empty `Host`. There is no way to keep a
   connected replica out of this list, so an empty `Host` is a row to read,
   never a row to discard.
3. **Zero does not mean "unset".** The statement's `Port` bullet says "A
   zero in this column means that the replica port (`--report-port`) was not
   set." A replica started without `--report-port` reports 3306 — its actual
   listening port. Here **the manual contradicts itself**, and the server
   follows the other page: the `report_port` variable description says "The
   default value for this option is the port number actually used by the
   replica. This is also the default value displayed by SHOW REPLICAS."

What the manual gets right, and what the fact's contract rests on instead:
rows cover "servers that are or have been connected as replicas", so a
listed replica is not necessarily connected now; a replica that has never
connected leaves no row at all; `Host` is the replica's self-reported
`report_host`, deliberately, because the socket peer address "may not be
valid for connecting to the replica" (NAT); and the privilege differs from
the status statements — `REPLICATION SLAVE`, not `REPLICATION CLIENT`.

**Reproduction.** One source and two replicas, both connected with
`SOURCE_AUTO_POSITION=1`. Server-id 2 was started with
`--report-host=repl<v>-replica`; server-id 3 with neither `--report-host`
nor `--report-port`:

```sql
SHOW REPLICAS;
```

```
+-----------+----------------+------+-----------+--------------------------------------+
| Server_Id | Host           | Port | Source_Id | Replica_UUID                         |
+-----------+----------------+------+-----------+--------------------------------------+
|         3 |                | 3306 |         1 | 52706cf4-9b6b-11f1-aeed-4e420465c000 |
|         2 | repl84-replica | 3306 |         1 | 5271e680-9b6b-11f1-9105-763aa1b53002 |
+-----------+----------------+------+-----------+--------------------------------------+
```

Identical in shape on the matrix run named in the introduction: the same header
spelling on all three, and the replica that reports nothing listed on all three
with an empty `Host` and `Port` 3306. Only the hostname and the
UUIDs differ between them.

**Handling:** the fact promises the spellings the server actually sends,
returns every row it receives — empty `Host` included — and documents `Port`
as the reported port, where zero means only that the server returned zero.
The list is still never proof of absence, now on the two grounds that
survive: a row may be stale, and a replica that never connected leaves none.
Nothing in the library treats the list as exhaustive (the same conservatism
as entry 5). No Performance Schema equivalent exists for asynchronous
replication — the `replication_*` tables are all replica-side — and the
manual's only source-side alternative,
`performance_schema.threads WHERE PROCESSLIST_COMMAND LIKE 'Binlog Dump%'`,
answers a different question: connections currently streaming binlog, with
no replica server-id or UUID identity. That query is recorded here as the
documented complement and deliberately not used. Delivered by
`pkg/replication` in **v1.1.0** and pinned by
[`TestCompat22RegisteredReplicasIntegration`](../pkg/replication/compat_integration_test.go),
which requires the source to list **both** replicas: `ServerID` 2 with its
reported hostname, and `ServerID` 3 — the one that reports nothing — with an
empty `Host` and `Port` 3306. An implementation that treated an empty `Host`
as a row to discard fails that second assertion on every version, which is
what makes this the pin rather than a restatement of the manual.

**Reference:** documented, and contradicted by the server. Refman
"SHOW REPLICAS Statement" (8.0, 8.4, and 9.7, wording identical) carries
both wrong claims — the example output spelling `Server_id`/`Source_id`, and
"A zero in this column means that the replica port (--report-port) was not
set" — alongside the correct "servers that are or have been connected as
replicas" and `REPLICATION SLAVE`. Refman, `report_host` system-variable
description: "Leave the value unset if you do not want the replica to
register itself with the source", plus the NAT note — the first half is
contradicted live, the NAT rationale is not. Refman, `report_port`
system-variable description: "The default value for this option is the port
number actually used by the replica. This is also the default value
displayed by SHOW REPLICAS" — this page is the one the server obeys, and the
statement page contradicts it. Refman "Monitoring Replication Main Threads"
(the `threads` query; `Binlog Dump%` covers both `Binlog Dump` and
`Binlog Dump GTID`). Refman 8.4 "What Is New in MySQL 8.4 since MySQL 8.0"
(SHOW SLAVE HOSTS removed; use SHOW REPLICAS).

## 23. Replication system variables renamed in 8.0.26 and pruned in 9.x ✅

**Affected:** the `replica_*` system-variable spellings the v1.1.0 facts
read (`log_replica_updates`, `replica_parallel_workers`, …) date from the
**8.0.26** rename wave, so they exist on every supported version; the
`slave_*` spellings were **removed in 8.4**. Two 9.x prunings sit adjacent
to the facts: `replica_parallel_workers` can no longer be 0 from **9.3.0**
(minimum 1 — the single-threaded applier configuration exists only on 8.x
within the supported set), and `replica_parallel_type` was **removed in
9.5.0** (deprecated since 8.0.29).

**Symptom:** reading a removed spelling — `SELECT @@GLOBAL.slave_parallel_workers`
on 8.4+, or `@@GLOBAL.replica_parallel_type` on 9.5+ — raises
`ER_UNKNOWN_SYSTEM_VARIABLE` (1193, SQLSTATE HY000). A consumer that treats
`replica_parallel_workers = 0` as a reachable state carries a dead branch
from 9.3 on.

**Handling:** read only spellings valid across the whole range: the
`replica_*` forms, never the `slave_*` forms, and never
`replica_parallel_type`. The other configuration facts carry no drift:
`log_replica_updates` is enabled by default, and the `read_only` /
`super_read_only` descriptions are unchanged from 8.0 to 9.7, including that
the value on a replica is independent of the source's. Delivered by
`pkg/replication` in **v1.1.0** and pinned by
[`TestCompat23ReplicationConfigIntegration`](../pkg/replication/compat_integration_test.go),
which reads the same single statement on the source and both replicas of every
version's trio — proving one spelling suffices across the range — and asserts
the source is writable while both replicas are `read_only`. On 9.7 it also
requires `replica_parallel_workers >= 1`, the post-9.3.0 minimum (observed 4).

**Reference:** documented. MySQL 8.0 Release Notes, 8.0.26 (2021-07-20),
"Incompatible Change": "new aliases or replacement names are provided for
most remaining identifiers" containing master/slave/mts. Refman 8.4 §1.4 and
§1.5 (options and variables removed in 8.4). MySQL 9.7 Release Notes,
Changes in MySQL 9.3.0 (2025-04-15), Deprecation and Removal Notes (WL
#13957: "can no longer be set to 0; the minimum permitted value is now 1");
Changes in MySQL 9.5.0 (2025-10-21), Deprecation and Removal Notes (WL
#13955: `replica_parallel_type` removed). MySQL 8.0 Error Message Reference:
error 1193, symbol `ER_UNKNOWN_SYSTEM_VARIABLE`, SQLSTATE HY000. Refman 8.4
§19.1.2.2, "Setting the Replica Configuration" (`log_replica_updates`
"enabled by default"); Refman 8.0 and 9.7 server system variable
descriptions for `read_only` and `super_read_only`.

## 24. Constraint types have separate namespaces; foreign-key names compare case-insensitively ✅

**Affected:** all supported versions.

**Symptom:** `information_schema.TABLE_CONSTRAINTS` represents primary keys,
unique keys, foreign keys, and CHECK constraints through one name column, but
MySQL permits different constraint types to share a name. In addition,
`CHECK_CONSTRAINTS` has no table-name column. Joining either table on a name
without distinguishing its type can therefore attach another constraint's row:
a CHECK can acquire another table's clause, and a foreign key can acquire a
unique key's nonreferencing key parts.

Foreign-key names add a second server rule. The manual's §11.2.3,
"Identifier Case Sensitivity", lists constraint names among identifiers whose
uppercase forms do not make them duplicates. On MySQL 8.0.46, 8.4.9, and
9.7.1, however, `Fk1` and `fk1` cannot coexist: one table rejects the pair with
error 1061, and two tables in one schema reject it with error 1826. This entry
does not claim the same behavior for CHECK names; that case was not tested.

**Handling:** CHECK capture restricts `TABLE_CONSTRAINTS` to `CHECK` rows, and
foreign-key capture discards `KEY_COLUMN_USAGE` rows whose referenced table is
NULL. Captured constraints sort on `(name, kind)`, so legal same-named
constraints have a deterministic order. Foreign-key row grouping relies on
the server-enforced, case-insensitive schema-wide name uniqueness pinned by
[`TestForeignKeyNamesAreCaseInsensitiveIntegration`](../pkg/validations/validations_integration_test.go).
The cross-type joins and order are pinned by
[`TestTableSpecConstraintNameCollisionsIntegration`](../pkg/validations/validations_integration_test.go),
and unit tests pin both SQL predicates and the comparator independently.

**Reference:** documented except for the foreign-key case comparison, where
the server contradicts §11.2.3. The supported manuals agree as follows:

| Claim | 8.0 | 8.4 | 9.7 |
|---|---|---|---|
| Each constraint type has its own namespace per schema | §15.1.20, "Indexes, Foreign Keys, and CHECK Constraints" | §15.1.20, same wording | §15.1.25, same wording |
| CHECK names are case-sensitive but not accent-sensitive | §15.1.20.6, "CHECK Constraints" | §15.1.20.6, same wording | §15.1.25.6, same wording |
| A foreign-key `CONSTRAINT` symbol must be unique in the database | §15.1.20.5, "FOREIGN KEY Constraints"; includes pre-8.0.16 and NDB history | §15.1.20.5; the old history is absent | §15.1.25.5; same current rule as 8.4 |
| Constraint names are not duplicates merely because their uppercase forms match | §11.2.3, "Identifier Case Sensitivity" | §11.2.3, identical | §11.2.3, identical |
| `CHECK_CONSTRAINTS` has no table-name column | §28.3.5, "The INFORMATION_SCHEMA CHECK_CONSTRAINTS Table" | §28.3.5, same columns | §28.3.5, same columns |

## 25. Column grants are stored outside `TABLE_PRIVILEGES` ⚠️

**Affected:** all supported versions.

**Symptom:** a privilege granted only on one column produces a row in
`COLUMN_PRIVILEGES` and no row in `TABLE_PRIVILEGES`. Reading only the latter
therefore makes a privilege the account can exercise on part of the table look
absent for the entire table.

**Handling:** `Grants` reads `COLUMN_PRIVILEGES` as a weakening-only source. A
matching column row changes an otherwise-absent `Grants.Table` answer to
`GrantUnconfirmed`; it never proves the table-level privilege and never affects
`Grants.Schema` or `Grants.Global`. Pinned by
[`TestColumnGrantDowngradesTableAbsenceIntegration`](../pkg/validations/validations_integration_test.go).

**Reference:** documented, with identical substance across the supported
manuals:

| Claim | 8.0 | 8.4 | 9.7 |
|---|---|---|---|
| `COLUMN_PRIVILEGES` takes its values from `mysql.columns_priv` | §28.3.10, "The INFORMATION_SCHEMA COLUMN_PRIVILEGES Table" | §28.3.10, identical | §28.3.10, identical |
| Table and column privileges occupy separate grant-table columns | §8.2.3, "Grant Tables", Table 8.9 (`tables_priv.Table_priv` and `columns_priv.Column_priv`) | §8.2.3, identical | §8.2.3, identical |

## 26. A blank-`User` `db` row applies to a named session ⚠️

**Affected:** all supported versions.

**Symptom:** on 8.0.46, 8.4.9, and 9.7.1, a named account with no grant of its
own can read a schema granted only to `''@'%'`, while `CURRENT_USER()` still
reports the named account. Anonymous global, table, and column grants do not
apply to the named account. The database-level result contradicts the manual's
rule: "A blank User value matches the anonymous user. A nonblank value matches
literally; there are no wildcards in user names."

**Handling:** when visible, blank-user `SCHEMA_PRIVILEGES` rows are an
anonymous, weakening-only source. They can change an otherwise-absent schema or
table answer to `GrantUnconfirmed`, but never to `GrantPresent`. Anonymous rows
from `USER_PRIVILEGES`, `TABLE_PRIVILEGES`, and `COLUMN_PRIVILEGES` are excluded.
An exact match to the current anonymous account still has account provenance
and can prove a positive. The server behavior and the three excluded scopes are
pinned by
[`TestAnonymousDbRowAppliesToNamedAccountIntegration`](../pkg/validations/validations_integration_test.go);
the fact behavior is pinned by
[`TestAnonymousGrantWeakensAbsenceWhenVisibleIntegration`](../pkg/validations/validations_integration_test.go).

**Reference:** contradicted by the server at database scope; the manuals are
worded identically:

| Claim | 8.0 | 8.4 | 9.7 |
|---|---|---|---|
| Blank `User` means the anonymous user; nonblank `User` matches literally | §8.2.7, "Access Control, Stage 2: Request Verification" | §8.2.7, identical | §8.2.7, identical |
| `SCHEMA_PRIVILEGES` takes its values from `mysql.db` | §28.3.33 | §28.3.33 | §28.3.39 |

## 27. Privilege-table row visibility follows direct `SELECT` on `mysql` ⚠️

**Affected:** all supported versions.

**Symptom:** a least-privileged account sees only its own grantee in
`USER_PRIVILEGES` and cannot see privilege rows belonging to other accounts.
That makes absence unsafe: an anonymous `mysql.db` row can affect the session
while remaining invisible to the fact.

**Handling:** `GrantAbsent` requires a pinned, role-free session whose account
holds a direct schema-level `SELECT` on `mysql`, or a direct global `SELECT`
while partial revokes are disabled. Either is a sufficient condition for seeing
other accounts' privilege rows. A table-level grant on `mysql.user`, a global
grant while `mysql` is partially revoked, a role-held grant, or no qualifying
row does not prove completeness; every otherwise-absent answer is then
`GrantUnconfirmed`. A role-held `SELECT ON mysql.*` widened visibility live, but
is deliberately not a proving source because this fact does not prove
role-derived privileges. Pinned by
[`TestPrivilegeTableVisibilityIntegration`](../pkg/validations/validations_integration_test.go)
and
[`TestBroadVisibilityNegativeControlsIntegration`](../pkg/validations/validations_integration_test.go).

The before/after server pin produced:

| Server | Narrow `USER_PRIVILEGES` grantees | After direct `SELECT ON mysql.*` |
|---|---:|---:|
| 8.0.46 | 1 | 6 |
| 8.4.9 | 1 | 7 |
| 9.7.1 | 1 | 6 |

**Reference:** not documented. Refman §28.1, "Introduction", gives only the
general rule that most `INFORMATION_SCHEMA` tables show rows for objects on
which the user has proper access. The four privilege-table sections describe
their source tables and say their results are not equivalent to `SHOW GRANTS`,
but state no rule for when foreign grantees become visible:

| Table sections | 8.0 | 8.4 | 9.7 |
|---|---|---|---|
| `COLUMN_PRIVILEGES` / `SCHEMA_PRIVILEGES` / `TABLE_PRIVILEGES` / `USER_PRIVILEGES` | §28.3.10 / §28.3.33 / §28.3.44 / §28.3.47 | §28.3.10 / §28.3.33 / §28.3.43 / §28.3.46 | §28.3.10 / §28.3.39 / §28.3.49 / §28.3.52 |

---

## Adding an entry

Every version-specific behavior the code accommodates gets an entry here **and**
a test that pins it in the integration matrix. A quirk handled in code but not
recorded here is a quirk that will be "fixed" by someone who does not know why
the code is shaped that way.

Each entry also gets a **Reference** line. Look the claim up before writing it
down — a version threshold, an error number, or an `information_schema` column
recalled from memory is not a fact, and three of the entries above were wrong
until they were checked against the source. See AGENTS.md, "Look before
asserting", for where to look and how to cite. When the manual turns out to be silent, say so: "not documented" is a
useful finding, because it tells the next reader that the pinning test is the
only thing standing between us and a silent behavior change.
