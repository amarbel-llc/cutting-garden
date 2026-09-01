package organize

import (
	"net/url"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

// TestGroupNodes pins the bucketing: objects with no value land ungrouped, the
// rest under value buckets sorted ascending, lines sorted by id, ids relative,
// and inlineType=false drops the box `!type` (envelope spelling).
func TestGroupNodes(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/b.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"status": {{Key: "NEEDS-ACTION"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/a.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"status": {{Key: "NEEDS-ACTION"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/d.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"status": {{Key: "COMPLETED"}}},
		},
		{URI: mustURL(t, "caldav://h/c/e.ics"), Type: "caldav-object-v1"}, // ungrouped
	}

	ungrouped, buckets := groupNodes(nodes, groupSpec{Dim: "status"}, anchor, nil, false, nil)

	if len(ungrouped) != 1 || ungrouped[0].ID != "e.ics" {
		t.Fatalf("ungrouped = %+v, want just e.ics", ungrouped)
	}
	wantBuckets := []string{"COMPLETED", "NEEDS-ACTION"}
	if len(buckets) != len(wantBuckets) {
		t.Fatalf("bucket count = %d, want %d (%+v)", len(buckets), len(wantBuckets), buckets)
	}
	for i, want := range wantBuckets {
		if buckets[i].Value != want {
			t.Errorf("bucket[%d] = %q, want %q", i, buckets[i].Value, want)
		}
	}
	na := buckets[1].Lines
	if len(na) != 2 || na[0].ID != "a.ics" || na[1].ID != "b.ics" {
		t.Fatalf("NEEDS-ACTION lines not id-sorted: %+v", na)
	}
	if na[0].Type != "" {
		t.Errorf("inlineType=false should drop the box type, got %q", na[0].Type)
	}
}

// TestGroupNodes_DeclaredBuckets pins that declared write-side buckets are
// pre-rendered in order (empty ones included) before observed-but-undeclared
// values, and that inlineType=true carries the box `!type`.
func TestGroupNodes_DeclaredBuckets(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/a.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"status": {{Key: "COMPLETED"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/b.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"status": {{Key: "TENTATIVE"}}},
		}, // undeclared
	}
	declared := []string{"NEEDS-ACTION", "IN-PROCESS", "COMPLETED", "CANCELLED"}

	_, buckets := groupNodes(nodes, groupSpec{Dim: "status"}, anchor, declared, true, nil)

	wantOrder := []string{"NEEDS-ACTION", "IN-PROCESS", "COMPLETED", "CANCELLED", "TENTATIVE"}
	if len(buckets) != len(wantOrder) {
		t.Fatalf("bucket count = %d, want %d (%+v)", len(buckets), len(wantOrder), buckets)
	}
	for i, want := range wantOrder {
		if buckets[i].Value != want {
			t.Errorf("bucket[%d] = %q, want %q", i, buckets[i].Value, want)
		}
	}
	if len(buckets[0].Lines) != 0 || len(buckets[2].Lines) != 1 {
		t.Errorf("declared empties/populated wrong: %+v", buckets[:3])
	}
	if buckets[2].Lines[0].Type != "caldav-object-v1" {
		t.Errorf("inlineType=true should carry the box type, got %q", buckets[2].Lines[0].Type)
	}
}

// TestGroupNodes_MultiMembership pins multi-membership rendering (tags design D7,
// docs/plans/2026-08-20-tags-design.md; #231 slice 1): a node carrying several
// values for the grouped dimension emits the SAME line under EVERY value bucket,
// while a node with no value lands ungrouped exactly once. groupNodes already
// loops over all facet values (group.go), so multi-membership falls out for free
// — this pins it against a genuinely multi-valued dimension, which nothing did.
func TestGroupNodes_MultiMembership(t *testing.T) {
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/t1.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "work"}, {Key: "urgent"}}},
		},
		{URI: mustURL(t, "caldav://h/c/t2.ics"), Type: "caldav-object-v1"}, // no value → ungrouped
	}

	ungrouped, buckets := groupNodes(nodes, groupSpec{Dim: "categories"}, anchor, nil, false, nil)

	if len(ungrouped) != 1 || ungrouped[0].ID != "t2.ics" {
		t.Fatalf("ungrouped = %+v, want just t2.ics once", ungrouped)
	}
	wantBuckets := []string{"urgent", "work"} // observed values sort ascending
	if len(buckets) != len(wantBuckets) {
		t.Fatalf("bucket count = %d, want %d (%+v)", len(buckets), len(wantBuckets), buckets)
	}
	for i, want := range wantBuckets {
		if buckets[i].Value != want {
			t.Errorf("bucket[%d] = %q, want %q", i, buckets[i].Value, want)
		}
		// The SAME multi-valued node's line appears under every one of its buckets.
		if len(buckets[i].Lines) != 1 || buckets[i].Lines[0].ID != "t1.ics" {
			t.Errorf("bucket %q lines = %+v, want the single t1.ics line", want, buckets[i].Lines)
		}
	}
}

