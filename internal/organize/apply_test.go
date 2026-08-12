package organize

import (
	"sort"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// taskNode builds a live caldav VTODO node with the given status membership; an
// empty status omits the facet (the node contributes nothing to the dimension).
func taskNode(t *testing.T, uri, status string) cgp.Node {
	facets := map[string][]cgp.FacetValue{"component": {{Key: "VTODO"}}}
	if status != "" {
		facets["status"] = []cgp.FacetValue{{Key: status}}
	}
	return cgp.Node{URI: mustURL(t, uri), Type: "caldav-object-v1", Facets: facets}
}

// docWith builds a document from a box-id → bucket assignment: an empty value
// places the object ungrouped (above the dimension heading), a non-empty value
// under a `## =<value>` bucket beneath the `# <dim>=` heading.
func docWith(dim string, assignment map[string]string) document {
	doc := document{Sections: []section{{Depth: 1, Term: dim + "="}}}
	byValue := map[string][]objectLine{}
	for id, v := range assignment {
		if v == "" {
			doc.Ungrouped = append(doc.Ungrouped, objectLine{ID: id})
		} else {
			byValue[v] = append(byValue[v], objectLine{ID: id})
		}
	}
	keys := make([]string, 0, len(byValue))
	for k := range byValue {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		doc.Sections = append(doc.Sections, section{Depth: 2, Term: "=" + k, Lines: byValue[k]})
	}
	return doc
}

// TestApplyMode pins the wet-run-by-default truth table (cutting-garden#213): a
// terminal writes-with-confirm by default, a pipe stays dry-run unless -commit,
// and -dry-run forces preview everywhere. The confirm gate (interactive) is armed
// only at a terminal, so huh's prompt is never run headless.
func TestApplyMode(t *testing.T) {
	cases := []struct {
		name                        string
		dryRun, commitFlag, tty     bool
		wantCommit, wantInteractive bool
	}{
		{"tty default writes with confirm", false, false, true, true, true},
		{"tty -commit still confirms", false, true, true, true, true},
		{"tty -dry-run previews", true, false, true, false, false},
		{"pipe default dry-run", false, false, false, false, false},
		{"pipe -commit writes headless", false, true, false, true, false},
		{"pipe -dry-run previews", true, true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			commit, interactive := applyMode(tc.dryRun, tc.commitFlag, tc.tty)
			if commit != tc.wantCommit || interactive != tc.wantInteractive {
				t.Errorf("applyMode(dryRun=%v, commit=%v, tty=%v) = (%v, %v), want (%v, %v)",
					tc.dryRun, tc.commitFlag, tc.tty, commit, interactive,
					tc.wantCommit, tc.wantInteractive)
			}
			if interactive && !tc.tty {
				t.Errorf("confirm gate armed without a terminal (dryRun=%v, commit=%v, tty=%v)",
					tc.dryRun, tc.commitFlag, tc.tty)
			}
		})
	}
}

// TestCheckMoveWritable pins the pre-apply writability gate (cutting-garden#221):
// a writable dimension passes; a read-only (none) dimension and an unmapped type
// are rejected. This is the check that lets a read-only reorganization refuse
// before the diff/confirm rather than after the user confirms.
func TestCheckMoveWritable(t *testing.T) {
	writes := map[string]cgp.FacetWrite{
		"caldav-object-vtodo-v1":  {DimensionKey: "priority", Mode: cgp.FacetWriteOne, Field: "priority"},
		"caldav-object-vevent-v1": {DimensionKey: "component", Mode: cgp.FacetWriteNone},
	}
	if err := checkMoveWritable(writes, move{Node: cgp.Node{Type: "caldav-object-vtodo-v1"}}); err != nil {
		t.Errorf("writable move rejected: %v", err)
	}
	if err := checkMoveWritable(writes, move{Node: cgp.Node{Type: "caldav-object-vevent-v1"}}); err == nil {
		t.Error("read-only (none) dimension move must be rejected")
	}
	if err := checkMoveWritable(writes, move{Node: cgp.Node{Type: "unmapped-v1"}}); err == nil {
		t.Error("unmapped type move must be rejected")
	}
}

// TestPlanMoves_CleanMove pins that a node moved in the edit, with the live state
// still agreeing with the base, yields exactly one move.
func TestPlanMoves_CleanMove(t *testing.T) {
	base := docWith("status", map[string]string{"t1.ics": "NEEDS-ACTION"})
	edited := docWith("status", map[string]string{"t1.ics": "COMPLETED"})
	live := []cgp.Node{taskNode(t, "caldav://h/c/t1.ics", "NEEDS-ACTION")}
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"

	moves, err := planMoves(edited, base, "status", live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves = %d, want 1 (%+v)", len(moves), moves)
	}
	if mv := moves[0]; mv.From != "NEEDS-ACTION" || mv.To != "COMPLETED" {
		t.Errorf("unexpected move: %+v", mv)
	}
}

// TestPlanMoves_NullToValue pins a move out of the ungrouped set (a statusless
// task assigned a status) — the caldav tracer's actual scenario.
func TestPlanMoves_NullToValue(t *testing.T) {
	base := docWith("status", map[string]string{"t1.ics": ""})
	edited := docWith("status", map[string]string{"t1.ics": "COMPLETED"})
	live := []cgp.Node{taskNode(t, "caldav://h/c/t1.ics", "")}
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"

	moves, err := planMoves(edited, base, "status", live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 1 || moves[0].From != "" || moves[0].To != "COMPLETED" {
		t.Fatalf("moves = %+v, want one ''->COMPLETED", moves)
	}
}

// TestPlanMoves_Unmoved pins that an unedited document produces no moves.
func TestPlanMoves_Unmoved(t *testing.T) {
	base := docWith("status", map[string]string{"t1.ics": "NEEDS-ACTION"})
	edited := docWith("status", map[string]string{"t1.ics": "NEEDS-ACTION"})
	live := []cgp.Node{taskNode(t, "caldav://h/c/t1.ics", "NEEDS-ACTION")}
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"

	moves, err := planMoves(edited, base, "status", live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 0 {
		t.Fatalf("moves = %d, want 0", len(moves))
	}
}

// TestPlanMoves_Conflict pins the three-way conflict: the edit moves a node, but
// the live state has already drifted from the base, so the apply is rejected.
func TestPlanMoves_Conflict(t *testing.T) {
	base := docWith("status", map[string]string{"t1.ics": "NEEDS-ACTION"})
	edited := docWith("status", map[string]string{"t1.ics": "COMPLETED"})
	live := []cgp.Node{taskNode(t, "caldav://h/c/t1.ics", "CANCELLED")} // drifted
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"

	if _, err := planMoves(edited, base, "status", live); err == nil {
		t.Fatal("expected a conflict error when live drifted from base")
	}
}
