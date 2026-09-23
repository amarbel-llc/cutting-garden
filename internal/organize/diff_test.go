package organize

import (
	"net/url"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

func TestRenderWholeValue(t *testing.T) {
	if got := renderWholeValue("HQ", "Corner store", false); got != "[-HQ-]{+Corner store+}" {
		t.Errorf("renderWholeValue = %q", got)
	}
	// An added-from-nothing / removed-to-nothing degrades cleanly.
	if got := renderWholeValue("", "5", false); got != "{+5+}" {
		t.Errorf("renderWholeValue(empty old) = %q", got)
	}
	// With color the markers are WRAPPED in ANSI, not replaced by it (git-style
	// word-diff shows both), so removed/added spans read even side by side.
	if got := renderWholeValue("HQ", "Annex", true); got != ansiRed+"[-HQ-]"+ansiReset+ansiGreen+"{+Annex+}"+ansiReset {
		t.Errorf("renderWholeValue(color) = %q", got)
	}
}

// TestRenderWordDiff pins the word-level diff on free text: shared words stay
// plain, the inserted word is added-green, a full replacement shows both.
func TestRenderWordDiff(t *testing.T) {
	cases := []struct{ old, new, want string }{
		{"Buy milk", "Buy oat milk", "Buy {+oat+} milk"},
		{"foo", "bar", "[-foo-] {+bar+}"},
		{"same text", "same text", "same text"},
		{"drop this", "drop", "drop [-this-]"},
	}
	for _, c := range cases {
		if got := renderWordDiff(c.old, c.new, false); got != c.want {
			t.Errorf("renderWordDiff(%q,%q) = %q, want %q", c.old, c.new, got, c.want)
		}
	}
}

// The apply preview (cutting-garden#260/#270) renders each changed object as
// the SAME espalier line the edited document shows for it, with per-atom
// word-diff markers: a tag-set edit diffs as SETS (each removed tag `[-t-]`,
// each added `{+t+}`, unchanged plain, one SortKey-ordered sequence), a
// single-valued field edit or facet move as `name=[-old-]{+new+}` in its atom
// position, the trailer as a word diff. These tests drive buildChanges and
// renderChange together — the one renderer every apply path shares.

const previewAnchor = "fake://mail/"

func previewNode(id string, tags ...string) cgp.Node {
	u, _ := url.Parse(previewAnchor + id)
	n := cgp.Node{URI: u, Type: "thread", Facets: map[string][]cgp.FacetValue{}}
	for _, t := range tags {
		n.Facets["tags"] = append(n.Facets["tags"], cgp.FacetValue{Key: t})
	}
	return n
}

func previewMembership(id string, old, new []string) membershipEdit {
	n := previewNode(id, old...)
	return membershipEdit{URI: n.URIString(), Node: n, NewTags: new}
}

func naiveInterp(t *testing.T) cgp.TagInterpreter {
	t.Helper()
	interp, ok := cgp.LookupTagInterpreter("naive")
	if !ok {
		t.Fatal("naive interpreter not registered")
	}
	return interp
}

// previewLines runs the fold and the renderer over one edit set, joining the
// rendered lines.
func previewLines(
	t *testing.T, edited, base document, moves []move, fieldEdits []objectFieldEdit,
	memberships []membershipEdit, interp cgp.TagInterpreter, color bool,
) string {
	t.Helper()
	changes := buildChanges(
		edited, base, moves, fieldEdits, memberships, "status", "tags",
		map[string]string{"thread": "summary"}, interp, boxIDsFor(nil, previewAnchor),
	)
	var lines []string
	for _, c := range changes {
		lines = append(lines, renderChange(c, edited.TagAtoms == tagAtomsTrailing, color))
	}
	return strings.Join(lines, "\n")
}

func assertPreview(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("preview =\n%s\nwant\n%s", got, want)
	}
}

