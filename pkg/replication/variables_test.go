package replication

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func TestBinaryLogEnabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value driver.Value
		want  bool
	}{
		{name: "enabled as int64", value: int64(1), want: true},
		{name: "disabled as int64", value: int64(0), want: false},
		{name: "enabled as bool", value: true, want: true},
		{name: "disabled as bool", value: false, want: false},
		{name: "enabled as text", value: []byte("ON"), want: true},
		{name: "disabled as text", value: []byte("OFF"), want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
				Match:   "@@GLOBAL.log_bin",
				Columns: []string{"@@GLOBAL.log_bin"},
				Rows:    [][]driver.Value{{testCase.value}},
			})
			defer db.Close()

			got, err := NewInspector(db).BinaryLogEnabled(t.Context())
			if err != nil {
				t.Fatalf("BinaryLogEnabled() returned error %v, want nil", err)
			}
			if got != testCase.want {
				t.Errorf("BinaryLogEnabled() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestGTIDStatus(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "@@GLOBAL.gtid_mode",
		Columns: []string{"@@GLOBAL.gtid_mode", "@@GLOBAL.gtid_executed", "@@GLOBAL.gtid_purged"},
		Rows: [][]driver.Value{{
			[]byte("ON"),
			[]byte("3E11FA47-71CA-11E1-9E33-C80AA9429562:aa:1-5"),
			[]byte(""),
		}},
	})
	defer db.Close()

	got, err := NewInspector(db).GTIDStatus(t.Context())
	if err != nil {
		t.Fatalf("GTIDStatus() returned error %v, want nil", err)
	}

	want := GTIDStatus{
		Mode:     "ON",
		Executed: "3E11FA47-71CA-11E1-9E33-C80AA9429562:aa:1-5",
		Purged:   "",
	}
	if got != want {
		t.Errorf("GTIDStatus() = %#v, want %#v", got, want)
	}
}

func TestReplicationConfig(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match: "@@GLOBAL.read_only",
		Columns: []string{
			"@@GLOBAL.read_only",
			"@@GLOBAL.super_read_only",
			"@@GLOBAL.server_id",
			"@@GLOBAL.log_replica_updates",
			"@@GLOBAL.replica_parallel_workers",
		},
		Rows: [][]driver.Value{{
			int64(1),
			[]byte("0"),
			int64(101),
			[]byte("1"),
			int64(4),
		}},
	})
	defer db.Close()

	got, err := NewInspector(db).ReplicationConfig(t.Context())
	if err != nil {
		t.Fatalf("ReplicationConfig() returned error %v, want nil", err)
	}

	want := Config{
		ReadOnly:               true,
		SuperReadOnly:          false,
		ServerID:               101,
		LogReplicaUpdates:      true,
		ReplicaParallelWorkers: 4,
	}
	if got != want {
		t.Errorf("ReplicationConfig() = %#v, want %#v", got, want)
	}
}

func TestReplicationConfigSingleThreadedApplier(t *testing.T) {
	t.Parallel()

	// COMPAT 23: replica_parallel_workers = 0 is reachable on 8.x only, and it
	// must decode as the state it is rather than as a decode failure.
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match: "@@GLOBAL.read_only",
		Columns: []string{
			"@@GLOBAL.read_only",
			"@@GLOBAL.super_read_only",
			"@@GLOBAL.server_id",
			"@@GLOBAL.log_replica_updates",
			"@@GLOBAL.replica_parallel_workers",
		},
		Rows: [][]driver.Value{{int64(0), int64(0), int64(1), int64(0), int64(0)}},
	})
	defer db.Close()

	got, err := NewInspector(db).ReplicationConfig(t.Context())
	if err != nil {
		t.Fatalf("ReplicationConfig() returned error %v, want nil", err)
	}
	if got.ReplicaParallelWorkers != 0 {
		t.Errorf("ReplicationConfig().ReplicaParallelWorkers = %d, want 0", got.ReplicaParallelWorkers)
	}
}

func TestVariableFactsQueryError(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection lost")
	cases := []struct {
		name string
		call func(*Inspector) error
		op   string
	}{
		{
			name: "binary log enabled",
			call: func(inspector *Inspector) error {
				_, err := inspector.BinaryLogEnabled(t.Context())

				return err
			},
			op: opBinaryLogEnabled,
		},
		{
			name: "gtid status",
			call: func(inspector *Inspector) error {
				_, err := inspector.GTIDStatus(t.Context())

				return err
			},
			op: opGTIDStatus,
		},
		{
			name: "replication config",
			call: func(inspector *Inspector) error {
				_, err := inspector.ReplicationConfig(t.Context())

				return err
			},
			op: opReplicationConfig,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
				Match: "SELECT",
				Err:   cause,
			})
			defer db.Close()

			err := testCase.call(NewInspector(db))
			if err == nil {
				t.Fatal("fact returned nil error, want the scripted failure")
			}
			if !errors.Is(err, cause) {
				t.Errorf("errors.Is(%v, cause) = false, want true", err)
			}

			var opErr *OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("errors.As(%v, *OpError) = false, want true", err)
			}
			if opErr.Op != testCase.op {
				t.Errorf("OpError.Op = %q, want %q", opErr.Op, testCase.op)
			}
		})
	}
}

