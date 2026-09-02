package organize

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// tagAtomWholeDoc builds a `(tags)`-grouped document shaped like generate's
// output under `_tag-strip = placement`: t1 carries {work, errand} so it
// appears under BOTH buckets, each box showing only the OTHER (non-Via) tag;
// t2 is untagged and ungrouped.
func tagAtomWholeDoc() document {
	return document{
		GroupBy:   "(tags)",
		Ungrouped: []objectLine{{ID: "t2.ics"}},
		Sections: []section{
			{Depth: 1, Term: "errand", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"work"}}}},
			{Depth: 1, Term: "work", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"errand"}}}},
		},
	}
}

var tagWholeSpec = groupSpec{Dim: "categories", Kind: groupKindTagWhole}

// TestPlanTagAtomDeltas_UnchangedYieldsNothing pins the pass-through half of
// the G7 planner: an unchanged document, a pure membership removal (the boxes
// gone with their lines), and a field-bucket move carrying a reordered-but-
// equal tag set all produce zero deltas and no conflict.
func TestPlanTagAtomDeltas_UnchangedYieldsNothing(t *testing.T) {
	base := tagAtomWholeDoc()

	deltas, err := planTagAtomDeltas(tagAtomWholeDoc(), base, tagWholeSpec, false, nil)
	if err != nil || len(deltas) != 0 {
		t.Errorf("unchanged document: deltas=%v err=%v, want none", deltas, err)
	}

	// The headings-reset shape: t1 removed from BOTH buckets to ungrouped, its
	// boxes (per-bucket Via-stripped) gone with the placements — a membership
	// removal for the bucket diff, never a tag-atom delta.
	edited := document{
		GroupBy: "(tags)",
		Ungrouped: []objectLine{
			{ID: "t1.ics"},
			{ID: "t2.ics"},
		},
		Sections: []section{
			{Depth: 1, Term: "errand"},
			{Depth: 1, Term: "work"},
		},
	}
	deltas, err = planTagAtomDeltas(edited, base, tagWholeSpec, false, nil)
	if err != nil || len(deltas) != 0 {
		t.Errorf("membership removal: deltas=%v err=%v, want none", deltas, err)
	}

	// A FIELD-grouped move carrying the full rendered tag set along: nothing is
	// placement-derived, but the set equals the base's — unchanged.
	fieldBase := document{
		Sections: []section{
			{Depth: 1, Term: "status="},
			{Depth: 2, Term: "=A", Lines: []objectLine{{ID: "f.ics", Tags: []string{"errand", "work"}}}},
			{Depth: 2, Term: "=B"},
		},
	}
	fieldEdited := document{
		Sections: []section{
			{Depth: 1, Term: "status="},
			{Depth: 2, Term: "=A"},
			{Depth: 2, Term: "=B", Lines: []objectLine{{ID: "f.ics", Tags: []string{"work", "errand"}}}},
		},
	}
	deltas, err = planTagAtomDeltas(fieldEdited, fieldBase, groupSpec{Dim: "status"}, false, nil)
	if err != nil || len(deltas) != 0 {
		t.Errorf("field-bucket move with reordered tags: deltas=%v err=%v, want none", deltas, err)
	}
}

