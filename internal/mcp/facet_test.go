package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
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
		// A fixed per-child-container breakdown (RFC 0012 §13,
		// cutting-garden#170), so tests can pin that ReadFacets/the facet
		// cache propagate it rather than dropping it on the way to the
		// wire.
		ByContainer: []cutting_garden_plugins.FacetContainerBreakdown{
			{URI: "faketest://h/work/personal", Name: "Personal", Count: 2},
			{URI: "faketest://h/work/team", Name: "Team", Count: 1},
		},
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

// TestReadResource_ContainerFacetsCarryByContainer pins RFC 0012 §13's mcp
// binding on the resources/read path: a container's facet block carries the
// plugin's per-child-container breakdown (cutting-garden#170) verbatim,
// exactly as the counts themselves do.
func TestReadResource_ContainerFacetsCarryByContainer(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	fv := facetBlockOf(t, got.Contents)
	if len(fv.ByContainer) != 2 {
		t.Fatalf("ByContainer = %+v, want 2 entries", fv.ByContainer)
	}
	if fv.ByContainer[0].Name != "Personal" || fv.ByContainer[0].Count != 2 {
		t.Errorf("ByContainer[0] = %+v, want {Personal, 2 matches}", fv.ByContainer[0])
	}
	if fv.ByContainerTruncated {
		t.Error("ByContainerTruncated = true, want false")
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
		// Carries the compute count too, so a cache-propagation test can
		// tell "served the memoized ByContainer" (unchanged since the
		// first compute) apart from "recomputed" (RFC 0012 §13,
		// cutting-garden#170).
		ByContainer: []cutting_garden_plugins.FacetContainerBreakdown{
			{URI: "faketest://h/work/a", Count: int64(l.computes)},
		},
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

// TestFacetCache_ReadsServeMemoizedByContainer pins RFC 0012 §13's
// memoization interaction (cutting-garden#170): the nil-filter path caches
// and serves the WHOLE FacetResult, so ByContainer rides the same cache
// entry Facets does — served unchanged across repeat reads, not
// recomputed or dropped.
func TestFacetCache_ReadsServeMemoizedByContainer(t *testing.T) {
	lister := &countingFacetLister{token: "t1"}
	r := newCountingResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	first, err := r.ReadFacets(ctx, uri, nil)
	if err != nil {
		t.Fatalf("first ReadFacets: %v", err)
	}
	if len(first.ByContainer) != 1 || first.ByContainer[0].Count != 1 {
		t.Fatalf("first view ByContainer = %+v, want [{…, Count:1}]", first.ByContainer)
	}

	second, err := r.ReadFacets(ctx, uri, nil)
	if err != nil {
		t.Fatalf("second ReadFacets: %v", err)
	}
	if lister.computeCount() != 1 {
		t.Fatalf("FacetCounts ran %d times across 2 reads, want 1", lister.computeCount())
	}
	if len(second.ByContainer) != 1 || second.ByContainer[0].Count != 1 {
		t.Errorf("second view ByContainer = %+v, want the SAME cached "+
			"entry as the first (Count:1, not recomputed)", second.ByContainer)
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

// TestResourcesReadFacets_NilFilterServesMemoizedSummary pins the read_facets
// tool's nil-filter path (cutting-garden#151): it serves the SAME memoized
// summary a container's resources/read facets block would.
func TestResourcesReadFacets_NilFilterServesMemoizedSummary(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	view, err := r.ReadFacets(context.Background(), "faketest://h/work", nil)
	if err != nil {
		t.Fatalf("ReadFacets: %v", err)
	}
	if !view.Complete {
		t.Error("complete = false, want true")
	}
	if got := view.Facets["status"]["CONFIRMED"]; got != 2 {
		t.Errorf("status[CONFIRMED] = %d, want 2", got)
	}
}

// TestResourcesReadFacets_FilteredPathPropagatesByContainer pins RFC 0012
// §13 on the read_facets tool's filtered (direct-compute) path
// (cutting-garden#170): FacetCounts's ByContainer/ByContainerTruncated ride
// through to the served view unchanged, the same way Facets/Complete do.
func TestResourcesReadFacets_FilteredPathPropagatesByContainer(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	view, err := r.ReadFacets(context.Background(), "faketest://h/work",
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "CONFIRMED"}})
	if err != nil {
		t.Fatalf("ReadFacets: %v", err)
	}
	if len(view.ByContainer) != 2 {
		t.Fatalf("ByContainer = %+v, want 2 entries", view.ByContainer)
	}
	if view.ByContainer[1].Name != "Team" || view.ByContainer[1].Count != 1 {
		t.Errorf("ByContainer[1] = %+v, want {Team, 1 match}", view.ByContainer[1])
	}
}

// TestResourcesReadFacets_NonFacetCounterErrors pins the "facets unavailable
// for this scheme" error path: a plugin with no FacetCounter capability.
func TestResourcesReadFacets_NonFacetCounterErrors(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	if _, err := r.ReadFacets(
		context.Background(), "faketest://h/work", nil,
	); err == nil {
		t.Fatal("ReadFacets on a non-FacetCounter plugin: want error, got nil")
	}
}

// TestResourcesReadFacets_FilterComputesDirectlyAndBypassesCache pins RFC
// 0012 §9's explicit-request path: a non-empty filter computes FRESH via
// FacetCounts directly, without touching (or being served by) the memoized
// cache a nil-filter call uses.
func TestResourcesReadFacets_FilterComputesDirectlyAndBypassesCache(t *testing.T) {
	lister := &countingFacetLister{token: "t1"}
	r := newCountingResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	// Warm the cache via a nil-filter read (computes=1).
	if _, err := r.ReadFacets(ctx, uri, nil); err != nil {
		t.Fatalf("nil-filter ReadFacets: %v", err)
	}
	if got := lister.computeCount(); got != 1 {
		t.Fatalf("computes after nil-filter read = %d, want 1", got)
	}

	// A filtered read computes directly (computes=2): fresh, and NOT served
	// from (or stored into) the cache entry the nil-filter read populated.
	view, err := r.ReadFacets(ctx, uri, cutting_garden_plugins.FacetFilter{
		{Dimension: "status", Value: "CONFIRMED"},
	})
	if err != nil {
		t.Fatalf("filtered ReadFacets: %v", err)
	}
	if got := lister.computeCount(); got != 2 {
		t.Fatalf("computes after filtered read = %d, want 2 (direct compute)", got)
	}
	if view.Freshness != freshnessFresh {
		t.Errorf("filtered view freshness = %q, want %q", view.Freshness, freshnessFresh)
	}
	if view.Facets["status"]["CONFIRMED"] != 2 {
		t.Errorf("filtered view summary = %+v, want the direct compute", view.Facets)
	}

	// The cache entry is untouched by the filtered call: a subsequent
	// nil-filter read still serves the ORIGINAL memoized summary.
	view, err = r.ReadFacets(ctx, uri, nil)
	if err != nil {
		t.Fatalf("nil-filter ReadFacets (2nd): %v", err)
	}
	if got := lister.computeCount(); got != 2 {
		t.Errorf("nil-filter read after a filtered call recomputed (computes=%d)", got)
	}
	if view.Facets["status"]["CONFIRMED"] != 1 {
		t.Errorf("nil-filter view = %+v, want the ORIGINAL cached summary (CONFIRMED=1)", view.Facets)
	}
}

// TestResourcesReadFacets_FilterErrorIsFailFast pins RFC 0012 §9: an explicit
// (filtered) facet request's compute error MUST surface, never degrade.
func TestResourcesReadFacets_FilterErrorIsFailFast(t *testing.T) {
	lister := &countingFacetLister{fail: true}
	r := newCountingResources(t, lister)

	_, err := r.ReadFacets(
		context.Background(), "faketest://h/work",
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "x"}},
	)
	if err == nil {
		t.Fatal("a filtered ReadFacets compute failure must surface as an " +
			"error (RFC 0012 §9 fail-fast), got nil")
	}
}

