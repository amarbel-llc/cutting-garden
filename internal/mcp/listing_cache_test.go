package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

// jsonUnmarshalContents decodes the listing content block's (contents[0])
// JSON text into out — a small helper so the caching tests don't repeat the
// contents[0].Text plumbing.
func jsonUnmarshalContents(res *protocol.ResourceReadResult, out any) error {
	return json.Unmarshal([]byte(res.Contents[0].Text), out)
}

// countingEnrichedLister is an EnrichedLister that counts invocations and
// serves a movable change token plus an optional volatile facet dimension —
// the listingCache counterpart of facet_test.go's countingFacetLister /
// volatileFacetLister fixtures.
type countingEnrichedLister struct {
	fakeLister
	mu       sync.Mutex
	calls    int
	token    string
	volatile bool
}

func (l *countingEnrichedLister) ListEnriched(
	_ context.Context, node *url.URL, filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	l.mu.Lock()
	l.calls++
	n := cutting_garden_plugins.Node{
		URI:  &url.URL{Scheme: "faketest", Host: node.Host, Path: "/work/task1.ics"},
		Name: "task1.ics",
		Type: "test-object-v1",
		Facets: map[string][]cutting_garden_plugins.FacetValue{
			"status": {{Key: "CONFIRMED"}},
		},
		Fields: map[string]any{"summary": "Buy milk"},
	}
	if l.volatile {
		n.Facets["due_band"] = []cutting_garden_plugins.FacetValue{{Key: "overdue"}}
	}
	l.mu.Unlock()
	if !filter.Matches(n.Facets) {
		return nil, true, nil
	}
	return []cutting_garden_plugins.Node{n}, true, nil
}

func (l *countingEnrichedLister) FacetVersion(
	context.Context, *url.URL,
) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.token, l.token != "", nil
}

func (l *countingEnrichedLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "status", Kind: cutting_garden_plugins.FacetCategorical},
			{
				Key:             "due_band",
				Kind:            cutting_garden_plugins.FacetNumericBucket,
				RevalidateAfter: testVolatileWindow,
				Values: []cutting_garden_plugins.FacetValue{
					{Key: "overdue"}, {Key: "later"},
				},
			},
		},
	}}
}

func (l *countingEnrichedLister) set(fn func(*countingEnrichedLister)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(l)
}

func (l *countingEnrichedLister) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func newEnrichedCacheResources(
	t *testing.T, lister *countingEnrichedLister,
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

// TestListingCache_ServeMemoizesAcrossReads pins the phase-3 amortization
// this whole slice is for: repeated unfiltered reads of the same container
// invoke the plugin's enrichment fetch (ListEnriched) exactly once.
func TestListingCache_ServeMemoizesAcrossReads(t *testing.T) {
	lister := &countingEnrichedLister{token: "t1"}
	r := newEnrichedCacheResources(t, lister)

	for range 3 {
		if _, err := r.ReadResource(context.Background(), "faketest://h/work"); err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
	}
	if got := lister.callCount(); got != 1 {
		t.Errorf("ListEnriched ran %d times across 3 reads, want 1", got)
	}
}

// TestListingCache_TokenGatedRefresh pins the refresher's two paths,
// mirroring TestFacetCache_TokenGatedRefresh: an unmoved token
// re-verifies without recomputation; a moved token recomputes.
func TestListingCache_TokenGatedRefresh(t *testing.T) {
	lister := &countingEnrichedLister{token: "t1"}
	r := newEnrichedCacheResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	if _, err := r.ReadResource(ctx, uri); err != nil {
		t.Fatalf("first read: %v", err)
	}

	r.listings.refreshOne(ctx, r.resolve, uri)
	if got := lister.callCount(); got != 1 {
		t.Fatalf("refresh with unmoved token recomputed (calls=%d)", got)
	}

	lister.set(func(l *countingEnrichedLister) { l.token = "t2" })
	r.listings.refreshOne(ctx, r.resolve, uri)
	if got := lister.callCount(); got != 2 {
		t.Fatalf("refresh with moved token did not recompute (calls=%d)", got)
	}

	got, err := r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("read after refresh: %v", err)
	}
	var views []nodeView
	if err := jsonUnmarshalContents(got, &views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 1 || views[0].Fields["summary"] != "Buy milk" {
		t.Errorf("served listing after refresh = %+v, want the recomputed nodes", views)
	}
}

// TestListingCache_VolatileWindowForcesRecompute pins that a volatile facet
// dimension present in the cached nodes' Facets (RFC 0012 §11.3, reused via
// facetKeyPresenceOf/volatileWindowFor) forces recomputation once its
// window lapses, even with an unmoved token — the same rule facetCache
// applies to summaries, now applied to listings.
func TestListingCache_VolatileWindowForcesRecompute(t *testing.T) {
	lister := &countingEnrichedLister{token: "t1", volatile: true}
	r := newEnrichedCacheResources(t, lister)
	clock := &fakeClock{t: time.Now()}
	r.listings.now = clock.now
	ctx := context.Background()
	const uri = "faketest://h/work"

	if _, err := r.ReadResource(ctx, uri); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got := lister.callCount(); got != 1 {
		t.Fatalf("calls after first read = %d, want 1", got)
	}

	// Inside the window: unmoved token re-verifies only.
	clock.advance(5 * time.Minute)
	r.listings.refreshOne(ctx, r.resolve, uri)
	if got := lister.callCount(); got != 1 {
		t.Fatalf("in-window refresh recomputed (calls=%d)", got)
	}

	// Past the window: recompute despite the unmoved token.
	clock.advance(6 * time.Minute)
	r.listings.refreshOne(ctx, r.resolve, uri)
	if got := lister.callCount(); got != 2 {
		t.Fatalf("post-window refresh did not recompute (calls=%d)", got)
	}
}

// TestListingCache_RefreshFailureKeepsLastGood pins the degrade path: a
// failing refresh keeps serving the last good listing rather than losing
// it — mirroring facetCache's stale-serve behavior, applied to listings.
func TestListingCache_RefreshFailureKeepsLastGood(t *testing.T) {
	lister := &countingEnrichedLister{token: "t1"}
	r := newEnrichedCacheResources(t, lister)
	ctx := context.Background()
	const uri = "faketest://h/work"

	if _, err := r.ReadResource(ctx, uri); err != nil {
		t.Fatalf("first read: %v", err)
	}

	// Point resolve at a broken resolver for the refresh pass only.
	brokenResolve := func(string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		return nil, nil, context.DeadlineExceeded
	}
	r.listings.refreshOne(ctx, brokenResolve, uri)

	got, err := r.ReadResource(ctx, uri)
	if err != nil {
		t.Fatalf("read after failed refresh: %v", err)
	}
	var views []nodeView
	if err := jsonUnmarshalContents(got, &views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 1 {
		t.Errorf("last-good listing lost after a failed refresh: %+v", views)
	}
}
