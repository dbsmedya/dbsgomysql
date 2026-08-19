package replication

import (
	"errors"
	"fmt"
)

// ErrNilQuerier means an Inspector was constructed without a connection.
var ErrNilQuerier = errors.New("replication: nil Querier")

// OpError reports a failed replication fact read. Channel and Column are empty
// when attribution to a single channel or result column is impossible. Err is
// the underlying cause and is never nil for errors returned by this package.
//
// OpError is immutable after construction and is safe for concurrent use
// provided callers do not mutate its exported fields.
type OpError struct {
	// Op names the facts operation that failed: "binary_log_enabled",
	// "binary_log_status", "gtid_status", "replica_status",
	// "registered_replicas", or "replication_config". One value per fact
	// method, so a new fact is visibly missing from this list until it is
	// added.
	Op string
	// Channel names the replication channel the failure concerns, in the
	// server's own spelling, where the empty name is the default channel. It is
	// set only when the failing row's channel name decoded successfully, so an
	// unattributable failure never guesses one.
	Channel string
	// Column names the result column that caused the failure, in the server's
	// own spelling. It is empty when no single column is responsible.
	Column string
	// Err is the underlying cause.
	Err error
}

// Error returns the operation, its channel and column attribution, and the
// cause. The channel name is quoted so that a server spelling containing
// spaces stays readable; an unattributed channel or column is omitted rather
// than rendered empty.
//
// Error is safe for concurrent use provided the OpError is not mutated.
func (e *OpError) Error() string {
	attribution := ""
	if e.Channel != "" {
		attribution = fmt.Sprintf(" channel %q", e.Channel)
	}
	if e.Column != "" {
		attribution += " column " + e.Column
	}

	return fmt.Sprintf("replication: %s%s: %v", e.Op, attribution, e.Err)
}

// Unwrap returns the underlying cause.
//
// Unwrap is safe for concurrent use provided the OpError is not mutated.
func (e *OpError) Unwrap() error {
	return e.Err
}

func newOpError(op, channel, column string, err error) *OpError {
	return &OpError{
		Op:      op,
		Channel: channel,
		Column:  column,
		Err:     err,
	}
}
