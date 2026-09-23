package organize

import (
	"reflect"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// TestPlanFieldEdits pins the field/trailer three-way merge (cutting-garden#218):
// a writable atom or trailer change applies, a read-only atom is a notice, a live
// drift is a conflict, and a change the live state already matches is a no-op.
func TestPlanFieldEdits(t *testing.T) {
	const (
		anchor = "caldav:https://host/dav/cal/"
		typ    = "caldav-object-vtodo-v1"
	)
	writable := map[string]map[string]bool{typ: {"location": true, "summary": true}}
	trailer := map[string]string{typ: "summary"}
	present := func(n cgp.Node) []cgp.BoxAtom {
		loc, _ := n.Fields["location"].(string)
		return []cgp.BoxAtom{{Name: "location", Value: loc}}
	}
	live := func(summary, location string) cgp.Node {
		return cgp.Node{
			URI:    mustURL(t, "caldav:https://host/dav/cal/t.ics"),
			Type:   typ,
			Fields: map[string]any{"summary": summary, "location": location},
		}
	}
	base := document{Ungrouped: []objectLine{
		{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "HQ"}}, Desc: "Old title"},
	}}

	t.Run("writable atom and trailer edits apply", func(t *testing.T) {
		edited := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Annex"}}, Desc: "New title"},
		}}
		edits, notices, err := planFieldEdits(
			edited, base, []cgp.Node{live("Old title", "HQ")}, boxIDsFor(nil, anchor), writable, trailer, present,
		)
		if err != nil {
			t.Fatalf("planFieldEdits: %v", err)
		}
		if len(notices) != 0 {
			t.Errorf("notices = %v, want none", notices)
		}
		if len(edits) != 1 {
			t.Fatalf("edits = %+v, want exactly one object", edits)
		}
		want := []cgp.FieldEdit{{Name: "location", Value: "Annex"}, {Name: "summary", Value: "New title"}}
		if !reflect.DeepEqual(edits[0].Edits, want) {
			t.Errorf("edits = %+v, want %+v", edits[0].Edits, want)
		}
	})

	t.Run("read-only atom is a notice, not an edit", func(t *testing.T) {
		roBase := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "date_start", Value: "2026-08-15"}}, Desc: "Old title"},
		}}
		edited := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "date_start", Value: "2026-09-01"}}, Desc: "Old title"},
		}}
		edits, notices, err := planFieldEdits(
			edited, roBase, []cgp.Node{live("Old title", "HQ")}, boxIDsFor(nil, anchor), writable, trailer, present,
		)
		if err != nil {
			t.Fatalf("planFieldEdits: %v", err)
		}
		if len(edits) != 0 {
			t.Errorf("edits = %+v, want none (date_start is read-only)", edits)
		}
		if !reflect.DeepEqual(notices, []string{"t.ics"}) {
			t.Errorf("notices = %v, want [t.ics]", notices)
		}
	})

	t.Run("live drift is a conflict", func(t *testing.T) {
		edited := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Annex"}}, Desc: "Old title"},
		}}
		if _, _, err := planFieldEdits(
			edited, base, []cgp.Node{live("Old title", "Elsewhere")}, boxIDsFor(nil, anchor), writable, trailer, present,
		); err == nil {
			t.Error("expected a conflict when the live value drifted from base")
		}
	})

	t.Run("no-op when live already equals the edit", func(t *testing.T) {
		edited := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Annex"}}, Desc: "Old title"},
		}}
		edits, _, err := planFieldEdits(
			edited, base, []cgp.Node{live("Old title", "Annex")}, boxIDsFor(nil, anchor), writable, trailer, present,
		)
		if err != nil {
			t.Fatalf("planFieldEdits: %v", err)
		}
		if len(edits) != 0 {
			t.Errorf("edits = %+v, want none (live already matches the edit)", edits)
		}
	})

	t.Run("split date atom is writable via its source field", func(t *testing.T) {
		// dtstart is writable; date_start is not itself a writable key, but its
		// live atom carries Field "dtstart", so the gate resolves through it.
		wr := map[string]map[string]bool{typ: {"dtstart": true}}
		pr := func(n cgp.Node) []cgp.BoxAtom {
			ds, _ := n.Fields["dtstart"].(string)
			return []cgp.BoxAtom{{Name: "date_start", Value: ds, Field: "dtstart"}}
		}
		liveDS := cgp.Node{
			URI:    mustURL(t, "caldav:https://host/dav/cal/t.ics"),
			Type:   typ,
			Fields: map[string]any{"dtstart": "2026-08-15"},
		}
		b := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "date_start", Value: "2026-08-15"}}},
		}}
		e := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "date_start", Value: "2026-09-01"}}},
		}}
		edits, notices, err := planFieldEdits(e, b, []cgp.Node{liveDS}, boxIDsFor(nil, anchor), wr, nil, pr)
		if err != nil {
			t.Fatalf("planFieldEdits: %v", err)
		}
		if len(notices) != 0 {
			t.Errorf("notices = %v, want none", notices)
		}
		if len(edits) != 1 || len(edits[0].Edits) != 1 ||
			edits[0].Edits[0] != (cgp.FieldEdit{Name: "date_start", Value: "2026-09-01"}) {
			t.Errorf("edits = %+v, want one date_start=2026-09-01 routed via dtstart", edits)
		}
	})
}

