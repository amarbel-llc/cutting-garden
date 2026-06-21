package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

// TestRenderContents_SkipsFacetBlock guards the read_node/list_nodes tool
// output: it is consumed as a single JSON value (the listing array or a leaf
// object), so the container facet-summary block must not be flattened in
// beside it (a second JSON object breaks downstream jq).
func TestRenderContents_SkipsFacetBlock(t *testing.T) {
	listing := `[{"name":"Personal","container":true}]`
	contents := []protocol.ResourceContent{
		{URI: "caldav://h/dav/", MimeType: mimeListing, Text: listing},
		{
			URI:      "caldav://h/dav/",
			MimeType: mimeFacets,
			Text:     `{"facets":{"status":{"CONFIRMED":2}},"complete":true}`,
		},
	}
	if got := renderContents(contents); got != listing {
		t.Errorf("renderContents = %q, want the listing only (facets skipped)", got)
	}
}

// fakeFacetLister is a fakeLister that also declares facets and answers a
// one-shot FacetCounter — the RFC 0012 surface the mcp server exposes over
// JSON-RPC (describe_node_types + a container's resources/read facets block).
type fakeFacetLister struct{ fakeLister }

func (fakeFacetLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{
				Key:   "status",
				Label: "Status",
				Kind:  cutting_garden_plugins.FacetCategorical,
			},
			{
				Key:  "read",
				Kind: cutting_garden_plugins.FacetCategorical,
				Values: []cutting_garden_plugins.FacetValue{
					{Key: "read"}, {Key: "unread"},
				},
			},
		},
	}}
}

func (fakeFacetLister) FacetCounts(
	_ context.Context,
	node *url.URL,
	_ cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	// Only a container is summarized; a leaf object declines (ok=false).
	if strings.HasSuffix(node.Path, ".ics") {
		return cutting_garden_plugins.FacetResult{}, false, nil
	}
	return cutting_garden_plugins.FacetResult{
		Summary: cutting_garden_plugins.FacetSummary{
			"status": {"CONFIRMED": 2, "CANCELLED": 1},
		},
		Complete: true,
	}, true, nil
}

func newFakeFacetResources(t *testing.T, rootStrs ...string) *Resources {
	t.Helper()
	r := newFakeResources(t, rootStrs...)
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, fakeFacetLister{}, nil
	}
	return r
}

// TestReadResource_ContainerCarriesFacets is the RFC 0012 §7 mcp binding: a
// container read returns its child listing AND a facet-summary content block.
func TestReadResource_ContainerCarriesFacets(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got.Contents) != 2 {
		t.Fatalf("got %d content blocks, want 2 (listing + facets)", len(got.Contents))
	}
	if got.Contents[0].MimeType != mimeListing {
		t.Errorf("content[0] mimetype = %q, want listing %q",
			got.Contents[0].MimeType, mimeListing)
	}

	facets := got.Contents[1]
	if facets.MimeType != mimeFacets {
		t.Fatalf("content[1] mimetype = %q, want facets %q", facets.MimeType, mimeFacets)
	}
	var fv facetView
	if err := json.Unmarshal([]byte(facets.Text), &fv); err != nil {
		t.Fatalf("decode facets %q: %v", facets.Text, err)
	}
	if !fv.Complete {
		t.Error("facets complete = false, want true")
	}
	if got := fv.Facets["status"]["CONFIRMED"]; got != 2 {
		t.Errorf("status[CONFIRMED] = %d, want 2", got)
	}
	if got := fv.Facets["status"]["CANCELLED"]; got != 1 {
		t.Errorf("status[CANCELLED] = %d, want 1", got)
	}
}

// TestReadResource_LeafHasNoFacets pins that a childless (leaf) read carries
// no facet block — the summary is a container concern.
func TestReadResource_LeafHasNoFacets(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	got, err := r.ReadResource(
		context.Background(), "faketest://h/work/task1.ics",
	)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	for _, c := range got.Contents {
		if c.MimeType == mimeFacets {
			t.Errorf("leaf read carried a facet block, want none: %+v", c)
		}
	}
}

// TestCollectSchema_IncludesFacetDimensions is the describe_node_types facet
// surface: a FacetDescriber's dimensions appear on its node type, with the
// closed-domain flag derived from a non-nil Values list.
func TestCollectSchema_IncludesFacetDimensions(t *testing.T) {
	schemes := collectSchema([]cutting_garden_plugins.Plugin{fakeFacetLister{}})

	var dims []facetDimSchema
	for _, s := range schemes {
		for _, ts := range s.Types {
			if ts.Tag == "test-object-v1" {
				dims = ts.Facets
			}
		}
	}
	if len(dims) != 2 {
		t.Fatalf("test-object-v1 facets = %d, want 2: %+v", len(dims), dims)
	}

	byKey := map[string]facetDimSchema{}
	for _, d := range dims {
		byKey[d.Key] = d
	}
	if byKey["status"].Kind != string(cutting_garden_plugins.FacetCategorical) {
		t.Errorf("status kind = %q, want categorical", byKey["status"].Kind)
	}
	if byKey["status"].Closed {
		t.Error("status is open-domain (no Values); Closed must be false")
	}
	if !byKey["read"].Closed {
		t.Error("read declares Values; Closed must be true")
	}
}
