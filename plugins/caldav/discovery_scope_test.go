package caldav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// countingPropfinds serves f and counts the PROPFINDs it receives.
func countingPropfinds(t *testing.T, f *fakeCalDAV) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var propfinds atomic.Int64
	inner := f.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			propfinds.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &propfinds
}

// listHome is what the trellis evaluator does at a calendar-home: an
// enriched listing that declines, then the plain ListRoots fallback.
func listHome(t *testing.T, ctx context.Context, home string) {
	t.Helper()
	node := mustParseURL(t, home)
	if _, ok, err := (Plugin{}).ListEnriched(ctx, node, nil); err != nil || ok {
		t.Fatalf("ListEnriched(home): ok=%v err=%v, want a decline", ok, err)
	}
	if nodes, err := (Plugin{}).ListRoots(ctx, node); err != nil || len(nodes) != 1 {
		t.Fatalf("ListRoots(home): %d nodes, err=%v, want the one calendar", len(nodes), err)
	}
}

// Two clients on one base URL with different users do not share a discovery
// inside one call: what a calendar-home holds can differ by account.
func TestDiscovery_NotSharedAcrossUsers(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/t1.ics", "VTODO", vtodoFull("t1", "Task", "NEEDS-ACTION", "20260101"))
	srv, propfinds := countingPropfinds(t, f)
	ctx := cutting_garden_plugins.WithCallScope(context.Background())

	for _, user := range []string{"alice", "bob"} {
		if _, _, err := newClient(srv.URL+"/dav/", user, "pw").discoverCalendarsThisCall(ctx); err != nil {
			t.Fatalf("discover as %s: %v", user, err)
		}
	}
	if got := propfinds.Load(); got != 2 {
		t.Errorf("%d PROPFINDs for two users on one base in one call, want 2", got)
	}
}

// TestDiscovery_OncePerCall pins cutting-garden#398 step 3: inside one host
// call, the declined ListEnriched and the ListRoots fallback share one
// discovery PROPFIND; a new call discovers again, and outside a call scope
// every listing does.
func TestDiscovery_OncePerCall(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/t1.ics", "VTODO", vtodoFull("t1", "Task", "NEEDS-ACTION", "20260101"))
	srv, propfinds := countingPropfinds(t, f)
	home := "caldav:" + srv.URL + "/dav/"

	listHome(t, cutting_garden_plugins.WithCallScope(context.Background()), home)
	if got := propfinds.Swap(0); got != 1 {
		t.Errorf("one call: %d PROPFINDs, want 1", got)
	}
	listHome(t, cutting_garden_plugins.WithCallScope(context.Background()), home)
	if got := propfinds.Swap(0); got != 1 {
		t.Errorf("a second call: %d PROPFINDs, want 1 (it discovers again)", got)
	}
	listHome(t, context.Background(), home)
	if got := propfinds.Swap(0); got != 2 {
		t.Errorf("no call scope: %d PROPFINDs, want 2", got)
	}
}
