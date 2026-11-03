package organize

import (
	"slices"
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The `(tree)` grouping (cutting-garden#353) lays a plugin's nodes out by their
// OWN full hyphen names — `trips-26-01-a` is the heading `trips`, `-26`, `-01`
// and the line `-a` — rather than by a tags field. The name of a node is read
// from the node type's Name listing field (cutting-garden#378: the org plugin
// derives it for the files its account lists as tag vocabularies).
//
// This slice is READ-ONLY: it generates the flat layout (one line per node, the
// line text its full name), recognizes any nesting of the same names, and
// refuses every apply that would change a name (cutting-garden#380 specifies
// what such edits mean). The nested layout is the next slice.

// treeNames resolves each selected node's full hyphen name from the Name
// listing field its type declares, refusing a plugin that declares none, a node
// that carries none, and two nodes that carry the same one.
func treeNames(nodes []cgp.Node, lister cgp.RootLister) (map[string]string, error) {
	var sets []cgp.NodeTypeListingFields
	if d, ok := lister.(cgp.ListingFieldsDescriber); ok {
		sets = d.DescribeListingFields()
	}
	names := make(map[string]string, len(nodes))
	byName := make(map[string]string, len(nodes))
	for _, n := range nodes {
		uri := n.URIString()
		key, ok := cgp.ListingNameKey(sets, n.Type)
		if !ok {
			return nil, errors.BadRequestf(
				"organize: --group-by (tree): the plugin declares no hyphen-name field for "+
					"node type %q (a type needs a listing field marked Name; a wire plugin "+
					"cannot declare one yet)", n.Type)
		}
		name, _ := n.Fields[key].(string)
		if name == "" {
			return nil, errors.BadRequestf(
				"organize: --group-by (tree): %s has no hyphen name (for an org file, list "+
					"it in the account's tag_files)", uri)
		}
		if prior, dup := byName[name]; dup && prior != uri {
			return nil, errors.BadRequestf(
				"organize: --group-by (tree): %s and %s have the same full name %q; "+
					"rename one of them", prior, uri, name)
		}
		byName[name] = uri
		names[uri] = name
	}
	return names, nil
}

// buildTreeDocument is buildDocument's `(tree)` branch: the FLAT layout, one
// box per node in selection order, the box carrying only the node's id and its
// text the node's full hyphen name. Tags and detail atoms are not presented —
// the tree is about names. A single node type only (the envelope `_type`).
func buildTreeDocument(
	nodes []cgp.Node, anchor, query string, spec groupSpec, lister cgp.RootLister,
) (document, error) {
	names, err := treeNames(nodes, lister)
	if err != nil {
		return document{}, err
	}
	types := distinctTypes(nodes)
	if len(types) > 1 {
		return document{}, errors.BadRequestf(
			"organize: --group-by (tree): the selection mixes node types %v; select one type", types)
	}
	idOf := boxIDsFor(lister, anchor)
	doc := document{
		Anchor:     anchor,
		Query:      query,
		Provenance: provenance(spec.String(), query, anchor),
		GroupBy:    spec.groupByEncoding(),
	}
	if len(types) == 1 {
		doc.Type = types[0]
	}
	entries := make([]objectLine, 0, len(nodes))
	for _, n := range nodes {
		entries = append(entries, objectLine{ID: idOf(n.URIString()), Desc: names[n.URIString()]})
	}
	switch nest := effectiveNest(spec.Nest, ""); nest {
	case nestNone:
		doc.Ungrouped = entries
	default:
		doc.Ungrouped, doc.Sections = layoutTree(entries, nest)
	}
	return doc, nil
}

// trieNode is one hyphen prefix of the name trie: a real node when line is set
// (a node of exactly that name exists), synthetic when not. kids are in order of
// first appearance in the selection.
type trieNode struct {
	prefix string
	line   *objectLine
	kids   []*trieNode
}

// nameBoundaries are the prefix lengths of name at which it splits: before
// every `-` past the first character, then the whole name. A name with no
// such `-` is one segment.
func nameBoundaries(name string) []int {
	var cuts []int
	for i := 1; i < len(name); i++ {
		if name[i] == '-' {
			cuts = append(cuts, i)
		}
	}
	return append(cuts, len(name))
}

// layoutTree lays the name set out as nested continuation headings, in the
// order the selection lists them (cutting-garden#353):
//
//   - one heading per hyphen segment, never collapsed (`# trips` > `## -26` >
//     `### -01` > `- -a`), BOXED when a node has exactly that prefix name and
//     SYNTHETIC (no box) when none does;
//   - a line's text is its name relative to its heading;
//   - under a heading, leaves list before sub-headings, each in order of first
//     appearance; a childless top-level node is a line above the first heading;
//   - `nest = 1` stops after the family heading: every deeper node is a line
//     whose text is its name relative to that family.
//
// Whether a heading is boxed depends on the SELECTION: a parent the query
// filtered out is a synthetic heading over members that keep their names.
func layoutTree(entries []objectLine, nest string) (ungrouped []objectLine, sections []section) {
	root := &trieNode{}
	for i := range entries {
		node := root
		for _, cut := range nameBoundaries(entries[i].Desc) {
			prefix := entries[i].Desc[:cut]
			var next *trieNode
			for _, kid := range node.kids {
				if kid.prefix == prefix {
					next = kid
					break
				}
			}
			if next == nil {
				next = &trieNode{prefix: prefix}
				node.kids = append(node.kids, next)
			}
			node = next
		}
		line := entries[i]
		node.line = &line
	}

	maxDepth := 1 << 30
	if nest == nestOne {
		maxDepth = 1
	}
	var emit func(n *trieNode, parent string, depth int)
	emit = func(n *trieNode, parent string, depth int) {
		sec := section{Depth: depth, Term: headingTerm(n.prefix[len(parent):])}
		if n.line != nil {
			sec.Box = &objectLine{ID: n.line.ID}
		}
		var subs []*trieNode
		if depth >= maxDepth {
			// Past the cap: every node below is a line relative to this heading.
			var walk func(*trieNode)
			walk = func(k *trieNode) {
				if k.line != nil {
					ln := *k.line
					ln.Desc = k.prefix[len(n.prefix):]
					sec.Lines = append(sec.Lines, ln)
				}
				for _, kid := range k.kids {
					walk(kid)
				}
			}
			for _, kid := range n.kids {
				walk(kid)
			}
		} else {
			for _, kid := range n.kids {
				if len(kid.kids) == 0 {
					ln := *kid.line
					ln.Desc = kid.prefix[len(n.prefix):]
					sec.Lines = append(sec.Lines, ln)
				} else {
					subs = append(subs, kid)
				}
			}
		}
		sections = append(sections, sec)
		for _, sub := range subs {
			emit(sub, n.prefix, depth+1)
		}
	}
	for _, top := range root.kids {
		if len(top.kids) == 0 {
			ungrouped = append(ungrouped, *top.line)
			continue
		}
		emit(top, "", 1)
	}
	return ungrouped, sections
}

// headingTerm spells a name segment as a heading term: as is when it reads back
// as itself, quoted otherwise.
func headingTerm(segment string) string {
	if v, _, err := parseHeadingValue(segment); err == nil && v == segment {
		return segment
	}
	return trellis.QuoteIfNeeded(segment)
}

// headingText is a heading term's text as it joins into a name: a bare or
// quoted identifier decoded, anything else as written.
func headingText(term string) string {
	if v, _, err := parseHeadingValue(term); err == nil {
		return v
	}
	return term
}

// treeEntries is the document's NAME SET: one entry per node it names, each an
// objectLine whose Desc is the node's full name. A line's name is the heading
// terms above it concatenated with its own text (`trips` + `-26` + `-01` +
// `-a`); a boxed heading names its own node by the terms down to and including
// it; a synthetic heading names nothing. The flat and every nested spelling of
// one set of names therefore yield the same entries (in layout order).
func (doc document) treeEntries() []objectLine {
	out := append([]objectLine(nil), doc.Ungrouped...)
	prefix := func(stack []int) string {
		var b strings.Builder
		for _, idx := range stack {
			b.WriteString(headingText(doc.Sections[idx].Term))
		}
		return b.String()
	}
	var stack []int
	for i, s := range doc.Sections {
		stack = popTo(stack, doc.Sections, s.Depth)
		stack = append(stack, i)
		path := prefix(stack)
		if s.Box != nil {
			e := *s.Box
			e.Desc = path
			out = append(out, e)
		}
		for _, ln := range s.Lines {
			ln.Desc = path + ln.Desc
			out = append(out, ln)
		}
	}
	return out
}

// treeCanonical is the `(tree)` canonical form: the name set as a FLAT list
// sorted by full name (then id), with no headings. The bytes `_base`
// content-addresses therefore do not depend on how the names are laid out —
// nested, flat, any `_nest` — which is what makes a respelling a no-op rather
// than an edit.
func (doc document) treeCanonical() document {
	entries := doc.treeEntries()
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Desc != entries[j].Desc {
			return entries[i].Desc < entries[j].Desc
		}
		return entries[i].ID < entries[j].ID
	})
	doc.Ungrouped = entries
	doc.Sections = nil
	return doc
}