// TestGroupNodes_StripsRedundantGroupedAtom pins cutting-garden#229: when the
// `=<value>` heading a box is filed under already shows the grouped dimension's
// value in FULL, that dimension's box atom is dropped (pure redundancy), while
// its sibling atoms stay.
func TestGroupNodes_StripsRedundantGroupedAtom(t *testing.T) {
	anchor := "caldav://h/c/"
	node := cgp.Node{
		URI: mustURL(t, anchor+"s.ics"), Type: "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"status": {{Key: "COMPLETED"}}},
	}
	present := func(cgp.Node) []cgp.BoxAtom {
		return []cgp.BoxAtom{{Name: "status", Value: "COMPLETED"}, {Name: "location", Value: "Bank"}}
	}

	_, buckets := groupNodes(
		[]cgp.Node{node}, groupSpec{Dim: "status"}, anchor, nil, false, present,
	)
	if len(buckets) != 1 || len(buckets[0].Lines) != 1 {
		t.Fatalf("status buckets = %+v", buckets)
	}
	got := buckets[0].Lines[0].Fields
	if len(got) != 1 || got[0].Name != "location" {
		t.Errorf("grouped status atom must be stripped, sibling kept; got %+v", got)
	}

	// A bare-day date grouping (the user's reported case): the atom value equals
	// the day bucket, so date_due is stripped — but its split sibling time_due,
	// which the heading does NOT show, is kept.
	gDay, _ := cgp.ParseDateGranularity("day")
	dateNode := cgp.Node{
		URI: mustURL(t, anchor+"d.ics"), Type: "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"date_due": {{Key: "2026-08-15"}}},
	}
	presentDate := func(cgp.Node) []cgp.BoxAtom {
		return []cgp.BoxAtom{{Name: "date_due", Value: "2026-08-15"}, {Name: "time_due", Value: "14-30"}}
	}
	_, db := groupNodes(
		[]cgp.Node{dateNode}, groupSpec{Dim: "date_due", Granularity: gDay}, anchor, nil, false, presentDate,
	)
	if got := db[0].Lines[0].Fields; len(got) != 1 || got[0].Name != "time_due" {
		t.Errorf("day-granularity date_due atom must be stripped, time_due kept; got %+v", got)
	}
}

// TestGroupNodes_KeepsAtomUnderCoarserHeading pins the other side of #229: when
// the heading is COARSER than the atom (a month bucket over a day-precise
// date), the atom carries precision the heading drops and MUST be kept. The
// rule is rendered-value equality, never dimension identity — the second case
// pins that an atom whose rendered value differs from its bucket key survives
// even when it names the grouped dimension. (caldav's priority atom now renders
// its band, so in practice it strips; a presenter that renders something finer
// than the bucket keeps its atom, which is what this pins.)
func TestGroupNodes_KeepsAtomUnderCoarserHeading(t *testing.T) {
	anchor := "caldav://h/c/"
	gMonth, _ := cgp.ParseDateGranularity("month")

	dateNode := cgp.Node{
		URI: mustURL(t, anchor+"d.ics"), Type: "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"date_due": {{Key: "2026-08-15"}}},
	}
	presentDate := func(cgp.Node) []cgp.BoxAtom {
		return []cgp.BoxAtom{{Name: "date_due", Value: "2026-08-15"}, {Name: "time_due", Value: "14-30"}}
	}
	_, mb := groupNodes(
		[]cgp.Node{dateNode}, groupSpec{Dim: "date_due", Granularity: gMonth}, anchor, nil, false, presentDate,
	)
	if len(mb) != 1 || mb[0].Value != "2026-08" {
		t.Fatalf("month bucket = %+v", mb)
	}
	if got := mb[0].Lines[0].Fields; len(got) != 2 {
		t.Errorf("day-precise date_due atom must be kept under a month bucket; got %+v", got)
	}

	priNode := cgp.Node{
		URI: mustURL(t, anchor+"p.ics"), Type: "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"priority": {{Key: "0_must"}}},
	}
	presentPri := func(cgp.Node) []cgp.BoxAtom {
		return []cgp.BoxAtom{{Name: "priority", Value: "1"}}
	}
	_, pb := groupNodes(
		[]cgp.Node{priNode}, groupSpec{Dim: "priority"}, anchor, nil, false, presentPri,
	)
	if got := pb[0].Lines[0].Fields; len(got) != 1 || got[0].Name != "priority" {
		t.Errorf("an atom rendered finer than its bucket key must be kept; got %+v", got)
	}
}

