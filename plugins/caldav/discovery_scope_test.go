package caldav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// countingPropfinds serves f and counts the PROPFINDs it receives.
func countingPropfinds(t *testing.T, f *fakeCalDAV) (*httptest.Server, *atomic.Int64) {
	return slowPropfinds(t, f, 0)
}

// slowPropfinds is countingPropfinds with every PROPFIND held for delay, so
// concurrent listings overlap.
func slowPropfinds(t *testing.T, f *fakeCalDAV, delay time.Duration) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var propfinds atomic.Int64
	inner := f.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			propfinds.Add(1)
			time.Sleep(delay)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &propfinds
}

// Concurrent listings of one calendar-home in one call share a single
// discovery (cutting-garden#313): the first PROPFIND is held long enough that
// all eight are in flight, and still only one reaches the server.
func TestDiscovery_ConcurrentListingsShareOne(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/t1.ics", "VTODO", vtodoFull("t1", "Task", "NEEDS-ACTION", "20260101"))
	srv, propfinds := slowPropfinds(t, f, 200*time.Millisecond)
	home := mustParseURL(t, "caldav:"+srv.URL+"/dav/")
	ctx := cutting_garden_plugins.WithCallScope(context.Background())
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodes, err := (Plugin{}).ListRoots(ctx, home)
			if err == nil && len(nodes) != 1 {
				err = fmt.Errorf("%d nodes, want the one calendar", len(nodes))
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := propfinds.Load(); got != 1 {
		t.Errorf("%d PROPFINDs for 8 concurrent listings of one home in one call, want 1", got)
	}
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
