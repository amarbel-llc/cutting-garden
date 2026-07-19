package caldav

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/plugins/caldav/caldavtestserver"
)

// resetRootLabelCache clears the process-lifetime RootLabels memoization
// cache (config.go's rootLabelCacheM) so tests don't leak cached labels
// into each other via shared account names.
func resetRootLabelCache(t *testing.T) {
	t.Helper()
	rootLabelCacheMu.Lock()
	rootLabelCacheM = map[string]string{}
	rootLabelCacheMu.Unlock()
}

// TestRootLabels_CalendarScopedAccountSurfacesDisplayName is the direct
// cutting-garden#120 proof: an account configured directly at a single
// calendar collection (selfIsCalendar == true) gets its DAV displayname
// back from RootLabels, keyed by the SAME URL Roots() returns — the
// remaining #120 surface after #162's home-level discovery already solved
// the calendar-home case.
func TestRootLabels_CalendarScopedAccountSurfacesDisplayName(t *testing.T) {
	resetRootLabelCache(t)
	srv := caldavtestserver.Start("/dav/cal/")
	srv.Calendars[0].DisplayName = "My Personal Calendar"
	t.Cleanup(srv.Close)

	setAccounts(t, acct("scoped", "caldav:"+srv.URL()+"/dav/cal/", "", ""))

	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d: %+v", len(roots), roots)
	}
	key := roots[0].String()

	labels, err := (Plugin{}).RootLabels(context.Background())
	if err != nil {
		t.Fatalf("RootLabels: %v", err)
	}
	if got := labels[key]; got != "My Personal Calendar" {
		t.Errorf("labels[%q] = %q, want %q", key, got, "My Personal Calendar")
	}
}

// TestRootLabels_CalendarHomeAccountOmitsLabel pins the scope boundary: a
// calendar-HOME account (selfIsCalendar == false) gets NO top-level label
// from RootLabels — its children already get their displayname during
// descent (ListRoots -> calendarNodes -> calendarLabel, cutting-garden#162),
// and the home URL itself has no calendar displayname of its own to give.
func TestRootLabels_CalendarHomeAccountOmitsLabel(t *testing.T) {
	resetRootLabelCache(t)
	_, home := startMultiCalendarFake(t)
	setAccounts(t, acct("home", home, "", ""))

	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d: %+v", len(roots), roots)
	}
	key := roots[0].String()

	labels, err := (Plugin{}).RootLabels(context.Background())
	if err != nil {
		t.Fatalf("RootLabels: %v", err)
	}
	if label, ok := labels[key]; ok {
		t.Errorf("calendar-home account got a top-level label %q; want none", label)
	}
}

// TestRootLabels_UnreachableAccountDegradesNonFatal pins cutting-garden#165:
// a PROPFIND failure (an unreachable/misconfigured account) is a non-fatal
// omission — RootLabels returns no error and simply leaves that account
// unlabeled, so the caller falls back to its default label rather than
// root aggregation failing outright.
func TestRootLabels_UnreachableAccountDegradesNonFatal(t *testing.T) {
	resetRootLabelCache(t)
	// Port 1 is a reserved/privileged port essentially never listening in
	// a test sandbox, so the connection fails fast and deterministically.
	setAccounts(t, acct("dead", "caldav://127.0.0.1:1/dav/cal/", "", ""))

	labels, err := (Plugin{}).RootLabels(context.Background())
	if err != nil {
		t.Fatalf("RootLabels must degrade non-fatally, not error: %v", err)
	}
	if len(labels) != 0 {
		t.Errorf("unreachable account produced labels = %+v, want none", labels)
	}
}

// TestRootLabels_CachesAcrossCalls pins the process-lifetime memoization
// (cutting-garden#120's "consider caching" note): once a calendar-scoped
// account's displayname is resolved, a second RootLabels call serves it
// from cache rather than re-PROPFINDing — proven here by closing the
// server between calls and observing the label still comes back.
func TestRootLabels_CachesAcrossCalls(t *testing.T) {
	resetRootLabelCache(t)
	srv := caldavtestserver.Start("/dav/cal/")
	srv.Calendars[0].DisplayName = "Cached Calendar"

	setAccounts(t, acct("cached", "caldav:"+srv.URL()+"/dav/cal/", "", ""))

	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	key := roots[0].String()

	labels, err := (Plugin{}).RootLabels(context.Background())
	if err != nil {
		t.Fatalf("RootLabels (1st, server up): %v", err)
	}
	if got := labels[key]; got != "Cached Calendar" {
		t.Fatalf("labels[%q] = %q, want %q", key, got, "Cached Calendar")
	}

	// No t.Cleanup(srv.Close): closed explicitly here, mid-test, so the
	// SECOND call below has nothing to PROPFIND against.
	srv.Close()

	labels, err = (Plugin{}).RootLabels(context.Background())
	if err != nil {
		t.Fatalf("RootLabels (2nd, server closed) must degrade to the cached value, not error: %v", err)
	}
	if got := labels[key]; got != "Cached Calendar" {
		t.Errorf("cached labels[%q] = %q, want %q (server is closed; only the cache could have served this)",
			key, got, "Cached Calendar")
	}
}
