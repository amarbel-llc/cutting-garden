package mcp

import (
	"context"
	"fmt"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// labelledFacetLister declares a FacetLabelled "owner" dimension beside a
// plain categorical "status", and resolves owner labels via
// ResolveFacetLabels — the RFC 0012 §7 consumption fixture.
type labelledFacetLister struct {
	fakeLister
	failResolve bool
}

func (labelledFacetLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "status", Kind: cutting_garden_plugins.FacetCategorical},
			{Key: "owner", Kind: cutting_garden_plugins.FacetLabelled},
		},
	}}
}

var facetLabelNames = map[string]string{"u1": "Alice", "u2": "Bob"}

func (l labelledFacetLister) ResolveFacetLabels(
	_ context.Context, dimension string, keys []string,
) (map[string]string, error) {
	if l.failResolve {
		return nil, fmt.Errorf("directory unavailable")
	}
	if dimension != "owner" {
		return nil, nil
	}
	out := map[string]string{}
	for _, k := range keys {
		if n, ok := facetLabelNames[k]; ok {
			out[k] = n
		}
	}
	return out, nil
}

// TestAttachLabels_ResolvesOnlyLabelledDimensionsPresentInSummary pins
// RFC 0012 §7's scope: labels attach only for dimensions declared
// FacetLabelled, and only for keys actually present in the summary.
func TestAttachLabels_ResolvesOnlyLabelledDimensionsPresentInSummary(t *testing.T) {
	view := &facetView{Facets: cutting_garden_plugins.FacetSummary{
		"status": {"CONFIRMED": 2},
		"owner":  {"u1": 3, "u2": 1},
	}}
	attachLabels(context.Background(), labelledFacetLister{}, view)

	if view.Labels == nil {
		t.Fatal("Labels is nil, want owner resolved")
	}
	if _, ok := view.Labels["status"]; ok {
		t.Error("status is not FacetLabelled; must not carry labels")
	}
	if view.Labels["owner"]["u1"] != "Alice" || view.Labels["owner"]["u2"] != "Bob" {
		t.Errorf("owner labels = %+v, want u1=Alice u2=Bob", view.Labels["owner"])
	}
}

// TestAttachLabels_ResolverFailureIsNonFatal pins RFC 0012 §7/§9: a
// ResolveFacetLabels error degrades to no labels for that dimension — it
// never fails the caller, and it never mutates the counts.
func TestAttachLabels_ResolverFailureIsNonFatal(t *testing.T) {
	view := &facetView{Facets: cutting_garden_plugins.FacetSummary{
		"owner": {"u1": 1},
	}}
	attachLabels(context.Background(), labelledFacetLister{failResolve: true}, view)

	if view.Labels != nil {
		t.Errorf("Labels = %+v, want nil after a resolver error (non-fatal degrade)", view.Labels)
	}
	if view.Facets["owner"]["u1"] != 1 {
		t.Errorf("Facets mutated by a label failure: %+v", view.Facets)
	}
}

// TestAttachLabels_NoFacetLabelerLeavesLabelsNil pins the OPTIONAL-capability
// contract: a plugin without FacetLabeler is unaffected, not an error.
func TestAttachLabels_NoFacetLabelerLeavesLabelsNil(t *testing.T) {
	view := &facetView{Facets: cutting_garden_plugins.FacetSummary{
		"status": {"CONFIRMED": 2},
	}}
	attachLabels(context.Background(), fakeLister{}, view)
	if view.Labels != nil {
		t.Errorf("Labels = %+v, want nil (plugin has no FacetLabeler)", view.Labels)
	}
}

// TestAttachLabels_UnresolvedKeyFallsBackSilently pins §7's "a key absent
// from the result means no label" rule: an unresolved key is simply absent
// from view.Labels, not an error and not a zero-value entry.
func TestAttachLabels_UnresolvedKeyFallsBackSilently(t *testing.T) {
	view := &facetView{Facets: cutting_garden_plugins.FacetSummary{
		"owner": {"u1": 1, "u3": 1}, // u3 has no fixture label
	}}
	attachLabels(context.Background(), labelledFacetLister{}, view)
	if _, ok := view.Labels["owner"]["u3"]; ok {
		t.Errorf("u3 has no fixture label but appeared: %+v", view.Labels["owner"])
	}
	if view.Labels["owner"]["u1"] != "Alice" {
		t.Errorf("u1 label = %q, want Alice", view.Labels["owner"]["u1"])
	}
}

// TestAttachLabels_EmptyFacetsIsNoop guards the nil/empty-view short circuit.
func TestAttachLabels_EmptyFacetsIsNoop(t *testing.T) {
	view := &facetView{}
	attachLabels(context.Background(), labelledFacetLister{}, view)
	if view.Labels != nil {
		t.Errorf("Labels = %+v, want nil for an empty summary", view.Labels)
	}
}
