package validations

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
)

const (
	opColumns          = "columns"
	opForeignKeys      = "foreign_keys"
	opGrants           = "grants"
	opInvisibleColumns = "invisible_columns"
	opPrimaryKeys      = "primary_keys"
	opTableSpec        = "table_spec"
	opTables           = "tables"
	opTriggers         = "triggers"
)

// Querier is the connection behavior an Inspector needs. *sql.DB, *sql.Tx,
// and *sql.Conn satisfy it.
//
// An implementation must document its own concurrency behavior. Inspector may
// invoke it concurrently when the Inspector is shared by callers.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Inspector reads metadata from one schema through a caller-owned connection.
// It never opens, configures, or closes that connection.
//
// Inspector is immutable and safe for concurrent use when its Querier is safe
// for concurrent use.
type Inspector struct {
	q      Querier
	schema string
}

// NewInspector binds q and schema without performing I/O. Invalid arguments
// are reported by the first facts call so construction remains infallible.
//
// NewInspector is safe for concurrent use.
func NewInspector(q Querier, schema string) *Inspector {
	return &Inspector{q: q, schema: schema}
}

func (i *Inspector) validate(op string, tables []string) error {
	if i == nil || isNilQuerier(i.q) {
		schema := ""
		if i != nil {
			schema = i.schema
		}

		return newObjectError(op, schema, "", ErrNilQuerier)
	}
	if i.schema == "" {
		return newObjectError(op, "", "", ErrEmptySchema)
	}
	for index, table := range tables {
		if table == "" {
			cause := fmt.Errorf("table at index %d: %w", index, ErrEmptyTableName)

			return newObjectError(op, i.schema, "", cause)
		}
	}

	return nil
}

func isNilQuerier(q Querier) bool {
	if q == nil {
		return true
	}

	value := reflect.ValueOf(q)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
