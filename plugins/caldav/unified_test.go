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

// TestCategoriesCodec_FormatProducesTagSet pins G6 (native tags slice 2):
// Format presents the stored CATEGORIES list verbatim under the categories key,
// in STORED order, tolerating both the native []string shape and the []any a
// JSON enrichment round-trip produces (the wire/MCP path). Absent or empty
// CATEGORIES contributes nothing (absent key), matching the other codecs.
func TestCategoriesCodec_FormatProducesTagSet(t *testing.T) {
	c := categoriesCodec{}

	presented, err := c.Format(map[string]any{
		listingFieldCategories: []string{"work", "errand", "planning, misc"},
	})
	if err != nil {
		t.Fatalf("Format([]string): %v", err)
	}
	want := []string{"work", "errand", "planning, misc"}
	if !slices.Equal(presented[facetCategories], want) {
		t.Errorf("Format([]string) = %v, want %v (stored order)",
			presented[facetCategories], want)
	}

	presented, err = c.Format(map[string]any{
		listingFieldCategories: []any{"work", "urgent"},
	})
	if err != nil {
		t.Fatalf("Format([]any): %v", err)
	}
	if !slices.Equal(presented[facetCategories], []string{"work", "urgent"}) {
		t.Errorf("Format([]any) = %v, want [work urgent] (JSON round-trip shape)",
			presented[facetCategories])
	}

	for name, stored := range map[string]map[string]any{
		"absent": {},
		"empty":  {listingFieldCategories: []string{}},
	} {
		presented, err := c.Format(stored)
		if err != nil {
			t.Fatalf("Format(%s): %v", name, err)
		}
		if len(presented) != 0 {
			t.Errorf("Format(%s) = %v, want no keys", name, presented)
		}
	}
}

// TestCategoriesCodec_FormatAgreesWithFacetValues pins the G6 agreement: the
// facet VALUES the counting path (facetsFromView) emits for categories equal
// Format's presented tag set as SETS, for an object carrying multiple tags
// including a comma-bearing one (parseCategories is escape-aware, so
// `planning\, misc` is ONE tag). Both sides read the same parse, so any drift
// between counting and presenting surfaces here.
func TestCategoriesCodec_FormatAgreesWithFacetValues(t *testing.T) {
	raw := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:t1\n" +
		"SUMMARY:Tagged\nSTATUS:NEEDS-ACTION\n" +
		"CATEGORIES:work,errand,planning\\, misc\n" +
		"END:VTODO\nEND:VCALENDAR\n"
	view, ok := parseObjectView(raw)
	if !ok {
		t.Fatal("parseObjectView failed on the tagged VTODO fixture")
	}

	var fromFacets []string
	for _, v := range facetsFromView(view)[facetCategories] {
		fromFacets = append(fromFacets, v.Key)
	}

	presented, err := categoriesCodec{}.Format(listingFieldsOf(view))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	fromFormat := slices.Clone(presented[facetCategories])

	if len(fromFacets) != 3 || !slices.Contains(fromFacets, "planning, misc") {
		t.Fatalf("facet values = %v, want 3 tags including the comma-bearing one", fromFacets)
	}
	slices.Sort(fromFacets)
	slices.Sort(fromFormat)
	if !slices.Equal(fromFacets, fromFormat) {
		t.Errorf("counting path emits %v but Format presents %v; the two must agree as sets",
			fromFacets, fromFormat)
	}
}

// TestUnifiedFieldSetsValidate pins caldav's real declaration against the SDK's
// cross-codec invariants — in particular G6's one-FieldTag-per-type rule, which
// PresentUnifiedTags relies on. There is no framework-side enforcement point,
// so this unit pin IS the validation site for caldav.
func TestUnifiedFieldSetsValidate(t *testing.T) {
	if err := cutting_garden_plugins.ValidateUnifiedFieldSets(unifiedFieldSets()); err != nil {
		t.Fatalf("ValidateUnifiedFieldSets(unifiedFieldSets()) = %v, want nil", err)
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
