package caldav

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/caldavtestserver"
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

// vtodoWithPriority seeds a VTODO carrying an explicit PRIORITY so the priority
// band facet (cutting-garden#221) has signal.
func vtodoWithPriority(uid, status string, priority int) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + uid + "\nSTATUS:" + status +
		"\nPRIORITY:" + strconv.Itoa(priority) +
		"\nEND:VTODO\nEND:VCALENDAR\n"
}

// vtodoWithCategories seeds a VTODO carrying a CATEGORIES line so the read-only
// categories tag dimension (tags slice 1, RFC 0019) has signal. cats is the raw
// comma-separated CATEGORIES value (the ical parser splits it into the tag list).
func vtodoWithCategories(uid, status, cats string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + uid + "\nSTATUS:" + status +
		"\nCATEGORIES:" + cats +
		"\nEND:VTODO\nEND:VCALENDAR\n"
}

// startFakeFaceted seeds objects carrying STATUS and DTSTART so the status
// and date_start facet dimensions have signal (the shared startFake fixtures
// are bare UID/SUMMARY bodies).
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
	// typeVTODO carries every dimension asserted below (component, status,
	// date_start, date_due, due_band, priority); the event/journal subtypes
	// are narrower — date_due is task-only, and the retired year/month
	// dimensions (#230) are declared NOWHERE.
	byTag := map[string]map[string]cutting_garden_plugins.FacetKind{}
	for _, ntf := range (Plugin{}).DescribeFacets() {
		dims := map[string]cutting_garden_plugins.FacetKind{}
		for _, d := range ntf.Dimensions {
			dims[d.Key] = d.Kind
		}
		byTag[ntf.Tag] = dims
	}
	dims := byTag[typeVTODO]
	if dims == nil {
		t.Fatalf("no facet dimensions declared for %q", typeVTODO)
	}
	if dims[facetComponent] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("component kind = %q, want categorical", dims[facetComponent])
	}
	if dims[facetStatus] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("status kind = %q, want categorical", dims[facetStatus])
	}
	if dims[facetDateStart] != cutting_garden_plugins.FacetDate {
		t.Errorf("date_start kind = %q, want date", dims[facetDateStart])
	}
	if dims[facetDateDue] != cutting_garden_plugins.FacetDate {
		t.Errorf("date_due kind = %q, want date", dims[facetDateDue])
	}
	if dims[facetDueBand] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("due_band kind = %q, want numeric-bucket", dims[facetDueBand])
	}
	if dims[facetPriority] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("priority kind = %q, want categorical", dims[facetPriority])
	}
	for _, tag := range []string{typeVEVENT, typeVJOURNAL} {
		if byTag[tag][facetDateStart] != cutting_garden_plugins.FacetDate {
			t.Errorf("%s date_start kind = %q, want date", tag, byTag[tag][facetDateStart])
		}
		if _, present := byTag[tag][facetDateDue]; present {
			t.Errorf("%s declares date_due; DUE is task-only", tag)
		}
	}
	for tag, d := range byTag {
		for _, retired := range []string{"year", "month"} {
			if _, present := d[retired]; present {
				t.Errorf("%s still declares retired dimension %q (#230)", tag, retired)
			}
		}
	}
}

