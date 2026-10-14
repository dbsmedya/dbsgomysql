package validations

// limitAllRows ends every SELECT this package issues, appended at the call so
// that both the narrowed statement and its unnarrowed fallback carry it. A
// global sql_select_limit is inherited by new sessions and caps every SELECT
// without its own LIMIT, silently — the server returns the first rows and no
// error, which a fact would report as a complete answer. An explicit LIMIT
// takes precedence over the variable (refman "Server System Variables",
// sql_select_limit), so this bound, the variable's own maximum, returns every
// row whatever the session carries. It goes on the outer statement only: the
// cap applies to the outermost query block, and an inner LIMIT would force the
// requested-objects derived table to be materialized instead of merged. See
// docs/COMPAT.md entry 35.
const limitAllRows = "\nLIMIT 18446744073709551615"
