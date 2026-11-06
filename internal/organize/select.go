package organize

import (
	"context"
	"net/url"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/node_view"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/cutting-garden/internal/trellis_eval"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// selectNodes resolves the node set organize operates on: the trellis query's
// matches when a query is given, else the anchor's enriched immediate children.
// Either path prefers the plugin's EnrichedLister so Node.Facets is populated —
// caldav's status/date facets are body-derived and a plain ListRoots leaves them
// empty (cutting-garden#212), and organize groups by exactly those facets.
// tagsOverride is the global `[tags] interpreter` config override, so a bare-tag
// term selects the same nodes here as in `list --query` and the mcp tools
// (cutting-garden#245).
func selectNodes(
	ctx context.Context,
	lister cgp.RootLister,
	anchor *url.URL,
	query string,
	tagsOverride string,
) ([]cgp.Node, error) {
	if query != "" {
		q, perr := trellis.Parse(query)
		if perr != nil {
			return nil, errors.BadRequestf("organize: selection query %q: %s", query, perr)
		}
		// withTerminal makes the synthetic `_terminal` dimension matchable, so a
		// `_terminal=no`/`_terminal=yes` predicate evaluates through the ordinary
		// facet path (cutting-garden#214) in both generate and apply; a no-op for a
		// plugin declaring no terminal values.
		return trellis_eval.Evaluate(
			ctx, q, anchor, withTerminal(lister), trellis_eval.WithTagsInterpreter(tagsOverride),
		)
	}
	// The enriched-preferring fetch lives in node_view (shared with
	// `list -format json`, native tags slice 2 T4); the evaluator's own
	// listEnriched mirrors it so the no-query path enriches identically to
	// the query path.
	return node_view.ListEnrichedChildren(ctx, lister, anchor)
}
