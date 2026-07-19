package caldav

import (
	"context"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/caldavtestserver"
)

// startMultiCalendarFake spins up the SHARED caldavtestserver package (the
// same server binary the bats caldav lane spawns as a coproc) seeded with
// TWO calendars under one calendar-home, and returns the caldav: source arg
// pointing at the home — not either calendar directly. This exercises
// caldavtestserver's multi-calendar support (added for cutting-garden#162)
// from a fast, sandboxed Go unit test, complementing the slower bats e2e
// lane (zz-tests_bats/caldav.bats) that drives the real binary end to end.
func startMultiCalendarFake(t *testing.T) (*caldavtestserver.Server, string) {
	t.Helper()
	srv := caldavtestserver.Start("/dav/cal/")
	srv.Seed("/dav/cal/task1.ics", "VTODO", vtodo("task1", "Buy milk"))
	srv.Seed("/dav/cal/task2.ics", "VTODO", vtodo("task2", "Walk dog"))
	srv.AddCalendar("/dav/work/", "Work")
	srv.Seed("/dav/work/task3.ics", "VTODO", vtodo("task3", "Submit report"))
	t.Cleanup(srv.Close)
	return srv, "caldav:" + srv.URL() + "/dav/"
}

// TestListRoots_DiscoversMultipleCalendarsAtHome is the cutting-garden#162
// proof: a caldav account/root configured at the principal/calendar-home
// level (not a single calendar) already discovers every calendar
// collection beneath it via the RootLister traversal FDR 0014 specifies —
// no config schema change and no new discovery code were needed to make
// this work; what was missing was verification that the existing
// discoverCalendars-backed ListRoots differentiates N>1 calendars by their
// DAV displayname rather than collapsing or confusing them (every prior
// test, in-Go and bats, only ever seeded exactly one calendar under a
// home). The friendly Personal/Work names (instead of opaque path
// segments) are also the cutting-garden#120 friendly-label win, for
// accounts configured at the home level specifically.
func TestListRoots_DiscoversMultipleCalendarsAtHome(t *testing.T) {
	_, home := startMultiCalendarFake(t)
	ctx := context.Background()

	calendars, err := Plugin{}.ListRoots(ctx, mustParseURL(t, home))
	if err != nil {
		t.Fatalf("ListRoots(home): %v", err)
	}

	names := make([]string, 0, len(calendars))
	for _, c := range calendars {
		if c.Type != typeCalendar {
			t.Errorf("calendar %q Type = %q, want %q", c.Name, c.Type, typeCalendar)
		}
		names = append(names, c.Name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "Personal,Work" {
		t.Fatalf("discovered calendar names = %q, want %q", got, "Personal,Work")
	}

	// Each discovered calendar is independently descendable and scoped to
	// its own objects — not a flattened, cross-calendar listing.
	for _, c := range calendars {
		objects, err := Plugin{}.ListRoots(ctx, c.URI)
		if err != nil {
			t.Fatalf("ListRoots(%s): %v", c.Name, err)
		}
		objNames := make([]string, 0, len(objects))
		for _, o := range objects {
			objNames = append(objNames, o.Name)
		}
		sort.Strings(objNames)
		got := strings.Join(objNames, ",")
		switch c.Name {
		case "Personal":
			if got != "task1.ics,task2.ics" {
				t.Errorf("Personal objects = %q, want task1.ics,task2.ics", got)
			}
		case "Work":
			if got != "task3.ics" {
				t.Errorf("Work objects = %q, want task3.ics", got)
			}
		}
	}
}

// TestCaptureRoot_AggregatesAcrossDiscoveredCalendars proves capture at the
// calendar-home level (built on the same discoverCalendars primitive
// ListRoots uses, per FDR 0014's "capture and ListRoots share an internal
// traversal primitive" design) picks up objects from every discovered
// calendar, not just the first — the capture-side half of
// cutting-garden#162, so a principal-level account isn't merely
// list-only.
func TestCaptureRoot_AggregatesAcrossDiscoveredCalendars(t *testing.T) {
	_, home := startMultiCalendarFake(t)

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, home),
		RawArg:    home,
		BlobStore: newMemStore(t),
	})
	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0: %+v", result.FailCount, result.Failures)
	}

	gotPaths := entryPaths(result.Entries)
	sort.Strings(gotPaths)
	wantPaths := []string{"dav/cal/task1.ics", "dav/cal/task2.ics", "dav/work/task3.ics"}
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("captured paths = %v, want %v", gotPaths, wantPaths)
	}
}
