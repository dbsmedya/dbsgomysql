package replication

import (
	"context"
	"database/sql/driver"
)

// DriverReadValueForTest is the one-statement driver reader that every
// statement of GroupConsistencyOnDriverConn goes through: it issues query
// through conn.QueryContext with ctx unchanged and nil arguments, and reads at
// most one row of column as text. It exists for the integration test of
// contract (c), which needs a statement the server really holds; none of the
// function's own statements is one on the test topology.
func DriverReadValueForTest(
	ctx context.Context, conn driver.QueryerContext, query, column string,
) (value string, seen bool, err error) {
	return readOptional(ctx, driverStatements(conn), opGroupConsistency, query, column, decodeString)
}
