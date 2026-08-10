package validations

import (
	"reflect"
	"strconv"
	"testing"
)

// distinctNames returns count distinct, representable names.
func distinctNames(count int) []string {
	names := make([]string, 0, count)
	for index := range count {
		names = append(names, "t"+strconv.Itoa(index))
	}

	return names
}

func TestNarrowNamesDeduplicatesPreservingFirstOccurrence(t *testing.T) {
	t.Parallel()

	// The values matter, not only the count. A predicate carrying {"a","a"}
	// emits the same IN (?,?) as one carrying {"a","b"} and draws the same
	// rows from a scripted driver, so only this assertion distinguishes them.
	got, ok := narrowNames([]string{"a", "b", "a"}, 0)
	if !ok {
		t.Fatal("narrowNames refused three representable names")
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("narrowNames = %#v, want %#v", got, want)
	}
}

func TestNarrowNamesDeduplicationDoesNotSort(t *testing.T) {
	t.Parallel()

	// First-occurrence order, not sorted order. IN (...) is a set, so sorting
	// would not change any result — which is exactly why a later rewrite to
	// slices.Sort + slices.Compact would go unnoticed without this test.
	got, ok := narrowNames([]string{"b", "a", "b"}, 0)
	if !ok {
		t.Fatal("narrowNames refused three representable names")
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("narrowNames = %#v, want %#v", got, want)
	}
}

func TestNarrowNamesBoundIsInclusiveAtTheCeiling(t *testing.T) {
	t.Parallel()

	// Below, at, and above. The boundary is where an off-by-one swaps
	// narrowing for the fallback, and neither is visible in results.
	for _, testCase := range []struct {
		name      string
		count     int
		fixedArgs int
		want      bool
	}{
		{"below, no fixed args", maxStatementParameters - 1, 0, true},
		{"at, no fixed args", maxStatementParameters, 0, true},
		{"above, no fixed args", maxStatementParameters + 1, 0, false},
		{"below, one fixed arg", maxStatementParameters - 2, 1, true},
		{"at, one fixed arg", maxStatementParameters - 1, 1, true},
		{"above, one fixed arg", maxStatementParameters, 1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			params, ok := narrowNames(distinctNames(testCase.count), testCase.fixedArgs)
			if ok != testCase.want {
				t.Fatalf("narrowNames(%d names, %d fixed) ok = %t, want %t",
					testCase.count, testCase.fixedArgs, ok, testCase.want)
			}
			if ok && len(params) != testCase.count {
				t.Errorf("narrowed %d params, want %d", len(params), testCase.count)
			}
			if !ok && params != nil {
				t.Errorf("refused call returned %d params, want nil", len(params))
			}
		})
	}
}

func TestNarrowNamesBoundCountsDistinctNamesNotRequests(t *testing.T) {
	t.Parallel()

	// Dedup runs before the bound. A caller repeating one table far past the
	// ceiling must still narrow, because one distinct name is one parameter.
	// Reversing the order would fall back here, which no result would reveal.
	names := make([]string, 0, maxStatementParameters*2)
	for range maxStatementParameters * 2 {
		names = append(names, "only")
	}

	got, ok := narrowNames(names, 1)
	if !ok {
		t.Fatal("narrowNames fell back on one distinct name; the bound ran before dedup")
	}
	// Reported by length rather than by value: this slice is 131070 elements
	// when the deduplication is missing, and %#v on it buries the run.
	if want := []string{"only"}; !reflect.DeepEqual(got, want) {
		t.Errorf("narrowNames returned %d params, want exactly %#v", len(got), want)
	}
}

func TestNarrowNamesRefusesUnrepresentableNames(t *testing.T) {
	t.Parallel()

	// Neither is a validation error: the caller falls back to an unnarrowed
	// query, which returns absence and a nil error exactly as the pre-existing
	// contract promises. See docs/COMPAT.md entry 8.
	for _, testCase := range []struct {
		name  string
		names []string
	}{
		{"supplementary character", []string{"ordinary", "supp_\U00010000"}},
		{"invalid utf-8", []string{"ordinary", "bad_\xff"}},
		{"supplementary alone", []string{"\U0001F600"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			params, ok := narrowNames(testCase.names, 0)
			if ok {
				t.Fatalf("narrowNames(%q) narrowed; want fallback", testCase.names)
			}
			if params != nil {
				t.Errorf("refused call returned %#v, want nil", params)
			}
		})
	}
}

