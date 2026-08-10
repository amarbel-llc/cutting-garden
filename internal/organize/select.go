package organize

import (
	"context"
	"net/url"

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
	return listEnrichedChildren(ctx, lister, anchor)
}

// listEnrichedChildren returns anchor's immediate children, preferring the
// enriched listing (Facets/Fields populated) over metadata-only ListRoots —
// mirroring the evaluator's own listEnriched so the no-query path enriches
// identically to the query path.
func listEnrichedChildren(
	ctx context.Context, lister cgp.RootLister, anchor *url.URL,
) ([]cgp.Node, error) {
	if el, ok := lister.(cgp.EnrichedLister); ok {
		nodes, served, err := el.ListEnriched(ctx, anchor, nil)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		if served {
			return nodes, nil
		}
	}
	return lister.ListRoots(ctx, anchor)
}
