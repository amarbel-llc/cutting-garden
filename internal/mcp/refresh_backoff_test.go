package mcp

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// failingStore lists once, then fails every listing while failing is set:
// a plugin whose backend refuses (a GitHub 403 on an empty quota).
type failingStore struct {
	growingStore
	failing bool
	calls   int
}

func (f *failingStore) ListRoots(ctx context.Context, u *url.URL) ([]cutting_garden_plugins.Node, error) {
	f.calls++
	if f.failing {
		return nil, errors.New("403 rate limited")
	}
	return f.growingStore.ListRoots(ctx, u)
}

func TestRefreshBackoff_DoublesToACap(t *testing.T) {
	for failures, want := range map[int]time.Duration{
		1: facetRefreshInterval,
		2: 2 * facetRefreshInterval,
		3: 4 * facetRefreshInterval,
		9: maxRefreshBackoff,
	} {
		if got := refreshBackoff(failures); got != want {
			t.Errorf("refreshBackoff(%d) = %v, want %v", failures, got, want)
		}
	}
}

// A refresh that fails is not retried on every tick (cutting-garden#393):
// the entry waits out a growing backoff, and a success clears it.
func TestListingCache_FailedRefreshBacksOff(t *testing.T) {
	store := &failingStore{growingStore: growingStore{children: []string{"a"}}}
	resolve := func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, err := url.Parse(uriStr)
		return u, store, err
	}
	lc := newListingCache()
	u, _ := url.Parse("faketest://h/")
	ctx := context.Background()
	if _, _, err := lc.serve(ctx, store, u.String(), u); err != nil {
		t.Fatal(err)
	}
	start := lc.entries[u.String()].computedAt
	tick := func(after time.Duration) {
		lc.now = func() time.Time { return start.Add(after) }
		lc.refreshOne(ctx, resolve, u.String())
	}

	store.failing, store.calls = true, 0
	tick(facetTTL + time.Minute) // TTL lapsed: refresh, which fails
	if store.calls != 1 {
		t.Fatalf("first refresh calls = %d, want 1", store.calls)
	}
	tick(facetTTL + time.Minute + facetRefreshInterval/2) // inside the backoff
	if store.calls != 1 {
		t.Errorf("a refresh inside the backoff called the plugin: calls = %d", store.calls)
	}
	tick(facetTTL + time.Minute + facetRefreshInterval) // backoff over: retry, fails
	if store.calls != 2 {
		t.Errorf("after the backoff calls = %d, want 2", store.calls)
	}
	tick(facetTTL + time.Minute + 2*facetRefreshInterval) // second backoff is twice as long
	if store.calls != 2 {
		t.Errorf("the second backoff did not double: calls = %d", store.calls)
	}

	store.failing = false
	tick(facetTTL + time.Minute + 3*facetRefreshInterval) // succeeds
	if e := lc.entries[u.String()]; e.failures != 0 || e.lastErr != "" {
		t.Errorf("a successful refresh kept the failure: %+v", e)
	}
}
