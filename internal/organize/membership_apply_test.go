package organize

import (
	"sort"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

const (
	membershipDim    = "categories"
	membershipAnchor = "caldav://h/c/"
)

func mustInterp(t *testing.T) cgp.TagInterpreter {
	t.Helper()
	interp, ok := cgp.LookupTagInterpreter("naive")
	if !ok {
		t.Fatalf("naive interpreter must be registered")
	}
	return interp
}

// categoriesDoc builds a document that files each id under the buckets it maps to,
// under one `categories=` dimension heading. An id mapped to no buckets is placed
// ungrouped (present but no membership) — the legal remove-all shape.
func categoriesDoc(t *testing.T, membership map[string][]string) document {
	t.Helper()
	doc := document{
		Anchor:   membershipAnchor,
		Sections: []section{{Depth: 1, Term: membershipDim + "="}},
	}
	// A stable bucket order keeps the fixture deterministic.
	byBucket := map[string][]string{}
	var buckets []string
	for id, vals := range membership {
		if len(vals) == 0 {
			doc.Ungrouped = append(doc.Ungrouped, objectLine{ID: id})
			continue
		}
		for _, v := range vals {
			if _, seen := byBucket[v]; !seen {
				buckets = append(buckets, v)
			}
			byBucket[v] = append(byBucket[v], id)
		}
	}
	sort.Strings(buckets)
	sort.Slice(doc.Ungrouped, func(i, j int) bool { return doc.Ungrouped[i].ID < doc.Ungrouped[j].ID })
	for _, b := range buckets {
		ids := byBucket[b]
		sort.Strings(ids)
		lines := make([]objectLine, 0, len(ids))
		for _, id := range ids {
			lines = append(lines, objectLine{ID: id})
		}
		doc.Sections = append(doc.Sections, section{Depth: 2, Term: "=" + b, Lines: lines})
	}
	return doc
}

// liveNode builds a live categories-tagged node addressed relative to
// membershipAnchor, reusing apply_test.go's categoriesNode (full-URI form).
func liveNode(t *testing.T, id string, tags ...string) cgp.Node {
	t.Helper()
	return categoriesNode(t, membershipAnchor+id, tags...)
}

// TestPlanMemberships_AddOnly pins a single tag addition: the edited doc files the
// object under a new bucket, so the interpreter appends it to the live set.
func TestPlanMemberships_AddOnly(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"work"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"work", "urgent"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "work")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"work", "urgent"}) {
		t.Errorf("NewTags = %v, want set {work, urgent}", edits[0].NewTags)
	}
	if edits[0].URI != "caldav://h/c/t1.ics" {
		t.Errorf("URI = %q", edits[0].URI)
	}
}

// TestPlanMemberships_RemoveWithSurvivor pins dropping one tag while another
// survives: base carries two buckets, edited drops one.
func TestPlanMemberships_RemoveWithSurvivor(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"work", "urgent"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"work"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "work", "urgent")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"work"}) {
		t.Errorf("NewTags = %v, want set {work}", edits[0].NewTags)
	}
}

// TestPlanMemberships_NoOp pins that an unchanged document (base == edited) yields
// no edits.
func TestPlanMemberships_NoOp(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"work", "urgent"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"work", "urgent"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "work", "urgent")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 0 {
		t.Fatalf("edit count = %d, want 0 (%+v)", len(edits), edits)
	}
}

// TestPlanMemberships_IdempotentAgainstLive pins that when the live tags ALREADY
// match the intended set — a concurrent edit applied the same change — the fold
// produces a set-equal result and nothing is written.
func TestPlanMemberships_IdempotentAgainstLive(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"work"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"work", "urgent"}})
	// Live already carries both tags (someone else added urgent concurrently).
	live := []cgp.Node{liveNode(t, "t1.ics", "work", "urgent")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 0 {
		t.Fatalf("edit count = %d, want 0 — live already matches (%+v)", len(edits), edits)
	}
}

// TestPlanMemberships_LastLineVanish pins that an id present in base and live but
// absent from the edited document entirely is a loud rejection — deleting the last
// line is out of scope (cutting-garden#215).
func TestPlanMemberships_LastLineVanish(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"work"}})
	// Edited document has NO line for t1.ics at all — just the empty heading.
	edited := document{
		Anchor:   membershipAnchor,
		Sections: []section{{Depth: 1, Term: membershipDim + "="}},
	}
	live := []cgp.Node{liveNode(t, "t1.ics", "work")}

	_, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err == nil {
		t.Fatal("deleting an object's last line must reject")
	}
}

// TestPlanMemberships_LastLineVanishBatched pins that MULTIPLE deleted objects are
// accumulated into ONE error naming all of them — a user sees every deletion in a
// single apply attempt, mirroring planMoves/planFieldEdits' batched conflicts.
func TestPlanMemberships_LastLineVanishBatched(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"id1.ics": {"work"}, "id2.ics": {"urgent"}})
	// Edited document has NO line for either id — just the empty heading.
	edited := document{
		Anchor:   membershipAnchor,
		Sections: []section{{Depth: 1, Term: membershipDim + "="}},
	}
	live := []cgp.Node{liveNode(t, "id1.ics", "work"), liveNode(t, "id2.ics", "urgent")}

	_, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err == nil {
		t.Fatal("two deleted last lines must reject")
	}
	if !strings.Contains(err.Error(), "id1.ics") || !strings.Contains(err.Error(), "id2.ics") {
		t.Errorf("batched error must name both ids, got: %v", err)
	}
}

// TestPlanMemberships_RemoveAllLegal pins that moving an object OUT of every bucket
// (present ungrouped, empty membership) is a legal remove-all: one edit with an
// empty NewTags, NOT the last-line-vanish rejection.
func TestPlanMemberships_RemoveAllLegal(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"work"}})
	// t1.ics is present ungrouped (empty membership) — moved out of every bucket.
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {}})
	live := []cgp.Node{liveNode(t, "t1.ics", "work")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim)
	if err != nil {
		t.Fatalf("remove-all must be legal: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if len(edits[0].NewTags) != 0 {
		t.Errorf("NewTags = %v, want empty (all tags removed)", edits[0].NewTags)
	}
}
