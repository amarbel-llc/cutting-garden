package ynab

import (
	"context"
	"slices"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Regression tests for the slice 1c /code-review findings.

// The date predicate is dropped once pushed down, so the plugin must re-check
// every returned row itself: a server that ignored since/until may not make a
// month's listing or summary cover other months while claiming completeness.
func TestWindow_ARowOutsideTheWindowIsNeverShown(t *testing.T) {
	srv := newFixture(t)
	srv.IgnoreDateBounds(true)

	const raw = "ynab://personal/Consolidated/transactions/"
	got := nodeNames(enriched(t, raw, dateFilter("2026-08")))
	if !slices.Equal(got, []string{"2026-08-01 Landlord -$900.00"}) {
		t.Errorf("August listing leaked other months: %v", got)
	}
	res := counts(t, raw, dateFilter("2026-08"))
	if got := res.Summary[facetCategory]; len(got) != 1 || got["Rent"] != 1 {
		t.Errorf("August summary leaked other months: %v", got)
	}
	if !res.Complete {
		t.Error("an explicit window is still complete once the local check enforced it")
	}
}

func TestWindow_ContradictoryDatePredicatesSendNoRequest(t *testing.T) {
	srv := newFixture(t)
	const raw = "ynab://personal/Consolidated/transactions/"
	filter := dateFilter("2026", "2025-12")

	list(t, "ynab://personal/Consolidated/") // warm the budget lookup
	before := srv.Requests()
	res := counts(t, raw, filter)
	if len(res.Summary) != 0 || !res.Complete {
		t.Errorf("a contradictory window should be a complete, empty summary: %+v", res)
	}
	if nodes := enriched(t, raw, filter); len(nodes) != 0 {
		t.Errorf("a contradictory window listed %d nodes", len(nodes))
	}
	if n := srv.Requests() - before; n != 0 {
		t.Errorf("a contradictory window cost %d requests, want 0", n)
	}
}

// A budget disambiguated by ?id= must have children that resolve.
func TestListRoots_BudgetChosenByIDHasNavigableChildren(t *testing.T) {
	srv := newFixture(t)
	srv.AddBudget(ynabtestserver.Budget{ID: "b3", Name: "Consolidated"})
	srv.AddAccount(ynabtestserver.Account{BudgetID: "b3", ID: "a9", Name: "Offshore"})
	cacheReset()

	sections := list(t, "ynab://personal/Consolidated/?id=b3")
	uris := nodeURIs(sections)
	if !slices.Contains(uris, "ynab://personal/b3/accounts/") {
		t.Fatalf("sections of ?id=b3 were not minted under the id: %v", uris)
	}
	for _, s := range sections {
		list(t, s.URI.String()) // none may fail with an ambiguity error
	}
	if got := nodeNames(list(t, "ynab://personal/b3/accounts/")); !slices.Equal(got, []string{"Offshore"}) {
		t.Errorf("b3 accounts = %v", got)
	}
}

// A budget named by a unique name keeps its readable segment.
func TestListRoots_UniquelyNamedBudgetKeepsItsNameSegment(t *testing.T) {
	newFixture(t)
	uris := nodeURIs(list(t, "ynab://personal/Consolidated/"))
	if !slices.Contains(uris, "ynab://personal/Consolidated/accounts/") {
		t.Errorf("sections lost the readable segment: %v", uris)
	}
}

// A hybrid leg with no parent id cannot be addressed or read: skip it.
func TestListing_ALegWithoutAParentIsSkipped(t *testing.T) {
	srv := newFixture(t)
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "orphan-leg", Date: "2026-10-19", Amount: -1000,
		AccountID: "a1", AccountName: "Checking", PayeeName: str("Orphan"),
		CategoryID: str("c2"), CategoryName: str("Dining Out"),
		Type: "subtransaction", HybridOnly: true,
	})
	cacheReset()
	const raw = "ynab://personal/Consolidated/categories/Food/Dining%20Out/"
	for _, u := range nodeURIs(list(t, raw)) {
		if u == "ynab://personal/Consolidated/transactions/orphan-leg" {
			t.Errorf("listed an unreadable leg id: %s", u)
		}
	}
	if got := counts(t, raw, dateFilter("2026-10")).Summary[facetCategory]["Dining Out"]; got != 1 {
		t.Errorf("an orphan leg was counted: Dining Out = %d, want 1 (just t1)", got)
	}
}

