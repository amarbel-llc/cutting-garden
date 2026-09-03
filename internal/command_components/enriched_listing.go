package command_components

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ListEnrichedChildren returns node's immediate children, preferring the
// plugin's enriched listing (Facets/Fields populated) over metadata-only
// ListRoots — caldav's facets and listing fields are body-derived and a plain
// ListRoots leaves them empty (cutting-garden#212). Moved here from
// internal/organize (native tags slice 2 T4) so `list -format json` — whose
// `tags` array presents off Node.Fields — shares the one fetch preference
// with organize's no-query selection; the trellis evaluator's listEnriched
// mirrors the same rule.
func ListEnrichedChildren(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if el, ok := lister.(cutting_garden_plugins.EnrichedLister); ok {
		nodes, served, err := el.ListEnriched(ctx, node, nil)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		if served {
			return nodes, nil
		}
	}
	return lister.ListRoots(ctx, node)
}
