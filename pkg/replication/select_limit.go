package replication

// limitAllRows ends every SELECT this package issues. A global
// sql_select_limit is inherited by new sessions and caps every SELECT without
// its own LIMIT, silently — the server returns the first rows and no error. An
// explicit LIMIT takes precedence over the variable (refman "Server System
// Variables", sql_select_limit), so this bound, the variable's own maximum,
// returns every row whatever the session carries. It goes on the outer
// statement only. SHOW statements take no LIMIT; how they behave under the
// variable is recorded in docs/COMPAT.md entry 35.
const limitAllRows = " LIMIT 18446744073709551615"
