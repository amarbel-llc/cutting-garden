package mcp

import (
	"context"
	"net/url"
	"sync"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// listingCache memoizes ENRICHED, UNFILTERED container listings per node
// URI (cutting-garden#160) — the caching half of "enriched by default":
// without it, every list_nodes/resources-read call on a plugin whose
// enrichment needs a data-bearing fetch (caldav's one-REPORT ListEnriched)
// would re-run that fetch on every read. It reuses the exact token/TTL/
// volatile-window machinery facet_cache.go built for #135 (tokenFor,
// volatileWindowFor, the same freshness/TTL constants) rather than
// reinventing memoization — the two caches are frequently backed by the
// SAME underlying plugin fetch (caldav's FacetCounts and ListEnriched both
// REPORT the same objects), so keeping their expiry semantics identical
// matters as much as the code reuse itself.
//
// A FILTERED listing request (list_nodes' filter param) is an explicit ask
// — RFC 0012 §9's precedent from ReadFacets — and always computes fresh via
// enrichedListing directly, bypassing this cache entirely; only the
// unfiltered "give me the enriched listing" case is memoized. This mirrors
// facetCache's nil-vs-filter split exactly: a filtered read is inherently
// narrower and rarer than the base listing every consumer of a container
// pays for, so caching it would mean an unbounded key space (one entry per
// distinct filter string) for comparatively little reuse.
type listingCache struct {
	mu      sync.Mutex
	entries map[string]*listingCacheEntry
	now     func() time.Time
}

// listingCacheEntry is one node's memoized enriched-unfiltered listing plus
// its provenance. Structurally mirrors facetCacheEntry.
type listingCacheEntry struct {
	nodes      []cutting_garden_plugins.Node
	token      string
	hasToken   bool
	computedAt time.Time
	verifiedAt time.Time
	dirty      bool
	lastErr    string

	// revalidateAfter is the volatile window (RFC 0012 §11.3), derived from
	// which volatile facet dimensions are PRESENT across the cached nodes'
	// Facets — the same rule volatileWindowFor applies to a facet summary,
	// reused here via a presence-only pseudo-summary (facetKeyPresenceOf).
	revalidateAfter time.Duration
}

func (e *listingCacheEntry) windowExpired(now time.Time) bool {
	return e.revalidateAfter > 0 &&
		!now.Before(e.computedAt.Add(e.revalidateAfter))
}

// freshness classifies the entry's provenance, mirroring
// facetCacheEntry.freshness exactly (the two caches share their expiry
// semantics — see the type doc): a recorded error or dirty mark, or a
// lapsed volatile window, is stale; a token verified at/after computation
// is fresh; anything else is unverified.
func (e *listingCacheEntry) freshness(now time.Time) string {
	switch {
	case e.lastErr != "" || e.dirty:
		return freshnessStale
	case e.windowExpired(now):
		return freshnessStale
	case e.hasToken && !e.verifiedAt.Before(e.computedAt):
		return freshnessFresh
	default:
		return freshnessUnverified
	}
}

// listingProvenance is the snapshot identity of a served listing
// (cutting-garden#203): the FacetVersion token the entry was computed
// against, plus when and how fresh. hasVersion is false when the entry's
// plugin declares no FacetVersioner — the listing then carries no version.
// The token MUST correspond to the nodes served alongside it (serve returns
// the entry's own token, never a fresh re-read), so two calls comparing
// tokens compare the snapshots they actually received.
type listingProvenance struct {
	hasVersion bool
	version    string
	computedAt time.Time
	freshness  string
}

// provenance projects an entry to its served snapshot identity.
func (e *listingCacheEntry) provenance(now time.Time) listingProvenance {
	if !e.hasToken {
		return listingProvenance{}
	}

	return listingProvenance{
		hasVersion: true,
		version:    e.token,
		computedAt: e.computedAt,
		freshness:  e.freshness(now),
	}
}

// view projects the provenance to the wire-form listingVersion (RFC3339
// timestamp); the zero (all-omitempty) value when no version is available,
// so a listing whose plugin declares no FacetVersioner carries no version
// fields at all.
func (p listingProvenance) view() listingVersion {
	if !p.hasVersion {
		return listingVersion{}
	}

	return listingVersion{
		Version:           p.version,
		VersionComputedAt: p.computedAt.UTC().Format(time.RFC3339),
		Freshness:         p.freshness,
	}
}

func newListingCache() *listingCache {
	return &listingCache{entries: map[string]*listingCacheEntry{}, now: time.Now}
}

// serve returns the enriched, unfiltered listing for uri: a memoized entry
// is served as-is (subject to its volatile window), a miss computes once
// via enrichedListing, caches, and serves. Unlike facetCache.serve, a
// compute error on a miss is NOT degraded — a container's child listing
// cannot be faked the way a facet summary's error-noted block can, so the
// error propagates exactly as it did before #160 (ReadResource's existing
// "list roots under %s" wrap).
func (lc *listingCache) serve(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) ([]cutting_garden_plugins.Node, listingProvenance, error) {
	lc.mu.Lock()
	entry := lc.entries[uri]
	if entry != nil && !entry.windowExpired(lc.now()) {
		nodes := entry.nodes
		prov := entry.provenance(lc.now())
		lc.mu.Unlock()
		return nodes, prov, nil
	}
	lc.mu.Unlock()

	return lc.computeAndStore(ctx, lister, uri, u)
}

// computeAndStore is the first-touch (and expired-window) path: fetch the
// change token (best-effort), compute the enriched listing, memoize, serve.
// Two racing computes for the same uri compute twice and the later store
// wins — harmless, both computed the same thing.
func (lc *listingCache) computeAndStore(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) ([]cutting_garden_plugins.Node, listingProvenance, error) {
	token, hasToken := tokenFor(ctx, lister, u)

	nodes, _, err := enrichedListing(ctx, lister, u, nil)
	if err != nil {
		return nil, listingProvenance{}, err
	}

	now := lc.now()
	entry := &listingCacheEntry{
		nodes:           nodes,
		token:           token,
		hasToken:        hasToken,
		computedAt:      now,
		verifiedAt:      now,
		revalidateAfter: volatileWindowFor(lister, facetKeyPresenceOf(nodes)),
	}
	lc.mu.Lock()
	lc.entries[uri] = entry
	lc.mu.Unlock()
	return nodes, entry.provenance(now), nil
}

// facetKeyPresenceOf builds a presence-only pseudo-summary from a node set's
// Facets — the shape volatileWindowFor expects (a FacetSummary keyed by
// dimension), with the histogram values themselves irrelevant since
// volatileWindowFor only checks key presence. Lets listingCache reuse
// volatileWindowFor unchanged rather than duplicating its dimension-lookup
// logic for a differently-shaped cache.
func facetKeyPresenceOf(
	nodes []cutting_garden_plugins.Node,
) cutting_garden_plugins.FacetSummary {
	summary := cutting_garden_plugins.FacetSummary{}
	for _, n := range nodes {
		for dim := range n.Facets {
			if _, ok := summary[dim]; !ok {
				summary[dim] = cutting_garden_plugins.FacetHistogram{}
			}
		}
	}
	return summary
}

// uris snapshots the cached node set for a refresh pass.
func (lc *listingCache) uris() []string {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	out := make([]string, 0, len(lc.entries))
	for uri := range lc.entries {
		out = append(out, uri)
	}
	return out
}

// maintain is the eager-refresh loop, mirroring facetCache.maintain: every
// interval, verify each cached entry's token (cheap) and recompute only the
// entries whose token moved, whose volatile window lapsed, or whose TTL
// lapsed. Runs until ctx is done.
func (lc *listingCache) maintain(
	ctx context.Context,
	resolve resolveFunc,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, uri := range lc.uris() {
				lc.refreshOne(ctx, resolve, uri)
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}

// refreshOne re-validates a single cached entry: token unchanged (and
// volatile window not lapsed) → mark verified; token moved, volatile window
// lapsed, or TTL lapsed → recompute; any failure → keep the last good
// listing and mark the entry stale with the error recorded, exactly
// mirroring facetCache.refreshOne's degrade path (the CACHE degrades on
// refresh failure; a cold-miss compute failure still propagates from
// serve/computeAndStore).
func (lc *listingCache) refreshOne(
	ctx context.Context,
	resolve resolveFunc,
	uri string,
) {
	lc.mu.Lock()
	entry := lc.entries[uri]
	if entry == nil {
		lc.mu.Unlock()
		return
	}
	prevToken, hadToken := entry.token, entry.hasToken
	computedAt := entry.computedAt
	windowExpired := entry.windowExpired(lc.now())
	lc.mu.Unlock()

	u, lister, err := resolve(uri)
	if err != nil {
		lc.markError(uri, err)
		return
	}

	if !windowExpired {
		if hadToken {
			if versioner, ok := lister.(cutting_garden_plugins.FacetVersioner); ok {
				token, tokenOK, verr := versioner.FacetVersion(ctx, u)
				if verr != nil {
					lc.markError(uri, verr)
					return
				}
				if tokenOK && token == prevToken {
					lc.markVerified(uri)
					return
				}
			}
		} else if lc.now().Sub(computedAt) < facetTTL {
			return
		}
	}

	token, hasToken := tokenFor(ctx, lister, u)
	nodes, _, err := enrichedListing(ctx, lister, u, nil)
	if err != nil {
		lc.markError(uri, err)
		return
	}
	now := lc.now()
	lc.mu.Lock()
	lc.entries[uri] = &listingCacheEntry{
		nodes:           nodes,
		token:           token,
		hasToken:        hasToken,
		computedAt:      now,
		verifiedAt:      now,
		revalidateAfter: volatileWindowFor(lister, facetKeyPresenceOf(nodes)),
	}
	lc.mu.Unlock()
}

func (lc *listingCache) markVerified(uri string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if e := lc.entries[uri]; e != nil {
		e.verifiedAt = lc.now()
		e.dirty = false
		e.lastErr = ""
	}
}

func (lc *listingCache) markError(uri string, err error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if e := lc.entries[uri]; e != nil {
		e.dirty = true
		e.lastErr = err.Error()
	}
}
