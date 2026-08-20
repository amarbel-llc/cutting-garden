package organize

import (
	"sort"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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

// categoriesNode builds a live caldav node carrying a MULTI-VALUED `categories`
// membership — the read-only, multi-appearance dimension of the tags design
// (docs/plans/2026-08-20-tags-design.md D7). Values are appended in order, so the
// first argument is firstFacetKey's result. No cats omits the facet entirely.
func categoriesNode(t *testing.T, uri string, cats ...string) cgp.Node {
	facets := map[string][]cgp.FacetValue{}
	for _, c := range cats {
		facets["categories"] = append(facets["categories"], cgp.FacetValue{Key: c})
	}
	return cgp.Node{URI: mustURL(t, uri), Type: "caldav-object-v1", Facets: facets}
}

// TestApply_MultiValuedModeNoneRejectsMove pins the loud read-only rejection
// against a MULTI-VALUED dimension (the tags design's read-only `categories`,
// D7; #231 slice 1): a document move on a Mode-none dimension is refused by
// checkMoveWritable BEFORE any patch is built, exactly as for a single-valued
// read-only dimension. The live node's multi-membership — its first value still
// agreeing with the base bucket — drives planMoves to a real move, which the
// writability gate then rejects.
func TestApply_MultiValuedModeNoneRejectsMove(t *testing.T) {
	base := docWith("categories", map[string]string{"t1.ics": "work"})
	edited := docWith("categories", map[string]string{"t1.ics": "archived"})
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"
	live := []cgp.Node{categoriesNode(t, "caldav://h/c/t1.ics", "work", "urgent")}

	moves, err := planMoves(edited, base, groupSpec{Dim: "categories"}, live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves = %d, want 1 (%+v)", len(moves), moves)
	}

	writes := map[string]cgp.FacetWrite{
		"caldav-object-v1": {DimensionKey: "categories", Mode: cgp.FacetWriteNone},
	}
	err = checkMoveWritable(writes, moves[0])
	if err == nil {
		t.Fatal("a move on a read-only (Mode-none) multi-valued dimension must be rejected")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("expected a bad request, got %v", err)
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("error should name the read-only cause: %v", err)
	}
}

// TestApply_MultiValuedUnmovedRoundTrips pins that an UNMOVED document for a
// multi-valued node yields zero moves and zero conflicts, EVEN WHEN the document
// files the node under a bucket that is NOT its first live facet value
// (#231 slice 1). planMoves keys the live assignment off firstFacetKey (first
// value only), but the unmoved short-circuit (edited bucket == base bucket) fires
// before the live value is consulted — so a node whose live first-membership has
// drifted or reordered is neither spuriously moved nor spuriously conflicted when
// the user made no edit. (Per the tags plan, a genuinely multi-APPEARANCE document
// — the same box id under two `=<value>` buckets, as groupNodes renders — is a
// separate matter: document.assignments already rejects it loudly as "appears
// twice"; see the task report.)
func TestApply_MultiValuedUnmovedRoundTrips(t *testing.T) {
	base := docWith("categories", map[string]string{"t1.ics": "urgent"})
	edited := docWith("categories", map[string]string{"t1.ics": "urgent"})
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"
	// firstFacetKey(live) == "work", which DISAGREES with the document's "urgent"
	// bucket; the unmoved short-circuit must still produce no moves, no conflicts.
	live := []cgp.Node{categoriesNode(t, "caldav://h/c/t1.ics", "work", "urgent")}

	moves, err := planMoves(edited, base, groupSpec{Dim: "categories"}, live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 0 {
		t.Fatalf("moves = %+v, want none (unmoved multi-valued node, no spurious conflict)", moves)
	}
}

// TestPlanMoves_CleanMove pins that a node moved in the edit, with the live state
// still agreeing with the base, yields exactly one move.
func TestPlanMoves_CleanMove(t *testing.T) {
	base := docWith("status", map[string]string{"t1.ics": "NEEDS-ACTION"})
	edited := docWith("status", map[string]string{"t1.ics": "COMPLETED"})
	live := []cgp.Node{taskNode(t, "caldav://h/c/t1.ics", "NEEDS-ACTION")}
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"

	moves, err := planMoves(edited, base, groupSpec{Dim: "status"}, live)
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

	moves, err := planMoves(edited, base, groupSpec{Dim: "status"}, live)
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

	moves, err := planMoves(edited, base, groupSpec{Dim: "status"}, live)
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

	if _, err := planMoves(edited, base, groupSpec{Dim: "status"}, live); err == nil {
		t.Fatal("expected a conflict error when live drifted from base")
	}
}

// dateDocs builds a base/edited pair grouped `date_due:month=` with t1.ics under
// the given month buckets — the apply-side date-granularity fixture (#230).
func dateDocs(baseBucket, editedBucket string) (base, edited document) {
	base = docWith("date_due:month", map[string]string{"t1.ics": baseBucket})
	edited = docWith("date_due:month", map[string]string{"t1.ics": editedBucket})
	base.Anchor, edited.Anchor = "fake://cal/", "fake://cal/"
	return base, edited
}

// TestPlanMoves_DateGranularityUnmoved pins the round-trip invariant
// (cutting-garden#230): a `date_due:month=` document's headings carry the
// granularity, so the live day-precise value coarsens to the month for the
// three-way comparison — an unmoved line is neither a move nor a conflict,
// with NO config consulted on the apply path.
func TestPlanMoves_DateGranularityUnmoved(t *testing.T) {
	base, edited := dateDocs("2026-08", "2026-08")
	live := []cgp.Node{dueNode(t, "t1.ics", "2026-08-15")}

	spec, err := edited.groupedSpec()
	if err != nil {
		t.Fatalf("groupedSpec: %v", err)
	}
	if spec.Dim != "date_due" || spec.Granularity != cgp.GranularityMonth {
		t.Fatalf("recovered spec = %+v, want date_due:month", spec)
	}

	moves, err := planMoves(edited, base, spec, live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 0 {
		t.Fatalf("moves = %+v, want none (live 2026-08-15 coarsens to 2026-08)", moves)
	}
}

// TestPlanMoves_DateGranularityMove pins a coarse bucket move: the line moved
// under `=2026-09` yields exactly one move whose To is the coarse bucket the
// plugin's shape-dispatching write splices from.
func TestPlanMoves_DateGranularityMove(t *testing.T) {
	base, edited := dateDocs("2026-08", "2026-09")
	live := []cgp.Node{dueNode(t, "t1.ics", "2026-08-15")}

	spec, err := edited.groupedSpec()
	if err != nil {
		t.Fatalf("groupedSpec: %v", err)
	}
	moves, err := planMoves(edited, base, spec, live)
	if err != nil {
		t.Fatalf("planMoves: %v", err)
	}
	if len(moves) != 1 || moves[0].From != "2026-08" || moves[0].To != "2026-09" {
		t.Fatalf("moves = %+v, want one 2026-08 -> 2026-09", moves)
	}
}

// TestGroupedSpec_RejectsUnknownGranularity pins the apply-side loud rejection:
// a document heading spelling an unknown granularity is a bad request, not a
// silent exact-match degradation.
func TestGroupedSpec_RejectsUnknownGranularity(t *testing.T) {
	doc := docWith("date_due:week", map[string]string{"t1.ics": "2026-08"})
	if _, err := doc.groupedSpec(); err == nil {
		t.Fatal("a date_due:week= heading must reject loudly")
	}
}
