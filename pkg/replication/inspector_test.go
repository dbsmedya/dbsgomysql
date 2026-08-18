package replication

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestNewInspectorNilQuerier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		inspector *Inspector
		op        string
	}{
		{
			name:      "nil inspector",
			inspector: nil,
			op:        opBinaryLogEnabled,
		},
		{
			name:      "nil querier field",
			inspector: &Inspector{},
			op:        opGTIDStatus,
		},
		{
			name:      "nil querier",
			inspector: NewInspector(nil),
			op:        opReplicaStatus,
		},
		{
			name:      "typed nil querier",
			inspector: NewInspector((*sql.DB)(nil)),
			op:        opReplicationConfig,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.inspector.validate(testCase.op)
			if err == nil {
				t.Fatalf("(*Inspector).validate(%q) = nil, want an error", testCase.op)
			}
			if !errors.Is(err, ErrNilQuerier) {
				t.Errorf("errors.Is(%v, ErrNilQuerier) = false, want true", err)
			}

			var opErr *OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("errors.As(%v, *OpError) = false, want true", err)
			}
			if opErr.Op != testCase.op {
				t.Errorf("OpError.Op = %q, want %q", opErr.Op, testCase.op)
			}
			if opErr.Channel != "" {
				t.Errorf("OpError.Channel = %q, want empty", opErr.Channel)
			}
			if opErr.Column != "" {
				t.Errorf("OpError.Column = %q, want empty", opErr.Column)
			}
		})
	}
}

func TestValidateAcceptsUsableQuerier(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{})
	if err := inspector.validate(opBinaryLogStatus); err != nil {
		t.Errorf("(*Inspector).validate(%q) = %v, want nil", opBinaryLogStatus, err)
	}
}

type panicQuerier struct{}

func (panicQuerier) QueryContext(
	context.Context,
	string,
	...any,
) (*sql.Rows, error) {
	panic("QueryContext called during argument validation")
}

func (panicQuerier) QueryRowContext(
	context.Context,
	string,
	...any,
) *sql.Row {
	panic("QueryRowContext called during argument validation")
}
