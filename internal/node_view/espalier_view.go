package node_view

import (
	"net/url"
	"slices"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// This file is the ONE home of the framework-side espalier LINE projection
// (native tags design G8/G13, slice 4): the anchor-relative box id, the
// description trailer, and the box-atom presenter that organize's object
// lines are built from. `list -format espalier` renders through these SAME
// helpers (plus trellis.WriteLiteral, the one shared writer), so the two
// surfaces produce byte-identical lines for the same node — RFC 0014's
// isometry ("an organize document is a materialized query result in the
// query's own syntax") holds by construction, not by parallel maintenance.
// Moved here from internal/organize (which now delegates) exactly as the
// tag-view helpers were in native tags slice 2 T4.

// WriteObjectLine renders one full espalier object line, newline included:
// `- [<id> !<type> <tag>… <name>=<value>…] <desc>`. The interior is spelled
// by trellis.WriteLiteral (design G13): the id, the type, the tag terms,
// then the detail atoms, each a ground `name=value` espalier field
// (cutting-garden#47). trailingTags (organize's `_tag-atoms = trailing`
// document lever, design G1) moves the tag terms after the atoms via
// trellis.WriteLiteralTrailingTags — presentation-only, since the parser
// collects tags wherever they sit; `list -format espalier` has no document
// carrying the lever and always passes false (the leading default).
func WriteObjectLine(
	b *strings.Builder, lit trellis.Literal, desc string, trailingTags bool,
) {
	b.WriteString("- [")
	if trailingTags {
		trellis.WriteLiteralTrailingTags(b, lit)
	} else {
		trellis.WriteLiteral(b, lit)
	}
	b.WriteByte(']')
	if desc != "" {
		b.WriteByte(' ')
		b.WriteString(desc)
	}
	b.WriteByte('\n')
}

// DistinctTypes returns the sorted set of node types present — THE espalier
// spelling selector (organize's buildDocument rule, shared with `list
// -format espalier`): one type keeps object boxes bare (organize distributes
// it via the envelope `_type`; list simply omits it), several inline each
// box's `!type`.
func DistinctTypes(nodes []cutting_garden_plugins.Node) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range nodes {
		if n.Type != "" && !seen[n.Type] {
			seen[n.Type] = true
			out = append(out, n.Type)
		}
	}
	sort.Strings(out)
	return out
}

// RelativeIDFor is THE box-id resolver (fastmail tags slice 1, Task 5b):
// the plugin's own anchor-relative id when lister implements
// cutting_garden_plugins.NodeIDer and accepts the node, else the host+path
// default (RelativeID). Every id organize and `list -format espalier`
// render or re-derive goes through here, bound to the one lister that
// served the nodes, so generate and apply always agree. A nil lister takes
// the default.
func RelativeIDFor(
	lister cutting_garden_plugins.RootLister, uriStr, anchorStr string,
) string {
	if ider, ok := lister.(cutting_garden_plugins.NodeIDer); ok {
		if id, ok := ider.RelativeNodeID(uriStr, anchorStr); ok {
			return id
		}
	}
	return RelativeID(uriStr, anchorStr)
}

// BoxIDs binds RelativeIDFor to one lister and anchor — the per-document
// id function organize's generate and apply paths thread down.
func BoxIDs(
	lister cutting_garden_plugins.RootLister, anchorStr string,
) func(uriStr string) string {
	return func(uriStr string) string {
		return RelativeIDFor(lister, uriStr, anchorStr)
	}
}

// RelativeID is the DEFAULT box id (RelativeIDFor's fallback for a plugin
// without NodeIDer): a node URI relative to the anchor when it sits under it
// (the short `task1.ics` form), else the full URI. Comparison is
// form-independent: it matches on host+path so an anchor spelled
// `caldav:https://host/cal/` shortens a node URI spelled
// `caldav://host/cal/x.ics` (a real caldav divergence — the plugin
// normalizes node URIs but not the anchor arg). Deterministic on
// (uri, anchor), which lets organize's apply engine re-derive a stored box
// id from a live node URI and match it.
func RelativeID(uriStr, anchorStr string) string {
	u, err1 := url.Parse(uriStr)
	a, err2 := url.Parse(anchorStr)
	if err1 == nil && err2 == nil {
		up, ap := canonicalHostPath(u), canonicalHostPath(a)
		if ap != "" && strings.HasPrefix(up, ap) {
			return strings.TrimPrefix(up, ap)
		}
	}
	return uriStr
}

// canonicalHostPath projects a URL to a scheme-form-independent "host/path"
// key, handling the caldav opaque spelling (`caldav:https://host/path`) as
// well as the plain hierarchical form (`caldav://host/path`).
func canonicalHostPath(u *url.URL) string {
	if u.Opaque != "" {
		s := u.Opaque
		s = strings.TrimPrefix(s, "https://")
		s = strings.TrimPrefix(s, "http://")
		return s
	}
	return u.Host + u.Path
}

// NodeDescription resolves the box's description trailer: the human-readable
// summary/title/name projection a plugin declares (caldav's summary lives in
// Node.Fields, its Name being the href filename), falling back to Node.Name.
func NodeDescription(n cutting_garden_plugins.Node) string {
	for _, key := range []string{"summary", "title", "name"} {
		if s, ok := n.Fields[key].(string); ok && s != "" {
			return CollapseToSingleLine(s)
		}
	}
	return CollapseToSingleLine(n.Name)
}

// CollapseToSingleLine is THE espalier-facing newline decision (native tags
// slice 1.5 F): a stored value may legitimately contain real newlines — the
// caldav ical layer unescapes RFC 5545 `\n` TEXT escapes into real newlines
// in the STORED struct (plugins/caldav/ical/text.go) — but every espalier
// slot is single-line (the description trailer is raw-to-EOL, a box atom
// lives inside one line), so the PRESENTATION collapses each newline run to
// one space. This is presentation-only: NodeDescription, organize's
// descriptionOf, and BoxAtomPresenter all collapse identically, so
// base/edited/live compare equal for an untouched multiline value and
// NOTHING is written back — the stored newlines survive any apply that does
// not edit that value. Only a deliberate edit of a collapsed trailer/atom
// persists the single-line form.
func CollapseToSingleLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == '\r' || r == '\n'
	}), " ")
}

// BoxAtomPresenter returns the plugin's box-atom presentation function when
// it implements FieldPresenter (cutting-garden#47), or nil — in which case
// object boxes carry no detail atoms (today's behavior for a plugin without
// the capability). Atom values pass through CollapseToSingleLine: a stored
// TEXT value (e.g. a caldav LOCATION) may carry real newlines, but an atom
// lives inside one document line — same presentation-only rule as the
// description trailer (native tags slice 1.5 F; see CollapseToSingleLine).
func BoxAtomPresenter(
	lister cutting_garden_plugins.RootLister,
) func(cutting_garden_plugins.Node) []cutting_garden_plugins.BoxAtom {
	p, ok := lister.(cutting_garden_plugins.FieldPresenter)
	if !ok {
		return nil
	}
	return func(n cutting_garden_plugins.Node) []cutting_garden_plugins.BoxAtom {
		// Copy before collapsing: the plugin may hand back a cached slice, and
		// mutating it in place would corrupt the plugin's own state.
		atoms := slices.Clone(p.PresentBoxAtoms(n))
		for i := range atoms {
			atoms[i].Value = CollapseToSingleLine(atoms[i].Value)
		}
		return atoms
	}
}