// TestMultilineValuesCollapseInPresentationOnly pins the native tags slice 1.5 F
// newline decision: the caldav ical layer unescapes RFC 5545 `\n` into REAL
// newlines in the stored struct, and organize — whose trailer and atoms are
// single-line document slots — collapses newlines to single spaces at the
// PRESENTATION layer only (collapseToSingleLine, applied identically by
// nodeDescription, descriptionOf, and boxAtomPresenter's wrapper). Because all
// three collapse the same way, an untouched multiline trailer compares equal
// across base/edited/live and writes NOTHING back.
func TestMultilineValuesCollapseInPresentationOnly(t *testing.T) {
	cases := []struct{ in, want string }{
		{"single line", "single line"},
		{"one\ntwo", "one two"},
		{"one\r\ntwo\rthree", "one two three"},
		{"blank\n\nrun", "blank run"},
	}
	for _, tc := range cases {
		if got := collapseToSingleLine(tc.in); got != tc.want {
			t.Errorf("collapseToSingleLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	n := cgp.Node{Fields: map[string]any{"summary": "Plan the trip\nthen pack"}}
	if got := nodeDescription(n); got != "Plan the trip then pack" {
		t.Errorf("nodeDescription = %q, want the collapsed single line", got)
	}
	if got := descriptionOf(n, "summary"); got != "Plan the trip then pack" {
		t.Errorf("descriptionOf = %q, want the collapsed single line", got)
	}

	// The untouched-trailer no-write guarantee: base and edited documents carry
	// the collapsed rendering, live carries the real newline — no edit, no
	// conflict, so the stored newlines survive the apply.
	const typ = "caldav-object-vtodo-v1"
	live := cgp.Node{
		URI:    mustURL(t, "caldav:https://host/dav/cal/ml.ics"),
		Type:   typ,
		Fields: map[string]any{"summary": "Plan the trip\nthen pack"},
	}
	doc := document{Ungrouped: []objectLine{{ID: "ml.ics", Desc: "Plan the trip then pack"}}}
	edits, notices, err := planFieldEdits(
		doc, doc, []cgp.Node{live}, boxIDsFor(nil, "caldav:https://host/dav/cal/"),
		map[string]map[string]bool{typ: {"summary": true}},
		map[string]string{typ: "summary"}, nil,
	)
	if err != nil {
		t.Fatalf("planFieldEdits: %v", err)
	}
	if len(edits) != 0 || len(notices) != 0 {
		t.Errorf("edits = %+v notices = %v, want none (untouched multiline trailer)", edits, notices)
	}
}

// TestRejectMoveFieldCollisions pins the plan-time refusal of an object that is
// both moved between buckets of the grouped dimension AND has the same stored
// property edited through a box atom: the move and the field write each patch
// that property, the field patch is built from the pre-move node, so the last
// write silently wins. A collision is by atom name (the grouped `date_due`
// atom) or by the atom's source field (`time_due` splices into the same DUE);
// an edit of an unrelated property alongside a move is fine.
func TestRejectMoveFieldCollisions(t *testing.T) {
	const anchor = "caldav:https://host/dav/cal/"
	node := cgp.Node{URI: mustURL(t, anchor+"s.ics"), Type: "task"}
	present := func(cgp.Node) []cgp.BoxAtom {
		return []cgp.BoxAtom{
			{Name: "date_due", Value: "2026-08-15", Field: "due"},
			{Name: "time_due", Value: "14-30", Field: "due"},
			{Name: "location", Value: "HQ"},
		}
	}
	moves := []move{{URI: node.URIString(), From: "2026-08", To: "2026-09", Node: node}}
	idOf := boxIDsFor(nil, anchor)
	edit := func(name, value string) []objectFieldEdit {
		return []objectFieldEdit{{URI: node.URIString(), Node: node, Edits: []cgp.FieldEdit{{Name: name, Value: value}}}}
	}

	for _, tc := range []struct{ name, value, want string }{
		{"date_due", "2026-09-20", `s.ics: moved to bucket 2026-09 and date_due edited to 2026-09-20 in the same document; make one edit`},
		{"time_due", "10-00", `s.ics: moved to bucket 2026-09 and time_due edited to 10-00 in the same document; make one edit`},
	} {
		err := rejectMoveFieldCollisions(moves, edit(tc.name, tc.value), "date_due", present, idOf)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
	if err := rejectMoveFieldCollisions(moves, edit("location", "Annex"), "date_due", present, idOf); err != nil {
		t.Errorf("unrelated property: err = %v, want nil", err)
	}
}
