package list

import (
	"bytes"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

func facetResultWithGaps(complete bool, gaps map[string]cutting_garden_plugins.FacetGap) cutting_garden_plugins.FacetResult {
	return cutting_garden_plugins.FacetResult{
		Summary: cutting_garden_plugins.FacetSummary{
			"account": {"Checking": 4},
			"payee":   {"Grocer": 2, "Landlord": 1},
		},
		Complete:   complete,
		Incomplete: gaps,
	}
}

// `list --facets` says WHICH dimensions are not exhaustive when the plugin
// named them (RFC 0012 §5.1, cutting-garden#171), and that the rest are exact.
func TestWriteFacetsText_NamesThePartialDimensions(t *testing.T) {
	var buf bytes.Buffer
	err := writeFacetsText(&buf, facetResultWithGaps(false, map[string]cutting_garden_plugins.FacetGap{
		"payee": {Reason: "top 20 of 31 payees"},
		"label": {Declined: true, Reason: "too many repositories to sweep"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "(partial — label: declined (too many repositories to sweep); " +
		"payee: partial (top 20 of 31 payees); other dimensions are exact)\n"
	if !strings.HasSuffix(buf.String(), want) {
		t.Errorf("output = %q, want it to end with %q", buf.String(), want)
	}
}

// With no per-dimension attribution the old whole-summary marker is unchanged.
func TestWriteFacetsText_UnattributedPartialKeepsTheOldMarker(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFacetsText(&buf, facetResultWithGaps(false, nil)); err != nil {
		t.Fatal(err)
	}
	want := "(partial — summary does not cover the whole subtree)\n"
	if !strings.HasSuffix(buf.String(), want) {
		t.Errorf("output = %q, want it to end with %q", buf.String(), want)
	}
}

func TestWriteFacetsText_CompleteHasNoMarker(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFacetsText(&buf, facetResultWithGaps(true, nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "partial") {
		t.Errorf("output = %q, want no partial marker", buf.String())
	}
}

// A result that claims Complete beside a named gap is shown as partial.
func TestWriteFacetsText_AGapOverridesAClaimedComplete(t *testing.T) {
	var buf bytes.Buffer
	err := writeFacetsText(&buf, facetResultWithGaps(true, map[string]cutting_garden_plugins.FacetGap{
		"payee": {Reason: "capped"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "payee: partial (capped)") {
		t.Errorf("output = %q, want the payee gap shown", buf.String())
	}
}

func TestWriteFacetsJSON_CarriesTheGapsAndOmitsThemWhenAbsent(t *testing.T) {
	var gapped bytes.Buffer
	if err := writeFacetsJSON(&gapped, facetResultWithGaps(false, map[string]cutting_garden_plugins.FacetGap{
		"payee": {Reason: "top 20 of 31 payees"},
	})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gapped.String(), `"complete":false`) ||
		!strings.Contains(gapped.String(), `"incomplete":{"payee":{"reason":"top 20 of 31 payees"}}`) {
		t.Errorf("json = %s, want complete:false and the payee gap", gapped.String())
	}

	var clean bytes.Buffer
	if err := writeFacetsJSON(&clean, facetResultWithGaps(true, nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clean.String(), "incomplete") {
		t.Errorf("json = %s, want no incomplete key", clean.String())
	}
}
