package caldav

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

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

// TestFacetVersion_CtagBackedToken pins caldav's RFC 0012 §11 change token:
// the collection ctag from the same Depth:1 PROPFIND discovery issues. The
// token is stable while the ctag is, moves when it moves, and degrades to
// ok=false against a server that advertises no ctag.
func TestFacetVersion_CtagBackedToken(t *testing.T) {
	f, arg := startFakeFaceted(t)
	node := mustParseURL(t, arg)
	ctx := context.Background()

	// No ctag advertised: no token, not an error.
	if _, ok, err := (Plugin{}).FacetVersion(ctx, node); err != nil {
		t.Fatalf("FacetVersion without ctag: %v", err)
	} else if ok {
		t.Fatal("ok = true against a server with no ctag, want false")
	}

	f.ctag = "sync-001"
	tok1, ok, err := Plugin{}.FacetVersion(ctx, node)
	if err != nil || !ok {
		t.Fatalf("FacetVersion: ok=%v err=%v", ok, err)
	}
	tok1Again, _, err := Plugin{}.FacetVersion(ctx, node)
	if err != nil {
		t.Fatalf("FacetVersion repeat: %v", err)
	}
	if tok1 != tok1Again {
		t.Errorf("token unstable across unchanged ctag: %q vs %q", tok1, tok1Again)
	}

	f.ctag = "sync-002"
	tok2, ok, err := Plugin{}.FacetVersion(ctx, node)
	if err != nil || !ok {
		t.Fatalf("FacetVersion after ctag move: ok=%v err=%v", ok, err)
	}
	if tok2 == tok1 {
		t.Errorf("token did not move with the ctag: %q", tok2)
	}
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
	if dims[facetMonth] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("month kind = %q, want numeric-bucket", dims[facetMonth])
	}
	if dims[facetDueBand] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("due_band kind = %q, want numeric-bucket", dims[facetDueBand])
	}
}

// TestDueBandDeclaration pins the RFC 0012 §11.3 obligations on the
// volatile dimension: nonzero RevalidateAfter, a closed domain covering
// every bucket, and declaration Orders consistent with the bucketing
// map (the literal exists for stable Values ordering; the map is the
// single source at lift time).
func TestDueBandDeclaration(t *testing.T) {
	var dim *cutting_garden_plugins.FacetDimension
	for _, ntf := range (Plugin{}).DescribeFacets() {
		for i := range ntf.Dimensions {
			if ntf.Dimensions[i].Key == facetDueBand {
				dim = &ntf.Dimensions[i]
			}
		}
	}
	if dim == nil {
		t.Fatal("due_band not declared")
	}
	if dim.RevalidateAfter != dueBandRevalidateAfter {
		t.Errorf("RevalidateAfter = %v, want %v",
			dim.RevalidateAfter, dueBandRevalidateAfter)
	}
	if len(dim.Values) != len(dueBandOrder) {
		t.Fatalf("closed domain has %d values, want %d",
			len(dim.Values), len(dueBandOrder))
	}
	for _, v := range dim.Values {
		if want, ok := dueBandOrder[v.Key]; !ok || v.Order != want {
			t.Errorf("declared %q order %d inconsistent with"+
				" dueBandOrder (%d, declared=%t)", v.Key, v.Order, want, ok)
		}
	}
}

// TestDueBandOf pins the quantized, zone-anchored bucketing (#141):
// evaluation is against the day start in the DATE'S OWN zone (TZID when
// loadable, host-local otherwise), a UTC instant converts before the
// day is taken, and the domain totally partitions time. The evening
// evaluation instant (19:00 New York) makes the cross-zone cases
// discriminating: Berlin and Tokyo have already rolled into July 19.
func TestDueBandOf(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 18, 19, 0, 0, 0, ny)

	cases := []struct {
		in   string
		tzid string
		want string
	}{
		{"20260717", "", dueBandOverdue},
		{"20250101T120000Z", "", dueBandOverdue},
		{"20260718", "", dueBandToday},
		{"2026-07-18", "", dueBandToday},
		{"20260721T090000", "", dueBandThisWeek},
		{"20260724", "", dueBandThisWeek}, // today+6: the week's edge
		{"20260725", "", dueBandLater},    // today+7
		{"20270101", "", dueBandLater},
		{"", "", ""},
		{"garbage", "", ""},

		// Berlin is at 01:00 July 19: its "today" is the 19th, so a
		// task due end-of-day July 18 BERLIN time is already overdue —
		// host-local bucketing would still call it "today".
		{"20260718T235900", "Europe/Berlin", dueBandOverdue},
		// Tokyo is at 08:00 July 19: a July-19-morning Tokyo due is
		// "today" there — host-local would call it tomorrow.
		{"20260719T063000", "Asia/Tokyo", dueBandToday},
		// A non-IANA TZID falls back to the host-local anchor: July 19
		// is tomorrow in New York → this-week.
		{"20260719", "Customized Time Zone", dueBandThisWeek},
	}
	for _, c := range cases {
		key, order := dueBandOf(c.in, c.tzid, now)
		if key != c.want {
			t.Errorf("dueBandOf(%q, %q) = %q, want %q",
				c.in, c.tzid, key, c.want)
			continue
		}
		if key != "" && order != dueBandOrder[key] {
			t.Errorf("dueBandOf(%q, %q) order = %d, want %d",
				c.in, c.tzid, order, dueBandOrder[key])
		}
	}
}

