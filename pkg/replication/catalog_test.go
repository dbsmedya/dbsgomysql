package replication

import (
	"testing"
)

func TestCatalogSortedAndFresh(t *testing.T) {
	t.Parallel()

	want := []string{
		IDBinaryLogEnabled,
		IDGTIDModeOn,
		IDReplicationChannelsRunning,
		IDReplicationConfigured,
		IDSecondsBehindSourceWithin,
	}

	got := Catalog()
	if len(got) != len(want) {
		t.Fatalf("Catalog() returned %d entries, want %d", len(got), len(want))
	}
	for index, info := range got {
		if info.ID != want[index] {
			t.Errorf("Catalog()[%d].ID = %q, want %q", index, info.ID, want[index])
		}
		if info.Status != StatusImplemented {
			t.Errorf("Catalog()[%d].Status = %v, want %v", index, info.Status, StatusImplemented)
		}
		if info.Phase != "v1.1.0" {
			t.Errorf("Catalog()[%d].Phase = %q, want %q", index, info.Phase, "v1.1.0")
		}
		if info.Rationale == "" {
			t.Errorf("Catalog()[%d] (%s) has an empty Rationale", index, info.ID)
		}
	}

	// The catalog is built fresh per call: mutating one result never reaches
	// another caller.
	got[0].ID = "TAMPERED"
	got[0].Rationale = "tampered"

	second := Catalog()
	if second[0].ID != IDBinaryLogEnabled {
		t.Errorf("second Catalog()[0].ID = %q, want %q", second[0].ID, IDBinaryLogEnabled)
	}
	if second[0].Rationale == "tampered" {
		t.Error("second Catalog() returned the mutated rationale; each call must build fresh")
	}
}

func TestLookupCheckExact(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		id    string
		found bool
	}{
		{name: "known id", id: IDGTIDModeOn, found: true},
		{name: "lower case is not folded", id: "gtid_mode_on", found: false},
		{name: "mixed case is not folded", id: "Gtid_Mode_On", found: false},
		{name: "unknown id", id: "NO_SUCH_CHECK", found: false},
		{name: "empty id", id: "", found: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			info, ok := LookupCheck(testCase.id)
			if ok != testCase.found {
				t.Fatalf("LookupCheck(%q) found = %t, want %t", testCase.id, ok, testCase.found)
			}
			if !testCase.found && info.ID != "" {
				t.Errorf("LookupCheck(%q) = %#v, want the zero CheckInfo", testCase.id, info)
			}
		})
	}
}

func TestCheckInfoContract(t *testing.T) {
	t.Parallel()

	// One full entry pinned field by field. The rationale is spec §5's row for
	// GTID_MODE_ON.
	info, ok := LookupCheck(IDGTIDModeOn)
	if !ok {
		t.Fatalf("LookupCheck(%q) found = false, want true", IDGTIDModeOn)
	}

	want := CheckInfo{
		ID: "GTID_MODE_ON",
		Rationale: "Consumers that coordinate by GTID (resume, failover, CDC positioning) " +
			"need ON; in other modes the GTID sets describe only part of the write history.",
		Status: StatusImplemented,
		Phase:  "v1.1.0",
	}
	if info != want {
		t.Errorf("LookupCheck(%q) = %#v, want %#v", IDGTIDModeOn, info, want)
	}
}

func TestCheckStatusContract(t *testing.T) {
	t.Parallel()

	if StatusImplemented != 1 {
		t.Errorf("StatusImplemented = %d, want 1", StatusImplemented)
	}
	if got := StatusImplemented.String(); got != "implemented" {
		t.Errorf("StatusImplemented.String() = %q, want %q", got, "implemented")
	}
	if got := CheckStatus(0).String(); got != "CheckStatus(0)" {
		t.Errorf("CheckStatus(0).String() = %q, want %q", got, "CheckStatus(0)")
	}
	if got := CheckStatus(9).String(); got != "CheckStatus(9)" {
		t.Errorf("CheckStatus(9).String() = %q, want %q", got, "CheckStatus(9)")
	}
}