// TestPlanTagAtomDeltas_AddAndRemove pins the delta half (G7): a tag typed
// into a box is an add, a tag deleted from a box is a remove — both EXACT tag
// values, quoted or not.
func TestPlanTagAtomDeltas_AddAndRemove(t *testing.T) {
	// Add: `work-x` and the quoted `_ inbox` typed into t2's ungrouped box.
	added := tagAtomWholeDoc()
	added.Ungrouped[0].Tags = []string{"work-x", "_ inbox"}
	deltas, err := planTagAtomDeltas(added, tagAtomWholeDoc(), tagWholeSpec, false, nil)
	if err != nil {
		t.Fatalf("added tags: %v", err)
	}
	if d := deltas["t2.ics"]; !reflect.DeepEqual(d.adds, []string{"work-x", "_ inbox"}) || len(d.removes) != 0 {
		t.Errorf("added-tags delta = %+v, want adds [work-x, _ inbox]", d)
	}
	if _, dirty := deltas["t1.ics"]; dirty {
		t.Errorf("unedited t1.ics must carry no delta: %+v", deltas)
	}

	// Remove: a non-placement tag deleted from a field-grouped box.
	fieldBase := document{
		Sections: []section{
			{Depth: 1, Term: "status="},
			{Depth: 2, Term: "=A", Lines: []objectLine{{ID: "f.ics", Tags: []string{"errand", "work"}}}},
		},
	}
	fieldEdited := document{
		Sections: []section{
			{Depth: 1, Term: "status="},
			{Depth: 2, Term: "=A", Lines: []objectLine{{ID: "f.ics", Tags: []string{"errand"}}}},
		},
	}
	deltas, err = planTagAtomDeltas(fieldEdited, fieldBase, groupSpec{Dim: "status"}, false, nil)
	if err != nil {
		t.Fatalf("removed tag: %v", err)
	}
	if d := deltas["f.ics"]; !reflect.DeepEqual(d.removes, []string{"work"}) || len(d.adds) != 0 {
		t.Errorf("removed-tag delta = %+v, want removes [work]", d)
	}
}

// TestPlanTagAtomDeltas_CrossAppearanceDisagreement pins the N-way conflict
// (G7): a non-placement tag added to ONE of an object's boxes but not its
// siblings' refuses with exit-2 trouble naming both appearances.
func TestPlanTagAtomDeltas_CrossAppearanceDisagreement(t *testing.T) {
	edited := tagAtomWholeDoc()
	edited.Sections[0].Lines[0].Tags = []string{"work", "foo"} // errand box only
	_, err := planTagAtomDeltas(edited, tagAtomWholeDoc(), tagWholeSpec, false, nil)
	if err == nil {
		t.Fatal("one-box tag add on a multi-appearance object must conflict")
	}
	if errors.Is400BadRequest(err) {
		t.Errorf("a tag conflict is trouble (exit 2), not a bad request: %v", err)
	}
	for _, want := range []string{
		"tag conflict(s)",
		"object t1.ics: appearances disagree on tag foo: present under errand, absent under work",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("conflict %q should mention %q", err, want)
		}
	}
}

// TestPlanTagAtomDeltas_PlacementVsBox pins G7 rule 1 under `_tag-strip =
// placement`: a tag REMOVED from a matched box while the object still sits
// under that tag's bucket is "placement says X, box says not-X".
func TestPlanTagAtomDeltas_PlacementVsBox(t *testing.T) {
	edited := tagAtomWholeDoc()
	edited.Sections[0].Lines[0].Tags = nil // drop `work` from t1's errand box
	_, err := planTagAtomDeltas(edited, tagAtomWholeDoc(), tagWholeSpec, false, nil)
	if err == nil {
		t.Fatal("removing a still-placed tag from a box must conflict")
	}
	want := "object t1.ics: placement says work (still under work), box says not-work (removed under errand)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("conflict %q should mention %q", err, want)
	}

	// The SAME removal is clean once the placement goes too: t1 also leaves
	// the `work` bucket, so the bucket diff owns the removal and no atom delta
	// or conflict remains.
	consistent := document{
		GroupBy: "(tags)",
		Ungrouped: []objectLine{
			{ID: "t2.ics"},
		},
		Sections: []section{
			{Depth: 1, Term: "errand", Lines: []objectLine{{ID: "t1.ics"}}},
			{Depth: 1, Term: "work"},
		},
	}
	deltas, err := planTagAtomDeltas(consistent, tagAtomWholeDoc(), tagWholeSpec, false, nil)
	if err != nil || len(deltas) != 0 {
		t.Errorf("consistent removal: deltas=%v err=%v, want none", deltas, err)
	}
}

