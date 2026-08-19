package replication

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// Two distinct concrete error types, so the both-fail path can be proven to
// preserve each cause's identity rather than merely its message.
type primaryCause struct{ s string }

func (c primaryCause) Error() string { return c.s }

type fallbackCause struct{ s string }

func (c fallbackCause) Error() string { return c.s }

func binaryLogStatusColumns(extra ...string) []string {
	columns := []string{
		"File",
		"Position",
		"Binlog_Do_DB",
		"Binlog_Ignore_DB",
		"Executed_Gtid_Set",
	}

	return append(columns, extra...)
}

func binaryLogStatusRow() []driver.Value {
	return []driver.Value{
		[]byte("binlog.000004"),
		int64(157),
		[]byte(""),
		[]byte(""),
		[]byte("3E11FA47-71CA-11E1-9E33-C80AA9429562:1-5"),
	}
}

func wantBinaryLogStatus() BinaryLogStatus {
	return BinaryLogStatus{
		File:            "binlog.000004",
		Position:        157,
		BinlogDoDB:      "",
		BinlogIgnoreDB:  "",
		ExecutedGTIDSet: "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-5",
	}
}

func TestBinaryLogStatusFallback(t *testing.T) {
	t.Parallel()

	t.Run("primary answers", func(t *testing.T) {
		t.Parallel()

		var log []string
		db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
			Match:   "SHOW BINARY LOG STATUS",
			Columns: binaryLogStatusColumns(),
			Rows:    [][]driver.Value{binaryLogStatusRow()},
		})
		defer db.Close()

		got, err := NewInspector(db).BinaryLogStatus(t.Context())
		if err != nil {
			t.Fatalf("BinaryLogStatus() returned error %v, want nil", err)
		}
		if got == nil {
			t.Fatal("BinaryLogStatus() = nil, want a status")
		}
		if want := wantBinaryLogStatus(); *got != want {
			t.Errorf("BinaryLogStatus() = %#v, want %#v", *got, want)
		}

		// The fallback is never issued when the primary statement answers.
		if len(log) != 1 {
			t.Fatalf("issued %d statements (%q), want exactly 1", len(log), log)
		}
		if log[0] != "SHOW BINARY LOG STATUS" {
			t.Errorf("statement = %q, want %q", log[0], "SHOW BINARY LOG STATUS")
		}
	})

	t.Run("fallback answers after primary fails", func(t *testing.T) {
		t.Parallel()

		var log []string
		db := testsupport.OpenScriptedDBWithLog(&log,
			testsupport.ScriptedQuery{
				Match: "SHOW BINARY LOG STATUS",
				Err:   primaryCause{s: "You have an error in your SQL syntax"},
			},
			testsupport.ScriptedQuery{
				Match:   "SHOW MASTER STATUS",
				Columns: binaryLogStatusColumns(),
				Rows:    [][]driver.Value{binaryLogStatusRow()},
			},
		)
		defer db.Close()

		got, err := NewInspector(db).BinaryLogStatus(t.Context())
		if err != nil {
			t.Fatalf("BinaryLogStatus() returned error %v, want nil; success supersedes", err)
		}
		if got == nil {
			t.Fatal("BinaryLogStatus() = nil, want the fallback result")
		}
		if want := wantBinaryLogStatus(); *got != want {
			t.Errorf("BinaryLogStatus() = %#v, want %#v", *got, want)
		}

		if len(log) != 2 {
			t.Fatalf("issued %d statements (%q), want exactly 2", len(log), log)
		}
		if log[0] != "SHOW BINARY LOG STATUS" {
			t.Errorf("first statement = %q, want %q", log[0], "SHOW BINARY LOG STATUS")
		}
		if log[1] != "SHOW MASTER STATUS" {
			t.Errorf("second statement = %q, want %q", log[1], "SHOW MASTER STATUS")
		}
	})

	t.Run("both fail", func(t *testing.T) {
		t.Parallel()

		primary := primaryCause{s: "primary refused"}
		fallback := fallbackCause{s: "fallback refused"}
		db := testsupport.OpenScriptedDB(
			testsupport.ScriptedQuery{Match: "SHOW BINARY LOG STATUS", Err: primary},
			testsupport.ScriptedQuery{Match: "SHOW MASTER STATUS", Err: fallback},
		)
		defer db.Close()

		got, err := NewInspector(db).BinaryLogStatus(t.Context())
		if err == nil {
			t.Fatalf("BinaryLogStatus() = %#v, nil; want both failures preserved", got)
		}
		if got != nil {
			t.Errorf("BinaryLogStatus() = %#v, want nil alongside the error", got)
		}

		// Both causes remain reachable: either can be the decisive one.
		if !errors.Is(err, primary) {
			t.Errorf("errors.Is(%v, primaryCause) = false, want true", err)
		}
		if !errors.Is(err, fallback) {
			t.Errorf("errors.Is(%v, fallbackCause) = false, want true", err)
		}

		var gotPrimary primaryCause
		if !errors.As(err, &gotPrimary) {
			t.Errorf("errors.As(%v, *primaryCause) = false, want true", err)
		} else if gotPrimary != primary {
			t.Errorf("errors.As extracted %#v, want %#v", gotPrimary, primary)
		}

		var gotFallback fallbackCause
		if !errors.As(err, &gotFallback) {
			t.Errorf("errors.As(%v, *fallbackCause) = false, want true", err)
		} else if gotFallback != fallback {
			t.Errorf("errors.As extracted %#v, want %#v", gotFallback, fallback)
		}

		var opErr *OpError
		if !errors.As(err, &opErr) {
			t.Fatalf("errors.As(%v, *OpError) = false, want true", err)
		}
		if opErr.Op != opBinaryLogStatus {
			t.Errorf("OpError.Op = %q, want %q", opErr.Op, opBinaryLogStatus)
		}

		// Each cause is named by the statement that produced it.
		message := err.Error()
		for _, statement := range []string{"SHOW BINARY LOG STATUS", "SHOW MASTER STATUS"} {
			if !strings.Contains(message, statement) {
				t.Errorf("error message %q does not name %q", message, statement)
			}
		}
	})
}

