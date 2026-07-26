# MySQL Version-Specific Compatibility Matrix

This document records the MySQL behavior observed while implementing
`pkg/sqlutil` on 2026-07-26. It is an evidence snapshot for future development:
[`COMPAT.md`](COMPAT.md) remains the canonical registry of compatibility quirks
and their library handling.

## Tested environment

| Matrix target | Resolved server version | Docker image | Result |
|---|---:|---|---|
| MySQL 8.0 | 8.0.46 | `mysql:8.0` | Passed |
| MySQL 8.4 | 8.4.9 | `mysql:8.4` | Passed |
| MySQL 9.7 | 9.7.1 | `mysql:9.7` | Passed |

The tests used `github.com/go-sql-driver/mysql` v1.10.0 and a connection whose
default database was unset. Every object was addressed with a fully qualified,
separately quoted schema and table name.

The Docker tags above follow release lines rather than immutable patch
versions. A later run may resolve them to newer patch releases; always record
the result of `SELECT VERSION()` when refreshing this matrix.

## Compatibility matrix

| Behavior | MySQL 8.0.46 | MySQL 8.4.9 | MySQL 9.7.1 | Library treatment |
|---|---|---|---|---|
| Embedded backtick in a quoted table name | Exact round-trip | Exact round-trip | Exact round-trip | `QuoteIdentifier` doubles backticks |
| 64-character table name | Accepted | Accepted | Accepted | `ValidateIdentifier` accepts it |
| 65-character table name | Rejected | Rejected | Rejected | `ErrIdentifierTooLong` |
| BMP Unicode table name such as `表_é` | Exact round-trip | Exact round-trip | Exact round-trip | Accepted |
| Supplementary character such as `U+10000` in a table name | Statement accepted; stored as `?` | Statement accepted; stored as `?` | Statement accepted; stored as `?` | `ErrIdentifierSupplementary` |
| `information_schema.TABLES` comparison with the original supplementary-character parameter | Error 3988 | Error 3988 | Error 3988 | Reject before querying |
| Table name ending in ASCII space | Rejected | Rejected | Rejected | `ErrIdentifierTrailingSpace` |

No behavior differed between the three tested release lines. The issues below
are still compatibility concerns because they are surprising, can silently
change an identifier, and must remain pinned as MySQL evolves.

## Issues encountered

### Supplementary characters are silently replaced

MySQL accepted a quoted table name containing `U+10000`, but the name did not
round-trip:

```sql
CREATE TABLE `supp_𐀀` (id INT);
SHOW TABLES;
```

The server reported the stored name as `supp_?` on all three versions.
Repeating the original SQL text can appear to work because the same conversion
to `?` occurs again. That apparent success is unsafe: the configured name is
not preserved and can collide with a name that contains a literal question
mark.

`ValidateIdentifier` therefore returns `ErrIdentifierSupplementary`.
`QuoteIdentifier` remains total because quoting safety and server validity are
separate guarantees. The behavior is pinned by
[`TestIdentifierCharacterSetIntegration`](../pkg/sqlutil/sqlutil_integration_test.go).

### `information_schema` cannot compare the original value

After creating the supplementary-character table, comparing
`information_schema.TABLES.TABLE_NAME` with the original `utf8mb4` parameter
failed on every tested version:

```text
Error 3988 (HY000): Conversion from collation utf8mb4_general_ci
into utf8mb3_bin impossible for parameter
```

This is consistent with the `utf8mb3` metadata behavior already tracked in
[`COMPAT.md`](COMPAT.md). Code must not interpret this error as “table does not
exist.” For `pkg/sqlutil`, the safe boundary is earlier:
`ValidateIdentifier` rejects the non-round-trippable name before SQL or
metadata lookup.

## Refresh procedure

When a matrix image advances or a new supported release line is added:

1. Start the services in [`tests/docker/compose.yaml`](../tests/docker/compose.yaml).
2. Record `SELECT VERSION()` for each server.
3. Set `DBSGOMYSQL_TEST_DSN` and `DBSGOMYSQL_TEST_MYSQL_VERSION` for the
   service, run `go clean -testcache`, then run `make test-integration`.
4. Update this matrix if the resolved versions or results changed.
5. If behavior changes, update [`COMPAT.md`](COMPAT.md), the handling code, and
   the pinning test together.
