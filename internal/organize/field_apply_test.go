package organize

import (
	"reflect"
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
			edited, base, []cgp.Node{live("Old title", "HQ")}, anchor, writable, trailer, present,
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
			edited, roBase, []cgp.Node{live("Old title", "HQ")}, anchor, writable, trailer, present,
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
			edited, base, []cgp.Node{live("Old title", "Elsewhere")}, anchor, writable, trailer, present,
		); err == nil {
			t.Error("expected a conflict when the live value drifted from base")
		}
	})

	t.Run("no-op when live already equals the edit", func(t *testing.T) {
		edited := document{Ungrouped: []objectLine{
			{ID: "t.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Annex"}}, Desc: "Old title"},
		}}
		edits, _, err := planFieldEdits(
			edited, base, []cgp.Node{live("Old title", "Annex")}, anchor, writable, trailer, present,
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
		edits, notices, err := planFieldEdits(e, b, []cgp.Node{liveDS}, anchor, wr, nil, pr)
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