// TestPlanTagAtomDeltas_NamespaceMoveKeepsSiblings pins the planner under a
// NAMESPACE grouping (G10a): a line moved from a continuation bucket to the
// root — its box keeping the out-of-namespace sibling tag the strip left
// behind — yields no delta (the placement change is the bucket diff's), while
// DROPPING the sibling is a real membership remove.
func TestPlanTagAtomDeltas_NamespaceMoveKeepsSiblings(t *testing.T) {
	interp, ok := cgp.LookupTagInterpreter("dodder-hyphen")
	if !ok {
		t.Fatal("dodder-hyphen interpreter not registered")
	}
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}
	base := document{
		GroupBy:   "project",
		Ungrouped: []objectLine{{ID: "d.ics", Tags: []string{"other"}}},
		Sections: []section{
			{Depth: 1, Term: "project"},
			{Depth: 2, Term: "-client", Lines: []objectLine{{ID: "a.ics", Tags: []string{"urgent"}}}},
		},
	}
	edited := document{
		GroupBy: "project",
		Sections: []section{
			{Depth: 1, Term: "project", Lines: []objectLine{
				{ID: "a.ics", Tags: []string{"urgent"}},
				{ID: "d.ics", Tags: []string{"other"}},
			}},
			{Depth: 2, Term: "-client"},
		},
	}
	deltas, err := planTagAtomDeltas(edited, base, spec, false, interp)
	if err != nil || len(deltas) != 0 {
		t.Errorf("namespace move keeping siblings: deltas=%v err=%v, want none", deltas, err)
	}

	// The same move with the sibling tag dropped IS a membership remove now
	// (the pre-T3 gate refused it).
	edited.Sections[0].Lines[1].Tags = nil
	deltas, err = planTagAtomDeltas(edited, base, spec, false, interp)
	if err != nil {
		t.Fatalf("dropped sibling: %v", err)
	}
	if d := deltas["d.ics"]; !reflect.DeepEqual(d.removes, []string{"other"}) || len(d.adds) != 0 {
		t.Errorf("dropped-sibling delta = %+v, want removes [other]", d)
	}
}

