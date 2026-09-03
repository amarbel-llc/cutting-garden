package organize

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/cutting-garden/internal/trellis_eval"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// selectNodes resolves the node set organize operates on: the trellis query's
// matches when a query is given, else the anchor's enriched immediate children.
// Either path prefers the plugin's EnrichedLister so Node.Facets is populated —
// caldav's status/date facets are body-derived and a plain ListRoots leaves them
// empty (cutting-garden#212), and organize groups by exactly those facets.
func selectNodes(
	ctx context.Context,
	lister cgp.RootLister,
	anchor *url.URL,
	query string,
) ([]cgp.Node, error) {
	if query != "" {
		q, perr := trellis.Parse(query)
		if perr != nil {
			return nil, errors.BadRequestf("organize --query: %s", perr)
		}
		// withTerminal makes the synthetic `_terminal` dimension matchable, so a
		// `_terminal=no`/`_terminal=yes` predicate evaluates through the ordinary
		// facet path (cutting-garden#214) in both generate and apply; a no-op for a
		// plugin declaring no terminal values.
		return trellis_eval.Evaluate(ctx, q, anchor, withTerminal(lister))
	}
	// The enriched-preferring fetch lives in command_components (shared with
	// `list -format json`, native tags slice 2 T4); the evaluator's own
	// listEnriched mirrors it so the no-query path enriches identically to
	// the query path.
	return command_components.ListEnrichedChildren(ctx, lister, anchor)
}