// TestResourcesReadFacets_UndeclaredDimensionIsRejected pins
// cutting-garden#161: a filter naming a dimension the plugin never declared
// via FacetDescriber is a REJECTED, actionable error — never a silent
// {facets:{}} indistinguishable from a filter that genuinely matches
// nothing. The error names both the bad dimension and the declared ones.
func TestResourcesReadFacets_UndeclaredDimensionIsRejected(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	_, err := r.ReadFacets(context.Background(), "faketest://h/work",
		cutting_garden_plugins.FacetFilter{{Dimension: "bogus", Value: "x"}})
	if err == nil {
		t.Fatal("filter naming an undeclared dimension: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bogus") {
		t.Errorf("error %q does not name the bad dimension", msg)
	}
	if !strings.Contains(msg, "status") || !strings.Contains(msg, "read") {
		t.Errorf("error %q does not list the declared dimensions", msg)
	}
}

// TestResourcesReadFacets_ClosedDimensionInvalidValueIsRejected pins the
// closed-domain half of cutting-garden#161: a value outside a CLOSED
// dimension's declared set ("read" ∈ {read,unread} per fakeFacetLister) is
// a rejected, actionable error naming the valid values — the exact
// ergonomic study finding (a guessed "read=false" must not silently return
// an empty summary).
func TestResourcesReadFacets_ClosedDimensionInvalidValueIsRejected(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	_, err := r.ReadFacets(context.Background(), "faketest://h/work",
		cutting_garden_plugins.FacetFilter{{Dimension: "read", Value: "false"}})
	if err == nil {
		t.Fatal("filter with an out-of-domain closed-dimension value: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "false") {
		t.Errorf("error %q does not name the bad value", msg)
	}
	if !strings.Contains(msg, "read") || !strings.Contains(msg, "unread") {
		t.Errorf("error %q does not list the valid values", msg)
	}
}

// TestResourcesReadFacets_OpenDimensionAcceptsAnyValue pins that an OPEN
// dimension (status, Values == nil) is checked only by dimension name — any
// value passes validation (its domain is discovered at enumeration, not
// declared up front), so the guessed-value ergonomics fix does not turn
// open dimensions into a second guessing game.
func TestResourcesReadFacets_OpenDimensionAcceptsAnyValue(t *testing.T) {
	r := newFakeFacetResources(t, "faketest://h/")

	view, err := r.ReadFacets(context.Background(), "faketest://h/work",
		cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "ANYTHING"}})
	if err != nil {
		t.Fatalf("open-dimension filter with an undeclared value: want no error, got %v", err)
	}
	if view == nil {
		t.Fatal("view is nil")
	}
}

