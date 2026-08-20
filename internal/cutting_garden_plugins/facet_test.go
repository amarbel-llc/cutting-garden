package cutting_garden_plugins

import (
	"fmt"
	"strings"
	"testing"
)

// validateTestDims is the fixture NodeTypeFacets schema for Validate's
// tests: an open "status" dimension (Values nil, any value accepted) and a
// closed "due_band" dimension (mirroring caldav's real due_band — RFC 0012
// §3, §8), used to pin cutting-garden#161's filter-validation contract.
var validateTestDims = []NodeTypeFacets{{
	Tag: "test-object-v1",
	Dimensions: []FacetDimension{
		{Key: "status", Kind: FacetCategorical},
		{
			Key:  "due_band",
			Kind: FacetNumericBucket,
			Values: []FacetValue{
				{Key: "overdue", Order: 4},
				{Key: "today", Order: 3},
				{Key: "this-week", Order: 2},
				{Key: "later", Order: 1},
			},
		},
	},
}}

// TestFacetFilter_Validate_EmptyFilterAlwaysPasses pins that an empty filter
// never fails validation — there is nothing to check, regardless of dims.
func TestFacetFilter_Validate_EmptyFilterAlwaysPasses(t *testing.T) {
	var f FacetFilter
	if err := f.Validate(validateTestDims); err != nil {
		t.Errorf("empty filter: want nil, got %v", err)
	}
	if err := f.Validate(nil); err != nil {
		t.Errorf("empty filter against nil dims: want nil, got %v", err)
	}
}

// TestFacetFilter_Validate_NoDeclaredSchemaPassesThrough pins that a
// plugin with no declared schema (dims == nil — no FacetDescriber, or one
// declaring zero dimensions) validates any filter unchecked: there is no
// schema to be wrong against, so today's pre-#161 behavior is preserved.
func TestFacetFilter_Validate_NoDeclaredSchemaPassesThrough(t *testing.T) {
	f := FacetFilter{{Dimension: "anything", Value: "goes"}}
	if err := f.Validate(nil); err != nil {
		t.Errorf("filter against a plugin with no declared schema: want nil, got %v", err)
	}
}