// A removed placement tag (`_inbox`, the tag a move out of `# _inbox`
// removes) is SHOWN with its marker although the document strips it; the
// unchanged tags appear once, plain, in the document's order; the inline atoms
// and trailer are the document's; separators are single spaces (#260).
func TestPreview_TagRemovalMarksOnlyTheRemovedTag(t *testing.T) {
	line := objectLine{
		ID: "T1", Tags: []string{"_unread", "payee-one_medical"},
		Fields: []cgp.BoxAtom{{Name: "from", Value: "a@example.com"}}, Desc: "Your statement is ready",
	}
	doc := document{Ungrouped: []objectLine{line}}
	got := previewLines(t, doc, doc, nil, nil, []membershipEdit{previewMembership(
		"T1", []string{"_inbox", "_unread", "payee-one_medical"}, []string{"_unread", "payee-one_medical"},
	)}, naiveInterp(t), false)
	assertPreview(t, got,
		`  - [T1 [-_inbox-] _unread payee-one_medical from="a@example.com"] Your statement is ready`)
}

// An UNCHANGED placement tag stays stripped, exactly as the document strips
// it (T3 sits under `# _inbox`, whose Via tag its box does not repeat); the
// added tag is marked in SortKey position.
func TestPreview_UnchangedPlacementTagStaysStripped(t *testing.T) {
	doc := document{Sections: []section{{Depth: 1, Term: "_inbox", Lines: []objectLine{{
		ID: "T3", Tags: []string{"_flagged", "payee-acme"},
		Fields: []cgp.BoxAtom{{Name: "from", Value: "b@example.com"}}, Desc: "Lunch next week?",
	}}}}}
	got := previewLines(t, doc, doc, nil, nil, []membershipEdit{previewMembership(
		"T3", []string{"_flagged", "_inbox"}, []string{"_flagged", "_inbox", "payee-acme"},
	)}, naiveInterp(t), false)
	assertPreview(t, got,
		`  - [T3 _flagged {+payee-acme+} from="b@example.com"] Lunch next week?`)
}

// sortKeyReversed orders tags by the reverse of their spelling, proving the
// merged sequence follows the interpreter's SortKey, not lexical order.
type sortKeyReversed struct{ cgp.TagInterpreter }

func (sortKeyReversed) SortKey(tag string) string {
	r := []rune(tag)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// Add and remove ride in one line, merged into one SortKey-ordered sequence;
// a tag needing quotes is spelled through the one quoting rule inside its
// marker.
func TestPreview_AddAndRemoveInOneSortKeyOrderedLine(t *testing.T) {
	doc := document{Ungrouped: []objectLine{{
		ID: "lit3.ics", Tags: []string{"urgent", "ya"},
		Fields: []cgp.BoxAtom{{Name: "location", Value: "Bank"}}, Desc: "Plan, then do",
	}}}
	got := previewLines(t, doc, doc, nil, nil, []membershipEdit{previewMembership(
		"lit3.ics", []string{"planning, misc", "ya"}, []string{"urgent", "ya"},
	)}, sortKeyReversed{naiveInterp(t)}, false)
	// Reversed keys: "ay" < "csim ,gninnalp" < "tnegru".
	assertPreview(t, got,
		`  - [lit3.ics ya [-"planning, misc"-] {+urgent+} location=Bank] Plan, then do`)
}

// `_tag-atoms = trailing` places the whole tag sequence — markers included —
// after the atoms, as the document does.
func TestPreview_TrailingTagAtoms(t *testing.T) {
	doc := document{TagAtoms: tagAtomsTrailing, Ungrouped: []objectLine{{
		ID: "lit3.ics", Tags: []string{"urgent"},
		Fields: []cgp.BoxAtom{{Name: "location", Value: "Bank"}}, Desc: "Plan, then do",
	}}}
	got := previewLines(t, doc, doc, nil, nil, []membershipEdit{previewMembership(
		"lit3.ics", []string{"planning, misc"}, []string{"urgent"},
	)}, naiveInterp(t), false)
	assertPreview(t, got,
		`  - [lit3.ics location=Bank [-"planning, misc"-] {+urgent+}] Plan, then do`)
}

// `_tag-atoms = none`: the document shows no tags, so the preview shows only
// the CHANGED ones (the edit being previewed), in the leading position.
func TestPreview_NoTagAtomsShowsOnlyChangedTags(t *testing.T) {
	doc := document{TagAtoms: tagAtomsNone, Ungrouped: []objectLine{{
		ID: "lit3.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Bank"}}, Desc: "Plan, then do",
	}}}
	got := previewLines(t, doc, doc, nil, nil, []membershipEdit{previewMembership(
		"lit3.ics", []string{"keep", "planning, misc"}, []string{"keep", "urgent"},
	)}, naiveInterp(t), false)
	assertPreview(t, got,
		`  - [lit3.ics [-"planning, misc"-] {+urgent+} location=Bank] Plan, then do`)
}

