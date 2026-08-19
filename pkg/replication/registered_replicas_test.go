package replication

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// The live output shape, verified on MySQL 8.0.46, 8.4.9, and 9.7.1: the two
// identity columns carry a capital I. The manual's own example prints
// Server_id and Source_id, and no server spells them that way
// (docs/COMPAT.md entry 22).
func registeredReplicasColumns() []string {
	return []string{"Server_Id", "Host", "Port", "Source_Id", "Replica_UUID"}
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
			// A replica started without --report-host and without
			// --report-port. It registers all the same, with an empty Host
			// and its actual listening port — the live shape on every
			// supported version.
			[]byte("3"),
			[]byte(""),
			int64(3306),
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
	// A row whose Host is empty is data the server returned, not a row to
	// discard: the replica is registered and the source knows nothing about
	// where to reach it. Dropping it would report a smaller topology than the
	// server described.
	if got[1].Host != "" {
		t.Errorf("RegisteredReplicas()[1].Host = %q, want the empty host the server returned", got[1].Host)
	}
	if got[1].Port != 3306 {
		t.Errorf("RegisteredReplicas()[1].Port = %d, want 3306 — the port the server reported", got[1].Port)
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
		[]string{"Server_Id", "Host", "Port", "Source_Id"},
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
