package caldav

import (
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestCaseFoldCodec_PresentsLowercaseWritesUppercase pins FDR 0025's case-fold
// codec (native tags slice 1.5 E): Format presents the stored STATUS lowercased,
// and Parse folds every write UP to canonical RFC 5545 uppercase — never
// persisting lowercase. An observed OUT-OF-ENUM stored value still presents
// (lowercased) and round-trips to ITS uppercase on write; no test fixture
// carries one (the testserver stores only RFC enum values), so this is the unit
// pin for that behavior.
func TestCaseFoldCodec_PresentsLowercaseWritesUppercase(t *testing.T) {
	codec := caseFoldCodec{Field: cutting_garden_plugins.UnifiedField{Key: listingFieldStatus}}

	cases := []struct{ stored, presented string }{
		{"NEEDS-ACTION", "needs-action"},
		{"COMPLETED", "completed"},
		{"X-CUSTOM", "x-custom"}, // out-of-enum: still presents, lowercased
	}
	for _, tc := range cases {
		presented, err := codec.Format(map[string]any{listingFieldStatus: tc.stored})
		if err != nil {
			t.Fatalf("Format(%q): %v", tc.stored, err)
		}
		got := presented[listingFieldStatus]
		if len(got) != 1 || got[0] != tc.presented {
			t.Errorf("Format(%q) = %v, want [%q]", tc.stored, got, tc.presented)
		}

		updates, err := codec.Parse(
			map[string][]string{listingFieldStatus: {tc.presented}}, nil,
		)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.presented, err)
		}
		if updates[listingFieldStatus] != tc.stored {
			t.Errorf("Parse(%q) = %v, want %q (folds up to ITS uppercase)",
				tc.presented, updates[listingFieldStatus], tc.stored)
		}
	}
}

// TestCaseFoldCodec_AbsentValueContributesNothing mirrors IdentityCodec's
// absence semantics: no stored value means no atom, and no edit means no
// stored update.
func TestCaseFoldCodec_AbsentValueContributesNothing(t *testing.T) {
	codec := caseFoldCodec{Field: cutting_garden_plugins.UnifiedField{Key: listingFieldStatus}}

	presented, err := codec.Format(map[string]any{})
	if err != nil {
		t.Fatalf("Format(empty): %v", err)
	}
	if len(presented) != 0 {
		t.Errorf("Format(empty) = %v, want no atoms", presented)
	}

	updates, err := codec.Parse(map[string][]string{}, nil)
	if err != nil {
		t.Fatalf("Parse(empty): %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("Parse(empty) = %v, want no updates", updates)
	}
}

// TestStatusDimensionFoldCase pins the FoldCase declaration flowing from the
// unified field into the derived FacetDimension — the switch that makes every
// framework matching surface (filter predicates, closed-domain validation, the
// trellis field compare) fold both sides for status — and the TerminalValues
// spelling in the PRESENTED (lowercase) domain.
func TestStatusDimensionFoldCase(t *testing.T) {
	dims := Plugin{}.DescribeFacets()
	dim, ok := cutting_garden_plugins.FindFacetDimension(dims, facetStatus)
	if !ok {
		t.Fatal("status dimension not declared")
	}
	if !dim.FoldCase {
		t.Error("status FacetDimension.FoldCase = false, want true")
	}
	want := []string{"completed", "cancelled"}
	if !slices.Equal(dim.TerminalValues, want) {
		t.Errorf("status TerminalValues = %v, want %v (presented domain)",
			dim.TerminalValues, want)
	}
}

// TestFacetFilter_FoldCaseMatchesBothSpellings pins the matching-layer rule
// end to end at the filter surface: a Validate-armed predicate against the
// FoldCase status dimension matches the presented lowercase facet value under
// EITHER spelling — `status=completed` and the legacy `status=COMPLETED`.
func TestFacetFilter_FoldCaseMatchesBothSpellings(t *testing.T) {
	dims := Plugin{}.DescribeFacets()
	facets := map[string][]cutting_garden_plugins.FacetValue{
		facetStatus: {{Key: "completed"}},
	}
	for _, spelling := range []string{"completed", "COMPLETED"} {
		filter, err := cutting_garden_plugins.ParseFacetFilter("status=" + spelling)
		if err != nil {
			t.Fatalf("ParseFacetFilter(status=%s): %v", spelling, err)
		}
		if err := filter.Validate(dims); err != nil {
			t.Fatalf("Validate(status=%s): %v", spelling, err)
		}
		if !filter.Matches(facets) {
			t.Errorf("status=%s did not match the presented \"completed\" facet", spelling)
		}
	}
}
