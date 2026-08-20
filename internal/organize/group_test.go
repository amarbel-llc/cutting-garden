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
