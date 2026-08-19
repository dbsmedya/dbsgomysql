package replication

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// The server spells these two columns with a lowercase id.
func registeredReplicasColumns() []string {
	return []string{"Server_id", "Host", "Port", "Source_id", "Replica_UUID"}
}

func scriptRegisteredReplicas(t *testing.T, columns []string, rows [][]driver.Value) *sql.DB {
	t.Helper()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "SHOW REPLICAS",
		Columns: columns,
		Rows:    rows,
	})
	t.Cleanup(func() { db.Close() })

	return db
}

func TestRegisteredReplicas(t *testing.T) {
	t.Parallel()

	db := scriptRegisteredReplicas(t, registeredReplicasColumns(), [][]driver.Value{
		{
			int64(2),
			[]byte("replica1.example.com"),
			int64(3306),
			int64(1),
			[]byte("3E11FA47-71CA-11E1-9E33-C80AA9429562"),
		},
		{
			[]byte("3"),
			[]byte("replica2.example.com"),
			int64(0), // report_port unset: a legitimate value, not a failure.
			[]byte("1"),
			[]byte("5CD1FA47-71CA-11E1-9E33-C80AA9429999"),
		},
	})

	got, err := NewInspector(db).RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("RegisteredReplicas() returned error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("RegisteredReplicas() returned %d replicas, want 2", len(got))
	}

	want := RegisteredReplica{
		ServerID:    2,
		Host:        "replica1.example.com",
		Port:        3306,
		SourceID:    1,
		ReplicaUUID: "3E11FA47-71CA-11E1-9E33-C80AA9429562",
	}
	if got[0] != want {
		t.Errorf("RegisteredReplicas()[0] = %#v, want %#v", got[0], want)
	}

	// Server row order is preserved, not sorted.
	if got[1].ServerID != 3 {
		t.Errorf("RegisteredReplicas()[1].ServerID = %d, want 3", got[1].ServerID)
	}
	if got[1].Port != 0 {
		t.Errorf("RegisteredReplicas()[1].Port = %d, want 0 for an unset report_port", got[1].Port)
	}
	if got[1].SourceID != 1 {
		t.Errorf("RegisteredReplicas()[1].SourceID = %d, want 1", got[1].SourceID)
	}
}

func TestRegisteredReplicasEmpty(t *testing.T) {
	t.Parallel()

	db := scriptRegisteredReplicas(t, registeredReplicasColumns(), nil)

	got, err := NewInspector(db).RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("RegisteredReplicas() returned error %v, want nil", err)
	}
	if got == nil {
		t.Fatal("RegisteredReplicas() = nil, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("RegisteredReplicas() returned %d replicas, want 0", len(got))
	}
}

func TestRegisteredReplicasMissingColumn(t *testing.T) {
	t.Parallel()

	const dropped = "Replica_UUID"
	db := scriptRegisteredReplicas(t,
		[]string{"Server_id", "Host", "Port", "Source_id"},
		[][]driver.Value{{int64(2), []byte("replica1.example.com"), int64(3306), int64(1)}},
	)

	got, err := NewInspector(db).RegisteredReplicas(t.Context())
	if err == nil {
		t.Fatalf("RegisteredReplicas() = %#v, nil; want a missing-column error", got)
	}
	if !errors.Is(err, errMissingColumn) {
		t.Errorf("errors.Is(%v, errMissingColumn) = false, want true", err)
	}
	assertOpError(t, err, opRegisteredReplicas, "", dropped)
}

func TestRegisteredReplicasFreshSlices(t *testing.T) {
	t.Parallel()

	db := scriptRegisteredReplicas(t, registeredReplicasColumns(), [][]driver.Value{
		{
			int64(2),
			[]byte("replica1.example.com"),
			int64(3306),
			int64(1),
			[]byte("3E11FA47-71CA-11E1-9E33-C80AA9429562"),
		},
	})
	inspector := NewInspector(db)

	first, err := inspector.RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("first RegisteredReplicas() returned error %v, want nil", err)
	}

	first[0].Host = "tampered"
	first[0].ServerID = 999
	first[0].ReplicaUUID = "tampered"

	second, err := inspector.RegisteredReplicas(t.Context())
	if err != nil {
		t.Fatalf("second RegisteredReplicas() returned error %v, want nil", err)
	}
	if second[0].Host != "replica1.example.com" {
		t.Errorf("second RegisteredReplicas()[0].Host = %q, want %q",
			second[0].Host, "replica1.example.com")
	}
	if second[0].ServerID != 2 {
		t.Errorf("second RegisteredReplicas()[0].ServerID = %d, want 2", second[0].ServerID)
	}
	if second[0].ReplicaUUID != "3E11FA47-71CA-11E1-9E33-C80AA9429562" {
		t.Errorf("second RegisteredReplicas()[0].ReplicaUUID = %q, want the server value",
			second[0].ReplicaUUID)
	}
	if &first[0] == &second[0] {
		t.Error("both calls returned the same backing array; each call must build a fresh slice")
	}
}

func TestRegisteredReplicasSQLAndQueryCount(t *testing.T) {
	t.Parallel()

	var log []string
	db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
		Match:   "SHOW REPLICAS",
		Columns: registeredReplicasColumns(),
		Rows: [][]driver.Value{
			{int64(2), []byte("replica1.example.com"), int64(3306), int64(1), []byte("uuid")},
		},
	})
	defer db.Close()

	if _, err := NewInspector(db).RegisteredReplicas(t.Context()); err != nil {
		t.Fatalf("RegisteredReplicas() returned error %v, want nil", err)
	}

	if len(log) != 1 {
		t.Fatalf("RegisteredReplicas() issued %d statements (%q), want exactly 1", len(log), log)
	}
	if log[0] != "SHOW REPLICAS" {
		t.Errorf("statement = %q, want %q", log[0], "SHOW REPLICAS")
	}
}

func TestRegisteredReplicaJSONContract(t *testing.T) {
	t.Parallel()

	replica := RegisteredReplica{
		ServerID:    2,
		Host:        "replica1.example.com",
		Port:        3306,
		SourceID:    1,
		ReplicaUUID: "3E11FA47-71CA-11E1-9E33-C80AA9429562",
	}

	const want = `{"server_id":2,"host":"replica1.example.com","port":3306,` +
		`"source_id":1,"replica_uuid":"3E11FA47-71CA-11E1-9E33-C80AA9429562"}`

	encoded, err := json.Marshal(replica)
	if err != nil {
		t.Fatalf("json.Marshal returned error %v, want nil", err)
	}
	if string(encoded) != want {
		t.Errorf("json.Marshal = %s, want %s", encoded, want)
	}
}
