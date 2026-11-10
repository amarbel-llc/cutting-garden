package mcp

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// versionCountingStore is a growingStore whose FacetVersion counts calls
// per URI and never moves its token: a refresh tick then costs exactly one
// FacetVersion call per entry it visits.
type versionCountingStore struct {
	growingStore
	mu    sync.Mutex
	calls map[string]int
}

func (s *versionCountingStore) FacetVersion(
	_ context.Context, u *url.URL,
) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[u.String()]++
	return "t1", true, nil
}

// takeCalls returns the per-URI FacetVersion counts since the last call and
// resets them.
func (s *versionCountingStore) takeCalls() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.calls
	s.calls = nil
	return out
}

func boundTestURI(i int) string { return fmt.Sprintf("faketest://h/c%03d", i) }

func newBoundTestCache(
	t *testing.T, store *versionCountingStore, n int,
) (*listingCache, resolveFunc) {
	t.Helper()
	resolve := func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, err := url.Parse(uriStr)
		return u, store, err
	}
	lc := newListingCache()
	clock := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	lc.now = func() time.Time { return clock }
	for i := range n {
		clock = clock.Add(time.Second)
		uri := boundTestURI(i)
		u, _ := url.Parse(uri)
		if _, _, err := lc.serve(context.Background(), store, uri, u); err != nil {
			t.Fatalf("serve %s: %v", uri, err)
		}
	}
	return lc, resolve
}

// B1: one refresh tick visits at most 32 entries (maxRefreshesPerTick), and
// a cursor carried across ticks makes the next tick resume where the last
// stopped, so every entry is still revisited.
func TestListingCache_RefreshTickVisitsAtMost32(t *testing.T) {
	store := &versionCountingStore{}
	lc, resolve := newBoundTestCache(t, store, 40)
	ctx := context.Background()
	store.takeCalls() // drop the calls the initial serves made

	lc.refreshTick(ctx, resolve)
	first := store.takeCalls()
	if len(first) != 32 {
		t.Errorf("first tick refreshed %d entries, want 32", len(first))
	}

	lc.refreshTick(ctx, resolve)
	second := store.takeCalls()
	if len(second) != 8 {
		t.Errorf("second tick refreshed %d entries, want the other 8", len(second))
	}
	for uri := range second {
		if first[uri] != 0 {
			t.Errorf("second tick revisited %s before the rest were refreshed", uri)
		}
	}
}

// B2: the cache holds at most 512 entries (maxCachedEntries); inserting past
// that evicts the least recently served entry, not the oldest inserted.
func TestListingCache_EvictsLeastRecentlyServedPast512(t *testing.T) {
	store := &versionCountingStore{}
	lc, _ := newBoundTestCache(t, store, 512)

	// Re-serve the oldest entry so it becomes the most recently served.
	now := lc.now().Add(time.Second)
	lc.now = func() time.Time { return now }
	u0, _ := url.Parse(boundTestURI(0))
	if _, _, err := lc.serve(context.Background(), store, boundTestURI(0), u0); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	u512, _ := url.Parse(boundTestURI(512))
	if _, _, err := lc.serve(context.Background(), store, boundTestURI(512), u512); err != nil {
		t.Fatal(err)
	}

	if got := len(lc.entries); got != 512 {
		t.Errorf("len(entries) = %d after 513 distinct reads, want 512", got)
	}
	if _, ok := lc.entries[boundTestURI(0)]; !ok {
		t.Errorf("%s was evicted though it was served most recently", boundTestURI(0))
	}
	if _, ok := lc.entries[boundTestURI(1)]; ok {
		t.Errorf("%s (least recently served) was not evicted", boundTestURI(1))
	}
}

// B2: a configured root is never evicted, though it is the least recently
// served entry; the next least recently served goes instead.
func TestListingCache_NeverEvictsAPinnedRoot(t *testing.T) {
	store := &versionCountingStore{}
	lc := newListingCache()
	lc.pinned = pinnedURIs([]string{boundTestURI(0)})
	ctx := context.Background()
	for i := range maxCachedEntries + 1 {
		u, _ := url.Parse(boundTestURI(i))
		if _, _, err := lc.serve(ctx, store, boundTestURI(i), u); err != nil {
			t.Fatalf("serve %s: %v", boundTestURI(i), err)
		}
	}

	if _, ok := lc.entries[boundTestURI(0)]; !ok {
		t.Errorf("pinned root %s was evicted", boundTestURI(0))
	}
	if _, ok := lc.entries[boundTestURI(1)]; ok {
		t.Errorf("%s (least recently served, unpinned) was not evicted", boundTestURI(1))
	}
}

// facetCountingStore is a versionCountingStore that also answers FacetCounts,
// so the facet cache memoizes it.
type facetCountingStore struct {
	versionCountingStore
}

func (*facetCountingStore) FacetCounts(
	context.Context, *url.URL, cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	return cutting_garden_plugins.FacetResult{
		Summary:  cutting_garden_plugins.FacetSummary{"status": {"open": 1}},
		Complete: true,
	}, true, nil
}

func newBoundTestFacetCache(
	t *testing.T, store *facetCountingStore, n int,
) (*facetCache, resolveFunc) {
	t.Helper()
	resolve := func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, err := url.Parse(uriStr)
		return u, store, err
	}
	fc := newFacetCache()
	for i := range n {
		u, _ := url.Parse(boundTestURI(i))
		if _, err := fc.serve(context.Background(), store, boundTestURI(i), u); err != nil {
			t.Fatalf("serve %s: %v", boundTestURI(i), err)
		}
	}
	return fc, resolve
}

// B1 for the facet cache: 40 entries take two ticks, 32 then the other 8.
func TestFacetCache_RefreshTickVisitsAtMost32(t *testing.T) {
	store := &facetCountingStore{}
	fc, resolve := newBoundTestFacetCache(t, store, 40)
	ctx := context.Background()
	store.takeCalls()

	fc.refreshTick(ctx, resolve)
	first := store.takeCalls()
	if len(first) != maxRefreshesPerTick {
		t.Errorf("first tick refreshed %d entries, want %d", len(first), maxRefreshesPerTick)
	}
	fc.refreshTick(ctx, resolve)
	second := store.takeCalls()
	if len(second) != 8 {
		t.Errorf("second tick refreshed %d entries, want the other 8", len(second))
	}
	for uri := range second {
		if first[uri] != 0 {
			t.Errorf("second tick revisited %s before the rest were refreshed", uri)
		}
	}
}

// B2 for the facet cache: past 512 entries the least recently served goes.
func TestFacetCache_EvictsLeastRecentlyServedPast512(t *testing.T) {
	store := &facetCountingStore{}
	fc, _ := newBoundTestFacetCache(t, store, maxCachedEntries)
	ctx := context.Background()
	for _, i := range []int{0, maxCachedEntries} {
		u, _ := url.Parse(boundTestURI(i))
		if _, err := fc.serve(ctx, store, boundTestURI(i), u); err != nil {
			t.Fatal(err)
		}
	}

	if got := len(fc.entries); got != maxCachedEntries {
		t.Errorf("len(entries) = %d, want %d", got, maxCachedEntries)
	}
	if _, ok := fc.entries[boundTestURI(0)]; !ok {
		t.Errorf("%s was evicted though it was served most recently", boundTestURI(0))
	}
	if _, ok := fc.entries[boundTestURI(1)]; ok {
		t.Errorf("%s (least recently served) was not evicted", boundTestURI(1))
	}
}
