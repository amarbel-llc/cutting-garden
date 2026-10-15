package node_view

import (
	"net/url"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// NodeURIFor is RelativeIDFor's inverse (cutting-garden#315): it turns an
// anchor-relative id — the box id organize prints — back into the node URI,
// so the mutate_nodes MCP tool can address a node from its id without
// listing the anchor. Three spellings resolve:
//
//   - a full URI in the anchor's scheme, accepted only when it lies under
//     the anchor (RelativeIDFor shortens it);
//   - a plugin id, through the plugin's NodeIDResolver;
//   - the host+path default, RelativeID's exact inverse: the id appended to
//     the anchor's path (or opaque part, for the `caldav:https://…` spelling).
//
// Every failure is a bad request: an id is caller input, and a wrong one is
// the caller's to fix.
func NodeURIFor(
	lister cutting_garden_plugins.RootLister, id, anchor string,
) (*url.URL, error) {
	if id == "" {
		return nil, errors.BadRequestf("empty node id (anchor %q)", anchor)
	}

	a, err := url.Parse(anchor)
	if err != nil || a.Scheme == "" {
		return nil, errors.BadRequestf("anchor %q is not a node URI", anchor)
	}

	if full, err := url.Parse(id); err == nil && full.Scheme == a.Scheme {
		if RelativeIDFor(lister, id, anchor) == id {
			return nil, errors.BadRequestf(
				"node %q is not under the anchor %q", id, anchor,
			)
		}
		return full, nil
	}

	if resolver, ok := lister.(cutting_garden_plugins.NodeIDResolver); ok {
		if resolved, ok := resolver.ResolveNodeID(id, anchor); ok {
			u, err := url.Parse(resolved)
			if err != nil {
				return nil, errors.ErrorWithStackf(
					"scheme %q: ResolveNodeID(%q) returned %q: %s",
					a.Scheme, id, resolved, err,
				)
			}
			return u, nil
		}
	}

	joined := *a
	joined.RawQuery, joined.Fragment, joined.RawFragment = "", "", ""
	if a.Opaque != "" {
		joined.Opaque = a.Opaque + id
	} else {
		joined.Path, joined.RawPath = a.Path+id, ""
	}

	// The default is right only for ids the plugin defers to it; an id the
	// plugin would spell differently does not round-trip, and resolving it
	// anyway would address a node the caller never named.
	if got := RelativeIDFor(lister, joined.String(), anchor); got != id {
		return nil, errors.BadRequestf(
			"node id %q does not resolve under the anchor %q", id, anchor,
		)
	}

	return &joined, nil
}
