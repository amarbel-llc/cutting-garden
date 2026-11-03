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
	for _, n := range nodes {
		doc.Ungrouped = append(doc.Ungrouped, objectLine{
			ID: idOf(n.URIString()), Desc: names[n.URIString()],
		})
	}
	return doc, nil
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
	var out []objectLine
	for _, ln := range doc.Ungrouped {
		out = append(out, ln)
	}
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

// refuseUnbuiltNest is generate's guard while only the flat layout exists: a
// `(tree)` whose effective `_nest` is not none cannot be rendered yet.
func refuseUnbuiltNest(nest string) error {
	if nest == nestNone {
		return nil
	}
	return errors.BadRequestf(
		"organize: --group-by (tree) can only render the flat layout so far; set "+
			"`[organize] nest = \"none\"` (or `- _nest = none` in the document), not %q "+
			"(cutting-garden#353)", nest)
}
