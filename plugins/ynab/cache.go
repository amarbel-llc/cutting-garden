package ynab

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// freshnessWindow is how long a fetched resource is served from memory. YNAB
// allows 200 requests per hour per token and a name-addressed walk spends
// several per node (FDR 0027 §Caching and rate limit), so a listing followed
// by reads of what it listed must not re-fetch. A fixed window is a heuristic:
// data edited in the YNAB app is invisible for up to this long. The proper
// boundary is one host call, which needs the SDK convention proposed in
// cutting-garden#311 (the org plugin has the same trade-off); this plugin
// moves to it when it exists. A package var solely so tests can shorten it.
var freshnessWindow = 60 * time.Second

// cacheKind names what a cache entry holds.
type cacheKind string

const (
	cacheBudgets      cacheKind = "budgets"
	cacheAccounts     cacheKind = "accounts"
	cacheCategories   cacheKind = "categories"
	cacheTransactions cacheKind = "transactions"
	cacheTransaction  cacheKind = "transaction"
	cachePayees       cacheKind = "payees"
)

// cacheKey identifies one fetched resource. scope is the client's
// account+base (two accounts, or a test server and the real API, never share
// an entry); budget and detail narrow it (detail is the transaction window
// "scope|since|until", or a transaction id).
type cacheKey struct {
	scope  string
	kind   cacheKind
	budget string
	detail string
}

// txnKey addresses one transaction in the by-id index.
type txnKey struct {
	scope, budget, id string
}

type cacheEntry struct {
	at    time.Time
	value any
}

var (
	cacheMu      sync.Mutex
	cacheEntries = map[cacheKey]cacheEntry{}
	// txnIndex maps a transaction id to its row in the freshest cached
	// listing that carried it, so a leaf read is a map lookup rather than a
	// scan of every cached window.
	txnIndex = map[txnKey]cacheEntry{}
)

// cacheGet returns a still-fresh entry's value.
func cacheGet(key cacheKey) (any, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cacheEntries[key]
	if !ok || now().Sub(e.at) >= freshnessWindow {
		return nil, false
	}
	return e.value, true
}

// flightKey identifies one in-flight fetch: a key at an invalidation
// generation, so a caller arriving after a write's invalidation never joins a
// request that began before it.
type flightKey struct {
	key cacheKey
	gen uint64
}

// flight is one request in progress; its waiters read value and err after done
// closes.
type flight struct {
	done  chan struct{}
	value any
	err   error
}

// cacheFlights holds the in-flight fetches, guarded by cacheMu.
var cacheFlights = map[flightKey]*flight{}

// cachedFetch returns key's fresh cached value, else runs fetch and caches its
// result. Concurrent misses of one key share a single fetch (singleflight), so
// N overlapping readers spend one request, not N, of YNAB's 200-per-hour
// budget. A waiter whose own context ends stops waiting; if the fetching
// caller's context ended instead, the waiter fetches for itself rather than
// inherit another caller's cancellation. Any other error is shared.
func cachedFetch[T any](
	ctx context.Context, key cacheKey, fetch func(context.Context) (T, error),
) (T, error) {
	var zero T
	for {
		cacheMu.Lock()
		if e, ok := cacheEntries[key]; ok && now().Sub(e.at) < freshnessWindow {
			cacheMu.Unlock()
			return e.value.(T), nil
		}
		gen := cacheGens[genKey{key.scope, key.budget}]
		fk := flightKey{key, gen}
		if f, ok := cacheFlights[fk]; ok {
			cacheMu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return zero, ctx.Err()
			}
			if f.err == nil {
				return f.value.(T), nil
			}
			if ctx.Err() == nil && (errors.Is(f.err, context.Canceled) ||
				errors.Is(f.err, context.DeadlineExceeded)) {
				continue
			}
			return zero, f.err
		}
		f := &flight{done: make(chan struct{})}
		cacheFlights[fk] = f
		cacheMu.Unlock()

		return runFlight(ctx, fk, f, fetch)
	}
}

// runFlight runs the fetch for the flight fk registered, publishes its result
// to the waiters and, on success, caches it. The deferred publication also
// runs if fetch panics, so waiters are never left blocked.
func runFlight[T any](
	ctx context.Context, fk flightKey, f *flight, fetch func(context.Context) (T, error),
) (value T, err error) {
	defer func() {
		r := recover()
		cacheMu.Lock()
		delete(cacheFlights, fk)
		if r == nil && err == nil {
			cachePutLocked(fk.key, value, fk.gen)
		}
		cacheMu.Unlock()
		if r != nil {
			f.err = fmt.Errorf("ynab plugin: fetch panicked: %v", r)
			close(f.done)
			panic(r)
		}
		f.value, f.err = value, err
		close(f.done)
	}()
	return fetch(ctx)
}