func TestGTIDStatusRejectsNULL(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "@@GLOBAL.gtid_mode",
		Columns: []string{"@@GLOBAL.gtid_mode", "@@GLOBAL.gtid_executed", "@@GLOBAL.gtid_purged"},
		Rows:    [][]driver.Value{{nil, []byte(""), []byte("")}},
	})
	defer db.Close()

	got, err := NewInspector(db).GTIDStatus(t.Context())
	if err == nil {
		t.Fatalf("GTIDStatus() = %#v, nil; want an error rather than a silent empty Mode", got)
	}
	if !errors.Is(err, errUnexpectedNULL) {
		t.Errorf("errors.Is(%v, errUnexpectedNULL) = false, want true", err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("errors.As(%v, *OpError) = false, want true", err)
	}
	if opErr.Op != opGTIDStatus {
		t.Errorf("OpError.Op = %q, want %q", opErr.Op, opGTIDStatus)
	}
	if opErr.Column != "@@GLOBAL.gtid_mode" {
		t.Errorf("OpError.Column = %q, want %q", opErr.Column, "@@GLOBAL.gtid_mode")
	}
}

func TestVariableFactsSQLAndQueryCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		call    func(*Inspector) error
		wantSQL string
	}{
		{
			name: "binary log enabled",
			call: func(inspector *Inspector) error {
				_, err := inspector.BinaryLogEnabled(t.Context())

				return err
			},
			wantSQL: "SELECT @@GLOBAL.log_bin",
		},
		{
			name: "gtid status",
			call: func(inspector *Inspector) error {
				_, err := inspector.GTIDStatus(t.Context())

				return err
			},
			wantSQL: "SELECT @@GLOBAL.gtid_mode, @@GLOBAL.gtid_executed, @@GLOBAL.gtid_purged",
		},
		{
			name: "replication config",
			call: func(inspector *Inspector) error {
				_, err := inspector.ReplicationConfig(t.Context())

				return err
			},
			wantSQL: "SELECT @@GLOBAL.read_only, @@GLOBAL.super_read_only, @@GLOBAL.server_id, " +
				"@@GLOBAL.log_replica_updates, @@GLOBAL.replica_parallel_workers",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var log []string
			db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
				Match:   "@@GLOBAL.log_bin",
				Columns: []string{"@@GLOBAL.log_bin"},
				Rows:    [][]driver.Value{{int64(1)}},
			}, testsupport.ScriptedQuery{
				Match:   "@@GLOBAL.gtid_mode",
				Columns: []string{"@@GLOBAL.gtid_mode", "@@GLOBAL.gtid_executed", "@@GLOBAL.gtid_purged"},
				Rows:    [][]driver.Value{{[]byte("OFF"), []byte(""), []byte("")}},
			}, testsupport.ScriptedQuery{
				Match: "@@GLOBAL.read_only",
				Columns: []string{
					"@@GLOBAL.read_only",
					"@@GLOBAL.super_read_only",
					"@@GLOBAL.server_id",
					"@@GLOBAL.log_replica_updates",
					"@@GLOBAL.replica_parallel_workers",
				},
				Rows: [][]driver.Value{{int64(0), int64(0), int64(1), int64(1), int64(2)}},
			})
			defer db.Close()

			if err := testCase.call(NewInspector(db)); err != nil {
				t.Fatalf("fact returned error %v, want nil", err)
			}

			if len(log) != 1 {
				t.Fatalf("fact issued %d statements (%q), want exactly 1", len(log), log)
			}
			if log[0] != testCase.wantSQL {
				t.Errorf("statement = %q, want %q", log[0], testCase.wantSQL)
			}
		})
	}
}

func TestVariableFactsJSONContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fact any
		want string
	}{
		{
			name: "gtid status",
			fact: GTIDStatus{
				Mode:     "ON",
				Executed: "uuid:1-5",
				Purged:   "uuid:1-2",
			},
			want: `{"mode":"ON","executed":"uuid:1-5","purged":"uuid:1-2"}`,
		},
		{
			name: "replication config",
			fact: Config{
				ReadOnly:               true,
				SuperReadOnly:          false,
				ServerID:               42,
				LogReplicaUpdates:      true,
				ReplicaParallelWorkers: 4,
			},
			want: `{"read_only":true,"super_read_only":false,"server_id":42,` +
				`"log_replica_updates":true,"replica_parallel_workers":4}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(testCase.fact)
			if err != nil {
				t.Fatalf("json.Marshal returned error %v, want nil", err)
			}
			if string(encoded) != testCase.want {
				t.Errorf("json.Marshal = %s, want %s", encoded, testCase.want)
			}
		})
	}
}
