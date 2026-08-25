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

// groupNodes buckets nodes by their membership in the spec's facet dimension. It
// returns the ungrouped set (no value for the dimension) and the ordered value
// buckets. A date spec's granularity coarsens each day-precise per-node value by
// prefix truncation BEFORE bucketing (cutting-garden#230), so a month grouping
// folds every day of a month under one `=YYYY-MM` heading. declaredValues are
// the plugin's write-side target buckets (FacetWrite.Values), pre-rendered in
// order even when empty (RFC 0015 "make it easy to swap states");
// observed-but-undeclared values follow, sorted ascending. inlineType controls
// the object box's `!type` tag: true for the type-as-heading spelling (each box
// self-describes), false when the envelope's `- _type` distributes it. present,
// when non-nil, populates each box's detail atoms (date/time/location;
// cutting-garden#47) from the plugin's FieldPresenter.
func groupNodes(
	nodes []cgp.Node, spec groupSpec, anchor string, declaredValues []string, inlineType bool,
	present func(cgp.Node) []cgp.BoxAtom,
) (ungrouped []objectLine, buckets []bucket) {
	byValue := map[string][]objectLine{}
	for _, n := range nodes {
		ln := objectLine{ID: relativeID(n.URIString(), anchor), Desc: nodeDescription(n)}
		if inlineType {
			ln.Type = n.Type
		}
		if present != nil {
			ln.Fields = present(n)
		}
		values := n.Facets[spec.Dim]
		if len(values) == 0 {
			ungrouped = append(ungrouped, ln)
			continue
		}
		for _, v := range values {
			key := coarsenBucket(v.Key, spec.Granularity)
			bucketLine := ln
			// Drop the grouped dimension's box atom when the `=<value>` heading it
			// is filed under already shows that atom's value in FULL — pure
			// redundancy (cutting-garden#229). A coarser heading keeps the atom: it
			// carries precision the heading drops (a `date_due:month` bucket over a
			// day-precise date_due atom; a priority band over the raw priority
			// integer). The comparison is against the atom's rendered value, not the
			// facet key, so a band/raw-int split is correctly kept.
			if a, ok := findAtom(ln.Fields, spec.Dim); ok && a.Value == key {
				bucketLine.Fields = withoutAtom(ln.Fields, spec.Dim)
			}
			byValue[key] = append(byValue[key], bucketLine)
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

// groupNodesByNamespace buckets nodes by their tag-namespace ROLLUP memberships
// (RFC 0019 tags slice 3 B2, cutting-garden#231) rather than by raw facet values.
// It mirrors groupNodes' line-building and sorting exactly — only the bucketing
// source differs: each node's tags for spec.Dim are handed to the resolved
// TagInterpreter's Buckets, which rolls a namespace's deeper tags up to their
// immediate next segment (dodder-hyphen's project-client-acme → -client). The
// interpreter deduplicates by bucket per node, so a node carrying several tags
// that roll to one bucket (project-client-acme, project-client-baxter → both
// -client) contributes a single line to that bucket. A node with no memberships
// (its tags aren't under the namespace) lands ungrouped, mirroring groupNodes'
// no-value case. An interpreter that declares no namespaces (naive) rejects a
// non-empty namespace — that error is propagated, not swallowed. The grouped
// dimension is not itself a box atom for a tag grouping, so no #229-style atom
// stripping applies; the box carries its other fields unchanged. Buckets are the
// open set of observed rollup keys (no declared/pre-rendered values), ordered by
// their `-<segment>` string.
func groupNodesByNamespace(
	nodes []cgp.Node, spec groupSpec, anchor string, interp cgp.TagInterpreter,
	inlineType bool, present func(cgp.Node) []cgp.BoxAtom,
) (ungrouped []objectLine, buckets []bucket, err error) {
	byBucket := map[string][]objectLine{}
	for _, n := range nodes {
		ln := objectLine{ID: relativeID(n.URIString(), anchor), Desc: nodeDescription(n)}
		if inlineType {
			ln.Type = n.Type
		}
		if present != nil {
			ln.Fields = present(n)
		}
		mems, e := interp.Buckets(facetKeys(n.Facets[spec.Dim]), spec.Namespace)
		if e != nil {
			return nil, nil, e
		}
		if len(mems) == 0 {
			ungrouped = append(ungrouped, ln)
			continue
		}
		for _, m := range mems {
			byBucket[m.Bucket] = append(byBucket[m.Bucket], ln)
		}
	}

	sort.Slice(ungrouped, func(i, j int) bool { return ungrouped[i].ID < ungrouped[j].ID })

	order := make([]string, 0, len(byBucket))
	for k := range byBucket {
		order = append(order, k)
	}
	sort.Strings(order)

	buckets = make([]bucket, 0, len(order))
	for _, k := range order {
		lines := byBucket[k]
		sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
		buckets = append(buckets, bucket{Value: k, Lines: lines})
	}
	return ungrouped, buckets, nil
}

// findAtom returns the box atom named name, or ok=false when the box carries no
// such atom (e.g. a groupable-but-not-inline dimension like categories).
func findAtom(atoms []cgp.BoxAtom, name string) (cgp.BoxAtom, bool) {
	for _, a := range atoms {
		if a.Name == name {
			return a, true
		}
	}
	return cgp.BoxAtom{}, false
}

// withoutAtom returns atoms with the atom named name removed. It allocates a
// fresh slice, so the caller's shared backing array (one objectLine filed under
// several buckets) is never mutated.
func withoutAtom(atoms []cgp.BoxAtom, name string) []cgp.BoxAtom {
	out := make([]cgp.BoxAtom, 0, len(atoms))
	for _, a := range atoms {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return out
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