// TestFacetFilter_Validate_UndeclaredDimensionErrors pins cutting-garden#161:
// a predicate naming a dimension absent from the declared schema is
// rejected with an actionable error naming the bad key and the declared
// ones — never silently producing an empty match.
func TestFacetFilter_Validate_UndeclaredDimensionErrors(t *testing.T) {
	f := FacetFilter{{Dimension: "bogus", Value: "x"}}
	err := f.Validate(validateTestDims)
	if err == nil {
		t.Fatal("undeclared dimension: want error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"bogus", "status", "due_band"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestFacetFilter_Validate_ClosedDimensionRejectsUnknownValue pins the
// closed-domain half of cutting-garden#161: a value outside a CLOSED
// dimension's declared set is rejected, naming the bad value and the
// dimension's complete valid-values list — the exact ergonomic-study
// finding (a guessed "read=false"-shaped predicate) reproduced generically
// against due_band.
func TestFacetFilter_Validate_ClosedDimensionRejectsUnknownValue(t *testing.T) {
	f := FacetFilter{{Dimension: "due_band", Value: "yesterday"}}
	err := f.Validate(validateTestDims)
	if err == nil {
		t.Fatal("out-of-domain closed-dimension value: want error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"yesterday", "due_band", "overdue", "today", "this-week", "later",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestFacetFilter_Validate_ClosedDimensionAcceptsDeclaredValue pins the
// happy path: a value that IS in the closed dimension's declared set
// validates cleanly.
func TestFacetFilter_Validate_ClosedDimensionAcceptsDeclaredValue(t *testing.T) {
	f := FacetFilter{{Dimension: "due_band", Value: "overdue"}}
	if err := f.Validate(validateTestDims); err != nil {
		t.Errorf("declared closed-dimension value: want nil, got %v", err)
	}
}

// TestFacetFilter_Validate_OpenDimensionAcceptsAnyValue pins that an OPEN
// dimension (Values == nil) is checked only by dimension name — its domain
// is discovered at enumeration, not declared up front, so any value passes.
func TestFacetFilter_Validate_OpenDimensionAcceptsAnyValue(t *testing.T) {
	f := FacetFilter{{Dimension: "status", Value: "anything-goes"}}
	if err := f.Validate(validateTestDims); err != nil {
		t.Errorf("open-dimension value: want nil, got %v", err)
	}
}

// TestFacetFilter_Validate_MultiplePredicatesFirstFailureWins pins that an
// AND-composed filter with one bad predicate among good ones still errors
// (partial validity does not pass the whole filter).
func TestFacetFilter_Validate_MultiplePredicatesFirstFailureWins(t *testing.T) {
	f := FacetFilter{
		{Dimension: "status", Value: "CONFIRMED"},
		{Dimension: "due_band", Value: "next-tuesday"},
	}
	if err := f.Validate(validateTestDims); err == nil {
		t.Fatal("one bad predicate in an AND-composed filter: want error, got nil")
	}
}

// A date-kind predicate prefix-matches by validated shape: =2026 matches the
// year, =2026-08 the month, =2026-08-15 the day; a malformed shape rejects at
// Validate. Non-date dimensions keep exact matching.
func TestFacetFilter_DatePrefixMatching(t *testing.T) {
	dims := []NodeTypeFacets{{Tag: "t", Dimensions: []FacetDimension{
		{Key: "date_start", Kind: FacetDate},
		{Key: "status", Kind: FacetCategorical},
	}}}
	facets := map[string][]FacetValue{
		"date_start": {{Key: "2026-08-15"}},
		"status":     {{Key: "2026"}}, // exact-match control
	}

	for _, val := range []string{"2026", "2026-08", "2026-08-15"} {
		f, err := ParseFacetFilter("date_start=" + val)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Validate(dims); err != nil {
			t.Fatalf("Validate(%q): %v", val, err)
		}
		if !f.Matches(facets) {
			t.Errorf("date_start=%q should prefix-match 2026-08-15", val)
		}
	}

	f, _ := ParseFacetFilter("date_start=2026-09")
	if err := f.Validate(dims); err != nil {
		t.Fatal(err)
	}
	if f.Matches(facets) {
		t.Error("date_start=2026-09 must not match 2026-08-15")
	}

	// Malformed shape rejects loudly at Validate.
	f, _ = ParseFacetFilter("date_start=aug-2026")
	if err := f.Validate(dims); err == nil {
		t.Error("malformed date shape must fail Validate")
	}

	// Non-date dimension: exact only ("202" must not prefix-match "2026").
	f, _ = ParseFacetFilter("status=202")
	if err := f.Validate(dims); err != nil {
		t.Fatal(err)
	}
	if f.Matches(facets) {
		t.Error("categorical predicate must stay exact-match")
	}
}

// TestSortAndLimitContainerBreakdown_OrdersByDescendingCount pins RFC 0012
// §13's ordering rule: the highest-contributing container comes first, so a
// truncated breakdown always keeps the most actionable entries.
func TestSortAndLimitContainerBreakdown_OrdersByDescendingCount(t *testing.T) {
	in := []FacetContainerBreakdown{
		{URI: "a", Count: 2},
		{URI: "b", Count: 9},
		{URI: "c", Count: 5},
	}
	got, truncated := SortAndLimitContainerBreakdown(in)
	if truncated {
		t.Fatal("truncated = true, want false (well under the limit)")
	}
	want := []string{"b", "c", "a"}
	for i, w := range want {
		if got[i].URI != w {
			t.Errorf("got[%d].URI = %q, want %q (full: %+v)", i, got[i].URI, w, got)
		}
	}
}

// TestSortAndLimitContainerBreakdown_TiesBrokenByURI pins the deterministic
// tiebreak: equal counts sort by ascending URI, so the ordering is stable
// across calls/plugins rather than depending on input (fold) order.
func TestSortAndLimitContainerBreakdown_TiesBrokenByURI(t *testing.T) {
	in := []FacetContainerBreakdown{
		{URI: "zeta", Count: 3},
		{URI: "alpha", Count: 3},
		{URI: "mid", Count: 3},
	}
	got, _ := SortAndLimitContainerBreakdown(in)
	want := []string{"alpha", "mid", "zeta"}
	for i, w := range want {
		if got[i].URI != w {
			t.Errorf("got[%d].URI = %q, want %q (full: %+v)", i, got[i].URI, w, got)
		}
	}
}

// TestSortAndLimitContainerBreakdown_TruncatesAtLimit pins the large-fan-out
// bound (RFC 0012 §13, cutting-garden#170's newsblur-285-feeds case): a
// breakdown over the limit is capped, keeps the highest-count entries, and
// reports truncation rather than silently dropping the tail.
func TestSortAndLimitContainerBreakdown_TruncatesAtLimit(t *testing.T) {
	n := FacetContainerBreakdownLimit + 10
	in := make([]FacetContainerBreakdown, n)
	for i := range in {
		// Strictly descending count by construction index, so index 0 has
		// the highest count and is guaranteed to survive truncation.
		in[i] = FacetContainerBreakdown{
			URI:   fmt.Sprintf("container-%02d", i),
			Count: int64(n - i),
		}
	}
	got, truncated := SortAndLimitContainerBreakdown(in)
	if !truncated {
		t.Fatal("truncated = false, want true (over the limit)")
	}
	if len(got) != FacetContainerBreakdownLimit {
		t.Fatalf("len(got) = %d, want %d", len(got), FacetContainerBreakdownLimit)
	}
	// The kept entries are exactly the top FacetContainerBreakdownLimit by
	// count — none of the low-count tail leaked in.
	for _, b := range got {
		if b.Count < 10 {
			t.Errorf("low-count entry survived truncation: %+v", b)
		}
	}
}
