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

type cacheEntry struct {
	at    time.Time
	value any
}

var (
	cacheMu      sync.Mutex
	cacheEntries = map[cacheKey]cacheEntry{}
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

func cachePut(key cacheKey, value any) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheEntries[key] = cacheEntry{at: now(), value: value}
}

// cacheFindTransaction looks for transaction id in any still-fresh cached
// transaction listing of one budget, so a leaf read after a listing costs no
// request. Hybrid rows (a split's leg) are skipped: their id is not the
// transaction's.
func cacheFindTransaction(scope, budget, id string) (Transaction, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for key, e := range cacheEntries {
		if key.scope != scope || key.budget != budget || now().Sub(e.at) >= freshnessWindow {
			continue
		}
		switch key.kind {
		case cacheTransactions:
			for _, t := range e.value.([]Transaction) {
				if t.ID == id && t.Type == "" {
					return t, true
				}
			}
		case cacheTransaction:
			if t := e.value.(Transaction); t.ID == id {
				return t, true
			}
		}
	}
	return Transaction{}, false
}

// cacheReset empties the cache. Production code never calls it (entries expire
// by age); tests use it between fixtures and after mutating a test server.
func cacheReset() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheEntries = map[cacheKey]cacheEntry{}
}