// TestFacetCounts_TimezoneAnchoring drives #141 end to end: a
// TZID-bearing task buckets in its own zone AND surfaces that zone
// through the pure timezone dimension, while zone-free objects
// contribute nothing there (host-local is the documented default).
func TestFacetCounts_TimezoneAnchoring(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	prev := dueBandNow
	dueBandNow = func() time.Time {
		return time.Date(2026, 7, 18, 19, 0, 0, 0, ny)
	}
	t.Cleanup(func() { dueBandNow = prev })

	berlinTask := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\n" +
		"UID:berlin1\nSUMMARY:EOD Berlin\nSTATUS:NEEDS-ACTION\n" +
		"DUE;TZID=Europe/Berlin:20260718T235900\n" +
		"END:VTODO\nEND:VCALENDAR\n"

	f := newFakeCalDAV()
	f.seed("/dav/cal/berlin.ics", "VTODO", berlinTask)
	f.seed("/dav/cal/plain.ics", "VTODO",
		vtodoFull("plain1", "No zone", "NEEDS-ACTION", "20260718"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	// The Berlin task is overdue in Berlin's day; the zone-free task is
	// "today" in the host's.
	assertCount(t, result.Summary, facetDueBand, dueBandOverdue, 1)
	assertCount(t, result.Summary, facetDueBand, dueBandToday, 1)

	assertCount(t, result.Summary, facetTimezone, "Europe/Berlin", 1)
	total := int64(0)
	for _, n := range result.Summary[facetTimezone] {
		total += n
	}
	if total != 1 {
		t.Errorf("timezone total = %d, want 1 (zone-free objects"+
			" contribute nothing): %+v", total, result.Summary[facetTimezone])
	}
}

// TestFacetCounts_DueBandVolatile drives the reference volatile
// dimension end to end: open tasks bucket against the injected today,
// completed tasks are excluded, events contribute nothing, and the
// §11.3 emission rule holds — every bucket key is present (informative
// zeros) because the calendar contains tasks, even though no task
// occupies "later".
func TestFacetCounts_DueBandVolatile(t *testing.T) {
	prev := dueBandNow
	dueBandNow = func() time.Time {
		return time.Date(2026, 7, 18, 9, 30, 0, 0, time.Local)
	}
	t.Cleanup(func() { dueBandNow = prev })

	f := newFakeCalDAV()
	f.seed("/dav/cal/t1.ics", "VTODO",
		vtodoFull("t1", "Yesterday", "NEEDS-ACTION", "20260717"))
	f.seed("/dav/cal/t2.ics", "VTODO",
		vtodoFull("t2", "Today", "NEEDS-ACTION", "20260718"))
	f.seed("/dav/cal/t3.ics", "VTODO",
		vtodoFull("t3", "Soon", "IN-PROCESS", "20260722"))
	f.seed("/dav/cal/t4.ics", "VTODO",
		vtodoFull("t4", "Done long ago", "COMPLETED", "20260101"))
	f.seed("/dav/cal/e1.ics", "VEVENT",
		veventFull("e1", "Party", "CONFIRMED", "20260719T180000Z"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertCount(t, result.Summary, facetDueBand, dueBandOverdue, 1)
	assertCount(t, result.Summary, facetDueBand, dueBandToday, 1)
	assertCount(t, result.Summary, facetDueBand, dueBandThisWeek, 1)
	// Informative zero: present, empty — the volatile expiry trigger.
	assertCount(t, result.Summary, facetDueBand, dueBandLater, 0)

	total := int64(0)
	for _, n := range result.Summary[facetDueBand] {
		total += n
	}
	if total != 3 {
		t.Errorf("due_band total = %d, want 3 (completed excluded,"+
			" events excluded): %+v", total, result.Summary[facetDueBand])
	}
}

// TestFacetCounts_NoTasksNoDueBand pins the other half of the emission
// rule: a task-free calendar omits the volatile dimension entirely, so
// its memoized summary stays purely token-gated (the §11.3 cost
// containment).
func TestFacetCounts_NoTasksNoDueBand(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/e1.ics", "VEVENT",
		veventFull("e1", "Standup", "CONFIRMED", "20260224T150000Z"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if _, present := result.Summary[facetDueBand]; present {
		t.Errorf("task-free summary carries due_band: %+v",
			result.Summary[facetDueBand])
	}
}

// TestMonthOf pins the YYYY-MM bucket derivation across the iCalendar
// date shapes the parser sees, including the chronological Order and the
// reject paths (short values, out-of-range months).
func TestMonthOf(t *testing.T) {
	cases := []struct {
		in    string
		key   string
		order int64
	}{
		{"20260224T150000Z", "2026-02", 202602},
		{"2026-06-13", "2026-06", 202606},
		{"20251231", "2025-12", 202512},
		{"2026", "", 0},
		{"20261324T000000Z", "", 0}, // month 13: reject
		{"", "", 0},
		{"garbage", "", 0},
	}
	for _, c := range cases {
		key, order := monthOf(c.in)
		if key != c.key || order != c.order {
			t.Errorf("monthOf(%q) = (%q, %d), want (%q, %d)",
				c.in, key, order, c.key, c.order)
		}
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
	assertCount(t, result.Summary, facetMonth, "2026-02", 1)
	assertCount(t, result.Summary, facetMonth, "2026-01", 1)
	assertCount(t, result.Summary, facetMonth, "2025-01", 1)
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