// TestPlanTagAtomDeltas_StripNoneMoveIsNotAnEdit pins the `_tag-strip = none`
// reading (G2/G7, the pre-T3 gate's known false reject): membership = the
// box's tag set ∪ the current placement's bucket tag, so a MOVED line whose
// box still carries the old tag keeps it — the only delta is the new
// placement's add — and deleting a still-placed tag from the boxes is a no-op
// (the placement re-derives it).
func TestPlanTagAtomDeltas_StripNoneMoveIsNotAnEdit(t *testing.T) {
	base := document{
		GroupBy: "(tags)",
		Sections: []section{
			{Depth: 1, Term: "urgent", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent", "work"}}}},
			{Depth: 1, Term: "work", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent", "work"}}}},
		},
	}
	// The `work` line moved to `# done`, box untouched.
	moved := document{
		GroupBy: "(tags)",
		Sections: []section{
			{Depth: 1, Term: "done", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent", "work"}}}},
			{Depth: 1, Term: "urgent", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent", "work"}}}},
		},
	}
	deltas, err := planTagAtomDeltas(moved, base, tagWholeSpec, true, nil)
	if err != nil {
		t.Fatalf("strip=none move: %v", err)
	}
	if d := deltas["t1.ics"]; !reflect.DeepEqual(d.adds, []string{"done"}) || len(d.removes) != 0 {
		t.Errorf("strip=none move delta = %+v, want adds [done] only (old tag stays)", d)
	}

	// Deleting `work` from every box while the line still sits under `# work`:
	// the placement re-derives it — no delta, no conflict.
	trimmed := document{
		GroupBy: "(tags)",
		Sections: []section{
			{Depth: 1, Term: "urgent", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent"}}}},
			{Depth: 1, Term: "work", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent"}}}},
		},
	}
	deltas, err = planTagAtomDeltas(trimmed, base, tagWholeSpec, true, nil)
	if err != nil || len(deltas) != 0 {
		t.Errorf("strip=none still-placed removal: deltas=%v err=%v, want none", deltas, err)
	}

	// Removing `work` from ONE box only under `none` disagrees across the
	// appearances (the boxes are authoritative) — a conflict, not a guess.
	oneBox := document{
		GroupBy: "(tags)",
		Sections: []section{
			{Depth: 1, Term: "done", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent"}}}},
			{Depth: 1, Term: "urgent", Lines: []objectLine{{ID: "t1.ics", Tags: []string{"urgent", "work"}}}},
		},
	}
	if _, err := planTagAtomDeltas(oneBox, base, tagWholeSpec, true, nil); err == nil ||
		!strings.Contains(err.Error(), "appearances disagree on tag work") {
		t.Errorf("strip=none one-box removal must conflict naming the tag, got %v", err)
	}
}

// TestPlanTagAtomDeltas_MigratedToPlacementIsNotARemove pins the box→placement
// migration: an ungrouped line whose box carried `work` moved UNDER `# work`
// with the (now redundant) atom cleaned from the box is not an atom remove —
// the bucket diff adds the membership; the atom planner stays silent.
func TestPlanTagAtomDeltas_MigratedToPlacementIsNotARemove(t *testing.T) {
	base := document{
		GroupBy:   "(tags)",
		Ungrouped: []objectLine{{ID: "t1.ics", Tags: []string{"work"}}},
		Sections:  []section{{Depth: 1, Term: "work"}},
	}
	edited := document{
		GroupBy:  "(tags)",
		Sections: []section{{Depth: 1, Term: "work", Lines: []objectLine{{ID: "t1.ics"}}}},
	}
	deltas, err := planTagAtomDeltas(edited, base, tagWholeSpec, false, nil)
	if err != nil || len(deltas) != 0 {
		t.Errorf("box→placement migration: deltas=%v err=%v, want none", deltas, err)
	}
}

// TestPlanAtomMembershipEdits_FoldsExact pins the single-valued branch's
// planner: each object's delta folds onto its live tags exactly (removes then
// adds), a live-equal result is skipped, and an id absent from live is out of
// scope.
func TestPlanAtomMembershipEdits_FoldsExact(t *testing.T) {
	interp, _ := cgp.LookupTagInterpreter("naive")
	live := []cgp.Node{
		categoriesNode(t, "caldav://h/c/t1.ics", "work"),
		categoriesNode(t, "caldav://h/c/t2.ics", "urgent"),
	}
	deltas := map[string]tagDelta{
		"t1.ics":   {adds: []string{"urgent"}, removes: []string{"work"}},
		"t2.ics":   {adds: []string{"urgent"}}, // live already has it — no write
		"gone.ics": {adds: []string{"x"}},      // not live — skipped
	}
	edits, err := planAtomMembershipEdits(deltas, live, "caldav://h/c/", interp, "categories")
	if err != nil {
		t.Fatalf("planAtomMembershipEdits: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edits = %+v, want exactly one (t1)", edits)
	}
	if edits[0].URI != "caldav://h/c/t1.ics" || !setEqual(edits[0].NewTags, []string{"urgent"}) {
		t.Errorf("edit = %+v, want t1 → {urgent}", edits[0])
	}
}

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
	return docWithHeading(dim+"=", assignment)
}

// docWithHeading is docWith with the dimension heading TERM given verbatim
// (`date_due=(month)`, or a retired spelling under test) rather than derived
// from a bare field name.
func docWithHeading(heading string, assignment map[string]string) document {
	doc := document{Sections: []section{{Depth: 1, Term: heading}}}
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

// docWithMulti is the multi-membership sibling of docWith: an object may be filed
// under SEVERAL `## =<value>` buckets (its line rendered once per bucket), the way
// groupNodes renders a multi-valued dimension. An empty value slice places the
// object ungrouped above the dimension heading.
func docWithMulti(dim string, assignment map[string][]string) document {
	doc := document{Sections: []section{{Depth: 1, Term: dim + "="}}}
	byValue := map[string][]objectLine{}
	for id, vs := range assignment {
		if len(vs) == 0 {
			doc.Ungrouped = append(doc.Ungrouped, objectLine{ID: id})
			continue
		}
		for _, v := range vs {
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

// recordedPatch is one PatchNode call membershipFake observed: the node URI and
// the tag set the membership patch carried (decoded from the fake applier's body).
type recordedPatch struct {
	uri     string
	newTags []string
}

// membershipFake is a RootLister that implements just enough of the write surface
// to drive the multi-valued apply path: NodeMutator + FacetWriteDescriber +
// MembershipWriteApplier. Its BuildMembershipWritePatch marshals the resolved tag
// set as the patch body and PatchNode decodes it, so a test can assert exactly
// which set reached the mutator. It declares NO ListingFields/FieldPresenter, so
// the field-edit path stays inert here.
type membershipFake struct {
	writes  []cgp.NodeTypeFacetWrites // when nil, the default categories=many mapping
	patches []recordedPatch
}

func (*membershipFake) Schemes() []string { return []string{"caldav"} }
func (*membershipFake) TypeTag() string   { return "" }

func (*membershipFake) Types() []cgp.NodeType { return nil }

func (*membershipFake) ListRoots(context.Context, *url.URL) ([]cgp.Node, error) {
	return nil, nil
}

func (f *membershipFake) DescribeFacetWrites() []cgp.NodeTypeFacetWrites {
	if f.writes != nil {
		return f.writes
	}
	return []cgp.NodeTypeFacetWrites{{
		Tag: "caldav-object-v1",
		Writes: []cgp.FacetWrite{
			{DimensionKey: "categories", Mode: cgp.FacetWriteMany, Field: "categories"},
		},
	}}
}

func (*membershipFake) BuildMembershipWritePatch(
	_ context.Context, _ cgp.Node, _ cgp.FacetWrite, newTags []string,
) ([]byte, error) {
	return json.Marshal(newTags)
}

func (f *membershipFake) PatchNode(
	_ context.Context, uri *url.URL, body io.Reader,
) ([]string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	var tags []string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil, err
	}
	f.patches = append(f.patches, recordedPatch{uri: uri.String(), newTags: tags})
	return []string{"categories"}, nil
}

func (*membershipFake) CreateNode(context.Context, *url.URL, io.Reader, string) error { return nil }
func (*membershipFake) PutNode(context.Context, *url.URL, io.Reader) error            { return nil }
func (*membershipFake) DeleteNode(context.Context, *url.URL) error                    { return nil }

// TestApplyMemberships_AddApplies pins the end-to-end membership write (#231 slice
// 2): a document that files a one-tag task under a SECOND `## =<tag>` bucket (base
// {work}, edited {work, urgent}) commits a single full-set PatchNode carrying the
// complete {work, urgent} set — the interpreter-resolved membership, built via
// BuildMembershipWritePatch, not a single-bucket move.
func TestApplyMemberships_AddApplies(t *testing.T) {
	base := docWithMulti("categories", map[string][]string{"t1.ics": {"work"}})
	edited := docWithMulti("categories", map[string][]string{"t1.ics": {"work", "urgent"}})
	base.Anchor, edited.Anchor = "caldav://h/c/", "caldav://h/c/"
	live := []cgp.Node{categoriesNode(t, "caldav://h/c/t1.ics", "work")}

	fake := &membershipFake{}
	cmd := newWithOutput(io.Discard)
	wrote, err := cmd.applyMemberships(
		context.Background(), edited, base, live, fake, "categories", "", "",
		nil, true, true, false, false,
	)
	if err != nil {
		t.Fatalf("applyMemberships: %v", err)
	}
	if !wrote {
		t.Fatal("a committed membership add must report it wrote")
	}
	if len(fake.patches) != 1 {
		t.Fatalf("PatchNode calls = %d, want 1 (%+v)", len(fake.patches), fake.patches)
	}
	p := fake.patches[0]
	if p.uri != "caldav://h/c/t1.ics" {
		t.Errorf("patched uri = %q, want caldav://h/c/t1.ics", p.uri)
	}
	if got := stringSet(p.newTags); len(got) != 2 || !hasAll(got, "work", "urgent") {
		t.Errorf("patched categories = %v, want set {work, urgent}", p.newTags)
	}
}

// hasAll reports whether set contains every wanted key.
func hasAll(set map[string]struct{}, want ...string) bool {
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// TestDedupFieldEditsByURI_NoDoubleWrite pins the multi-appearance guard (#231
// slice 2): a multi-membership object's line is parsed once per bucket it sits
// under, so planFieldEdits can return the same URI's field edit twice; dedup keeps
// one. Because executeFieldEdits patches 1:1 per edit, one deduped edit means at
// most one field-edit PatchNode for that object.
func TestDedupFieldEditsByURI_NoDoubleWrite(t *testing.T) {
	uri := "caldav://h/c/t1.ics"
	node := taskNode(t, uri, "NEEDS-ACTION")
	edits := []objectFieldEdit{
		{URI: uri, Node: node, Edits: []cgp.FieldEdit{{Name: "summary", Value: "A"}}},
		{URI: uri, Node: node, Edits: []cgp.FieldEdit{{Name: "summary", Value: "B"}}}, // divergent second appearance
		{URI: "caldav://h/c/t2.ics", Node: taskNode(t, "caldav://h/c/t2.ics", ""), Edits: nil},
	}
	got := dedupFieldEditsByURI(edits)
	if len(got) != 2 {
		t.Fatalf("deduped edits = %d, want 2 (one per URI)", len(got))
	}
	byURI := map[string]int{}
	for _, e := range got {
		byURI[e.URI]++
	}
	if byURI[uri] != 1 {
		t.Errorf("URI %s appears %d times after dedup, want exactly 1 (no double write)", uri, byURI[uri])
	}
	// The first appearance is kept, so a deterministic single apply survives.
	if got[0].URI == uri && got[0].Edits[0].Value != "A" {
		t.Errorf("dedup kept the wrong appearance: %+v", got[0].Edits)
	}
}

// TestPlanFieldEdits_DedupKeepsDocumentFirst pins the ordering guarantee through
// the REAL pipeline (#231 slice 2): a multi-membership object whose two document
// appearances carry DIVERGENT edits for the same writable field flows through
// planFieldEdits (which stable-sorts by URI) then dedupFieldEditsByURI (which keeps
// the first per URI), and the surviving edit is the DOCUMENT-FIRST appearance — not
// whatever an unstable sort would surface. The errand bucket precedes the work
// bucket in document order, so summary=A (errand) must win over summary=B (work).
func TestPlanFieldEdits_DedupKeepsDocumentFirst(t *testing.T) {
	anchor := "caldav://h/c/"
	edited := document{
		Anchor: anchor,
		Sections: []section{
			{Depth: 1, Term: "categories="},
			{Depth: 2, Term: "=errand", Lines: []objectLine{
				{ID: "t1.ics", Fields: []cgp.BoxAtom{{Name: "summary", Value: "A"}}},
			}},
			{Depth: 2, Term: "=work", Lines: []objectLine{
				{ID: "t1.ics", Fields: []cgp.BoxAtom{{Name: "summary", Value: "B"}}},
			}},
		},
	}
	base := document{
		Anchor: anchor,
		Sections: []section{
			{Depth: 1, Term: "categories="},
			{Depth: 2, Term: "=work", Lines: []objectLine{
				{ID: "t1.ics", Fields: []cgp.BoxAtom{{Name: "summary", Value: "orig"}}},
			}},
		},
	}
	live := []cgp.Node{{
		URI:    mustURL(t, "caldav://h/c/t1.ics"),
		Type:   "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"categories": {{Key: "work"}}},
	}}
	writable := map[string]map[string]bool{"caldav-object-v1": {"summary": true}}
	present := func(cgp.Node) []cgp.BoxAtom {
		return []cgp.BoxAtom{{Name: "summary", Value: "orig", Field: "summary"}}
	}

	edits, _, err := planFieldEdits(edited, base, live, anchor, writable, nil, present)
	if err != nil {
		t.Fatalf("planFieldEdits: %v", err)
	}
	if len(edits) != 2 {
		t.Fatalf("planFieldEdits edits = %d, want 2 (one per document appearance)", len(edits))
	}
	deduped := dedupFieldEditsByURI(edits)
	if len(deduped) != 1 {
		t.Fatalf("deduped = %d, want 1", len(deduped))
	}
	if got := deduped[0].Edits; len(got) != 1 || got[0].Value != "A" {
		t.Errorf("dedup kept %+v, want the document-first appearance summary=A", got)
	}
}

// TestGroupedIsMultiValued_Dispatch pins the write-mode dispatch (#231 slice 2):
// a write:many dimension routes to the membership path, a write:one dimension to
// the existing facet path, and a dimension declared with MIXED cardinality across
// present types is rejected loudly.
func TestGroupedIsMultiValued_Dispatch(t *testing.T) {
	live := []cgp.Node{categoriesNode(t, "caldav://h/c/t1.ics", "work")}

	manyFake := &membershipFake{writes: []cgp.NodeTypeFacetWrites{{
		Tag: "caldav-object-v1",
		Writes: []cgp.FacetWrite{
			{DimensionKey: "categories", Mode: cgp.FacetWriteMany, Field: "categories"},
			{DimensionKey: "status", Mode: cgp.FacetWriteOne, Field: "status"},
		},
	}}}

	if multi, err := groupedIsMultiValued(manyFake, "categories", live); err != nil || !multi {
		t.Errorf("categories should dispatch multi-valued: multi=%v err=%v", multi, err)
	}
	if multi, err := groupedIsMultiValued(manyFake, "status", live); err != nil || multi {
		t.Errorf("status should dispatch single-valued: multi=%v err=%v", multi, err)
	}

	mixedFake := &membershipFake{writes: []cgp.NodeTypeFacetWrites{
		{Tag: "a-v1", Writes: []cgp.FacetWrite{{DimensionKey: "categories", Mode: cgp.FacetWriteMany, Field: "c"}}},
		{Tag: "b-v1", Writes: []cgp.FacetWrite{{DimensionKey: "categories", Mode: cgp.FacetWriteOne, Field: "c"}}},
	}}
	mixedLive := []cgp.Node{
		{URI: mustURL(t, "caldav://h/c/a.ics"), Type: "a-v1"},
		{URI: mustURL(t, "caldav://h/c/b.ics"), Type: "b-v1"},
	}
	if _, err := groupedIsMultiValued(mixedFake, "categories", mixedLive); err == nil {
		t.Error("mixed single/multi-valued grouping must be rejected loudly")
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

// dateDocs builds a base/edited pair grouped `date_due=(month)` with t1.ics under
// the given month buckets — the apply-side date-granularity fixture (#230).
func dateDocs(baseBucket, editedBucket string) (base, edited document) {
	base = docWithHeading("date_due=(month)", map[string]string{"t1.ics": baseBucket})
	edited = docWithHeading("date_due=(month)", map[string]string{"t1.ics": editedBucket})
	base.Anchor, edited.Anchor = "fake://cal/", "fake://cal/"
	return base, edited
}

// TestPlanMoves_DateGranularityUnmoved pins the round-trip invariant
// (cutting-garden#230): a `date_due=(month)` document's headings carry the
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
		t.Fatalf("recovered spec = %+v, want date_due=(month)", spec)
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
	doc := docWithHeading("date_due=(week)", map[string]string{"t1.ics": "2026-08"})
	if _, err := doc.groupedSpec(); err == nil {
		t.Fatal("a date_due=(week) heading must reject loudly")
	}
	// The retired `dim:granularity=` heading is rejected with the new spelling.
	legacy := docWithHeading("date_due:month=", map[string]string{"t1.ics": "2026-08"})
	_, err := legacy.groupedSpec()
	if err == nil || !strings.Contains(err.Error(), "date_due=(month)") {
		t.Fatalf("a date_due:month= heading must reject naming date_due=(month), got %v", err)
	}
}
