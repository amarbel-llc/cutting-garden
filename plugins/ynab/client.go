package ynab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// requestTimeout caps every YNAB HTTP round-trip. The command's cancelable
// context still aborts in-flight requests earlier; this is the upper bound for
// an unresponsive server.
const requestTimeout = 30 * time.Second

// maxResponseBytes bounds one response body. A year of a busy budget's
// transactions is a few megabytes; this is generous headroom, not a limit
// anyone should meet, and it keeps a misbehaving server from exhausting memory.
const maxResponseBytes = 64 << 20

// budgetsPath is the budgets collection's path under the API base. YNAB's
// current docs call budgets "plans" (`/plans`), but `/budgets` was observed
// serving live on 2026-10-20 and is what the plugin uses; it is one constant
// so a future removal is a one-line change (FDR 0027 §Scope and API).
const budgetsPath = "/budgets"

// client is a minimal YNAB REST client: a base URL, a bearer token and an
// http.Client. Every call but the transaction PATCH is a GET, and every call
// costs one request against YNAB's 200-per-hour token limit, so callers fetch
// the most specific resource they can (FDR 0027 §Caching and rate limit).
type client struct {
	base  string
	token string
	http  *http.Client
	// scope keys this client's entries in the freshness cache (cache.go):
	// the config account plus the API base, so two accounts, or a test server
	// and the real API, never share an entry.
	scope string
}

// newClient builds a client for base and token whose cache entries live under
// scope; clientFor is the one production caller and passes the account plus
// the base, so the isolation the cache promises is defined in one place.
func newClient(base, token, scope string) *client {
	return &client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: requestTimeout},
		scope: scope,
	}
}

// resolveClient builds a client for a classified node: it resolves the node's
// config account to its token and API base. An unknown account is a bad
// request.
func resolveClient(ref nodeRef) (*client, error) {
	acct, ok := accountByName(ref.account)
	if !ok {
		return nil, errors.BadRequestf("ynab plugin: unknown account %q", ref.account)
	}
	return clientFor(acct)
}

func clientFor(acct config_common.Account) (*client, error) {
	token, err := acct.Password()
	if err != nil {
		return nil, errors.Wrapf(err, "ynab plugin: account %q", acct.Name)
	}
	base := apiBaseFor(acct)
	return newClient(base, token, acct.Name+"|"+strings.TrimRight(base, "/")), nil
}

