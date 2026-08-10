package organize

import (
	"reflect"
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

// TestBuildChanges pins the fold: a bucket move contributes the grouped dimension
// as an atom (from→to), a field edit contributes each atom with its OLD read from
// the pinned base, and the trailer edit becomes the description word-diff.
func TestBuildChanges(t *testing.T) {
	const anchor = "fake://cal/"
	base := document{Ungrouped: []objectLine{
		{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "HQ"}}, Desc: "Old title"},
	}}
	edited := document{Ungrouped: []objectLine{
		{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Annex"}}, Desc: "New title"},
	}}
	moves := []move{{URI: "fake://cal/t.ics", From: "NEEDS-ACTION", To: "COMPLETED", Node: cgp.Node{Type: "task"}}}
	fieldEdits := []objectFieldEdit{{
		URI:  "fake://cal/t.ics",
		Node: cgp.Node{Type: "task"},
		Edits: []cgp.FieldEdit{
			{Name: "location", Value: "Annex"},
			{Name: "summary", Value: "New title"},
		},
	}}
	trailer := map[string]string{"task": "summary"}

	changes := buildChanges(edited, base, moves, fieldEdits, "status", trailer, anchor)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want 1", changes)
	}
	c := changes[0]
	if c.ID != "t.ics" || c.Desc != "New title" {
		t.Errorf("id/desc = %q/%q", c.ID, c.Desc)
	}
	wantAtoms := []fieldDelta{
		{Field: "location", Old: "HQ", New: "Annex"},
		{Field: "status", Old: "NEEDS-ACTION", New: "COMPLETED"},
	}
	if !reflect.DeepEqual(c.Atoms, wantAtoms) {
		t.Errorf("atoms = %+v, want %+v", c.Atoms, wantAtoms)
	}
	if !c.DescChanged || c.DescOld != "Old title" || c.DescNew != "New title" {
		t.Errorf("desc delta = %+v", c)
	}
}

// TestRenderChange pins the box format: id, changed atoms as whole-value diffs,
// and the description word-diff after the box.
func TestRenderChange(t *testing.T) {
	c := objectChange{
		ID: "t.ics",
		Atoms: []fieldDelta{
			{Field: "location", Old: "HQ", New: "Annex"},
			{Field: "status", Old: "NEEDS-ACTION", New: "COMPLETED"},
		},
		DescChanged: true,
		DescOld:     "Old title",
		DescNew:     "New title",
	}
	got := renderChange(c, false)
	want := "  - [t.ics  location=[-HQ-]{+Annex+}  status=[-NEEDS-ACTION-]{+COMPLETED+}]  [-Old-] {+New+} title"
	if got != want {
		t.Errorf("renderChange =\n  %q\nwant\n  %q", got, want)
	}

	// An unchanged description renders plain.
	plain := objectChange{ID: "u.ics", Atoms: []fieldDelta{{Field: "priority", Old: "3", New: "1"}}, Desc: "Buy milk"}
	if got := renderChange(plain, false); got != "  - [u.ics  priority=[-3-]{+1+}]  Buy milk" {
		t.Errorf("renderChange(plain desc) = %q", got)
	}
}
