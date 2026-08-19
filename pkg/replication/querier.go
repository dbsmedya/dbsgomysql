package replication

import (
	"context"
	"database/sql"
	"reflect"
)

const (
	opBinaryLogEnabled   = "binary_log_enabled"
	opBinaryLogStatus    = "binary_log_status"
	opGTIDStatus         = "gtid_status"
	opRegisteredReplicas = "registered_replicas"
	opReplicaStatus      = "replica_status"
	opReplicationConfig  = "replication_config"
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

// Inspector reads replication state from one server through a caller-owned
// connection. It never opens, configures, or closes that connection.
//
// Inspector is immutable and safe for concurrent use when its Querier is safe
// for concurrent use.
type Inspector struct {
	q Querier
}

// NewInspector binds q without performing I/O, so construction is infallible.
// A nil or typed-nil Querier is reported by the first fact call as an OpError
// wrapping ErrNilQuerier.
//
// NewInspector is safe for concurrent use.
func NewInspector(q Querier) *Inspector {
	return &Inspector{q: q}
}

func (i *Inspector) validate(op string) error {
	if i == nil || isNilQuerier(i.q) {
		return newOpError(op, "", "", ErrNilQuerier)
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
