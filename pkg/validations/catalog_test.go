package validations

import (
	"regexp"
	"slices"
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