// TestResourcesReadFacets_NilFilterComputeFailureDegrades pins that the
// nil-filter (implicit-surface-mirroring) path degrades a cold-cache compute
// failure to a stale, error-noted view rather than failing the call — the
// SAME §9 implicit-surface degrade resources/read uses.
func TestResourcesReadFacets_NilFilterComputeFailureDegrades(t *testing.T) {
	lister := &countingFacetLister{fail: true}
	r := newCountingResources(t, lister)

	view, err := r.ReadFacets(context.Background(), "faketest://h/work", nil)
	if err != nil {
		t.Fatalf("nil-filter ReadFacets must degrade, not fail: %v", err)
	}
	if view.Freshness != freshnessStale || view.Error == "" {
		t.Errorf("degraded view = %+v, want stale with an error note", view)
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

	// cutting-garden#161: a closed dimension's complete value domain is
	// surfaced verbatim, so a filter value is discoverable rather than
	// guessed; an open dimension carries none (its values are discovered
	// at enumeration).
	if got := byKey["status"].Values; got != nil {
		t.Errorf("status (open) Values = %+v, want nil", got)
	}
	wantReadValues := []string{"read", "unread"}
	if got := byKey["read"].Values; len(got) != len(wantReadValues) ||
		got[0] != wantReadValues[0] || got[1] != wantReadValues[1] {
		t.Errorf("read (closed) Values = %+v, want %+v", got, wantReadValues)
	}
}

// volatileFacetLister adds a VOLATILE "due" dimension (RFC 0012 §11.3:
// closed domain, informative zeros, RevalidateAfter) beside the pure
// counting "status", plus a declared-but-never-emitted volatile "age" —
// the presence-based window derivation must ignore the latter.
type volatileFacetLister struct {
	countingFacetLister
}

const testVolatileWindow = 10 * time.Minute

func (l *volatileFacetLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{
				Key:  "status",
				Kind: cutting_garden_plugins.FacetCategorical,
			},
			{
				Key:  "due",
				Kind: cutting_garden_plugins.FacetNumericBucket,
				Values: []cutting_garden_plugins.FacetValue{
					{Key: "overdue"}, {Key: "today"}, {Key: "later"},
				},
				RevalidateAfter: testVolatileWindow,
			},
			{
				Key:  "age",
				Kind: cutting_garden_plugins.FacetNumericBucket,
				Values: []cutting_garden_plugins.FacetValue{
					{Key: "old"}, {Key: "new"},
				},
				RevalidateAfter: time.Minute,
			},
		},
	}}
}

func (l *volatileFacetLister) FacetCounts(
	ctx context.Context,
	u *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	result, ok, err := l.countingFacetLister.FacetCounts(ctx, u, filter)
	if err != nil || !ok {
		return result, ok, err
	}
	// The volatile dimension rides every summary with informative zeros
	// (§11.3's emission rule); "age" is deliberately never emitted.
	result.Summary["due"] = cutting_garden_plugins.FacetHistogram{
		"overdue": 0, "today": 1, "later": 2,
	}
	return result, true, nil
}

// fakeClock is an injectable, advanceable now() for the cache.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newVolatileResources(
	t *testing.T, lister *volatileFacetLister,
) (*Resources, *fakeClock) {
	t.Helper()
	r := newFakeResources(t, "faketest://h/")
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, lister, nil
	}
	clock := &fakeClock{t: time.Now()}
	r.facets.now = clock.now
	return r, clock
}

