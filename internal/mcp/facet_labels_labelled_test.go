package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// TestAttachLabels_FacetsLabelledPairsKeyLabelAndCount pins cutting-garden#174
// (option b): a labelled dimension is also served as a list of
// {key, label, count} rows beside the untouched facets and labels maps, so a
// consumer needs no second lookup. Rows are ordered by count descending, then
// key; an unresolved key keeps its row without a label.
func TestAttachLabels_FacetsLabelledPairsKeyLabelAndCount(t *testing.T) {
	view := &facetView{Facets: cutting_garden_plugins.FacetSummary{
		"status": {"CONFIRMED": 2},
		"owner":  {"u1": 3, "u2": 1, "u3": 5},
	}}
	attachLabels(context.Background(), labelledFacetLister{}, view)

	if _, ok := view.FacetsLabelled["status"]; ok {
		t.Error("status is not FacetLabelled; must not appear in facetsLabelled")
	}
	got := view.FacetsLabelled["owner"]
	want := []facetLabelledRow{
		{Key: "u3", Count: 5},
		{Key: "u1", Label: "Alice", Count: 3},
		{Key: "u2", Label: "Bob", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("facetsLabelled[owner] = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if view.Facets["owner"]["u1"] != 3 || view.Labels["owner"]["u1"] != "Alice" {
		t.Error("facets and labels must stay untouched")
	}

	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["facetsLabelled"]; !ok {
		t.Errorf("wire form lacks facetsLabelled: %s", body)
	}
}

// TestAttachLabels_FacetsLabelledAbsentWhenNothingResolves keeps the field off
// the wire exactly when labels are absent.
func TestAttachLabels_FacetsLabelledAbsentWhenNothingResolves(t *testing.T) {
	view := &facetView{Facets: cutting_garden_plugins.FacetSummary{
		"owner": {"u1": 3},
	}}
	attachLabels(context.Background(), labelledFacetLister{failResolve: true}, view)
	if view.FacetsLabelled != nil {
		t.Errorf("FacetsLabelled = %+v, want nil after a resolver error", view.FacetsLabelled)
	}
}