// get issues GET base+path with query and decodes the success body into out.
func (c *client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// do issues method base+path with query and, for a non-nil body, a JSON
// request body, and decodes the success body into out. Any 2xx is success (the
// published spec lists a 209 for the bulk update, which is surely a typo for
// 200, but the plugin does not depend on which). A failure status becomes a
// classified error: 404 and 400 are caller mistakes (an object that does not
// exist, a value YNAB refuses), 401/403 point at the token, 429 names the rate
// limit; anything else is a plugin failure.
func (c *client) do(
	ctx context.Context, method, path string, query url.Values, body, out any,
) (err error) {
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, merr := json.Marshal(body)
		if merr != nil {
			return errors.Wrapf(merr, "ynab plugin: marshal %s %s body", method, path)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return errors.Wrap(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.Wrapf(err, "ynab plugin: %s %s", method, path)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.Wrap(err)
	}
	if len(data) > maxResponseBytes {
		return errors.ErrorWithStackf(
			"ynab plugin: %s %s: response exceeds %d bytes; narrow the window", method, path, maxResponseBytes,
		)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.Wrapf(err, "ynab plugin: parse %s %s response", method, path)
		}
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return errors.BadRequestf("ynab plugin: %s %s: not found: %s", method, path, snippet(data))
	case resp.StatusCode == http.StatusBadRequest:
		return errors.BadRequestf("ynab plugin: %s %s: refused by YNAB: %s", method, path, snippet(data))
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return errors.ErrorWithStackf(
			"ynab plugin: %s %s: status %d: the access token was rejected "+
				"(check the account's password_env / password_key): %s",
			method, path, resp.StatusCode, snippet(data),
		)
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.ErrorWithStackf(
			"ynab plugin: %s %s: rate limited (YNAB allows 200 requests per hour "+
				"per token); wait and retry: %s", method, path, snippet(data),
		)
	default:
		return errors.ErrorWithStackf(
			"ynab plugin: %s %s: status %d: %s", method, path, resp.StatusCode, snippet(data),
		)
	}
}

// snippet trims an error-body excerpt so diagnostics stay readable.
func snippet(b []byte) string {
	const max = 256
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	// Cut on a rune boundary so a multi-byte character is never split.
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// --- typed reads (one request each on a cache miss; see cache.go) ---
//
// Cached slices are shared between callers and MUST NOT be mutated; the
// live* helpers and every caller here build new slices instead.

func (c *client) budgets(ctx context.Context) ([]Budget, error) {
	key := cacheKey{scope: c.scope, kind: cacheBudgets}
	if v, ok := cacheGet(key); ok {
		return v.([]Budget), nil
	}
	var r budgetsResponse
	if err := c.get(ctx, budgetsPath, nil, &r); err != nil {
		return nil, err
	}
	cachePut(key, r.Data.Budgets)
	return r.Data.Budgets, nil
}

func (c *client) accounts(ctx context.Context, budgetID string) ([]Account, error) {
	key := cacheKey{scope: c.scope, kind: cacheAccounts, budget: budgetID}
	if v, ok := cacheGet(key); ok {
		return v.([]Account), nil
	}
	var r accountsResponse
	if err := c.get(ctx, budgetPath(budgetID)+"/accounts", nil, &r); err != nil {
		return nil, err
	}
	cachePut(key, r.Data.Accounts)
	return r.Data.Accounts, nil
}

func (c *client) categoryGroups(ctx context.Context, budgetID string) ([]CategoryGroup, error) {
	key := cacheKey{scope: c.scope, kind: cacheCategories, budget: budgetID}
	if v, ok := cacheGet(key); ok {
		return v.([]CategoryGroup), nil
	}
	var r categoriesResponse
	if err := c.get(ctx, budgetPath(budgetID)+"/categories", nil, &r); err != nil {
		return nil, err
	}
	cachePut(key, r.Data.CategoryGroups)
	return r.Data.CategoryGroups, nil
}

// payees lists a budget's payees (deleted ones included: the full list is what
// the API returns without a delta request, and callers filter with livePayees).
func (c *client) payees(ctx context.Context, budgetID string) ([]Payee, error) {
	key := cacheKey{scope: c.scope, kind: cachePayees, budget: budgetID}
	if v, ok := cacheGet(key); ok {
		return v.([]Payee), nil
	}
	payees, err := c.freshPayees(ctx, budgetID)
	if err != nil {
		return nil, err
	}
	cachePut(key, payees)
	return payees, nil
}

// freshPayees lists a budget's payees from the server, bypassing the freshness
// cache: the payee write guards (a rename colliding with another payee, a
// transfer payee) validate against it.
func (c *client) freshPayees(ctx context.Context, budgetID string) ([]Payee, error) {
	var r payeesResponse
	if err := c.get(ctx, budgetPath(budgetID)+"/payees", nil, &r); err != nil {
		return nil, err
	}
	return r.Data.Payees, nil
}

// payee reads one payee: from a fresh cached list when there is one, else with a
// request for that payee alone.
func (c *client) payee(ctx context.Context, budgetID, id string) (Payee, error) {
	if v, ok := cacheGet(cacheKey{scope: c.scope, kind: cachePayees, budget: budgetID}); ok {
		for _, p := range v.([]Payee) {
			if p.ID == id {
				return p, nil
			}
		}
	}
	var r payeeResponse
	if err := c.get(ctx, budgetPath(budgetID)+"/payees/"+url.PathEscape(id), nil, &r); err != nil {
		return Payee{}, err
	}
	return r.Data.Payee, nil
}

// renamePayee renames one payee (`PATCH /budgets/{id}/payees/{payee_id}`) and
// returns the payee as YNAB saved it. It is the plugin's only payee write (the
// other write is patchTransactions). It
// invalidates the budget's cached payees and transactions whatever the outcome:
// transactions carry the payee's name, and a timeout may follow a rename YNAB
// did apply.
func (c *client) renamePayee(ctx context.Context, budgetID, id, name string) (Payee, error) {
	defer cacheInvalidatePayeesAndTransactions(c.scope, budgetID)
	var r struct {
		Data struct {
			Payee Payee `json:"payee"`
		} `json:"data"`
	}
	body := map[string]any{"payee": map[string]any{"name": name}}
	if err := c.do(ctx, http.MethodPatch, budgetPath(budgetID)+"/payees/"+url.PathEscape(id), nil, body, &r); err != nil {
		return Payee{}, err
	}
	return r.Data.Payee, nil
}

// transactions lists a budget's transactions in the window [since, until]
// (YYYY-MM-DD; until may be empty). scope is "" for the whole budget,
// "accounts/<id>" or "categories/<id>" for the per-parent endpoints, which
// serve a drill-down in one request instead of filtering the whole budget.
func (c *client) transactions(
	ctx context.Context, budgetID, scope, since, until string,
) ([]Transaction, error) {
	path := budgetPath(budgetID)
	if scope != "" {
		path += "/" + scope
	}
	path += "/transactions"
	key := cacheKey{
		scope: c.scope, kind: cacheTransactions, budget: budgetID,
		detail: scope + "|" + since + "|" + until,
	}
	if v, ok := cacheGet(key); ok {
		return v.([]Transaction), nil
	}
	q := url.Values{}
	if since != "" {
		q.Set("since_date", since)
	}
	if until != "" {
		q.Set("until_date", until)
	}
	var r transactionsResponse
	if err := c.get(ctx, path, q, &r); err != nil {
		return nil, err
	}
	cachePut(key, r.Data.Transactions)
	return r.Data.Transactions, nil
}

// transaction reads one transaction. A transaction already in a fresh cached
// listing of the budget is served from it, so reading what a listing just
// showed costs no request.
func (c *client) transaction(ctx context.Context, budgetID, id string) (Transaction, error) {
	if t, ok := cacheFindTransaction(c.scope, budgetID, id); ok {
		return t, nil
	}
	var r transactionResponse
	if err := c.get(ctx, budgetPath(budgetID)+"/transactions/"+url.PathEscape(id), nil, &r); err != nil {
		return Transaction{}, err
	}
	cachePut(cacheKey{scope: c.scope, kind: cacheTransaction, budget: budgetID, detail: id}, r.Data.Transaction)
	return r.Data.Transaction, nil
}

// freshTransaction reads one transaction from the server, bypassing the
// freshness cache: the write path validates against it.
func (c *client) freshTransaction(ctx context.Context, budgetID, id string) (Transaction, error) {
	var r transactionResponse
	if err := c.get(ctx, budgetPath(budgetID)+"/transactions/"+url.PathEscape(id), nil, &r); err != nil {
		return Transaction{}, err
	}
	return r.Data.Transaction, nil
}

// freshWindowDays bounds how far back freshTransactionsByID will download a
// listing to serve a batch: YNAB's own default window, a year. A target older
// than that is read by id rather than dragging a multi-year history into memory
// for the sake of one row.
const freshWindowDays = 365

// bulkListingMinTargets is how many targets make one fresh listing cheaper than
// reading each by id. Below it (and with fewer than two targets known to be
// cached) the targets are simply read by id.
const bulkListingMinTargets = 3

// freshTransactionsByID reads the given transactions from the server, bypassing
// the freshness cache, for a bulk write's guards. Reading each by id would cost
// one request per target, which is exactly what a bulk write exists to avoid, so
// when there are enough targets it spends ONE request on a fresh budget-wide
// listing instead and serves every target it contains from that. The window is
// the default 30 days, widened back (never past freshWindowDays) to the oldest
// target a still-fresh cached view knows the date of; the cached view only
// chooses WHERE to look, and the guards always see the listing's rows, never the
// cached ones. A target the listing does not return (older than the window, or
// not there) is read by id, so the cost is one request plus one per straggler,
// not one per target. The result maps an id to its row, or to the error reading
// it raised.
func (c *client) freshTransactionsByID(
	ctx context.Context, budgetID string, ids []string,
) (map[string]Transaction, map[string]error) {
	rows := make(map[string]Transaction, len(ids))
	errs := map[string]error{}

	floor := now().UTC().AddDate(0, 0, -freshWindowDays).Format("2006-01-02")
	since, known := defaultSince(), 0
	for _, id := range ids {
		if t, ok := cacheFindTransaction(c.scope, budgetID, id); ok {
			known++
			if t.Date >= floor && t.Date < since {
				since = t.Date
			}
		}
	}
	if known >= 2 || len(ids) >= bulkListingMinTargets {
		wanted := make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}
		var r transactionsResponse
		q := url.Values{"since_date": {since}}
		// A failed listing is not fatal: every id falls through to a read by id.
		if err := c.get(ctx, budgetPath(budgetID)+"/transactions", q, &r); err == nil {
			for _, t := range r.Data.Transactions {
				if wanted[t.ID] {
					rows[t.ID] = t
				}
			}
		}
	}
	for _, id := range ids {
		if _, ok := rows[id]; ok {
			continue
		}
		t, err := c.freshTransaction(ctx, budgetID, id)
		if err != nil {
			errs[id] = err
			continue
		}
		rows[id] = t
	}
	return rows, errs
}

// patchTransactions updates transactions in one request (YNAB's bulk
// `PATCH /budgets/{id}/transactions`, partial updates by id) and returns the
// transactions as YNAB saved them. Each entry is a map carrying the
// transaction's "id" plus only the fields to change; an explicit nil value is
// sent as JSON null (clearing a memo), an absent key leaves the field alone.
//
// It is the plugin's only TRANSACTION write (renamePayee is the only payee
// write; the two are its whole write surface). It invalidates the budget's cached
// transactions on success: a listing cached before the write would otherwise
// keep serving the old category for up to a minute (cache.go).
func (c *client) patchTransactions(
	ctx context.Context, budgetID string, entries []map[string]any,
) ([]Transaction, error) {
	var r saveTransactionsResponse
	body := map[string]any{"transactions": entries}
	// Invalidate whatever the outcome: a timeout or a torn response may follow
	// a write YNAB did apply, and the cache must not outlive that doubt.
	defer cacheInvalidateTransactions(c.scope, budgetID)
	if err := c.do(ctx, http.MethodPatch, budgetPath(budgetID)+"/transactions", nil, body, &r); err != nil {
		return nil, err
	}
	saved := r.Data.Transactions
	if len(saved) == 0 && r.Data.Transaction != nil {
		saved = []Transaction{*r.Data.Transaction}
	}
	return saved, nil
}

// budgetPath is the API path of one budget; the id is escaped as a segment.
func budgetPath(budgetID string) string {
	return budgetsPath + "/" + url.PathEscape(budgetID)
}
