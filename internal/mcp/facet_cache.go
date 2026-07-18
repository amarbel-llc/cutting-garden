package mcp

import (
	"context"
	"net/url"
	"sync"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// Facet summary freshness states served on the wire (RFC 0012 §11.2).
// Produced by the memoization layer, never by plugins.
const (
	// freshnessFresh: the change token was verified at/after computation.
	freshnessFresh = "fresh"
	// freshnessUnverified: no verification since computation — a tokenless
	// plugin inside its TTL window, or a tokened entry the refresher has
	// not revisited yet.
	freshnessUnverified = "unverified"
	// freshnessStale: a newer token is known or the last refresh failed;
	// the served summary is the last good one.
	freshnessStale = "stale"
)

const (
	// facetRefreshInterval is the eager-refresh cadence (RFC 0012 §11.2):
	// each tick verifies tokens (cheap) and recomputes only moved entries.
	facetRefreshInterval = 5 * time.Minute
	// facetTTL bounds how long a tokenless (no FacetVersioner) summary is
	// served before the refresher recomputes it unconditionally.
	facetTTL = 15 * time.Minute
)

// facetCacheEntry is one node's memoized summary plus its provenance.
type facetCacheEntry struct {
	result     cutting_garden_plugins.FacetResult
	token      string
	hasToken   bool
	computedAt time.Time
	verifiedAt time.Time
	dirty      bool
	lastErr    string

	// revalidateAfter, when nonzero, is the volatile window (RFC 0012
	// §11.3): the min RevalidateAfter over the volatile dimensions
	// PRESENT in result.Summary. Past computedAt+revalidateAfter the
	// entry expires regardless of token state — token verification alone
	// no longer proves freshness for a (data, now) summary.
	revalidateAfter time.Duration
}

// windowExpired reports whether the entry's volatile window has lapsed
// at now. Always false for pure entries (revalidateAfter == 0).
func (e *facetCacheEntry) windowExpired(now time.Time) bool {
	return e.revalidateAfter > 0 &&
		!now.Before(e.computedAt.Add(e.revalidateAfter))
}

func (e *facetCacheEntry) freshness(now time.Time) string {
	switch {
	case e.lastErr != "" || e.dirty:
		return freshnessStale
	case e.windowExpired(now):
		// Volatile window lapsed without recomputation: the served
		// summary is last-good by definition (RFC 0012 §11.3's
		// staleness bound) until the refresher replaces it.
		return freshnessStale
	case e.hasToken && !e.verifiedAt.Before(e.computedAt):
		return freshnessFresh
	default:
		return freshnessUnverified
	}
}

// facetCache memoizes hoisted facet summaries per node URI (RFC 0012 §11):
// in-memory, token-gated when the plugin implements FacetVersioner, TTL
// otherwise. Reads serve the cache and never recompute inline once an entry
// exists; the refresher owns recomputation. Live-tree summaries are
// deliberately NOT persisted across restarts (the #133 adopter calibration);
// content-addressed persistence for captured trees waits for a
// receipt-traversal surface to exist.
type facetCache struct {
	mu      sync.Mutex
	entries map[string]*facetCacheEntry
	now     func() time.Time
}

func newFacetCache() *facetCache {
	return &facetCache{
		entries: map[string]*facetCacheEntry{},
		now:     time.Now,
	}
}

// serve returns the facet view for node per RFC 0012 §11.2: a memoized
// entry is served as-is (its token possibly unverified — verification is
// the refresher's job, not the read path's); a miss computes once, caches,
// and serves. A declining counter (ok == false) yields (nil, nil). A
// compute error on a MISS is returned for the caller's §9 degrade path; an
// existing entry is never invalidated by a read.
func (fc *facetCache) serve(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) (*facetView, error) {
	counter, isCounter := lister.(cutting_garden_plugins.FacetCounter)
	if !isCounter {
		return nil, nil
	}

	fc.mu.Lock()
	entry := fc.entries[uri]
	if entry != nil {
		view := fc.viewLocked(entry)
		fc.mu.Unlock()
		return view, nil
	}
	fc.mu.Unlock()

	return fc.computeAndStore(ctx, lister, counter, uri, u)
}

// computeAndStore is the first-touch path: fetch the change token
// (best-effort — the token is an optimization, never a gate), compute the
// summary, memoize, serve. Two racing first touches compute twice and the
// later store wins — harmless, both computed the same thing.
func (fc *facetCache) computeAndStore(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	counter cutting_garden_plugins.FacetCounter,
	uri string,
	u *url.URL,
) (*facetView, error) {
	token, hasToken := fc.tokenFor(ctx, lister, u)

	result, ok, err := counter.FacetCounts(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	now := fc.now()
	entry := &facetCacheEntry{
		result:          result,
		token:           token,
		hasToken:        hasToken,
		computedAt:      now,
		verifiedAt:      now,
		revalidateAfter: volatileWindowFor(lister, result.Summary),
	}
	fc.mu.Lock()
	fc.entries[uri] = entry
	view := fc.viewLocked(entry)
	fc.mu.Unlock()
	return view, nil
}

// volatileWindowFor derives an entry's volatile window (RFC 0012 §11.3):
// the minimum RevalidateAfter over the plugin's declared volatile
// dimensions that are PRESENT in the summary. Presence is a correct
// trigger because §11.3 obliges plugins to emit a volatile dimension
// (informative zeros included) whenever the subtree contains its node
// type — so an empty-but-fillable bucket set is visible, and a wholly
// absent type can only start contributing via a data change the token
// already catches. Zero means the summary is pure and token/TTL
// semantics govern unchanged.
func volatileWindowFor(
	lister cutting_garden_plugins.RootLister,
	summary cutting_garden_plugins.FacetSummary,
) time.Duration {
	if len(summary) == 0 {
		return 0
	}
	describer, ok := lister.(cutting_garden_plugins.FacetDescriber)
	if !ok {
		return 0
	}

	var window time.Duration
	for _, typeFacets := range describer.DescribeFacets() {
		for _, dimension := range typeFacets.Dimensions {
			if dimension.RevalidateAfter <= 0 {
				continue
			}
			if _, present := summary[dimension.Key]; !present {
				continue
			}
			if window == 0 || dimension.RevalidateAfter < window {
				window = dimension.RevalidateAfter
			}
		}
	}
	return window
}

// tokenFor obtains the node's change token when the plugin offers one.
// Any failure (no capability, ok=false, error) degrades to tokenless — the
// entry then refreshes on TTL instead.
func (fc *facetCache) tokenFor(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	u *url.URL,
) (token string, hasToken bool) {
	versioner, ok := lister.(cutting_garden_plugins.FacetVersioner)
	if !ok {
		return "", false
	}
	token, tokenOK, err := versioner.FacetVersion(ctx, u)
	if err != nil || !tokenOK {
		return "", false
	}
	return token, true
}

// viewLocked projects an entry onto the wire shape. Caller holds fc.mu.
func (fc *facetCache) viewLocked(e *facetCacheEntry) *facetView {
	view := &facetView{
		Facets:     e.result.Summary,
		Complete:   e.result.Complete,
		ComputedAt: e.computedAt.UTC().Format(time.RFC3339),
		Freshness:  e.freshness(fc.now()),
		Error:      e.lastErr,
	}
	if e.revalidateAfter > 0 {
		// validUntil bounds only the volatile dimensions' currency
		// (RFC 0012 §11.3); pure dimensions in the same summary remain
		// token-fresh past it.
		view.ValidUntil = e.computedAt.Add(e.revalidateAfter).
			UTC().Format(time.RFC3339)
	}
	return view
}

// uris snapshots the cached node set for a refresh pass.
func (fc *facetCache) uris() []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	out := make([]string, 0, len(fc.entries))
	for uri := range fc.entries {
		out = append(out, uri)
	}
	return out
}

// maintain is the eager-refresh loop (RFC 0012 §11.2): every interval,
// verify each cached entry's token (cheap) and recompute only the entries
// whose token moved or whose TTL lapsed. Runs until ctx is done.
func (fc *facetCache) maintain(
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
			for _, uri := range fc.uris() {
				fc.refreshOne(ctx, resolve, uri)
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}

// refreshOne re-validates a single cached entry: token unchanged → mark
// verified; token moved or TTL lapsed → recompute; any failure → keep the
// last good summary and mark the entry stale with the error recorded
// (RFC 0012 §9's implicit-surface degrade).
func (fc *facetCache) refreshOne(
	ctx context.Context,
	resolve resolveFunc,
	uri string,
) {
	fc.mu.Lock()
	entry := fc.entries[uri]
	if entry == nil {
		fc.mu.Unlock()
		return
	}
	prevToken, hadToken := entry.token, entry.hasToken
	computedAt := entry.computedAt
	windowExpired := entry.windowExpired(fc.now())
	fc.mu.Unlock()

	u, lister, err := resolve(uri)
	if err != nil {
		fc.markError(uri, err)
		return
	}
	counter, isCounter := lister.(cutting_garden_plugins.FacetCounter)
	if !isCounter {
		fc.drop(uri)
		return
	}

	// A lapsed volatile window (RFC 0012 §11.3) forces recomputation
	// unconditionally: the summary is a function of (data, now), so an
	// unmoved token no longer proves freshness. Pure entries keep the
	// original two paths.
	if !windowExpired {
		// Cheap path: an unmoved token re-verifies the entry with one
		// round trip and no recomputation.
		if hadToken {
			if versioner, ok := lister.(cutting_garden_plugins.FacetVersioner); ok {
				token, tokenOK, verr := versioner.FacetVersion(ctx, u)
				if verr != nil {
					fc.markError(uri, verr)
					return
				}
				if tokenOK && token == prevToken {
					fc.markVerified(uri)
					return
				}
			}
		} else if fc.now().Sub(computedAt) < facetTTL {
			return
		}
	}

	// Token moved, TTL lapsed, or volatile window lapsed: recompute and
	// replace.
	token, hasToken := fc.tokenFor(ctx, lister, u)
	result, ok, err := counter.FacetCounts(ctx, u, nil)
	if err != nil {
		fc.markError(uri, err)
		return
	}
	if !ok {
		fc.drop(uri)
		return
	}
	now := fc.now()
	fc.mu.Lock()
	fc.entries[uri] = &facetCacheEntry{
		result:          result,
		token:           token,
		hasToken:        hasToken,
		computedAt:      now,
		verifiedAt:      now,
		revalidateAfter: volatileWindowFor(lister, result.Summary),
	}
	fc.mu.Unlock()
}

func (fc *facetCache) markVerified(uri string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if e := fc.entries[uri]; e != nil {
		e.verifiedAt = fc.now()
		e.dirty = false
		e.lastErr = ""
	}
}

func (fc *facetCache) markError(uri string, err error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if e := fc.entries[uri]; e != nil {
		e.dirty = true
		e.lastErr = err.Error()
	}
}

func (fc *facetCache) drop(uri string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.entries, uri)
}
