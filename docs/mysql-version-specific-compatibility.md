# MySQL Version Divergence Register

This file records **only behavior that differs between the supported MySQL
versions**. Anything MySQL does identically on all of them — including the
surprising things — belongs in [`COMPAT.md`](COMPAT.md), which is the canonical
registry of quirks and their library handling.

That split is what makes an entry here worth reading: it means a consumer's
result depends on which server they are connected to, and the library has to
absorb the difference. An invariant surprise does not.

## Current divergences

**None.** Every behavior exercised by `pkg/sqlutil` and the phase-1b
`pkg/validations` facts and checks is identical on all three supported versions.

Last probed 2026-07-26 against:

| Matrix target | Resolved version | Docker image |
|---|---:|---|
| MySQL 8.0 | 8.0.46 | `mysql:8.0` |
| MySQL 8.4 | 8.4.9 | `mysql:8.4` |
| MySQL 9.7 | 9.7.1 | `mysql:9.7` |

through `github.com/go-sql-driver/mysql` v1.10.0, on a connection with no
default database, addressing every object by a fully qualified and separately
quoted name.

The Docker tags follow release lines rather than immutable patch versions, so a
later run may resolve to newer patches. Record `SELECT VERSION()`, not the tag.

## Probed for divergence

Listed so that "none" is falsifiable rather than merely asserted. What each
behavior *is*, and how the library handles it, is documented in
[`COMPAT.md`](COMPAT.md) and [`sqlutil.md`](sqlutil.md) — not repeated here.

| Behavior | Pinned by |
|---|---|
| Embedded backtick in a quoted identifier | `TestQuoteIdentifierRoundTripIntegration` |
| Identifier length at the 64- and 65-character boundary | `TestIdentifierLengthBoundaryIntegration` |
| BMP Unicode identifiers | `TestIdentifierCharacterSetIntegration` |
| Supplementary characters above `U+FFFF` | `TestIdentifierCharacterSetIntegration` |
| `information_schema` lookup of a supplementary-character name | `TestIdentifierCharacterSetIntegration` |
| Trailing `U+0009`–`U+000D` and `U+0020`, per object kind | `TestIdentifierCharacterSetIntegration` |
| Leading space characters, trailing NBSP, trailing `U+3000` | `TestIdentifierCharacterSetIntegration` |
| `information_schema` name-column collations by category | `TestMetadataNameCollationsIntegration` |
| Case-insensitive column-name metadata lookup with exact returned spelling | `TestColumnNameCaseInsensitivityIntegration` |
| Case-distinct table names when `lower_case_table_names=0` | `TestTableNameCaseSensitivityIntegration` |
| Primary-key absence, type, unsigned flag, key order, and secondary-index overlap | `TestPrimaryKeysIntegration` |
| Plain and generated invisible columns | `TestInvisibleColumnsIntegration` |
| Trigger event separation, exact names, timing, and ordering | `TestTriggersIntegration` |
| InnoDB and MyISAM engine spelling | `TestStorageEngineIntegration` |
| Views as existing objects with a NULL storage engine | `TestViewsIntegration` |
| Unknown and privilege-invisible schemas producing indistinguishable facts | `TestUnknownAndInvisibleSchemaIntegration` |

The identifier pins live in
[`sqlutil_integration_test.go`](../pkg/sqlutil/sqlutil_integration_test.go);
the metadata pins live in
[`validations_integration_test.go`](../pkg/validations/validations_integration_test.go).
Both run against every version in the matrix.

## Refresh procedure

Run the matrix as described in [`testing.md`](testing.md), which owns the
harness conventions and is not restated here. Then:

1. Record `SELECT VERSION()` for each server and update the table above.
2. If a behavior now differs between versions, add it here **and** to
   [`COMPAT.md`](COMPAT.md), with the handling code and its pinning test in the
   same commit.
3. If nothing differs, update only the probe date.