// TestGroupNodesByNamespace pins the dodder-hyphen rollup grouping (RFC 0019
// tags slice 3 B2, #231): a namespace grouping over the categories tag dimension
// rolls each node's deeper tags up to their immediate next segment. Two nodes
// under project-client-* fold into one `-client` bucket, a project-cutting_garden
// node into `-cutting_garden`, and a node whose only tag (`other`) is not under
// `project` lands ungrouped. buckets[0] is the synthesized ROOT bucket (design
// G10a) — empty here, since no node carries the bare `project` tag — followed by
// the continuations ordered by their `-<segment>` key.
func TestGroupNodesByNamespace(t *testing.T) {
	interp, ok := cgp.LookupTagInterpreter("dodder-hyphen")
	if !ok {
		t.Fatal("dodder-hyphen interpreter not registered")
	}
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/acme.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project-client-acme"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/baxter.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project-client-baxter"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/cg.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project-cutting_garden"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/other.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "other"}}},
		},
	}
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}

	ungrouped, buckets, err := groupNodesByNamespace(nodes, spec, anchor, interp, false, nil)
	if err != nil {
		t.Fatalf("groupNodesByNamespace: %v", err)
	}

	if len(ungrouped) != 1 || ungrouped[0].ID != "other.ics" {
		t.Fatalf("ungrouped = %+v, want just other.ics (not under project)", ungrouped)
	}
	wantBuckets := []string{"project", "-client", "-cutting_garden"}
	if len(buckets) != len(wantBuckets) {
		t.Fatalf("bucket count = %d, want %d (%+v)", len(buckets), len(wantBuckets), buckets)
	}
	for i, want := range wantBuckets {
		if buckets[i].Value != want {
			t.Errorf("bucket[%d] = %q, want %q", i, buckets[i].Value, want)
		}
	}
	if len(buckets[0].Lines) != 0 {
		t.Errorf("root bucket lines = %+v, want none (no bare `project` tag)", buckets[0].Lines)
	}
	client := buckets[1].Lines
	if len(client) != 2 || client[0].ID != "acme.ics" || client[1].ID != "baxter.ics" {
		t.Fatalf("-client lines not the id-sorted client nodes: %+v", client)
	}
	cg := buckets[2].Lines
	if len(cg) != 1 || cg[0].ID != "cg.ics" {
		t.Fatalf("-cutting_garden lines = %+v, want just cg.ics", cg)
	}
}

// TestGroupNodesByNamespace_RootPlacement pins the G10a root bucket: a node
// carrying the BARE namespace tag files under buckets[0] (Value == the
// namespace), a node carrying the bare tag AND a deeper one files under the root
// AND its continuation, and a namespace matching NOTHING returns no buckets at
// all (no lone root heading — the all-ungrouped shape rejectEmptyNamespace keys
// off).
func TestGroupNodesByNamespace_RootPlacement(t *testing.T) {
	interp, _ := cgp.LookupTagInterpreter("dodder-hyphen")
	anchor := "caldav://h/c/"
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}

	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/bare.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/both.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{
				"categories": {{Key: "project"}, {Key: "project-client-acme"}},
			},
		},
	}
	ungrouped, buckets, err := groupNodesByNamespace(nodes, spec, anchor, interp, false, nil)
	if err != nil {
		t.Fatalf("groupNodesByNamespace: %v", err)
	}
	if len(ungrouped) != 0 {
		t.Fatalf("ungrouped = %+v, want none", ungrouped)
	}
	if len(buckets) != 2 || buckets[0].Value != "project" || buckets[1].Value != "-client" {
		t.Fatalf("buckets = %+v, want [project -client]", buckets)
	}
	root := buckets[0].Lines
	if len(root) != 2 || root[0].ID != "bare.ics" || root[1].ID != "both.ics" {
		t.Errorf("root lines = %+v, want id-sorted [bare.ics both.ics]", root)
	}
	if client := buckets[1].Lines; len(client) != 1 || client[0].ID != "both.ics" {
		t.Errorf("-client lines = %+v, want just both.ics", client)
	}

	// A namespace matching nothing: no buckets, everything ungrouped.
	none := []cgp.Node{{
		URI: mustURL(t, "caldav://h/c/o.ics"), Type: "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"categories": {{Key: "other"}}},
	}}
	ungrouped, buckets, err = groupNodesByNamespace(none, spec, anchor, interp, false, nil)
	if err != nil {
		t.Fatalf("groupNodesByNamespace: %v", err)
	}
	if len(buckets) != 0 || len(ungrouped) != 1 {
		t.Errorf("unmatched namespace: buckets = %+v ungrouped = %+v, want none / [o.ics]",
			buckets, ungrouped)
	}
}

