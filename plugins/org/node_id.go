package org

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.NodeIDResolver = Plugin{}

// RelativeNodeID gives a headline its box id: the file path relative to
// the anchor plus the headline's query (`_inbox.org?id=…`), because the
// framework's host+path default would give every headline in a file the
// same id. Folders and files defer to the default. Pure and deterministic.
func (Plugin) RelativeNodeID(nodeURI, anchor string) (string, bool) {
	n, err := url.Parse(nodeURI)
	if err != nil || n.Scheme != schemeOrg || n.RawQuery == "" {
		return "", false
	}
	a, err := url.Parse(anchor)
	if err != nil || a.Scheme != schemeOrg || a.Host != n.Host {
		return "", false
	}
	file := strings.TrimPrefix(n.Path, "/")
	anchorPath := strings.TrimPrefix(a.Path, "/")
	switch {
	case file == anchorPath:
		// Anchored at the file, or a headline in it: the query alone.
		return n.RawQuery, true
	case (anchorPath == "" || strings.HasSuffix(anchorPath, "/")) && strings.HasPrefix(file, anchorPath):
		// Anchored at an enclosing folder.
		return strings.TrimPrefix(file, anchorPath) + "?" + n.RawQuery, true
	default:
		return "", false
	}
}

// ResolveNodeID inverts RelativeNodeID (cutting-garden#315): `id=…` is a
// headline in the anchor's file, `_inbox.org?id=…` a headline in a file
// under the anchor folder. An id that does not round-trip is declined, so
// folders and files take the framework default, as they do forward.
func (p Plugin) ResolveNodeID(id, anchor string) (string, bool) {
	a, err := url.Parse(anchor)
	if err != nil || a.Scheme != schemeOrg {
		return "", false
	}
	anchorPath := strings.TrimPrefix(a.Path, "/")
	file, query, nested := strings.Cut(id, "?")
	switch {
	case nested:
		file = anchorPath + file
	case anchorPath == "" || strings.HasSuffix(anchorPath, "/"):
		// A bare query names a headline only under a FILE anchor; under a
		// folder it is a file or folder id, the framework default's.
		return "", false
	default:
		file, query = anchorPath, id
	}
	// A headline is addressed by id= or h=; anything else (a file name, a
	// typo) is not a headline id.
	if values, err := url.ParseQuery(query); err != nil ||
		(values.Get("id") == "" && values.Get("h") == "") {
		return "", false
	}
	resolved := (&url.URL{
		Scheme: schemeOrg, Host: a.Host, Path: "/" + file, RawQuery: query,
	}).String()
	if got, ok := p.RelativeNodeID(resolved, anchor); !ok || got != id {
		return "", false
	}
	return resolved, true
}
