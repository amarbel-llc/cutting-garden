package organize

import (
	"context"
	"net/url"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// The synthetic terminal dimension (cutting-garden#214): a framework-derived
// yes/no facet an object carries when it holds a DONE value in any
// terminal-bearing dimension. The `_` prefix marks it framework-synthesized, not
// a real plugin facet (like the _body pseudo-field / the _query envelope field).
const (
	terminalDim = "_terminal"
	terminalYes = "yes"
	terminalNo  = "no"
)

// terminalSchema maps, per node type tag, each terminal-bearing dimension key to
// its set of terminal values (FacetDimension.TerminalValues). nil when the plugin
// declares no terminal values anywhere — the gate for organize's default
// exclusion, so a plugin with no terminal notion is left untouched.
func terminalSchema(lister cgp.RootLister) map[string]map[string]map[string]struct{} {
	describer, ok := lister.(cgp.FacetDescriber)
	if !ok {
		return nil
	}
	schema := map[string]map[string]map[string]struct{}{}
	for _, nt := range describer.DescribeFacets() {
		for _, dim := range nt.Dimensions {
			if len(dim.TerminalValues) == 0 {
				continue
			}
			if schema[nt.Tag] == nil {
				schema[nt.Tag] = map[string]map[string]struct{}{}
			}
			set := make(map[string]struct{}, len(dim.TerminalValues))
			for _, v := range dim.TerminalValues {
				set[v] = struct{}{}
			}
			schema[nt.Tag][dim.Key] = set
		}
	}
	if len(schema) == 0 {
		return nil
	}
	return schema
}

// terminalLister decorates a lister so the synthetic `_terminal` dimension is a
// real, matchable facet (cutting-garden#214): DescribeFacets declares it (closed
// yes/no) on every type with a terminal-bearing dimension, and every listed node
// is annotated with its `_terminal` value. The trellis evaluator then matches
// `_terminal=no` / `_terminal=yes` through its ordinary facet path with no
// evaluator change — so the default exclusion, its `_query` echo, and re-applying
// that query all go through one mechanism. Used only for organize's selection.
type terminalLister struct {
	inner     cgp.RootLister
	enriched  cgp.EnrichedLister // nil when inner serves no enriched listing
	leaf      cgp.LeafReader     // nil when inner cannot fetch leaves
	describer cgp.FacetDescriber // non-nil (a terminal schema requires it)
	schema    map[string]map[string]map[string]struct{}
}

// withTerminal wraps lister so `_terminal` is matchable, or returns it unchanged
// when it declares no terminal values (nothing to synthesize).
func withTerminal(lister cgp.RootLister) cgp.RootLister {
	schema := terminalSchema(lister)
	if schema == nil {
		return lister
	}
	tl := &terminalLister{inner: lister, schema: schema}
	if el, ok := lister.(cgp.EnrichedLister); ok {
		tl.enriched = el
	}
	if lr, ok := lister.(cgp.LeafReader); ok {
		tl.leaf = lr
	}
	if fd, ok := lister.(cgp.FacetDescriber); ok {
		tl.describer = fd
	}
	return tl
}

// Schemes, TypeTag, and Types are pure pass-throughs — the decorator changes
// only facet declaration and node annotation, never the plugin's identity.
func (t *terminalLister) Schemes() []string     { return t.inner.Schemes() }
func (t *terminalLister) TypeTag() string       { return t.inner.TypeTag() }
func (t *terminalLister) Types() []cgp.NodeType { return t.inner.Types() }

func (t *terminalLister) ListRoots(ctx context.Context, node *url.URL) ([]cgp.Node, error) {
	nodes, err := t.inner.ListRoots(ctx, node)
	if err != nil {
		return nil, err
	}
	t.annotate(nodes)
	return nodes, nil
}

// ListEnriched delegates and annotates when inner serves an enriched listing;
// otherwise it declines (ok=false) so the evaluator falls back to ListRoots
// (which annotates too). A decline from inner (e.g. caldav at a calendar-home) is
// passed through unannotated — those children are containers, not terminal-bearing
// objects.
func (t *terminalLister) ListEnriched(
	ctx context.Context, node *url.URL, filter cgp.FacetFilter,
) ([]cgp.Node, bool, error) {
	if t.enriched == nil {
		return nil, false, nil
	}
	nodes, ok, err := t.enriched.ListEnriched(ctx, node, filter)
	if err != nil || !ok {
		return nodes, ok, err
	}
	t.annotate(nodes)
	return nodes, true, nil
}

// DescribeFacets appends the synthetic `_terminal` dimension to every type that
// has a terminal-bearing dimension, so the evaluator routes `_terminal=…` to the
// facet path (isFacetField) rather than a leaf fetch.
func (t *terminalLister) DescribeFacets() []cgp.NodeTypeFacets {
	var base []cgp.NodeTypeFacets
	if t.describer != nil {
		base = t.describer.DescribeFacets()
	}
	out := make([]cgp.NodeTypeFacets, 0, len(base))
	for _, nt := range base {
		if _, terminal := t.schema[nt.Tag]; terminal {
			dims := append([]cgp.FacetDimension(nil), nt.Dimensions...)
			dims = append(dims, cgp.FacetDimension{
				Key:    terminalDim,
				Label:  "Terminal",
				Kind:   cgp.FacetCategorical,
				Values: []cgp.FacetValue{{Key: terminalNo}, {Key: terminalYes}},
			})
			nt.Dimensions = dims
		}
		out = append(out, nt)
	}
	return out
}

func (t *terminalLister) ReadLeaf(
	ctx context.Context, node *url.URL,
) (cgp.LeafContent, bool, error) {
	if t.leaf == nil {
		return cgp.LeafContent{}, false, nil
	}
	return t.leaf.ReadLeaf(ctx, node)
}

// annotate stamps each node's synthetic `_terminal` facet: yes iff the node holds
// a terminal value in any of its type's terminal-bearing dimensions, else no. A
// node whose type has no terminal notion is left untouched.
func (t *terminalLister) annotate(nodes []cgp.Node) {
	for i := range nodes {
		dims, ok := t.schema[nodes[i].Type]
		if !ok {
			continue
		}
		if nodes[i].Facets == nil {
			nodes[i].Facets = map[string][]cgp.FacetValue{}
		}
		nodes[i].Facets[terminalDim] = []cgp.FacetValue{{Key: terminalValueOf(nodes[i], dims)}}
	}
}

func terminalValueOf(n cgp.Node, dims map[string]map[string]struct{}) string {
	for dimKey, terminals := range dims {
		for _, fv := range n.Facets[dimKey] {
			if _, isTerminal := terminals[fv.Key]; isTerminal {
				return terminalYes
			}
		}
	}
	return terminalNo
}

// effectiveQuery composes organize's selection query: the user's query with the
// default `_terminal=no` exclusion appended (cutting-garden#214), UNLESS the
// plugin declares no terminal values, the user asked to include terminal objects,
// or the user's query already references `_terminal` (explicit mention wins — the
// single composition rule). Appending applies the exclusion to the query's LAST
// step — the selected objects. The result is echoed into the document's `_query`,
// so the default is visible, editable, and re-applies identically on apply (which
// takes the echoed query verbatim, never re-injecting).
func effectiveQuery(lister cgp.RootLister, userQuery string, includeTerminal bool) string {
	if includeTerminal || terminalSchema(lister) == nil || referencesTerminal(userQuery) {
		return userQuery
	}
	clause := terminalDim + "=" + terminalNo
	if userQuery == "" {
		return clause
	}
	return userQuery + " " + clause
}

// referencesTerminal reports whether q mentions the synthetic `_terminal`
// dimension — a textual check, sufficient because `_terminal` is a reserved
// framework name that no plugin facet uses.
func referencesTerminal(q string) bool {
	return strings.Contains(q, terminalDim)
}
