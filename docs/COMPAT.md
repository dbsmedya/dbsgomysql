# MySQL Compatibility & Quirk Registry

`dbsgomysql` supports **MySQL 8.0 and newer**. It is tested against **8.0,
8.4, and 9.7**. The 26.x development line is watched but not supported: its CI
job is allowed to fail and no code accommodates it until it stabilizes.

This document is the registry of MySQL behaviors that differ across versions or
that surprise callers of `information_schema`. Each entry states the affected
versions, the observable symptom, and how the library handles it.

> **Status legend** — ✅ handled and pinned by a test · 🔜 registered, handling
> lands with the package that needs it · 👁 operator guidance only, no library
> code involved.
>
> The library is in its design phase, so entries below are 🔜 or 👁. Each
> becomes ✅ with a linked pinning test as `pkg/validations` and
> `pkg/replication` land.

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

## 1. Integer display widths dropped 🔜

**Affected:** 8.0.17 and newer report integer types without display width;
earlier servers and dumps taken from them include it.

**Symptom:** `information_schema.COLUMNS.COLUMN_TYPE` returns `bigint(20)` on
an older server and `bigint` on a current one. A naive string comparison
between a schema captured before 8.0.17 and one captured after reports a false
type mismatch on every integer column.

**Handling:** `ColumnSpec.NormalizedType` strips the display width from
`tinyint`, `smallint`, `mediumint`, `int`, `integer`, and `bigint`. The
`unsigned` and `zerofill` attributes are **preserved** — they change the value
range and are therefore real differences, not formatting noise.

## 2. `information_schema` name lookups are case-insensitive 🔜

**Affected:** all supported versions.

**Symptom:** string columns in `information_schema` collate under
`utf8mb3_tolower_ci`. A query like

```sql
SELECT ... FROM information_schema.COLUMNS WHERE COLUMN_NAME = 'log_id'
```

also matches a column actually named `LOG_ID`. Code that trusts such a lookup
to confirm exact naming silently accepts the wrong case, which then fails later
against a case-sensitive consumer or a differently configured server.

**Handling:** library-wide rule — **fetch the real name and compare it in Go**.
Every name returned by the library is the server's exact-case spelling, and
every equality check on a name happens in Go, never in SQL. This is core
behavior, not a workaround limited to one check.

## 3. `GRANTEE` does not escape embedded quotes 🔜

**Affected:** all supported versions (verified on 8.4; 8.0 and 9.7 pinned by
the matrix).

**Symptom:** MySQL builds the `GRANTEE` column of the `*_PRIVILEGES` tables by
naive concatenation. A user named `o'brien` yields the literal string
`'o'brien'@'%'` — the embedded quote is **not** doubled or escaped. Code that
constructs a grantee string with correct SQL escaping will never match.

**Handling:** the library reproduces MySQL's concatenation exactly, including
the missing escape, rather than producing well-formed SQL. Matching the
server's actual behavior is the requirement here.

## 4. Privileges held through nested roles are not resolved 🔜

**Affected:** all supported versions. **This is a known limitation, not a
workaround.**

**Symptom:** the library resolves the effective grantee set from
`CURRENT_USER()` plus the roles active under `CURRENT_ROLE()`. A privilege held
via a role that was granted *to another role* is not discovered by that
resolution, so a privilege check can report a privilege as missing when the
account does in fact hold it.

**Handling:** the library **fails conservatively** — it reports the privilege
as unconfirmed rather than assuming it is held. Consumers relying on nested
role grants should activate the relevant roles on the session before calling,
or grant directly.

## 5. Cross-schema foreign key metadata can be invisible 🔜

**Affected:** all supported versions.

**Symptom:** a foreign key constraint is exposed in `information_schema` only
to accounts privileged on the **child** table. An account without privileges on
some schema cannot see that schema at all — not even in `SCHEMATA`. A query for
"which foreign keys point into these tables?" therefore returns an
under-count with no error and no warning.

**Handling:** completeness of incoming-FK discovery is unprovable without
global `SELECT`. The `FK_METADATA_VISIBILITY` check probes for that privilege
and **fails closed**, reporting that the answer cannot be trusted rather than
returning an incomplete list as if it were complete.

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

---

## Adding an entry

Every version-specific behavior the code accommodates gets an entry here **and**
a test that pins it in the integration matrix. A quirk handled in code but not
recorded here is a quirk that will be "fixed" by someone who does not know why
the code is shaped that way.
