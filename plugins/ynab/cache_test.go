package ynab

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
)

func TestCache_RepeatedListingCostsNoRequests(t *testing.T) {
	srv := newFixture(t)
	list(t, "ynab://personal/Consolidated/accounts/")
	after := srv.Requests()
	list(t, "ynab://personal/Consolidated/accounts/")
	list(t, "ynab://personal/Consolidated/categories/")
	if n := srv.Requests() - after; n != 1 {
		// accounts is cached; the category tree is the one new fetch.
		t.Errorf("repeat accounts + first categories cost %d requests, want 1", n)
	}
	list(t, "ynab://personal/Consolidated/categories/")
	if n := srv.Requests() - after; n != 1 {
		t.Errorf("repeat categories cost a request: total %d, want 1", n)
	}
}

// The FDR 1c requirement: reading what a listing just showed must not spend
// the 200-per-hour budget again, even when the budget is addressed by name.
func TestCache_LeafReadAfterListingCostsNoRequest(t *testing.T) {
	srv := newFixture(t)
	nodes := list(t, "ynab://personal/Consolidated/transactions/")
	if len(nodes) == 0 {
		t.Fatal("fixture listed no transactions")
	}
	after := srv.Requests()
	for _, n := range nodes {
		if _, ok, err := (Plugin{}).ReadLeaf(context.Background(), n.URI); err != nil || !ok {
			t.Fatalf("ReadLeaf(%s): ok=%v err=%v", n.URI, ok, err)
		}
	}
	if n := srv.Requests() - after; n != 0 {
		t.Errorf("reading %d listed transactions cost %d requests, want 0", len(nodes), n)
	}
}

// A transaction the listing window did not cover is fetched once, then cached.
func TestCache_LeafReadOutsideTheListingFetchesOnce(t *testing.T) {
	srv := newFixture(t)
	list(t, "ynab://personal/Consolidated/transactions/") // t3 (2026-08-01) is outside
	after := srv.Requests()
	uri := mustParseURL(t, "ynab://personal/Consolidated/transactions/t3")
	for i := 0; i < 2; i++ {
		if _, ok, err := (Plugin{}).ReadLeaf(context.Background(), uri); err != nil || !ok {
			t.Fatalf("ReadLeaf #%d: ok=%v err=%v", i, ok, err)
		}
	}
	if n := srv.Requests() - after; n != 1 {
		t.Errorf("two reads of an unlisted transaction cost %d requests, want 1", n)
	}
}

func TestCache_EntriesExpireAfterTheFreshnessWindow(t *testing.T) {
	srv := newFixture(t)
	start := now()
	list(t, "ynab://personal/")
	before := srv.Requests()

	now = func() time.Time { return start.Add(freshnessWindow - time.Second) }
	list(t, "ynab://personal/")
	if n := srv.Requests() - before; n != 0 {
		t.Errorf("a listing inside the window cost %d requests, want 0", n)
	}

	now = func() time.Time { return start.Add(freshnessWindow) }
	list(t, "ynab://personal/")
	if n := srv.Requests() - before; n != 1 {
		t.Errorf("a listing at the window's end cost %d requests, want 1 (refetch)", n)
	}
}

// gatedTransport delivers its first response only once released, so a test can
// act while a GET is in flight whose server-side answer is already fixed.
type gatedTransport struct {
	inner    http.RoundTripper
	fetched  chan struct{}
	release  chan struct{}
	gateOnce sync.Once
}

func (g *gatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := g.inner.RoundTrip(req)
	g.gateOnce.Do(func() {
		close(g.fetched)
		<-g.release
	})
	return resp, err
}

// A GET that began before a write's invalidation must not re-cache its
// pre-write rows after it (#419): the next read has to see the write.
func TestCache_InFlightGetDoesNotRecachePreWriteRowsAfterInvalidation(t *testing.T) {
	srv := newFixture(t)
	c := newClient(srv.BaseURL(), testToken, "gen-test")
	gate := &gatedTransport{
		inner: http.DefaultTransport, fetched: make(chan struct{}), release: make(chan struct{}),
	}
	c.http.Transport = gate

	type result struct {
		txs []Transaction
		err error
	}
	done := make(chan result, 1)
	go func() {
		txs, err := c.transactions(context.Background(), "b1", "", "2026-01-01", "")
		done <- result{txs, err}
	}()
	<-gate.fetched // the pre-write answer is fixed; its caller has not cached it

	srv.SetCleared("b1", "t1", "uncleared")
	cacheInvalidateTransactions(c.scope, "b1") // what a write's deferred invalidation does
	close(gate.release)
	if r := <-done; r.err != nil {
		t.Fatal(r.err)
	}

	txs, err := c.transactions(context.Background(), "b1", "", "2026-01-01", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range txs {
		if tx.ID == "t1" && tx.Cleared != "uncleared" {
			t.Errorf("t1 cleared = %q after the write, want uncleared: a stale in-flight GET was re-cached", tx.Cleared)
		}
	}
}

// Fresh data after a change on the server is visible once the window passes.
func TestCache_ServesStaleWithinTheWindowAndFreshAfter(t *testing.T) {
	srv := newFixture(t)
	start := now()
	if got := len(list(t, "ynab://personal/")); got != 2 {
		t.Fatalf("budgets = %d, want 2", got)
	}
	srv.AddBudget(ynabtestserver.Budget{ID: "b9", Name: "Late"})
	if got := len(list(t, "ynab://personal/")); got != 2 {
		t.Errorf("inside the window the cached list should still show 2 budgets, got %d", got)
	}
	now = func() time.Time { return start.Add(freshnessWindow) }
	if got := len(list(t, "ynab://personal/")); got != 3 {
		t.Errorf("after the window the new budget should appear: got %d budgets", got)
	}
}