func TestBinaryLogStatusAbsent(t *testing.T) {
	t.Parallel()

	// Binary logging disabled: the statement succeeds and returns no rows.
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "SHOW BINARY LOG STATUS",
		Columns: binaryLogStatusColumns(),
		Rows:    nil,
	})
	defer db.Close()

	got, err := NewInspector(db).BinaryLogStatus(t.Context())
	if err != nil {
		t.Fatalf("BinaryLogStatus() returned error %v, want nil; absence is provable", err)
	}
	if got != nil {
		t.Errorf("BinaryLogStatus() = %#v, want nil", got)
	}
}

func TestBinaryLogStatusMissingColumn(t *testing.T) {
	t.Parallel()

	const dropped = "Executed_Gtid_Set"
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:   "SHOW BINARY LOG STATUS",
		Columns: []string{"File", "Position", "Binlog_Do_DB", "Binlog_Ignore_DB"},
		Rows: [][]driver.Value{{
			[]byte("binlog.000004"),
			int64(157),
			[]byte(""),
			[]byte(""),
		}},
	})
	defer db.Close()

	got, err := NewInspector(db).BinaryLogStatus(t.Context())
	if err == nil {
		t.Fatalf("BinaryLogStatus() = %#v, nil; want a missing-column error", got)
	}
	if !errors.Is(err, errMissingColumn) {
		t.Errorf("errors.Is(%v, errMissingColumn) = false, want true", err)
	}
	assertOpError(t, err, opBinaryLogStatus, "", dropped)
}

func TestBinaryLogStatusCloseErr(t *testing.T) {
	t.Parallel()

	// This fact reads one row and returns before the result set is exhausted,
	// so the driver's close error reaches it only through the deferred close.
	// A close failure supersedes the decoded value: the fact could not be
	// completed, so it reports an error rather than a value plus an error.
	cause := errors.New("close failed")
	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match:    "SHOW BINARY LOG STATUS",
		Columns:  binaryLogStatusColumns(),
		Rows:     [][]driver.Value{binaryLogStatusRow()},
		CloseErr: cause,
	})
	defer db.Close()

	got, err := NewInspector(db).BinaryLogStatus(t.Context())
	if err == nil {
		t.Fatalf("BinaryLogStatus() = %#v, nil; want the scripted close failure", got)
	}
	if got != nil {
		t.Errorf("BinaryLogStatus() = %#v, want nil alongside the error", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false, want true", err)
	}
	assertOpError(t, err, opBinaryLogStatus, "", "")
}

func TestBinaryLogStatusJSONContract(t *testing.T) {
	t.Parallel()

	status := BinaryLogStatus{
		File:            "binlog.000004",
		Position:        157,
		BinlogDoDB:      "shop",
		BinlogIgnoreDB:  "mysql",
		ExecutedGTIDSet: "uuid:1-5",
	}

	const want = `{"file":"binlog.000004","position":157,"binlog_do_db":"shop",` +
		`"binlog_ignore_db":"mysql","executed_gtid_set":"uuid:1-5"}`

	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("json.Marshal returned error %v, want nil", err)
	}
	if string(encoded) != want {
		t.Errorf("json.Marshal = %s, want %s", encoded, want)
	}
}
