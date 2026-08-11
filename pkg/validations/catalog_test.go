package validations

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// specCatalogIDs is the check catalog published in the design spec, §5.4. That
// table is prose; this slice is the machine-checked copy of it.
//
// The IDs here are deliberately written as literals while Catalog() is built
// from the ID constants, so this test cross-checks the constants' values rather
// than restating them. A check implemented without a catalog entry, an entry
// that drifts from the spec, or a constant whose value is quietly edited all
// fail here — which matters because the phase-3 goarchive port promises a
// byte-stable mapping onto these strings.
var specCatalogIDs = []string{
	"CASCADE_RULES",
	"FK_CLOSURE",
	"FK_INDEXED",
	"FK_METADATA_VISIBILITY",
	"INVISIBLE_COLUMNS",
	"PK_EXISTS",
	"PK_INTEGER_TYPE",
	"PK_MATCHES_EXPECTED",
	"PK_NAME_CASE",
	"PK_SINGLE_COLUMN",
	"SCHEMA_PRIVILEGES",
	"STORAGE_ENGINE",
	"TABLES_EXIST",
	"TABLE_PRIVILEGES",
	"TRIGGERS_PRESENT",
}

func TestCatalog(t *testing.T) {
	t.Parallel()

	idPattern := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	got := Catalog()

	if len(got) != len(specCatalogIDs) {
		t.Errorf("Catalog() has %d entries, want %d", len(got), len(specCatalogIDs))
	}

	seen := make(map[string]bool, len(got))
	for _, entry := range got {
		if !idPattern.MatchString(entry.ID) {
			t.Errorf("Catalog() ID %q does not match %s", entry.ID, idPattern)
		}
		if seen[entry.ID] {
			t.Errorf("Catalog() contains %q more than once", entry.ID)
		}
		if entry.Rationale == "" {
			t.Errorf("Catalog() entry %q has no rationale", entry.ID)
		}
		if entry.Status != StatusImplemented && entry.Status != StatusDeferred {
			t.Errorf("Catalog() entry %q has status %d, want implemented or deferred", entry.ID, entry.Status)
		}
		if entry.Status == StatusDeferred {
			t.Errorf("Catalog() entry %q remains deferred after phase 1c", entry.ID)
		}
		if entry.Phase == "" {
			t.Errorf("Catalog() entry %q names no phase", entry.ID)
		}
		seen[entry.ID] = true
	}

	for _, id := range specCatalogIDs {
		if !seen[id] {
			t.Errorf("Catalog() is missing %q, which design section 5.4 lists", id)
		}
	}
}

func TestFKIndexedRationaleDescribesSourceInvariant(t *testing.T) {
	t.Parallel()

	entry, ok := LookupCheck(IDFKIndexed)
	if !ok {
		t.Fatalf("LookupCheck(%q) reported no such check", IDFKIndexed)
	}
	lower := strings.ToLower(entry.Rationale)
	for _, obsolete := range []string{"full scan", "slow", "performance"} {
		if strings.Contains(lower, obsolete) {
			t.Errorf("FK_INDEXED rationale %q retains obsolete performance claim %q", entry.Rationale, obsolete)
		}
	}
	if !strings.Contains(lower, "mysql guarantees") {
		t.Errorf("FK_INDEXED rationale %q does not state the MySQL invariant", entry.Rationale)
	}
}

func TestCatalogIsSortedByID(t *testing.T) {
	t.Parallel()

	ids := make([]string, 0, len(Catalog()))
	for _, entry := range Catalog() {
		ids = append(ids, entry.ID)
	}

	if !slices.IsSorted(ids) {
		t.Errorf("Catalog() is not sorted by ID: %v", ids)
	}
}

func TestCatalogReturnsIndependentSlices(t *testing.T) {
	t.Parallel()

	first := Catalog()
	if len(first) == 0 {
		t.Fatal("Catalog() returned no entries")
	}
	want := first[0].ID
	first[0].ID = "MUTATED_BY_CALLER"

	if got := Catalog()[0].ID; got != want {
		t.Errorf("after a caller mutated a previous result, Catalog()[0].ID = %q, want %q; "+
			"the catalog shares state across calls", got, want)
	}
}

func TestCheckInfoMetadataContract(t *testing.T) {
	t.Parallel()

	want := CheckInfo{
		ID:        IDPKSingleColumn,
		Rationale: "A composite key cannot be filtered by one column without over-matching rows outside the intended set.",
		Status:    StatusImplemented,
		Phase:     "1b",
	}
	got, ok := LookupCheck(IDPKSingleColumn)
	if !ok {
		t.Fatalf("LookupCheck(%q) reported no such check", IDPKSingleColumn)
	}
	if got != want {
		t.Errorf("LookupCheck(%q) = %#v, want %#v", IDPKSingleColumn, got, want)
	}
}

// TestCheckStatusString asserts both declared values render as their word,
// and that the zero value — which CheckStatus does not declare a member for —
// renders as CheckStatus(0) rather than as unknownEnum. The second assertion
// is the whole point: unknownEnum is reserved for types with a declared
// unknown member, and CheckStatus(0) means "not a status", which
// unknownEnum would misstate as a valid, nameable state.
func TestCheckStatusString(t *testing.T) {
	t.Parallel()

	if got := StatusImplemented.String(); got != "implemented" {
		t.Errorf("StatusImplemented.String() = %q, want %q", got, "implemented")
	}
	if got := StatusDeferred.String(); got != "deferred" {
		t.Errorf("StatusDeferred.String() = %q, want %q", got, "deferred")
	}

	var zero CheckStatus
	want := "CheckStatus(0)"
	if got := zero.String(); got != want {
		t.Errorf("CheckStatus(0).String() = %q, want %q", got, want)
	}
	if got := zero.String(); got == unknownEnum {
		t.Errorf("CheckStatus(0).String() = %q; unknownEnum is reserved for a "+
			"declared zero-value member, which CheckStatus does not have", got)
	}
}

func TestLookupCheck(t *testing.T) {
	t.Parallel()

	entry, ok := LookupCheck(IDPKSingleColumn)
	if !ok {
		t.Fatalf("LookupCheck(%q) reported no such check", IDPKSingleColumn)
	}
	if entry.ID != IDPKSingleColumn {
		t.Errorf("LookupCheck(%q).ID = %q, want %q", IDPKSingleColumn, entry.ID, IDPKSingleColumn)
	}
	if entry.Rationale == "" {
		t.Errorf("LookupCheck(%q) returned an entry with no rationale", IDPKSingleColumn)
	}

	for _, id := range []string{"NO_SUCH_CHECK", "", "pk_single_column"} {
		if _, ok := LookupCheck(id); ok {
			t.Errorf("LookupCheck(%q) reported a check that does not exist", id)
		}
	}
}

func TestLookupCheckDoesNotAllocate(t *testing.T) {
	t.Parallel()

	allocations := testing.AllocsPerRun(100, func() {
		entry, ok := LookupCheck(IDTriggersPresent)
		if !ok || entry.ID != IDTriggersPresent {
			t.Fatalf("LookupCheck(%q) = (%#v, %t)", IDTriggersPresent, entry, ok)
		}
	})
	if allocations != 0 {
		t.Errorf("LookupCheck() allocations = %v, want 0", allocations)
	}
}
