package ynab

import (
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
// http.Client. Every call is a GET (slice 1 is read-only), and every call
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

func newClient(base, token string) *client {
	base = strings.TrimRight(base, "/")
	return &client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: requestTimeout},
		scope: base,
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
	c := newClient(apiBaseFor(acct), token)
	c.scope = acct.Name + "|" + c.base
	return c, nil
}

// get issues GET base+path with query and decodes the success body into out.
// A non-200 status becomes a classified error: 404 is a caller mistake (a
// budget or object that does not exist), 401/403 point at the token, 429 names
// the rate limit; anything else is a plugin failure.
func (c *client) get(ctx context.Context, path string, query url.Values, out any) (err error) {
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return errors.Wrap(err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.Wrapf(err, "ynab plugin: GET %s", path)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.Wrap(err)
	}
	if len(data) > maxResponseBytes {
		return errors.ErrorWithStackf(
			"ynab plugin: GET %s: response exceeds %d bytes; narrow the window", path, maxResponseBytes,
		)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(data, out); err != nil {
			return errors.Wrapf(err, "ynab plugin: parse GET %s response", path)
		}
		return nil
	case http.StatusNotFound:
		return errors.BadRequestf("ynab plugin: GET %s: not found: %s", path, snippet(data))
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.ErrorWithStackf(
			"ynab plugin: GET %s: status %d: the access token was rejected "+
				"(check the account's password_env / password_key): %s",
			path, resp.StatusCode, snippet(data),
		)
	case http.StatusTooManyRequests:
		return errors.ErrorWithStackf(
			"ynab plugin: GET %s: rate limited (YNAB allows 200 requests per hour "+
				"per token); wait and retry: %s", path, snippet(data),
		)
	default:
		return errors.ErrorWithStackf(
			"ynab plugin: GET %s: status %d: %s", path, resp.StatusCode, snippet(data),
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

// budgetPath is the API path of one budget; the id is escaped as a segment.
func budgetPath(budgetID string) string {
	return budgetsPath + "/" + url.PathEscape(budgetID)
}
