package mcp

import (
	"context"
	"slices"
)

// The memory and request bounds on the listing and facet caches
// (cutting-garden#393 B1/B2). Without them one mcp session that browses a
// large tree grows the caches without limit, and every refresher tick asks a
// plugin about every entry it ever served.
const (
	// maxRefreshesPerTick caps how many entries one refresher tick sends to
	// a plugin. A cursor carries the rest to the next ticks, so each entry is
	// still revisited, once per ceil(entries/32) ticks.
	maxRefreshesPerTick = 32
	// maxCachedEntries caps each cache. Storing a new entry past it evicts the
	// least recently served unpinned entry; the configured roots are pinned.
	maxCachedEntries = 512
)

// refreshCursor walks a cache's entries in URI order across refresher ticks:
// one tick resumes after the last URI the previous tick refreshed and stops at
// maxRefreshesPerTick refreshes or at the end of the pass, so a pass over N
// entries takes ceil(N/32) ticks. Only the cache's refresher goroutine (or a
// test calling refreshTick directly) touches it, so it needs no lock.
type refreshCursor struct {
	after string
}

// tick refreshes the next stretch of uris. refresh reports whether it called
// the plugin; an entry it skips (lazy, backing off, inside its TTL) does not
// count against the tick's budget.
func (c *refreshCursor) tick(
	ctx context.Context,
	uris []string,
	refresh func(uri string) bool,
) {
	slices.Sort(uris)
	start, found := slices.BinarySearch(uris, c.after)
	if found {
		start++
	}

	refreshed := 0
	for _, uri := range uris[start:] {
		if refresh(uri) {
			refreshed++
		}
		if ctx.Err() != nil || refreshed == maxRefreshesPerTick {
			c.after = uri
			return
		}
	}
	c.after = ""
}

// servedEntry is a cache entry that records when it was last served, as a
// value of its cache's served counter.
type servedEntry interface {
	servedSeq() uint64
}

// evictLeastRecentlyServed makes room for one new entry: when entries already
// holds maxCachedEntries, it deletes the unpinned entry served longest ago. A
// cache whose every entry is pinned is left to grow; roots are few.
func evictLeastRecentlyServed[E servedEntry](
	entries map[string]E,
	pinned func(uri string) bool,
) {
	if len(entries) < maxCachedEntries {
		return
	}

	victim, oldest := "", uint64(0)
	for uri, entry := range entries {
		if pinned != nil && pinned(uri) {
			continue
		}
		if seq := entry.servedSeq(); victim == "" || seq < oldest {
			victim, oldest = uri, seq
		}
	}
	if victim != "" {
		delete(entries, victim)
	}
}

// pinnedURIs is the pinned predicate for a fixed URI set; nil when the set is
// empty.
func pinnedURIs(uris []string) func(uri string) bool {
	if len(uris) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		set[uri] = struct{}{}
	}
	return func(uri string) bool {
		_, ok := set[uri]
		return ok
	}
}
