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

// mustDodderHyphen resolves the dodder-hyphen interpreter — the realistic
// namespace path (naive rejects namespaces; Complete is exact under either).
func mustDodderHyphen(t *testing.T) cgp.TagInterpreter {
	t.Helper()
	interp, ok := cgp.LookupTagInterpreter("dodder-hyphen")
	if !ok {
		t.Fatalf("dodder-hyphen interpreter must be registered")
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

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
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

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
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

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
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

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
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
	// Edited document has NO line for t1.ics at all — just the empty bucket.
	edited := document{
		Anchor:   membershipAnchor,
		Sections: []section{{Depth: 1, Term: membershipDim + "="}},
	}
	live := []cgp.Node{liveNode(t, "t1.ics", "work")}

	_, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
	if err == nil {
		t.Fatal("deleting an object's last line must reject")
	}
}

// TestPlanMemberships_LastLineVanishBatched pins that MULTIPLE deleted objects are
// accumulated into ONE error naming all of them — a user sees every deletion in a
// single apply attempt, mirroring planMoves/planFieldEdits' batched conflicts.
func TestPlanMemberships_LastLineVanishBatched(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"id1.ics": {"work"}, "id2.ics": {"urgent"}})
	// Edited document has NO line for either id — just the empty bucket.
	edited := document{
		Anchor:   membershipAnchor,
		Sections: []section{{Depth: 1, Term: membershipDim + "="}},
	}
	live := []cgp.Node{liveNode(t, "id1.ics", "work"), liveNode(t, "id2.ics", "urgent")}

	_, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
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

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustInterp(t), membershipDim, "")
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

// TestPlanMemberships_NamespaceAdd pins the namespace-rollup ADD reconstruction
// (RFC 0019 §6.2, #231 slice 3 B4): filing an object under rollup bucket `-client`
// reconstructs the namespace tag `project-client` (the unambiguous leaf) against
// namespace `project` and appends it exactly.
func TestPlanMemberships_NamespaceAdd(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"-client"}})
	live := []cgp.Node{liveNode(t, "t1.ics")} // no categories tags yet

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"project-client"}) {
		t.Errorf("NewTags = %v, want set {project-client}", edits[0].NewTags)
	}
}

// TestPlanMemberships_NamespaceRemoveSubtree pins the namespace-rollup REMOVE
// enumeration (RFC 0019 §6.2, #231 slice 3 B4): leaving rollup bucket `-client`
// removes EVERY live tag realizing the `project-client` subtree
// (project-client-acme, project-client-baxter) via exact removal, while an
// unrelated tag (`other`) survives — the apply layer owns the subtree walk, not
// the interpreter's exact Complete.
func TestPlanMemberships_NamespaceRemoveSubtree(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"-client"}})
	// t1.ics present ungrouped in edited — a legal remove-all of the -client bucket.
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {}})
	live := []cgp.Node{liveNode(t, "t1.ics", "project-client-acme", "project-client-baxter", "other")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"other"}) {
		t.Errorf("NewTags = %v, want set {other} (whole project-client subtree removed)", edits[0].NewTags)
	}
}

// TestPlanMemberships_RootBucketAdd pins the G10a root bucket's ADD semantics
// (native tags slice 1.5 A): filing an object DIRECTLY under the `# project`
// root heading (bucket value == the namespace) reconstructs exactly the BARE
// namespace tag `project` — never `project-project` — and leaves an unrelated
// out-of-namespace tag (`other`) untouched.
func TestPlanMemberships_RootBucketAdd(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"project"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "other")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"other", "project"}) {
		t.Errorf("NewTags = %v, want set {other, project}", edits[0].NewTags)
	}
}

// TestPlanMemberships_RootBucketRemoveBareOnly pins the G10a root bucket's
// REMOVE semantics: leaving the root bucket removes the bare tag `project` ONLY
// — never the `project-*` subtree, whose tags realize the CONTINUATION buckets
// (their removal is governed by their own bucket rows) — and an unrelated tag
// survives.
func TestPlanMemberships_RootBucketRemoveBareOnly(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"project", "-client"}})
	// t1.ics stays under -client but leaves the root bucket.
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"-client"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "project", "project-client-acme", "other")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"project-client-acme", "other"}) {
		t.Errorf("NewTags = %v, want set {project-client-acme, other} (bare project removed, subtree kept)",
			edits[0].NewTags)
	}
}

// TestPlanMemberships_RootBucketRemoveConcurrentNoOp pins the root-REMOVE
// idempotence against live drift: when the live set ALREADY lacks the bare
// namespace tag (a concurrent edit removed it), the root removal folds to a
// no-op — the result is set-equal to live, so nothing is written.
func TestPlanMemberships_RootBucketRemoveConcurrentNoOp(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"project"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {}})
	// Live already lost the bare tag; only the unrelated tag remains.
	live := []cgp.Node{liveNode(t, "t1.ics", "other")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 0 {
		t.Fatalf("edit count = %d, want 0 — live already matches (%+v)", len(edits), edits)
	}
}

// TestPlanMemberships_RootToContinuationMove pins that the root and continuation
// rules COMPOSE: moving an object from the root bucket to a continuation removes
// the bare `project` and adds the reconstructed `project-client`.
func TestPlanMemberships_RootToContinuationMove(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"project"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"-client"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "project")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"project-client"}) {
		t.Errorf("NewTags = %v, want set {project-client}", edits[0].NewTags)
	}
}

// TestPlanMemberships_NamespaceAddKeepsSiblings pins that a namespace ADD only
// appends the reconstructed leaf and leaves an existing sibling-namespace tag
// (project-sales) untouched: filing under `-client` while already under `-sales`
// yields both project-sales and project-client.
func TestPlanMemberships_NamespaceAddKeepsSiblings(t *testing.T) {
	base := categoriesDoc(t, map[string][]string{"t1.ics": {"-sales"}})
	edited := categoriesDoc(t, map[string][]string{"t1.ics": {"-sales", "-client"}})
	live := []cgp.Node{liveNode(t, "t1.ics", "project-sales")}

	edits, err := planMemberships(edited, base, live, membershipAnchor, mustDodderHyphen(t), membershipDim, "project")
	if err != nil {
		t.Fatalf("planMemberships: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1 (%+v)", len(edits), edits)
	}
	if !setEqual(edits[0].NewTags, []string{"project-sales", "project-client"}) {
		t.Errorf("NewTags = %v, want set {project-sales, project-client}", edits[0].NewTags)
	}
}
