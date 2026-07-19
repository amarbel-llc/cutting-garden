package cutting_garden_plugins

import (
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
