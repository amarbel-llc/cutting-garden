package mcp

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func lazyFor(schemes ...string) func(string) bool {
	return backgroundRefreshOff(schemes)
}

// A scheme opted out of background refresh is never recomputed by the
// refresher, so an idle server makes no plugin calls for it.
func TestListingCache_NoBackgroundRefreshSkipsTheRefresher(t *testing.T) {
	store := &growingStore{children: []string{"a"}}
	lc := newListingCache()
	lc.lazy = lazyFor("faketest")
	u, _ := url.Parse("faketest://h/")
	if _, _, err := lc.serve(context.Background(), store, u.String(), u); err != nil {
		t.Fatal(err)
	}
	computed := lc.entries[u.String()].computedAt

	lc.now = func() time.Time { return computed.Add(2 * facetTTL) }
	store.children = append(store.children, "b")
	lc.refreshOne(context.Background(), growingResolve(store), u.String())

	if got := lc.entries[u.String()]; len(got.nodes) != 1 {
		t.Errorf("the refresher recomputed an opted-out entry: %d nodes", len(got.nodes))
	}
}

// Instead, a read past the TTL recomputes, so opting out never serves a
// listing forever.
func TestListingCache_NoBackgroundRefreshRecomputesOnAStaleRead(t *testing.T) {
	store := &growingStore{children: []string{"a"}}
	lc := newListingCache()
	lc.lazy = lazyFor("*")
	u, _ := url.Parse("faketest://h/")
	ctx := context.Background()
	if _, _, err := lc.serve(ctx, store, u.String(), u); err != nil {
		t.Fatal(err)
	}
	computed := lc.entries[u.String()].computedAt
	store.children = append(store.children, "b")

	lc.now = func() time.Time { return computed.Add(facetTTL / 2) }
	if nodes, _, _ := lc.serve(ctx, store, u.String(), u); len(nodes) != 1 {
		t.Errorf("a read inside the TTL recomputed: %d nodes", len(nodes))
	}

	lc.now = func() time.Time { return computed.Add(2 * facetTTL) }
	if nodes, _, _ := lc.serve(ctx, store, u.String(), u); len(nodes) != 2 {
		t.Errorf("a read past the TTL served the old listing: %d nodes", len(nodes))
	}
}

// Without the opt-out, nothing changes: other schemes stay eager.
func TestBackgroundRefreshOff_MatchesOnlyNamedSchemes(t *testing.T) {
	off := lazyFor("smith")
	for uri, want := range map[string]bool{
		"smith://github/x":   true,
		"caldav://acct/cal/": false,
		"%%bad":              false,
	} {
		if got := off(uri); got != want {
			t.Errorf("off(%q) = %v, want %v", uri, got, want)
		}
	}
	if lazyFor() != nil {
		t.Error("no schemes named, yet an opt-out predicate was built")
	}
}