// genKey addresses the invalidation generation of one (scope, budget).
type genKey struct {
	scope, budget string
}

// cacheGens counts the invalidations of each (scope, budget). A fetch reads
// the generation before its request (cacheGeneration) and hands it to
// cachePut, which refuses a result a write has invalidated since: otherwise a
// GET that began before a write's invalidation would re-cache its pre-write
// rows after it, serving them as fresh for the whole window. Never reset: a
// reset could let an old fetch's generation match a new one.
var cacheGens = map[genKey]uint64{}

// cacheGeneration returns the current invalidation generation of one budget's
// entries; take it before issuing the request whose result will be cached.
func cacheGeneration(scope, budget string) uint64 {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return cacheGens[genKey{scope, budget}]
}

// cachePut stores a value fetched under generation gen (cacheGeneration, read
// before the fetch) and evicts everything that has expired, so a
// long-lived process (cg mcp, serve) holds only what is still fresh: the
// default window's key changes every day, and month and year drill-downs add
// more, each retaining a whole transaction slice. A value fetched before an
// invalidation of its budget is not stored; the caller still returns it, as
// the answer to a read that began before the write.
func cachePut(key cacheKey, value any, gen uint64) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachePutLocked(key, value, gen)
}

// cachePutLocked is cachePut for a caller holding cacheMu.
func cachePutLocked(key cacheKey, value any, gen uint64) {
	if cacheGens[genKey{key.scope, key.budget}] != gen {
		return
	}
	t := now()
	evictExpiredLocked(t)
	cacheEntries[key] = cacheEntry{at: t, value: value}

	switch key.kind {
	case cacheTransactions:
		for _, tx := range value.([]Transaction) {
			// Hybrid rows (a split's leg) are skipped: their id is not the
			// transaction's, and it is not readable as one.
			if tx.Type == "" {
				txnIndex[txnKey{key.scope, key.budget, tx.ID}] = cacheEntry{at: t, value: tx}
			}
		}
	case cacheTransaction:
		tx := value.(Transaction)
		txnIndex[txnKey{key.scope, key.budget, tx.ID}] = cacheEntry{at: t, value: tx}
	}
}

// evictExpiredLocked drops every entry older than the freshness window. The
// caller holds cacheMu.
func evictExpiredLocked(t time.Time) {
	for k, e := range cacheEntries {
		if t.Sub(e.at) >= freshnessWindow {
			delete(cacheEntries, k)
		}
	}
	for k, e := range txnIndex {
		if t.Sub(e.at) >= freshnessWindow {
			delete(txnIndex, k)
		}
	}
}

// cacheFindTransaction looks for transaction id in a still-fresh cached
// listing (or single read) of one budget, so a leaf read after a listing costs
// no request.
func cacheFindTransaction(scope, budget, id string) (Transaction, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := txnIndex[txnKey{scope, budget, id}]
	if !ok || now().Sub(e.at) >= freshnessWindow {
		return Transaction{}, false
	}
	return e.value.(Transaction), true
}

// cacheInvalidateTransactions drops every cached transaction listing and
// single read of one budget, and its by-id index rows, after a write: the
// listings were fetched before the change and would serve it stale. Accounts
// and the category tree are untouched (a transaction write moves no account
// or category definition).
func cacheInvalidateTransactions(scope, budget string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheGens[genKey{scope, budget}]++
	for k := range cacheEntries {
		if k.scope == scope && k.budget == budget &&
			(k.kind == cacheTransactions || k.kind == cacheTransaction) {
			delete(cacheEntries, k)
		}
	}
	for k := range txnIndex {
		if k.scope == scope && k.budget == budget {
			delete(txnIndex, k)
		}
	}
}

// cacheInvalidatePayeesAndTransactions drops one budget's cached payee list and
// every cached transaction view of it, after a payee rename: the transactions
// carry the payee's name, so a listing cached before the rename would keep
// showing the old one.
func cacheInvalidatePayeesAndTransactions(scope, budget string) {
	cacheInvalidateTransactions(scope, budget)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	delete(cacheEntries, cacheKey{scope: scope, kind: cachePayees, budget: budget})
}

// cacheReset empties the cache. Production code never calls it (entries expire
// by age); tests use it between fixtures and after mutating a test server.
func cacheReset() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheEntries = map[cacheKey]cacheEntry{}
	txnIndex = map[txnKey]cacheEntry{}
}
