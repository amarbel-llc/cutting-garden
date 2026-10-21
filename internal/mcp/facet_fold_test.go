package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// foldOnlyFacetLister declares a facet schema and puts membership on its
// nodes but implements NO FacetCounter: the shape RFC 0012 §4.2 hands to the
// framework fold (a lazily-walked plugin such as a bookmark tree).
type foldOnlyFacetLister struct{ fakeLister }

func (foldOnlyFacetLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "status", Kind: cutting_garden_plugins.FacetCategorical},
		},
	}}
}

func (l foldOnlyFacetLister) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	nodes, err := l.fakeLister.ListRoots(ctx, node)
	for i := range nodes {
		if nodes[i].Type == "test-object-v1" {
			nodes[i].Facets = map[string][]cutting_garden_plugins.FacetValue{
				"status": {{Key: "CONFIRMED"}},
			}
		}
	}
	return nodes, err
}

func newFoldOnlyResources(t *testing.T, rootStrs ...string) *Resources {
	t.Helper()
	r := newFakeResources(t, rootStrs...)
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, foldOnlyFacetLister{}, nil
	}
	return r
}

// TestResourcesReadFacets_FoldsWhenPluginHasNoFacetCounter pins the RFC 0012
// §4.2 fallback on the read_facets surface, both the memoized (nil filter)
// and the explicit filtered path: a described plugin with no FacetCounter is
// summarized by the framework instead of answering "no facet summary".
func TestResourcesReadFacets_FoldsWhenPluginHasNoFacetCounter(t *testing.T) {
	r := newFoldOnlyResources(t, "faketest://h/")

	view, err := r.ReadFacets(context.Background(), "faketest://h/", nil)
	if err != nil {
		t.Fatalf("ReadFacets (memoized): %v", err)
	}
	if got := view.Facets["status"]["CONFIRMED"]; got != 1 {
		t.Errorf("memoized status[CONFIRMED] = %d, want 1 (summary %v)", got, view.Facets)
	}
	if !view.Complete {
		t.Error("a fully-walked fold must be complete")
	}

	filter := cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "CONFIRMED"}}
	view, err = r.ReadFacets(context.Background(), "faketest://h/", filter)
	if err != nil {
		t.Fatalf("ReadFacets (filtered): %v", err)
	}
	if got := view.Facets["status"]["CONFIRMED"]; got != 1 {
		t.Errorf("filtered status[CONFIRMED] = %d, want 1 (summary %v)", got, view.Facets)
	}
}

// TestReadResource_ContainerCarriesFoldedFacets pins the implicit surface: a
// container read of a fold-only plugin carries the folded facets block beside
// its listing.
func TestReadResource_ContainerCarriesFoldedFacets(t *testing.T) {
	r := newFoldOnlyResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got.Contents) != 2 {
		t.Fatalf("got %d content blocks, want 2 (listing + facets)", len(got.Contents))
	}
	var fv facetView
	if err := json.Unmarshal([]byte(got.Contents[1].Text), &fv); err != nil {
		t.Fatalf("decode facets %q: %v", got.Contents[1].Text, err)
	}
	if v := fv.Facets["status"]["CONFIRMED"]; v != 1 {
		t.Errorf("status[CONFIRMED] = %d, want 1 (view %+v)", v, fv)
	}
}
