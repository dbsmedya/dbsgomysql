package validations

import (
	"errors"
	"fmt"

	"github.com/dbsmedya/dbsgomysql/pkg/sqlutil"
)

var (
	// ErrNilQuerier means an Inspector was constructed without a connection.
	ErrNilQuerier = errors.New("validations: nil Querier")
	// ErrEmptySchema means an Inspector was constructed without a schema name.
	ErrEmptySchema = errors.New("validations: empty schema name")
	// ErrEmptyTableName means a facts call received an empty table name.
	ErrEmptyTableName = errors.New("validations: empty table name")
	// ErrInvalidTriggerEvent means a facts call received an unknown trigger
	// event value.
	ErrInvalidTriggerEvent = errors.New("validations: invalid trigger event")
	// ErrInvalidFKSelector means ForeignKeys received a selector not produced
	// by IncomingTo, OutgoingFrom, or Within.
	ErrInvalidFKSelector = errors.New("validations: invalid foreign-key selector")
	// ErrInvalidTableRef means TableSpec received a TableRef that was not
	// produced by Ref, or one naming an empty schema or table.
	ErrInvalidTableRef = errors.New("validations: invalid table reference")
	// ErrTableNotFound means TableSpec was asked for a table the inspected
	// server does not expose under that exact name.
	ErrTableNotFound = errors.New("validations: table not found")
	// ErrUnsupportedTableType means TableSpec resolved an object that is not a
	// BASE TABLE, such as a view.
	ErrUnsupportedTableType = errors.New("validations: unsupported table type")
)

// ObjectError reports a failed schema inspection or invalid inspection
// argument. Table is empty when the failure applies to a set-wide query rather
// than one table. Err is the underlying cause and is never nil for errors
// returned by this package.
//
// ObjectError is immutable after construction and is safe for concurrent use
// provided callers do not mutate its exported fields.
type ObjectError struct {
	// Op names the facts operation that failed: "columns", "foreign_keys",
	// "grants", "invisible_columns", "primary_keys", "table_spec", "tables", or
	// "triggers". The order matches the op constants' declaration order, so a
	// new op added out of alphabetical position is visibly missing here.
	Op string
	// Schema names the schema the failure concerns. For every op that reads the
	// Inspector's own schema it is that schema; for "table_spec" it is the
	// requested TableRef's schema, which need not be the one the Inspector is
	// bound to. It is empty only when the schema argument itself is invalid.
	Schema string
	// Table names the affected table when attribution is possible.
	Table string
	// Err is the underlying cause.
	Err error
}

// Error returns the operation, safely quoted object attribution, and cause.
//
// Error is safe for concurrent use provided the ObjectError is not mutated.
func (e *ObjectError) Error() string {
	switch {
	case e.Table != "":
		return fmt.Sprintf(
			"validations: %s on %s: %v",
			e.Op,
			sqlutil.QuoteQualified(e.Schema, e.Table),
			e.Err,
		)
	case e.Schema != "":
		return fmt.Sprintf(
			"validations: %s in schema %s: %v",
			e.Op,
			sqlutil.QuoteIdentifier(e.Schema),
			e.Err,
		)
	default:
		return fmt.Sprintf("validations: %s: %v", e.Op, e.Err)
	}
}

// Unwrap returns the underlying cause.
//
// Unwrap is safe for concurrent use provided the ObjectError is not mutated.
func (e *ObjectError) Unwrap() error {
	return e.Err
}

func newObjectError(op, schema, table string, err error) *ObjectError {
	return &ObjectError{
		Op:     op,
		Schema: schema,
		Table:  table,
		Err:    err,
	}
}