// boxText spells an entry's box (id, type, tags, atoms) for comparison.
func boxText(ln objectLine) string {
	var b strings.Builder
	trellis.WriteLiteral(&b, objectLiteral(ln))
	return b.String()
}

// refuseTreeEdits is apply's read-only guard for a `(tree)` document: any
// difference between its name set and the pinned base's — a node renamed or
// moved (its name changes), deleted, an id the base does not hold, a box edited,
// a creation — is a bad request naming the node and pointing at #380, which
// specifies what such edits mean. A respelling of the same names (nesting,
// `_nest`) is no difference at all. Nothing is planned or written.
func refuseTreeEdits(base, edited document) error {
	refuse := func(format string, args ...any) error {
		return errors.BadRequestf(
			"organize --apply: (tree) "+format+"; editing a name tree is not supported "+
				"yet (see cutting-garden#380 for the edit semantics)", args...)
	}
	baseEntries := map[string]objectLine{}
	for _, e := range base.treeEntries() {
		baseEntries[e.ID] = e
	}
	editedIDs := map[string]bool{}
	for _, e := range edited.treeEntries() {
		if e.New {
			return refuse("body line %d creates an object (%q)", e.Line, e.Desc)
		}
		editedIDs[e.ID] = true
		b, held := baseEntries[e.ID]
		switch {
		case !held:
			return refuse("[%s] %q is an unknown id: the pinned base holds no such node", e.ID, e.Desc)
		case b.Desc != e.Desc:
			return refuse("[%s] was renamed (%q -> %q)", e.ID, b.Desc, e.Desc)
		case boxText(b) != boxText(e):
			return refuse("[%s] %q had its box edited", e.ID, e.Desc)
		}
	}
	baseIDs := make([]string, 0, len(baseEntries))
	for id := range baseEntries {
		baseIDs = append(baseIDs, id)
	}
	slices.Sort(baseIDs)
	for _, id := range baseIDs {
		if !editedIDs[id] {
			return refuse("[%s] %q was deleted", id, baseEntries[id].Desc)
		}
	}
	return nil
}
