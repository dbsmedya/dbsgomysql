package replication

import (
	"errors"
	"fmt"
	"testing"
)

func TestOpErrorFormat(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	cases := []struct {
		name string
		err  *OpError
		want string
	}{
		{
			name: "op only",
			err:  &OpError{Op: opRegisteredReplicas, Err: cause},
			want: "replication: registered_replicas: boom",
		},
		{
			name: "op and channel",
			err:  &OpError{Op: opReplicaStatus, Channel: "ch1", Err: cause},
			want: `replication: replica_status channel "ch1": boom`,
		},
		{
			name: "op and column",
			err: &OpError{
				Op:     opBinaryLogStatus,
				Column: "Executed_Gtid_Set",
				Err:    cause,
			},
			want: "replication: binary_log_status column Executed_Gtid_Set: boom",
		},
		{
			name: "op, channel, and column",
			err: &OpError{
				Op:      opReplicaStatus,
				Channel: "ch1",
				Column:  "Seconds_Behind_Source",
				Err:     cause,
			},
			want: `replication: replica_status channel "ch1" column Seconds_Behind_Source: boom`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := testCase.err.Error(); got != testCase.want {
				t.Errorf("OpError.Error() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestOpErrorUnwrap(t *testing.T) {
	t.Parallel()

	cause := errors.New("driver failed")
	opErr := &OpError{
		Op:      opReplicaStatus,
		Channel: "ch1",
		Err:     cause,
	}
	err := fmt.Errorf("outer one: %w", fmt.Errorf("outer two: %w", opErr))

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false, want true", err)
	}

	var got *OpError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(%v, *OpError) = false, want true", err)
	}
	if got != opErr {
		t.Errorf("errors.As extracted %p, want %p", got, opErr)
	}
	if got.Unwrap() == nil {
		t.Error("OpError.Unwrap() = nil; OpError.Err must remain reachable")
	}
}
