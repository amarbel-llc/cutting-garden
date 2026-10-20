package ynab

import (
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

// cachePut stores a value and evicts everything that has expired, so a
// long-lived process (cg mcp, serve) holds only what is still fresh: the
// default window's key changes every day, and month and year drill-downs add
// more, each retaining a whole transaction slice.
func cachePut(key cacheKey, value any) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
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

// cacheReset empties the cache. Production code never calls it (entries expire
// by age); tests use it between fixtures and after mutating a test server.
func cacheReset() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheEntries = map[cacheKey]cacheEntry{}
	txnIndex = map[txnKey]cacheEntry{}
}
