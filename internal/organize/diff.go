package organize

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/node_view"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// The apply diff (cutting-garden#224): before writing, organize renders each
// changed object as the espalier line the edited document shows for it, with
// its deltas painted inline as a word-diff — removals in red, additions in
// green — so the user reviews exactly what will be written and confirms
// (#260). A tag-set edit diffs per tag, as sets (#270); structured atom values
// (status, dates, priority, location) diff whole-value (kept together); the
// free-text description diffs at the word level. A bucket move is flattened
// into the box as the grouped dimension, so it reads identically to a field
// edit.

// ANSI colors, layered over the git-style [-removed-]/{+added+} word-diff
// markers. The markers are ALWAYS present (like git's default `--word-diff`), so
// the removed/added spans are legible even where color is absent or a whole-value
// diff renders two values adjacent; on a terminal the color wraps the marked
// span, off a terminal (piped/dry-run/tests) the markers stand alone.
const (
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiReset = "\x1b[0m"
)

func paintRemoved(s string, color bool) string {
	if s == "" {
		return ""
	}
	marked := "[-" + s + "-]"
	if color {
		return ansiRed + marked + ansiReset
	}
	return marked
}

func paintAdded(s string, color bool) string {
	if s == "" {
		return ""
	}
	marked := "{+" + s + "+}"
	if color {
		return ansiGreen + marked + ansiReset
	}
	return marked
}

// paintSeg paints one diff segment's text by its kind: plain when unchanged,
// removed-red or added-green otherwise.
func paintSeg(kind int, s string, color bool) string {
	switch kind {
	case segRemoved:
		return paintRemoved(s, color)
	case segAdded:
		return paintAdded(s, color)
	default:
		return s
	}
}

// renderWholeValue diffs a structured atom value as a unit: the old painted
// removed, the new painted added, adjacent (`[-HQ-]{+Corner store+}`).
func renderWholeValue(old, new string, color bool) string {
	return paintRemoved(old, color) + paintAdded(new, color)
}

// renderWordDiff diffs free text at the word level (an LCS over whitespace
// tokens): shared words stay plain, removed words are red, added words green,
// re-joined with single spaces (`Buy {+oat+} milk`).
func renderWordDiff(old, new string, color bool) string {
	segs := diffWords(strings.Fields(old), strings.Fields(new))
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, paintSeg(s.kind, s.word, color))
	}
	return strings.Join(parts, " ")
}

const (
	segSame = iota
	segRemoved
	segAdded
)

type wordSeg struct {
	kind int
	word string
}