// TestDescribeFacets_StatusTerminalValues pins that caldav names its done
// statuses on the status dimension (cutting-garden#214) — the primitive
// organize's default terminal exclusion derives its synthetic `_terminal` from.
// status stays an OPEN dimension (no closed Values); TerminalValues is
// orthogonal.
func TestDescribeFacets_StatusTerminalValues(t *testing.T) {
	seen := 0
	for _, ntf := range (Plugin{}).DescribeFacets() {
		for _, d := range ntf.Dimensions {
			if d.Key != facetStatus {
				continue
			}
			seen++
			got := map[string]bool{}
			for _, v := range d.TerminalValues {
				got[v] = true
			}
			if !got["COMPLETED"] || !got["CANCELLED"] {
				t.Errorf("%s status TerminalValues = %v, want COMPLETED and CANCELLED",
					ntf.Tag, d.TerminalValues)
			}
			if len(d.Values) != 0 {
				t.Errorf("%s status is not open (has closed Values %v); TerminalValues "+
					"is meant to be orthogonal", ntf.Tag, d.Values)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no status dimension found to check TerminalValues")
	}
}

// TestPriorityBandOf pins the RFC 5545 §3.8.1.9 three-level fold onto the four
// named bands (cutting-garden#221): 1–4 must, 5 should, 6–9 nice, 0/absent/
// out-of-range unspecified, with urgency-first Order. The canonical 1/5/9 values
// most clients emit land squarely in must/should/nice.
func TestPriorityBandOf(t *testing.T) {
	cases := []struct {
		in    int
		key   string
		order int64
	}{
		{1, priorityMust, 4},
		{4, priorityMust, 4},
		{5, priorityShould, 3},
		{6, priorityNice, 2},
		{9, priorityNice, 2},
		{0, priorityUnspecified, 1},
		{10, priorityUnspecified, 1}, // out of range → unspecified
		{-1, priorityUnspecified, 1},
	}
	for _, c := range cases {
		key, order := priorityBandOf(c.in)
		if key != c.key || order != c.order {
			t.Errorf("priorityBandOf(%d) = (%q, %d), want (%q, %d)",
				c.in, key, order, c.key, c.order)
		}
	}
}

// TestFacetCounts_PriorityBands drives the priority band facet end to end
// (cutting-garden#221): tasks bucket by their PRIORITY band, a task with no
// PRIORITY is 3_unspecified, and a COMPLETED task STILL contributes its band —
// priority is a stable property, unlike due_band which excludes terminal tasks.
func TestFacetCounts_PriorityBands(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/must.ics", "VTODO", vtodoWithPriority("must", "NEEDS-ACTION", 2))
	f.seed("/dav/cal/should.ics", "VTODO", vtodoWithPriority("should", "NEEDS-ACTION", 5))
	f.seed("/dav/cal/nice.ics", "VTODO", vtodoWithPriority("nice", "NEEDS-ACTION", 8))
	f.seed("/dav/cal/donemust.ics", "VTODO", vtodoWithPriority("donemust", "COMPLETED", 1))
	f.seed("/dav/cal/none.ics", "VTODO",
		vtodoFull("none", "No priority", "NEEDS-ACTION", "20260101"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertCount(t, result.Summary, facetPriority, priorityMust, 2) // live p2 + completed p1
	assertCount(t, result.Summary, facetPriority, priorityShould, 1)
	assertCount(t, result.Summary, facetPriority, priorityNice, 1)
	assertCount(t, result.Summary, facetPriority, priorityUnspecified, 1)
}

// The categories dimension (tags slice 1, RFC 0019): multi-valued, naive —
// one facet value per raw tag; a task with no CATEGORIES contributes
// nothing; counts sum per-tag (a two-tag task counts once under EACH tag).
func TestFacetCounts_CategoriesNaive(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/t1.ics", "VTODO", vtodoWithCategories("t1", "NEEDS-ACTION", "work,errand"))
	f.seed("/dav/cal/t2.ics", "VTODO", vtodoWithCategories("t2", "NEEDS-ACTION", "work"))
	f.seed("/dav/cal/t3.ics", "VTODO", vtodoFull("t3", "Untagged", "NEEDS-ACTION", "20260101"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	assertCount(t, result.Summary, facetCategories, "work", 2)
	assertCount(t, result.Summary, facetCategories, "errand", 1)

	total := int64(0)
	for _, n := range result.Summary[facetCategories] {
		total += n
	}
	if total != 3 {
		t.Errorf("categories total = %d, want 3 (untagged contributes nothing)", total)
	}
}

// The derived declaration: categories is FacetCategorical + Multi on every
// component, read-only (Mode none), and never an inline atom.
func TestDescribeFacets_CategoriesDeclaration(t *testing.T) {
	for _, ntf := range (Plugin{}).DescribeFacets() {
		var dim *cutting_garden_plugins.FacetDimension
		for i := range ntf.Dimensions {
			if ntf.Dimensions[i].Key == facetCategories {
				dim = &ntf.Dimensions[i]
			}
		}
		if dim == nil {
			t.Errorf("%s: categories not declared", ntf.Tag)
			continue
		}
		if !dim.Multi {
			t.Errorf("%s: categories must be Multi", ntf.Tag)
		}
	}
	for _, ntw := range (Plugin{}).DescribeFacetWrites() {
		for _, w := range ntw.Writes {
			if w.DimensionKey == facetCategories && w.Mode != cutting_garden_plugins.FacetWriteNone {
				t.Errorf("%s: categories write mode = %q, want none (slice 1 read-only)", ntw.Tag, w.Mode)
			}
		}
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

// TestDayBucketOf pins the ISO-day bucket derivation across the iCalendar
// date shapes the parser sees, including the numeric Order and the reject
// paths (short values, out-of-range months/days).
func TestDayBucketOf(t *testing.T) {
	cases := []struct {
		in    string
		key   string
		order int64
	}{
		{"20260224T150000Z", "2026-02-24", 20260224},
		{"2026-06-13", "2026-06-13", 20260613},
		{"20251231", "2025-12-31", 20251231},
		{"2026", "", 0},
		{"2026-02", "", 0},
		{"20261324T000000Z", "", 0}, // month 13: reject
		{"20260232", "", 0},         // day 32: reject
		{"", "", 0},
		{"garbage", "", 0},
	}
	for _, c := range cases {
		key, order := dayBucketOf(c.in)
		if key != c.key || order != c.order {
			t.Errorf("dayBucketOf(%q) = (%q, %d), want (%q, %d)",
				c.in, key, order, c.key, c.order)
		}
	}
}

// Facet values are day-precise per property; the SUMMARY lifts date
// dimensions at fixed month granularity (design 2026-08-20 §6).
func TestFacetCounts_DateDimensionsMonthLift(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/e1.ics", "VEVENT",
		veventFull("e1", "Standup", "CONFIRMED", "20260224T150000Z"))
	f.seed("/dav/cal/t1.ics", "VTODO",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:t1\nSUMMARY:Due only\n"+
			"STATUS:NEEDS-ACTION\nDUE:20260101\nEND:VTODO\nEND:VCALENDAR\n")

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	assertCount(t, result.Summary, "date_start", "2026-02", 1) // month key, not day
	assertCount(t, result.Summary, "date_due", "2026-01", 1)
	if _, present := result.Summary["date_start"]["2026-02-24"]; present {
		t.Error("summary must not carry day-granularity buckets")
	}
	if _, present := result.Summary["year"]; present {
		t.Error("the year dimension is retired")
	}
	if _, present := result.Summary["date_start"]["2026-01"]; present {
		t.Error("a DUE-only task contributes no date_start (per-property, no fallback)")
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
	assertCount(t, result.Summary, facetDateStart, "2026-02", 1)
	assertCount(t, result.Summary, facetDateStart, "2026-01", 1)
	assertCount(t, result.Summary, facetDateStart, "2025-01", 1)
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
	// date_start is re-hoisted over only the two events.
	assertCount(t, result.Summary, facetDateStart, "2026-02", 1)
	assertCount(t, result.Summary, facetDateStart, "2025-01", 1)
}

// TestFacetCounts_DateFilterNarrowsIndependentOfLift pins the
// filter-vs-lift decoupling (#230): a MONTH-granularity date_start filter
// prefix-matches the DAY-precise per-node values (narrowing to the one
// matching event), while the returned summary still lifts date_start at
// fixed month granularity — the two granularities are independent. The
// filter is parsed and Validated against the declared schema, mirroring
// the mcp layer, because Validate is what arms prefix matching.
func TestFacetCounts_DateFilterNarrowsIndependentOfLift(t *testing.T) {
	_, arg := startFakeFaceted(t)

	filter, err := cutting_garden_plugins.ParseFacetFilter("date_start=2026-02")
	if err != nil {
		t.Fatalf("ParseFacetFilter: %v", err)
	}
	if err := filter.Validate((Plugin{}).DescribeFacets()); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, arg), filter,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	// Only event1 (DTSTART 20260224T150000Z) matches the month prefix.
	assertCount(t, result.Summary, facetComponent, "VEVENT", 1)
	if _, present := result.Summary[facetComponent]["VTODO"]; present {
		t.Error("the 2026-01 task must be excluded by the date_start=2026-02 filter")
	}
	assertCount(t, result.Summary, facetDateStart, "2026-02", 1)
	for key := range result.Summary[facetDateStart] {
		if _, ok := cutting_garden_plugins.ParseDateBucket(key); !ok {
			t.Errorf("summary date_start key %q is not a date bucket", key)
		}
		if len(key) > len("2026-02") {
			t.Errorf("summary date_start key %q is day-granularity; the lift is month-fixed", key)
		}
	}
}

// startFakeFacetedMultiCalendar seeds THREE calendars under one
// calendar-home — Personal and Work each hold one overdue task, Someday
// holds one task due far in the future — via the SHARED caldavtestserver
// multi-calendar support added for cutting-garden#162
// (plugins/caldav/multicalendar_test.go's startMultiCalendarFake sibling),
// reused here to drive the cutting-garden#170 core scenario: a caller
// filtering read_facets at the home for `due_band=overdue` must learn
// exactly which calendars contributed, not merely how many matched in
// total.
func startFakeFacetedMultiCalendar(t *testing.T) string {
	t.Helper()
	srv := caldavtestserver.Start("/dav/personal/")
	srv.AddCalendar("/dav/work/", "Work")
	srv.AddCalendar("/dav/someday/", "Someday")
	srv.Seed("/dav/personal/t1.ics", "VTODO",
		vtodoFull("t1", "Overdue personal", "NEEDS-ACTION", "20260717"))
	srv.Seed("/dav/work/t2.ics", "VTODO",
		vtodoFull("t2", "Overdue work", "NEEDS-ACTION", "20260716"))
	srv.Seed("/dav/someday/t3.ics", "VTODO",
		vtodoFull("t3", "Not due yet", "NEEDS-ACTION", "20270101"))
	t.Cleanup(srv.Close)
	return "caldav:" + srv.URL() + "/dav/"
}

// TestFacetCounts_ByContainerAttributesMatchesToTheirCalendars is the
// cutting-garden#170 proof: with a `due_band=overdue` filter at the
// calendar-home, FacetCounts's ByContainer names exactly the TWO calendars
// (of three) that hold an overdue item, each with its own count, and
// excludes the calendar with no overdue items entirely — recovering the
// per-calendar attribution `foldCalendarFacets` already computes on the
// way to the merged Summary, previously discarded once folded together.
func TestFacetCounts_ByContainerAttributesMatchesToTheirCalendars(t *testing.T) {
	prev := dueBandNow
	dueBandNow = func() time.Time {
		return time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	}
	t.Cleanup(func() { dueBandNow = prev })

	home := startFakeFacetedMultiCalendar(t)
	node := mustParseURL(t, home)

	filter := cutting_garden_plugins.FacetFilter{
		{Dimension: facetDueBand, Value: dueBandOverdue},
	}
	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, filter)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertCount(t, result.Summary, facetDueBand, dueBandOverdue, 2)

	if len(result.ByContainer) != 2 {
		t.Fatalf("ByContainer = %+v, want exactly 2 entries (Personal, Work)",
			result.ByContainer)
	}
	byName := map[string]cutting_garden_plugins.FacetContainerBreakdown{}
	for _, b := range result.ByContainer {
		byName[b.Name] = b
	}
	for _, want := range []string{"Personal", "Work"} {
		b, ok := byName[want]
		if !ok {
			t.Fatalf("ByContainer missing %q: %+v", want, result.ByContainer)
		}
		if b.Count != 1 {
			t.Errorf("%s count = %d, want 1", want, b.Count)
		}
		if b.URI == "" {
			t.Errorf("%s URI is empty — a caller cannot descend into it", want)
		}
	}
	if _, present := byName["Someday"]; present {
		t.Errorf("Someday has no overdue items and must be excluded "+
			"from ByContainer: %+v", result.ByContainer)
	}
	if result.ByContainerTruncated {
		t.Error("ByContainerTruncated = true, want false (well under the limit)")
	}
}

// TestFacetCounts_SingleCalendarHasNoByContainer pins the honest-absence
// half of RFC 0012 §13: a node that IS a single calendar (no child
// containers beneath it to attribute across) reports no ByContainer at
// all, never an empty-but-present slice. startFakeFaceted's arg points at
// the calendar-HOME ("/dav/"), which discovers its one calendar as a
// CHILD (selfIsCalendar=false) — the "one-calendar home" case, which
// legitimately DOES carry a (single-entry) ByContainer; pointing directly
// at the calendar's own path ("/dav/cal/") is what makes the resolved node
// itself a calendar (selfIsCalendar=true), the case this test pins.
func TestFacetCounts_SingleCalendarHasNoByContainer(t *testing.T) {
	_, home := startFakeFaceted(t)
	calArg := home + "cal/"

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, calArg), nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if result.ByContainer != nil {
		t.Errorf("single-calendar FacetCounts carries ByContainer: %+v",
			result.ByContainer)
	}
}

// TestFacetCounts_SingleCalendarHomeHasSingleEntryByContainer pins the
// companion case: a calendar-HOME whose PROPFIND discovers exactly one
// child calendar (selfIsCalendar=false, len(calendars)==1) still reports
// a ByContainer — with exactly that one entry — since the resolved node
// genuinely has a child container to attribute across, however small.
func TestFacetCounts_SingleCalendarHomeHasSingleEntryByContainer(t *testing.T) {
	_, home := startFakeFaceted(t)

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(), mustParseURL(t, home), nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if len(result.ByContainer) != 1 {
		t.Fatalf("ByContainer = %+v, want exactly 1 entry (the home's one calendar)",
			result.ByContainer)
	}
	if result.ByContainer[0].Name != "Personal" {
		t.Errorf("ByContainer[0].Name = %q, want %q",
			result.ByContainer[0].Name, "Personal")
	}
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