func TestFormatMilliunits(t *testing.T) {
	cases := map[int64]string{
		0: "0.00", 1: "0.001", 10: "0.01", -12340: "-12.34", 12345: "12.345",
		2500000: "2500.00", -900000: "-900.00", 999: "0.999", -5: "-0.005",
	}
	for in, want := range cases {
		if got := formatMilliunits(in); got != want {
			t.Errorf("formatMilliunits(%d) = %q, want %q", in, got, want)
		}
	}
}

// Without amount_formatted the label is plain units, never raw milliunits.
func TestAmountLabel_NeverShowsRawMilliunits(t *testing.T) {
	if got := amountLabel(Transaction{Amount: -12340}); got != "-12.34" {
		t.Errorf("amountLabel without a formatted string = %q, want -12.34", got)
	}
	if got := amountLabel(Transaction{Amount: -12340, AmountFormatted: "-$12.34"}); got != "-$12.34" {
		t.Errorf("amountLabel with a formatted string = %q", got)
	}
	if got := transactionName(Transaction{Date: "2026-07-02", PayeeName: str("X"), Amount: -12340}); got != "2026-07-02 X -12.34" {
		t.Errorf("transactionName = %q", got)
	}
}

// The listing field and the facet agree for an uncategorized row.
func TestTransactionFields_UncategorizedMatchesTheFacet(t *testing.T) {
	fields := transactionFields(Transaction{Date: "2026-07-15", Amount: 2500000})
	if fields[facetCategory] != valueUncategorized {
		t.Errorf("category field = %v, want %q", fields[facetCategory], valueUncategorized)
	}
	facets := transactionFacets(Transaction{Date: "2026-07-15"})
	if facets[facetCategory][0].Key != fields[facetCategory] {
		t.Errorf("facet %q and field %q disagree", facets[facetCategory][0].Key, fields[facetCategory])
	}
}

// Expired entries are evicted by the next put, so a long-lived process does not
// accumulate a transaction slice per window forever.
func TestCache_ExpiredEntriesAreEvicted(t *testing.T) {
	newFixture(t)
	start := now()
	list(t, "ynab://personal/Consolidated/transactions/")
	cacheMu.Lock()
	filled := len(cacheEntries) + len(txnIndex)
	cacheMu.Unlock()
	if filled == 0 {
		t.Fatal("the listing cached nothing")
	}

	now = func() time.Time { return start.Add(2 * freshnessWindow) }
	list(t, "ynab://personal/") // a single new put
	cacheMu.Lock()
	entries, indexed := len(cacheEntries), len(txnIndex)
	cacheMu.Unlock()
	if entries != 1 || indexed != 0 {
		t.Errorf("after expiry the cache holds %d entries and %d indexed rows, want 1 and 0", entries, indexed)
	}
}

// Two accounts that point at the same API base never share cache entries.
func TestCache_AccountsDoNotShareEntries(t *testing.T) {
	srv := newFixture(t)
	first, _ := accountByName("personal")
	second := first
	second.Name = "other"
	second.URL = "ynab://other/"
	setAccounts(t, first, second)

	list(t, "ynab://personal/")
	before := srv.Requests()
	list(t, "ynab://other/")
	if n := srv.Requests() - before; n != 1 {
		t.Errorf("a second account's first listing cost %d requests, want 1 (its own cache scope)", n)
	}
	before = srv.Requests()
	list(t, "ynab://other/")
	list(t, "ynab://personal/")
	if n := srv.Requests() - before; n != 0 {
		t.Errorf("repeat listings cost %d requests, want 0", n)
	}
}

// Both root maps are the same account-name map.
func TestRootLabelsAndNamesAgree(t *testing.T) {
	setAccounts(t, account("a"), account("b"), config_common.Account{
		Root: config_common.Root{Name: "c", URL: "ynab://c/"},
	})
	labels, _ := (Plugin{}).RootLabels(context.Background())
	names, _ := (Plugin{}).RootNames(context.Background())
	if len(labels) != 3 || len(names) != 3 {
		t.Fatalf("labels %v names %v, want 3 each", labels, names)
	}
	for k, v := range labels {
		if names[k] != v {
			t.Errorf("RootLabels[%q] = %q but RootNames = %q", k, v, names[k])
		}
	}
}

// Contradictory-predicate and malformed-date handling stays a usage error.
func TestWindow_MalformedDateIsStillABadRequest(t *testing.T) {
	newFixture(t)
	_, _, err := (Plugin{}).FacetCounts(context.Background(),
		mustParseURL(t, "ynab://personal/Consolidated/transactions/"), dateFilter("soon"))
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("want a 400 for date=soon, got %v", err)
	}
}