// diffWords is a longest-common-subsequence word diff: the classic LCS-length
// table plus a backtrack emitting same/removed/added tokens in order.
func diffWords(o, n []string) []wordSeg {
	lcs := make([][]int, len(o)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(n)+1)
	}
	for i := len(o) - 1; i >= 0; i-- {
		for j := len(n) - 1; j >= 0; j-- {
			if o[i] == n[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var segs []wordSeg
	i, j := 0, 0
	for i < len(o) && j < len(n) {
		switch {
		case o[i] == n[j]:
			segs = append(segs, wordSeg{segSame, o[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			segs = append(segs, wordSeg{segRemoved, o[i]})
			i++
		default:
			segs = append(segs, wordSeg{segAdded, n[j]})
			j++
		}
	}
	for ; i < len(o); i++ {
		segs = append(segs, wordSeg{segRemoved, o[i]})
	}
	for ; j < len(n); j++ {
		segs = append(segs, wordSeg{segAdded, n[j]})
	}
	return segs
}

// fieldDelta is one changed single-valued atom of an object — a box-atom field
// edit, or a facet move of the grouped dimension: its name and old/new values.
type fieldDelta struct {
	Field string
	Old   string
	New   string
}

// objectChange is one changed object's preview line (cutting-garden#260/#270):
// Line is the edited document's line for the object (type, shown tags, inline
// atoms, trailer); Tags is the tag sequence the preview renders, each with its
// diff kind (segSame / segRemoved / segAdded); Atoms are the changed
// single-valued atoms; DescOld/DescNew carry the trailer word-diff when the
// description changed.
type objectChange struct {
	ID          string
	Line        objectLine
	Tags        []wordSeg
	Atoms       []fieldDelta
	DescChanged bool
	DescOld     string
	DescNew     string
}

// buildChanges folds every apply path's deltas into ONE preview line per
// object, keyed by anchor-relative id, so the facet, field, membership and
// tag-atom paths render through the same renderer and an object carrying
// several kinds of edit shows them together. A bucket move contributes the
// grouped dimension (dim) as an atom (from→to); a field edit contributes each
// atom (old read from the pinned base) or, for the trailer field, the
// description word-diff; a membership edit (a full tag-set replacement on
// tagDim) contributes a per-tag SET diff of the live set against the new one
// (diffTagSets). interp orders the tags by SortKey (nil: lexical).
func buildChanges(
	edited, base document,
	moves []move,
	fieldEdits []objectFieldEdit,
	memberships []membershipEdit,
	dim, tagDim string,
	trailer map[string]string,
	interp cgp.TagInterpreter,
	idOf boxIDer,
) []objectChange {
	baseLines := objectLinesByID(base)
	editedLines := objectLinesByID(edited)

	changes := make(map[string]*objectChange)
	get := func(id string) *objectChange {
		c := changes[id]
		if c == nil {
			ln := editedLines[id]
			c = &objectChange{ID: id, Line: ln, Tags: unchangedTags(ln.Tags)}
			changes[id] = c
		}
		return c
	}

	for _, mv := range moves {
		id := idOf(mv.URI)
		get(id).Atoms = append(get(id).Atoms, fieldDelta{Field: dim, Old: mv.From, New: mv.To})
	}
	for _, oe := range fieldEdits {
		id := idOf(oe.URI)
		c := get(id)
		baseAtoms := atomMap(baseLines[id].Fields)
		tf := trailer[oe.Node.Type]
		for _, e := range oe.Edits {
			if tf != "" && e.Name == tf {
				c.DescChanged = true
				c.DescOld = baseLines[id].Desc
				c.DescNew = e.Value
				continue
			}
			c.Atoms = append(c.Atoms, fieldDelta{Field: e.Name, Old: baseAtoms[e.Name], New: e.Value})
		}
	}
	for _, me := range memberships {
		c := get(idOf(me.URI))
		c.Tags = diffTagSets(facetKeys(me.Node.Facets[tagDim]), me.NewTags, c.Line.Tags, interp)
	}

	ids := make([]string, 0, len(changes))
	for id := range changes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]objectChange, 0, len(ids))
	for _, id := range ids {
		c := changes[id]
		sort.Slice(c.Atoms, func(i, j int) bool { return c.Atoms[i].Field < c.Atoms[j].Field })
		out = append(out, *c)
	}
	return out
}

// unchangedTags is the tag sequence of an object whose tag set is not edited:
// exactly the tags its document line shows, in the document's order.
func unchangedTags(shown []string) []wordSeg {
	segs := make([]wordSeg, 0, len(shown))
	for _, t := range shown {
		segs = append(segs, wordSeg{segSame, t})
	}
	return segs
}

// diffTagSets diffs an object's tag set old→new as SETS (#270): one sequence
// over their union, ordered by the interpreter's SortKey (the order the
// document renders tags in; ties and a nil interpreter fall back to lexical),
// each tag marked removed or added — or, when it is in both, rendered plain
// ONLY if the edited document shows it (shown). So a tag the document hides
// stays hidden unless it is the change being previewed: a placement tag
// stripped under `_tag-strip = placement` reappears exactly when the edit
// removes or adds it, and under `_tag-atoms = none` (no tags shown) the
// preview carries the changed tags alone.
func diffTagSets(old, new, shown []string, interp cgp.TagInterpreter) []wordSeg {
	inOld, inNew, isShown := stringSet(old), stringSet(new), stringSet(shown)
	all := appendMissing(slices.Clone(old), new)
	sortKey := func(t string) string {
		if interp == nil {
			return t
		}
		return interp.SortKey(t)
	}
	sort.SliceStable(all, func(i, j int) bool {
		ki, kj := sortKey(all[i]), sortKey(all[j])
		if ki != kj {
			return ki < kj
		}
		return all[i] < all[j]
	})
	segs := make([]wordSeg, 0, len(all))
	for _, t := range all {
		_, o := inOld[t]
		_, n := inNew[t]
		_, s := isShown[t]
		switch {
		case o && !n:
			segs = append(segs, wordSeg{segRemoved, t})
		case n && !o:
			segs = append(segs, wordSeg{segAdded, t})
		case s:
			segs = append(segs, wordSeg{segSame, t})
		}
	}
	return segs
}

// renderChange renders one object's preview line (cutting-garden#260/#270):
// the SAME espalier line the edited document shows for the object — the frame
// and slot layout are node_view.WriteSpelledObjectLine's, honoring the
// document's `_tag-atoms` placement (trailingTags) — with each changed slot
// re-spelled as a word-diff: a tag `[-t-]` / `{+t+}`, a single-valued atom
// `name=[-old-]{+new+}` in its own position, the trailer word-diffed. Every
// value is spelled through the ONE quoting rule (trellis.QuoteIfNeeded, #248).
// A changed atom the line does not carry inline (the grouped dimension of a
// facet move) follows the line's atoms. The line is indented two spaces under
// the preview header.
func renderChange(c objectChange, trailingTags, color bool) string {
	s := trellis.SpelledLiteral{ID: trellis.QuoteIfNeeded(c.ID), Type: c.Line.Type}
	for _, t := range c.Tags {
		s.Tags = append(s.Tags, paintSeg(t.kind, trellis.QuoteIfNeeded(t.word), color))
	}
	pending := make(map[string]fieldDelta, len(c.Atoms))
	for _, d := range c.Atoms {
		pending[d.Field] = d
	}
	for _, f := range c.Line.Fields {
		if d, ok := pending[f.Name]; ok {
			s.Atoms = append(s.Atoms, renderAtomDelta(d, color))
			delete(pending, f.Name)
			continue
		}
		s.Atoms = append(s.Atoms, trellis.SpellAtom(trellis.Atom{Name: f.Name, Value: f.Value}))
	}
	for _, d := range c.Atoms {
		if _, ok := pending[d.Field]; ok {
			s.Atoms = append(s.Atoms, renderAtomDelta(d, color))
		}
	}

	desc := c.Line.Desc
	if c.DescChanged {
		desc = renderWordDiff(c.DescOld, c.DescNew, color)
	}
	var b strings.Builder
	b.WriteString("  ")
	node_view.WriteSpelledObjectLine(&b, s, desc, trailingTags)
	return strings.TrimSuffix(b.String(), "\n")
}

// renderAtomDelta spells one single-valued atom change `name=[-old-]{+new+}`,
// each value quoted as the document would spell it; an empty side (an atom or
// bucket appearing from / vanishing to nothing) renders no marker.
func renderAtomDelta(d fieldDelta, color bool) string {
	return trellis.QuoteIfNeeded(d.Field) + "=" +
		renderWholeValue(quoteNonEmpty(d.Old), quoteNonEmpty(d.New), color)
}

func quoteNonEmpty(s string) string {
	if s == "" {
		return ""
	}
	return trellis.QuoteIfNeeded(s)
}

// renderChanges writes the preview lines (one per object). The caller writes
// the header and the confirm/dry-run footer around them.
func renderChanges(w io.Writer, changes []objectChange, trailingTags, color bool) {
	for _, c := range changes {
		fmt.Fprintln(w, renderChange(c, trailingTags, color))
	}
}

// objectLinesByID indexes a document's object lines by box id —
// last-line-wins for the type, atoms and trailer (a multi-appearance object's
// lines agree on everything but their bucket and its placement tag), with the
// shown Tags the UNION over every appearance (first-seen order), so a tag any
// appearance shows counts as shown. The union is a fresh slice: generate's
// lines may share tag backing arrays (tagRender.fill).
func objectLinesByID(doc document) map[string]objectLine {
	out := map[string]objectLine{}
	for _, ln := range doc.objectLines() {
		if prev, seen := out[ln.ID]; seen {
			ln.Tags = appendMissing(slices.Clone(prev.Tags), ln.Tags)
		}
		out[ln.ID] = ln
	}
	return out
}
