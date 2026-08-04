package organize

import (
	"sort"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// bucket is one grouped value and the object lines carrying it — the grouping
// intermediate generate folds into the document's `=<value>` sections.
type bucket struct {
	Value string
	Lines []objectLine
}

// groupNodes buckets nodes by their membership in facet dimension dim. It returns
// the ungrouped set (no value for the dimension) and the ordered value buckets.
// declaredValues are the plugin's write-side target buckets (FacetWrite.Values),
// pre-rendered in order even when empty (RFC 0015 "make it easy to swap states");
// observed-but-undeclared values follow, sorted ascending. inlineType controls
// the object box's `!type` tag: true for the type-as-heading spelling (each box
// self-describes), false when the envelope's `- _type` distributes it.
func groupNodes(
	nodes []cgp.Node, dim, anchor string, declaredValues []string, inlineType bool,
) (ungrouped []objectLine, buckets []bucket) {
	byValue := map[string][]objectLine{}
	for _, n := range nodes {
		ln := objectLine{ID: relativeID(n.URIString(), anchor), Desc: nodeDescription(n)}
		if inlineType {
			ln.Type = n.Type
		}
		values := n.Facets[dim]
		if len(values) == 0 {
			ungrouped = append(ungrouped, ln)
			continue
		}
		for _, v := range values {
			byValue[v.Key] = append(byValue[v.Key], ln)
		}
	}

	sort.Slice(ungrouped, func(i, j int) bool { return ungrouped[i].ID < ungrouped[j].ID })

	seen := make(map[string]bool, len(declaredValues))
	order := make([]string, 0, len(declaredValues)+len(byValue))
	for _, v := range declaredValues {
		if !seen[v] {
			seen[v] = true
			order = append(order, v)
		}
	}
	extra := make([]string, 0, len(byValue))
	for k := range byValue {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	order = append(order, extra...)

	buckets = make([]bucket, 0, len(order))
	for _, k := range order {
		lines := byValue[k]
		sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
		buckets = append(buckets, bucket{Value: k, Lines: lines})
	}
	return ungrouped, buckets
}

// nodeDescription resolves the box's description trailer: the human-readable
// summary/title/name projection a plugin declares (caldav's summary lives in
// Node.Fields, its Name being the href filename), falling back to Node.Name.
func nodeDescription(n cgp.Node) string {
	for _, key := range []string{"summary", "title", "name"} {
		if s, ok := n.Fields[key].(string); ok && s != "" {
			return s
		}
	}
	return n.Name
}

// distinctTypes returns the sorted set of node types present — one type selects
// the flatter envelope-`_type` spelling, several select per-type headings.
func distinctTypes(nodes []cgp.Node) []string {
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

// nodesOfType filters nodes to those of the given type.
func nodesOfType(nodes []cgp.Node, typ string) []cgp.Node {
	var out []cgp.Node
	for _, n := range nodes {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}
