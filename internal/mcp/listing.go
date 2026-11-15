package mcp

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis_eval"
)

// Filter-honoring modes a listing can report (cutting-garden#160). Mirrors
// the RFC 0012 §6 filter-precedence order: a plugin's own efficient
// filtered/enriched fetch is preferred, host-side Facets matching is the
// fallback, and an honest "could not filter" signal is the last resort —
// never a silent unfiltered listing presented as filtered.
const (
	// filterModePlugin: the plugin's EnrichedLister applied the filter
	// itself (a data-bearing REPORT/query, RFC 0012 §6 branch a).
	filterModePlugin = "plugin"
	// filterModeHost: the framework applied the filter host-side, over
	// Facets the plain ListRoots already populated (branch b).
	filterModeHost = "host"
	// filterModeNone: a filter was requested but could not be applied by
	// either path — the returned nodes are UNFILTERED (branch c). Never
	// silently presented as filtered.
	filterModeNone = "none"
)

// enrichedListing resolves node's children ENRICHED — Facets and Fields
// populated — and narrowed by filter, per the RFC 0012 §6 precedence
// (cutting-garden#160):
//
//	(a) lister.(EnrichedLister), when implemented, in ONE data-bearing
//	    fetch (caldav: the same REPORT foldCalendarFacets already issues);
//	(b) else the plain ListRoots nodes, host-side filtered via
//	    FacetFilter.Matches over whatever Facets they already carry (file,
//	    git, ytdlp populate Facets on ListRoots today, so this is free);
//	(c) else — no EnrichedLister AND no Facets to filter on — the
//	    unfiltered ListRoots nodes, with filterMode reporting "none" so the
//	    caller can surface the honest signal rather than pretend to have
//	    filtered.
//
// A nil/empty filter always requests the full enriched listing (branch a
// when available, else b/c degenerate to "no filtering to do"); filterMode
// is then meaningless and callers should ignore it (ReadResource does).
func enrichedListing(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	u *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (nodes []cutting_garden_plugins.Node, filterMode string, err error) {
	if el, ok := lister.(cutting_garden_plugins.EnrichedLister); ok {
		enriched, handled, eerr := el.ListEnriched(ctx, u, filter)
		if eerr != nil {
			return nil, "", eerr
		}
		if handled {
			mode := ""
			if len(filter) > 0 {
				mode = filterModePlugin
			}
			return enriched, mode, nil
		}
	}

	nodes, err = lister.ListRoots(ctx, u)
	if err != nil {
		return nil, "", err
	}
	if len(filter) == 0 {
		return nodes, "", nil
	}
	if !anyNodeHasFacets(nodes) {
		// Branch c: no plugin capability and nothing to filter host-side —
		// unfiltered, explicitly signaled as such.
		return nodes, filterModeNone, nil
	}
	aliases := cutting_garden_plugins.NoValueAliasesFor(lister)
	filtered := make([]cutting_garden_plugins.Node, 0, len(nodes))
	for _, n := range nodes {
		if filter.MatchesNode(aliases, n.Type, n.Facets) {
			filtered = append(filtered, n)
		}
	}
	return filtered, filterModeHost, nil
}

// filteredListing is a one-step filter's listing (RFC 0012 §6, §12.3): the
// pushdown through enrichedListing's precedence, then the residual matched
// host-side over what came back. The residual follows the pushdown's honesty
// rule: where nothing carries facets to match against (branch c; a negation
// would match every node) it is not applied either, and the mode is "none".
// A residual-only filter is reported "host" when it applied.
func filteredListing(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	u *url.URL,
	pushdown cutting_garden_plugins.FacetFilter,
	residual trellis_eval.Residual,
	opts ...trellis_eval.EvaluateOption,
) ([]cutting_garden_plugins.Node, string, error) {
	nodes, mode, err := enrichedListing(ctx, lister, u, pushdown)
	if err != nil || residual.Empty() || mode == filterModeNone {
		return nodes, mode, err
	}
	if mode == "" && !anyNodeHasFacets(nodes) {
		return nodes, filterModeNone, nil
	}
	nodes, err = residual.Filter(ctx, lister, u, nodes, opts...)
	if err != nil {
		return nil, "", err
	}
	if mode == "" {
		mode = filterModeHost
	}
	return nodes, mode, nil
}

// splitFilterFor splits a caller's one-step filter against lister's declared
// facet schema and validates the pushdown (cutting-garden#161, #388).
func splitFilterFor(
	raw string, lister cutting_garden_plugins.RootLister,
) (cutting_garden_plugins.FacetFilter, trellis_eval.Residual, error) {
	var schema []cutting_garden_plugins.NodeTypeFacets
	if fd, ok := lister.(cutting_garden_plugins.FacetDescriber); ok {
		schema = fd.DescribeFacets()
	}
	if len(schema) == 0 {
		schema = nil
	}
	pushdown, residual, _, err := trellis_eval.SplitFilter(raw, schema)
	if err != nil {
		return nil, trellis_eval.Residual{}, err
	}
	if err := pushdown.Validate(schema); err != nil {
		return nil, trellis_eval.Residual{}, err
	}
	if err := residual.ValidateFields(lister); err != nil {
		return nil, trellis_eval.Residual{}, err
	}
	return pushdown, residual, nil
}

// anyNodeHasFacets reports whether any node in the plain (non-enriched)
// listing carries facet membership — the signal that host-side filtering
// (branch b) is meaningful for this plugin's ListRoots output. A plugin
// that never populates Node.Facets on ListRoots (caldav's bare listing)
// yields false for every node, so filtering falls to the honest branch c
// rather than incorrectly matching every predicate against an empty map.
func anyNodeHasFacets(nodes []cutting_garden_plugins.Node) bool {
	for _, n := range nodes {
		if len(n.Facets) > 0 {
			return true
		}
	}
	return false
}
