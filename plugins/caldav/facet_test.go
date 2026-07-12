package caldav

import (
	"context"
	"net/http/httptest"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func veventFull(uid, summary, status, dtstart string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nSTATUS:" + status +
		"\nDTSTART:" + dtstart + "\nEND:VEVENT\nEND:VCALENDAR\n"
}

func vtodoFull(uid, summary, status, dtstart string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nSTATUS:" + status +
		"\nDTSTART:" + dtstart + "\nEND:VTODO\nEND:VCALENDAR\n"
}

// startFakeFaceted seeds objects carrying STATUS and DTSTART so the status
// and year facet dimensions have signal (the shared startFake fixtures are
// bare UID/SUMMARY bodies).
func startFakeFaceted(t *testing.T) (*fakeCalDAV, string) {
	t.Helper()
	f := newFakeCalDAV()
	f.seed("/dav/cal/event1.ics", "VEVENT",
		veventFull("event1", "Standup", "CONFIRMED", "20260224T150000Z"))
	f.seed("/dav/cal/event2.ics", "VEVENT",
		veventFull("event2", "Launch", "CANCELLED", "20250101T120000Z"))
	f.seed("/dav/cal/task1.ics", "VTODO",
		vtodoFull("task1", "Buy milk", "NEEDS-ACTION", "20260101"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return f, "caldav:" + srv.URL + "/dav/"
}

func TestDescribeFacets_DeclaresObjectDimensions(t *testing.T) {
	var dims map[string]cutting_garden_plugins.FacetKind
	for _, ntf := range (Plugin{}).DescribeFacets() {
		if ntf.Tag != typeObject {
			continue
		}
		dims = map[string]cutting_garden_plugins.FacetKind{}
		for _, d := range ntf.Dimensions {
			dims[d.Key] = d.Kind
		}
	}
	if dims == nil {
		t.Fatalf("no facet dimensions declared for %q", typeObject)
	}
	if dims[facetComponent] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("component kind = %q, want categorical", dims[facetComponent])
	}
	if dims[facetStatus] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("status kind = %q, want categorical", dims[facetStatus])
	}
	if dims[facetYear] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("year kind = %q, want numeric-bucket", dims[facetYear])
	}
}

func TestFacetCounts_AggregatesAcrossComponents(t *testing.T) {
	_, arg := startFakeFaceted(t)

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, arg), nil,
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if !ok {
		t.Fatal("FacetCounts ok = false, want true")
	}
	if !result.Complete {
		t.Error("Complete = false, want true (a calendar REPORT returns every member)")
	}

	assertCount(t, result.Summary, facetComponent, "VEVENT", 2)
	assertCount(t, result.Summary, facetComponent, "VTODO", 1)
	assertCount(t, result.Summary, facetStatus, "CONFIRMED", 1)
	assertCount(t, result.Summary, facetStatus, "CANCELLED", 1)
	assertCount(t, result.Summary, facetStatus, "NEEDS-ACTION", 1)
	assertCount(t, result.Summary, facetYear, "2026", 2)
	assertCount(t, result.Summary, facetYear, "2025", 1)
}

func TestFacetCounts_FilterNarrowsListingAndSummary(t *testing.T) {
	_, arg := startFakeFaceted(t)

	filter := cutting_garden_plugins.FacetFilter{
		{Dimension: facetComponent, Value: "VEVENT"},
	}
	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, arg), filter,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertCount(t, result.Summary, facetComponent, "VEVENT", 2)
	if _, present := result.Summary[facetComponent]["VTODO"]; present {
		t.Error("VTODO should be excluded under the component=VEVENT filter")
	}
	// Year is re-hoisted over only the two events.
	assertCount(t, result.Summary, facetYear, "2026", 1)
	assertCount(t, result.Summary, facetYear, "2025", 1)
}

func TestFacetCounts_NilNodeErrors(t *testing.T) {
	if _, _, err := (Plugin{}).FacetCounts(
		context.Background(), nil, nil,
	); err == nil {
		t.Fatal("FacetCounts(nil) must error")
	}
}

func assertCount(
	t *testing.T,
	summary cutting_garden_plugins.FacetSummary,
	dimension, key string,
	want int64,
) {
	t.Helper()
	hist, ok := summary[dimension]
	if !ok {
		t.Errorf("dimension %q absent from summary", dimension)
		return
	}
	if got := hist[key]; got != want {
		t.Errorf("summary[%s][%s] = %d, want %d", dimension, key, got, want)
	}
}
