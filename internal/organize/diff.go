package organize

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// The apply diff (cutting-garden#224): before writing, organize renders each
// changed object as one espalier box with its deltas shown inline as a word-diff
// — removals in red, additions in green — so the user reviews exactly what will
// be written and confirms. Structured atom values (status, dates, priority,
// location) diff whole-value (kept together); the free-text description diffs at
// the word level. A bucket move is flattened into the box as the grouped
// dimension, so it reads identically to a field edit.

// ANSI colors, used when the sink is a terminal; otherwise the diff falls back to
// git-style [-removed-]/{+added+} markers so a piped/dry-run diff stays readable
// and testable.
const (
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiReset = "\x1b[0m"
)

func paintRemoved(s string, color bool) string {
	if s == "" {
		return ""
	}
	if color {
		return ansiRed + s + ansiReset
	}
	return "[-" + s + "-]"
}

func paintAdded(s string, color bool) string {
	if s == "" {
		return ""
	}
	if color {
		return ansiGreen + s + ansiReset
	}
	return "{+" + s + "+}"
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
		switch s.kind {
		case segSame:
			parts = append(parts, s.word)
		case segRemoved:
			parts = append(parts, paintRemoved(s.word, color))
		case segAdded:
			parts = append(parts, paintAdded(s.word, color))
		}
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

// fieldDelta is one changed atom of an object: its box name and old/new values.
type fieldDelta struct {
	Field string
	Old   string
	New   string
}

// objectChange is one changed object's diff box: its id, the changed atoms
// (whole-value), and the description (a word-diff when it changed, else plain).
type objectChange struct {
	ID          string
	Desc        string // the after description (shown plain when unchanged)
	Atoms       []fieldDelta
	DescChanged bool
	DescOld     string
	DescNew     string
}

// buildChanges folds the bucket moves and field edits into one diff box per
// object, keyed by anchor-relative id. A move contributes the grouped dimension
// as an atom (from→to); a field edit contributes each atom (old read from the
// pinned base) or, for the trailer field, the description word-diff.
func buildChanges(
	edited, base document,
	moves []move,
	fieldEdits []objectFieldEdit,
	dim string,
	trailer map[string]string,
	anchor string,
) []objectChange {
	baseLines := make(map[string]objectLine)
	for _, ln := range base.objectLines() {
		baseLines[ln.ID] = ln
	}
	editedLines := make(map[string]objectLine)
	for _, ln := range edited.objectLines() {
		editedLines[ln.ID] = ln
	}

	changes := make(map[string]*objectChange)
	get := func(id string) *objectChange {
		c := changes[id]
		if c == nil {
			c = &objectChange{ID: id, Desc: editedLines[id].Desc}
			changes[id] = c
		}
		return c
	}

	for _, mv := range moves {
		id := relativeID(mv.URI, anchor)
		get(id).Atoms = append(get(id).Atoms, fieldDelta{Field: dim, Old: mv.From, New: mv.To})
	}
	for _, oe := range fieldEdits {
		id := relativeID(oe.URI, anchor)
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

// renderChange renders one object's diff box: `- [id  atom=diff …] desc`.
func renderChange(c objectChange, color bool) string {
	var b strings.Builder
	b.WriteString("  - [")
	b.WriteString(c.ID)
	for _, d := range c.Atoms {
		fmt.Fprintf(&b, "  %s=%s", d.Field, renderWholeValue(d.Old, d.New, color))
	}
	b.WriteByte(']')
	trailer := c.Desc
	if c.DescChanged {
		trailer = renderWordDiff(c.DescOld, c.DescNew, color)
	}
	if trailer != "" {
		b.WriteString("  ")
		b.WriteString(trailer)
	}
	return b.String()
}

// renderDiff writes the change boxes (one per object). The caller writes the
// header and the confirm/dry-run footer around it.
func renderDiff(w io.Writer, changes []objectChange, color bool) {
	for _, c := range changes {
		fmt.Fprintln(w, renderChange(c, color))
	}
}
