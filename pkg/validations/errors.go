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
)

// ObjectError reports a failed schema inspection or invalid inspection
// argument. Table is empty when the failure applies to a set-wide query rather
// than one table. Err is the underlying cause and is never nil for errors
// returned by this package.
//
// ObjectError is immutable after construction and is safe for concurrent use
// provided callers do not mutate its exported fields.
type ObjectError struct {
	// Op names the facts operation that failed: "tables", "primary_keys",
	// "triggers", "invisible_columns", "foreign_keys", or "grants".
	Op string
	// Schema is the Inspector's schema. It is empty only when the schema
	// argument itself is invalid.
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