func TestNarrowNamesInvalidUTF8IsNotCaughtByARuneComparison(t *testing.T) {
	t.Parallel()

	// Go's for range decodes each invalid byte as utf8.RuneError — U+FFFD,
	// below U+FFFF — so a rune loop alone accepts this name. utf8.ValidString
	// must come first, and this test is what fails if it is removed.
	for _, char := range "\xff" {
		if char > '￿' {
			t.Fatalf("premise broken: invalid byte decoded as %U, above U+FFFF", char)
		}
	}

	if _, ok := narrowNames([]string{"\xff"}, 0); ok {
		t.Error("narrowNames accepted invalid UTF-8 that a rune comparison cannot catch")
	}
}

func TestNarrowNamesAcceptsAnEmptyRequest(t *testing.T) {
	t.Parallel()

	// Every caller returns early on an empty request, so this only pins that
	// the helper does not decide to fall back on one.
	got, ok := narrowNames(nil, 1)
	if !ok {
		t.Fatal("narrowNames refused an empty request")
	}
	if len(got) != 0 {
		t.Errorf("narrowNames(nil) = %#v, want empty", got)
	}
}

func TestRequestedObjectsDeduplicatesAndPreservesFirstOccurrence(t *testing.T) {
	t.Parallel()

	query, args, ok := requestedObjects("shop", []string{"beta", "alpha", "beta"})
	if !ok {
		t.Fatal("requestedObjects refused representable names below its bound")
	}
	wantQuery := `(SELECT ? AS TABLE_SCHEMA, ? AS TABLE_NAME
	UNION ALL SELECT ?, ?) AS requested`
	if query != wantQuery {
		t.Errorf("query = %q, want %q", query, wantQuery)
	}
	wantArgs := []any{"shop", "beta", "shop", "alpha"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestRequestedObjectsUsesItsMeasuredBound(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		count int
		want  bool
	}{
		{"at the bound", maxPointLookupTables, true},
		{"above the bound", maxPointLookupTables + 1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			query, args, ok := requestedObjects("shop", distinctNames(testCase.count))
			if ok != testCase.want {
				t.Fatalf("requestedObjects(%d names) ok = %t, want %t",
					testCase.count, ok, testCase.want)
			}
			if ok {
				if query == "" {
					t.Error("accepted call returned an empty query")
				}
				if len(args) != testCase.count*2 {
					t.Errorf("accepted call returned %d args, want %d", len(args), testCase.count*2)
				}
			} else if query != "" || args != nil {
				t.Errorf("refused call returned query %q and %d args", query, len(args))
			}
		})
	}
}

func TestRequestedObjectsCountsDistinctNamesAtItsBound(t *testing.T) {
	t.Parallel()

	names := make([]string, maxPointLookupTables*2)
	for index := range names {
		names[index] = "only"
	}

	_, args, ok := requestedObjects("shop", names)
	if !ok {
		t.Fatal("requestedObjects refused one distinct name repeated past its bound")
	}
	if want := []any{"shop", "only"}; !reflect.DeepEqual(args, want) {
		t.Errorf("args = %#v, want %#v", args, want)
	}
}

func TestRequestedObjectsRefusesUnrepresentableInput(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		schema string
		tables []string
	}{
		{"schema supplementary character", "shop_\U00010000", []string{"orders"}},
		{"schema invalid utf-8", "shop_\xff", []string{"orders"}},
		{"table supplementary character", "shop", []string{"orders_\U00010000"}},
		{"table invalid utf-8", "shop", []string{"orders_\xff"}},
		{"empty tables", "shop", nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			query, args, ok := requestedObjects(testCase.schema, testCase.tables)
			if ok {
				t.Fatalf("requestedObjects(%q, %q) succeeded", testCase.schema, testCase.tables)
			}
			if query != "" || args != nil {
				t.Errorf("refused call returned query %q and %#v", query, args)
			}
		})
	}
}