// One object's tag edit, field edit, facet move and trailer edit fold into ONE
// line: the edited atom diffs in its own position (value quoted by the one
// rule), the moved dimension — not an inline atom — follows the atoms, and the
// description word-diffs.
func TestPreview_TagFieldMoveAndTrailerInOneLine(t *testing.T) {
	base := document{Ungrouped: []objectLine{{
		ID: "t.ics", Tags: []string{"home"},
		Fields: []cgp.BoxAtom{{Name: "location", Value: "HQ"}, {Name: "priority", Value: "5"}},
		Desc:   "Old title",
	}}}
	edited := document{Ungrouped: []objectLine{{
		ID: "t.ics", Tags: []string{"home", "urgent"},
		Fields: []cgp.BoxAtom{{Name: "location", Value: "Corner store"}, {Name: "priority", Value: "5"}},
		Desc:   "New title",
	}}}
	node := previewNode("t.ics", "home")
	moves := []move{{URI: node.URIString(), From: "NEEDS-ACTION", To: "COMPLETED", Node: node}}
	fieldEdits := []objectFieldEdit{{URI: node.URIString(), Node: node, Edits: []cgp.FieldEdit{
		{Name: "location", Value: "Corner store"},
		{Name: "summary", Value: "New title"},
	}}}
	memberships := []membershipEdit{previewMembership("t.ics", []string{"home"}, []string{"home", "urgent"})}
	got := previewLines(t, edited, base, moves, fieldEdits, memberships, naiveInterp(t), false)
	assertPreview(t, got,
		`  - [t.ics home {+urgent+} location=[-HQ-]{+"Corner store"+} priority=5 `+
			`status=[-NEEDS-ACTION-]{+COMPLETED+}] [-Old-] {+New+} title`)
}

// An object with only a field edit renders its document tags plain; an
// unchanged description renders plain.
func TestPreview_FieldEditKeepsDocumentTags(t *testing.T) {
	base := document{Ungrouped: []objectLine{{
		ID: "u.ics", Tags: []string{"work"}, Fields: []cgp.BoxAtom{{Name: "priority", Value: "3"}}, Desc: "Buy milk",
	}}}
	edited := document{Ungrouped: []objectLine{{
		ID: "u.ics", Tags: []string{"work"}, Fields: []cgp.BoxAtom{{Name: "priority", Value: "1"}}, Desc: "Buy milk",
	}}}
	node := previewNode("u.ics")
	fieldEdits := []objectFieldEdit{{URI: node.URIString(), Node: node, Edits: []cgp.FieldEdit{{Name: "priority", Value: "1"}}}}
	got := previewLines(t, edited, base, nil, fieldEdits, nil, naiveInterp(t), false)
	assertPreview(t, got, `  - [u.ics work priority=[-3-]{+1+}] Buy milk`)
}

// With color on, each changed atom's marker is wrapped in its own ANSI span;
// unchanged atoms stay uncolored.
func TestPreview_ColorPerAtom(t *testing.T) {
	doc := document{Ungrouped: []objectLine{{ID: "T1", Tags: []string{"b", "c"}, Desc: "d"}}}
	got := previewLines(t, doc, doc, nil, nil, []membershipEdit{previewMembership(
		"T1", []string{"a", "b"}, []string{"b", "c"},
	)}, naiveInterp(t), true)
	assertPreview(t, got,
		"  - [T1 "+ansiRed+"[-a-]"+ansiReset+" b "+ansiGreen+"{+c+}"+ansiReset+"] d")
}
