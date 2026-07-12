package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
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

// countingFacetLister counts FacetCounts invocations and serves a movable
// change token — the fixture for the RFC 0012 §11 memoization tests.
type countingFacetLister struct {
	fakeLister
	mu       sync.Mutex
	computes int
	token    string
	fail     bool
}

func (l *countingFacetLister) FacetCounts(
	context.Context, *url.URL, cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return cutting_garden_plugins.FacetResult{}, false,
			fmt.Errorf("backend unavailable")
	}
	l.computes++
	return cutting_garden_plugins.FacetResult{
		Summary: cutting_garden_plugins.FacetSummary{
			"status": {"CONFIRMED": int64(l.computes)},
		},
		Complete: true,
	}, true, nil
}

func (l *countingFacetLister) FacetVersion(
	context.Context, *url.URL,
) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.token, l.token != "", nil
}

func (l *countingFacetLister) set(fn func(*countingFacetLister)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(l)
}

func (l *countingFacetLister) computeCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.computes
}

func newCountingResources(
	t *testing.T, lister *countingFacetLister,
) *Resources {
	t.Helper()
	r := newFakeResources(t, "faketest://h/")
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, lister, nil
	}
	return r
}

// TestFacetCache_ReadsServeMemoized pins §11.2's serving rule: the second
// read serves the cached summary without recomputing.
func TestFacetCache_ReadsServeMemoized(t *testing.T) {
	lister := &countingFacetLister{token: "t1"}
	r := newCountingResources(t, lister)

	for range 3 {
		if _, err := r.ReadResource(
			context.Background(), "faketest://h/work",
		); err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
	}
	if got := lister.computeCount(); got != 1 {
		t.Errorf("FacetCounts ran %d times across 3 reads, want 1", got)
	}
}

// TestFacetCache_TokenGatedRefresh pins the refresher's two paths: an
// unmoved token re-verifies without recomputation; a moved token
// recomputes and the next read serves the new summary as fresh.
func TestFacetCache_TokenGatedRefresh(t *testing.T) {
	lister := &countingFacetLister{token: "t1"}
	r := newCountingResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	if _, err := r.ReadResource(ctx, uri); err != nil {
		t.Fatalf("first read: %v", err)
	}

	// Unmoved token: verification only.
	r.facets.refreshOne(ctx, r.resolve, uri)
	if got := lister.computeCount(); got != 1 {
		t.Fatalf("refresh with unmoved token recomputed (computes=%d)", got)
	}

	// Moved token: recompute; the served summary changes.
	lister.set(func(l *countingFacetLister) { l.token = "t2" })
	r.facets.refreshOne(ctx, r.resolve, uri)
	if got := lister.computeCount(); got != 2 {
		t.Fatalf("refresh with moved token did not recompute (computes=%d)", got)
	}

	got, err := r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("read after refresh: %v", err)
	}
	fv := facetBlockOf(t, got.Contents)
	if fv.Freshness != freshnessFresh {
		t.Errorf("freshness = %q, want %q", fv.Freshness, freshnessFresh)
	}
	if fv.Facets["status"]["CONFIRMED"] != 2 {
		t.Errorf("served summary is not the recomputed one: %+v", fv.Facets)
	}
}

// TestFacetCache_RefreshFailureServesLastGoodStale pins §9's degrade with
// a cache: a failing refresh keeps the last good summary, served stale
// with the error noted — and the read itself succeeds.
func TestFacetCache_RefreshFailureServesLastGoodStale(t *testing.T) {
	lister := &countingFacetLister{token: "t1"}
	r := newCountingResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	if _, err := r.ReadResource(ctx, uri); err != nil {
		t.Fatalf("first read: %v", err)
	}

	lister.set(func(l *countingFacetLister) {
		l.token = "t2" // moved, so the refresher tries to recompute
		l.fail = true  // ... and the recompute fails
	})
	r.facets.refreshOne(ctx, r.resolve, uri)

	got, err := r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("read after failed refresh: %v", err)
	}
	fv := facetBlockOf(t, got.Contents)
	if fv.Freshness != freshnessStale {
		t.Errorf("freshness = %q, want %q", fv.Freshness, freshnessStale)
	}
	if fv.Error == "" {
		t.Error("stale summary carries no error notation")
	}
	if fv.Facets["status"]["CONFIRMED"] != 1 {
		t.Errorf("stale serve is not the last good summary: %+v", fv.Facets)
	}
}

// TestReadResource_FacetErrorDoesNotFailRead pins §9's degrade without a
// cache: a first-touch facet failure yields an error-only facets block and
// the child listing is untouched.
func TestReadResource_FacetErrorDoesNotFailRead(t *testing.T) {
	lister := &countingFacetLister{fail: true}
	r := newCountingResources(t, lister)

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource must not fail on a facet error: %v", err)
	}
	if got.Contents[0].MimeType != mimeListing {
		t.Fatalf("child listing missing: %+v", got.Contents)
	}
	fv := facetBlockOf(t, got.Contents)
	if fv.Error == "" || fv.Freshness != freshnessStale {
		t.Errorf("error-only block wrong: %+v", fv)
	}
	if len(fv.Facets) != 0 {
		t.Errorf("error-only block carries counts: %+v", fv.Facets)
	}
}

// facetBlockOf finds and decodes the facets content block.
func facetBlockOf(
	t *testing.T, contents []protocol.ResourceContent,
) facetView {
	t.Helper()
	for _, c := range contents {
		if c.MimeType != mimeFacets {
			continue
		}
		var fv facetView
		if err := json.Unmarshal([]byte(c.Text), &fv); err != nil {
			t.Fatalf("decode facets %q: %v", c.Text, err)
		}
		return fv
	}
	t.Fatal("no facets block in contents")
	return facetView{}
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
