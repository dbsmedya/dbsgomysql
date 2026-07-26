// Package testsupport provides shared test-only infrastructure.
package testsupport

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

// OpenFailingDB returns a database whose attempts to establish a connection
// return cause.
func OpenFailingDB(cause error) *sql.DB {
	return sql.OpenDB(failingConnector{cause: cause})
}

type failingConnector struct {
	cause error
}

func (c failingConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, c.cause
}

func (c failingConnector) Driver() driver.Driver {
	return failingDriver(c)
}

type failingDriver struct {
	cause error
}

func (d failingDriver) Open(string) (driver.Conn, error) {
	return nil, d.cause
}
