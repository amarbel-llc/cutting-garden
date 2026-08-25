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
// the heading is COARSER than the atom (a month bucket over a day-precise date,
// a priority band over the raw integer), the atom carries precision the heading
// drops and MUST be kept.
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
		t.Errorf("raw-int priority atom must be kept under a band bucket; got %+v", got)
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
