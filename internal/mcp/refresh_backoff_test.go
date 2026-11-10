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

// Entries that failed together do not retry on the same tick
// (cutting-garden#393): each URI's backoff is spread by a deterministic amount,
// up to +25%, so a quota exhaustion does not line up every retry on one tick.
func TestRefreshBackoffFor_SpreadsByURI(t *testing.T) {
	uris := []string{"faketest://h/a", "faketest://h/b", "faketest://h/c", "faketest://h/d"}
	for _, failures := range []int{1, 3, 9} {
		base := refreshBackoff(failures)
		seen := map[time.Duration]bool{}
		for _, uri := range uris {
			got := refreshBackoffFor(uri, failures)
			if got < base || got > base+base/4 {
				t.Errorf("refreshBackoffFor(%q, %d) = %v, want within [%v, %v]",
					uri, failures, got, base, base+base/4)
			}
			if again := refreshBackoffFor(uri, failures); again != got {
				t.Errorf("refreshBackoffFor(%q, %d) is not deterministic: %v then %v",
					uri, failures, got, again)
			}
			seen[got] = true
		}
		if len(seen) < 2 {
			t.Errorf("failures=%d: all %d URIs got the same backoff %v",
				failures, len(uris), base)
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
	entry := lc.entries[u.String()]
	at := entry.computedAt
	tick := func(to time.Time) {
		at = to
		lc.now = func() time.Time { return at }
		lc.refreshOne(ctx, resolve, u.String())
	}

	store.failing, store.calls = true, 0
	tick(at.Add(facetTTL + time.Minute)) // TTL lapsed: refresh, which fails
	if store.calls != 1 {
		t.Fatalf("first refresh calls = %d, want 1", store.calls)
	}
	failedAt := at
	if got, want := entry.retryAfter.Sub(failedAt), refreshBackoffFor(u.String(), 1); got != want {
		t.Errorf("first backoff = %v, want %v", got, want)
	}
	tick(entry.retryAfter.Add(-time.Second)) // inside the backoff
	if store.calls != 1 {
		t.Errorf("a refresh inside the backoff called the plugin: calls = %d", store.calls)
	}
	tick(entry.retryAfter) // backoff over: retry, fails
	if store.calls != 2 {
		t.Errorf("after the backoff calls = %d, want 2", store.calls)
	}
	if got, want := entry.retryAfter.Sub(at), refreshBackoffFor(u.String(), 2); got != want {
		t.Errorf("second backoff = %v, want %v (doubled)", got, want)
	}
	tick(entry.retryAfter.Add(-time.Second)) // inside the second backoff
	if store.calls != 2 {
		t.Errorf("a refresh inside the second backoff called the plugin: calls = %d", store.calls)
	}

	store.failing = false
	tick(entry.retryAfter) // succeeds
	if e := lc.entries[u.String()]; e.failures != 0 || e.lastErr != "" {
		t.Errorf("a successful refresh kept the failure: %+v", e)
	}
}