// TestFacetCache_VolatileWindowForcesRecompute pins RFC 0012 §11.3's
// expiry rule: inside the window an unmoved token re-verifies without
// recomputation (volatile presence changes nothing early); past the
// window the refresher recomputes DESPITE the unmoved token. The window
// derives from the volatile dimension present in the summary — the
// declared-but-absent "age" (1m) must not shrink it.
func TestFacetCache_VolatileWindowForcesRecompute(t *testing.T) {
	lister := &volatileFacetLister{
		countingFacetLister: countingFacetLister{token: "t1"},
	}
	r, clock := newVolatileResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	if _, err := r.ReadResource(ctx, uri); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got := lister.computeCount(); got != 1 {
		t.Fatalf("computes after first read = %d, want 1", got)
	}

	// Inside the window ("age"'s 1m must not apply — it is absent from
	// the summary): unmoved token re-verifies only.
	clock.advance(5 * time.Minute)
	r.facets.refreshOne(ctx, r.resolve, uri)
	if got := lister.computeCount(); got != 1 {
		t.Fatalf("in-window refresh recomputed (computes=%d)", got)
	}

	// Past the window: recompute despite the unmoved token.
	clock.advance(6 * time.Minute)
	r.facets.refreshOne(ctx, r.resolve, uri)
	if got := lister.computeCount(); got != 2 {
		t.Fatalf("post-window refresh did not recompute (computes=%d)", got)
	}
}

// TestFacetCache_VolatileFreshnessAndValidUntil pins the wire metadata:
// a volatile summary carries validUntil = computedAt + window, serves
// fresh inside the window (token verified at compute), degrades to
// stale once the window lapses without recomputation, and returns to
// fresh after the refresher recomputes.
func TestFacetCache_VolatileFreshnessAndValidUntil(t *testing.T) {
	lister := &volatileFacetLister{
		countingFacetLister: countingFacetLister{token: "t1"},
	}
	r, clock := newVolatileResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	got, err := r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	fv := facetBlockOf(t, got.Contents)
	if fv.ValidUntil == "" {
		t.Fatal("volatile summary carries no validUntil")
	}
	computed, err := time.Parse(time.RFC3339, fv.ComputedAt)
	if err != nil {
		t.Fatal(err)
	}
	until, err := time.Parse(time.RFC3339, fv.ValidUntil)
	if err != nil {
		t.Fatal(err)
	}
	if want := computed.Add(testVolatileWindow); !until.Equal(want) {
		t.Errorf("validUntil = %v, want computedAt+%v = %v",
			until, testVolatileWindow, want)
	}

	// Window lapsed, no refresh yet: last-good served stale.
	clock.advance(testVolatileWindow + time.Minute)
	got, err = r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("post-window read: %v", err)
	}
	if fv := facetBlockOf(t, got.Contents); fv.Freshness != freshnessStale {
		t.Errorf("post-window freshness = %q, want %q",
			fv.Freshness, freshnessStale)
	}

	// The refresher recomputes; fresh again.
	r.facets.refreshOne(ctx, r.resolve, uri)
	got, err = r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("post-refresh read: %v", err)
	}
	if fv := facetBlockOf(t, got.Contents); fv.Freshness != freshnessFresh {
		t.Errorf("post-refresh freshness = %q, want %q",
			fv.Freshness, freshnessFresh)
	}
}

// TestFacetCache_PureSummariesUnaffectedByClock guards the pure path
// against the volatile machinery: a summary with no volatile dimension
// carries no validUntil, and hours of clock advance neither expire it
// nor force recomputation while its token is unmoved.
func TestFacetCache_PureSummariesUnaffectedByClock(t *testing.T) {
	lister := &countingFacetLister{token: "t1"}
	r := newCountingResources(t, lister)
	clock := &fakeClock{t: time.Now()}
	r.facets.now = clock.now
	ctx := context.Background()
	const uri = "faketest://h/work"

	got, err := r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if fv := facetBlockOf(t, got.Contents); fv.ValidUntil != "" {
		t.Errorf("pure summary carries validUntil %q", fv.ValidUntil)
	}

	clock.advance(6 * time.Hour)
	r.facets.refreshOne(ctx, r.resolve, uri)
	if got := lister.computeCount(); got != 1 {
		t.Errorf("pure entry recomputed on clock advance (computes=%d)", got)
	}
	got, err = r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if fv := facetBlockOf(t, got.Contents); fv.Freshness != freshnessFresh {
		t.Errorf("freshness = %q, want %q", fv.Freshness, freshnessFresh)
	}
}