// TestGroupNodesByNamespace_Coalesces pins per-node bucket dedup: a single node
// tagged with two tags that roll to the SAME segment (project-client-acme,
// project-client-baxter → both -client) contributes ONE line to a SINGLE
// -client bucket, relying on the interpreter's per-node dedup by bucket.
func TestGroupNodesByNamespace_Coalesces(t *testing.T) {
	interp, _ := cgp.LookupTagInterpreter("dodder-hyphen")
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/multi.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{
				"categories": {{Key: "project-client-acme"}, {Key: "project-client-baxter"}},
			},
		},
	}
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}

	ungrouped, buckets, err := groupNodesByNamespace(nodes, spec, anchor, interp, false, nil)
	if err != nil {
		t.Fatalf("groupNodesByNamespace: %v", err)
	}
	if len(ungrouped) != 0 {
		t.Fatalf("ungrouped = %+v, want none", ungrouped)
	}
	if len(buckets) != 2 || buckets[0].Value != "project" || buckets[1].Value != "-client" {
		t.Fatalf("buckets = %+v, want the root + a single -client bucket", buckets)
	}
	if lines := buckets[1].Lines; len(lines) != 1 || lines[0].ID != "multi.ics" {
		t.Errorf("-client lines = %+v, want the single multi.ics line (coalesced)", lines)
	}
}

// TestGroupNodesByNamespace_NaiveRejects pins that a namespace grouping under the
// naive interpreter — which declares no namespaces — propagates the interpreter's
// bad-request rather than swallowing it or silently grouping.
func TestGroupNodesByNamespace_NaiveRejects(t *testing.T) {
	interp, ok := cgp.LookupTagInterpreter("naive")
	if !ok {
		t.Fatal("naive interpreter not registered")
	}
	anchor := "caldav://h/c/"
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/a.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "project-client-acme"}}},
		},
	}
	spec := groupSpec{Dim: "categories", Namespace: "project", Kind: groupKindTagNamespace}

	_, _, err := groupNodesByNamespace(nodes, spec, anchor, interp, false, nil)
	if err == nil {
		t.Fatal("naive namespace grouping should error (naive declares no namespaces)")
	}
}

// TestCommonURIPrefix pins the anchor derivation that keeps box ids short
// regardless of the CLI arg form: a single-calendar node set yields the calendar
// dir, a multi-calendar set the shared ancestor dir, zero nodes the empty string.
func TestCommonURIPrefix(t *testing.T) {
	single := []cgp.Node{
		{URI: mustURL(t, "caldav://h/dav/cal/a.ics")},
		{URI: mustURL(t, "caldav://h/dav/cal/b.ics")},
	}
	if got := commonURIPrefix(single); got != "caldav://h/dav/cal/" {
		t.Errorf("single-calendar prefix = %q, want caldav://h/dav/cal/", got)
	}

	multi := []cgp.Node{
		{URI: mustURL(t, "caldav://h/dav/user/me/cal1/a.ics")},
		{URI: mustURL(t, "caldav://h/dav/user/me/cal2/b.ics")},
	}
	if got := commonURIPrefix(multi); got != "caldav://h/dav/user/me/" {
		t.Errorf("multi-calendar prefix = %q, want caldav://h/dav/user/me/", got)
	}

	if got := commonURIPrefix(nil); got != "" {
		t.Errorf("zero-node prefix = %q, want empty", got)
	}
}

// TestDistinctTypes pins the type-set derivation driving the spelling choice.
func TestDistinctTypes(t *testing.T) {
	one := []cgp.Node{{Type: "caldav-object-v1"}, {Type: "caldav-object-v1"}}
	if got := distinctTypes(one); len(got) != 1 || got[0] != "caldav-object-v1" {
		t.Errorf("distinctTypes(one) = %v", got)
	}
	two := []cgp.Node{{Type: "b-v1"}, {Type: "a-v1"}}
	if got := distinctTypes(two); len(got) != 2 || got[0] != "a-v1" || got[1] != "b-v1" {
		t.Errorf("distinctTypes(two) = %v, want sorted [a-v1 b-v1]", got)
	}
}
