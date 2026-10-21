package ynab

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared on transaction-v1 (the groupable fields of the
// unified declaration, unified.go). Every value is derived from fields already
// in hand in the list response (RFC 0012's cheapness rule): no per-node fetch.
const (
	facetDate       = "date"        // ISO day, prefix-coarsenable to month/year
	facetAccount    = "account"     // account name
	facetCategory   = "category"    // category name; "Split" for a split, "uncategorized" for none
	facetPayee      = "payee"       // payee name, open and high-cardinality (top-N capped)
	facetCleared    = "cleared"     // cleared | uncleared | reconciled
	facetApproved   = "approved"    // yes | no
	facetFlag       = "flag"        // the flag colour, absent when unflagged
	facetTransfer   = "transfer"    // yes when the transaction is a transfer between accounts
	facetAmountBand = "amount_band" // magnitude bucket of the absolute amount
)

// Closed-domain values.
const (
	valueYes           = "yes"
	valueNo            = "no"
	valueUncategorized = "uncategorized"

	clearedCleared    = "cleared"
	clearedUncleared  = "uncleared"
	clearedReconciled = "reconciled"
)

// amount_band keys, smallest to largest. They bucket the ABSOLUTE amount in
// whole currency units (milliunits / 1000), so an outflow and an inflow of the
// same size share a band; the currency is whatever the budget uses.
const (
	bandUnder10     = "under_10"
	band10To50      = "10_to_50"
	band50To200     = "50_to_200"
	band200To1000   = "200_to_1000"
	band1000AndOver = "1000_and_over"
)

// payeeTopN caps the open, high-cardinality `payee` dimension so it does not
// dwarf a summary. When capping drops payees the result is marked partial.
const payeeTopN = 20

// DescribeFacets declares the facet schema `describe_node_types` reports —
// DERIVED from the unified declaration (FDR 0025). Only transaction-v1 groups;
// budgets, sections, accounts and categories are navigation.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return cutting_garden_plugins.DeriveNodeTypeFacets(unifiedFieldSets())
}

// transactionFacets derives one transaction's facet values.
func transactionFacets(t Transaction) map[string][]cutting_garden_plugins.FacetValue {
	one := func(key string) []cutting_garden_plugins.FacetValue {
		return []cutting_garden_plugins.FacetValue{{Key: key}}
	}
	facets := map[string][]cutting_garden_plugins.FacetValue{}

	if day, order := dayBucketOf(t.Date); day != "" {
		facets[facetDate] = []cutting_garden_plugins.FacetValue{{Key: day, Order: order}}
	}
	if t.AccountName != "" {
		facets[facetAccount] = one(t.AccountName)
	}
	category := valueUncategorized
	if t.CategoryName != nil && *t.CategoryName != "" {
		category = *t.CategoryName
	}
	facets[facetCategory] = one(category)
	if t.PayeeName != nil && *t.PayeeName != "" {
		facets[facetPayee] = one(*t.PayeeName)
	}
	if t.Cleared != "" {
		facets[facetCleared] = one(t.Cleared)
	}
	facets[facetApproved] = one(yesNo(t.Approved))
	if t.FlagColor != nil && *t.FlagColor != "" {
		facets[facetFlag] = one(*t.FlagColor)
	}
	facets[facetTransfer] = one(yesNo(t.TransferAccountID != nil))
	band, order := amountBandOf(t.Amount)
	facets[facetAmountBand] = []cutting_garden_plugins.FacetValue{{Key: band, Order: order}}
	return facets
}

func yesNo(b bool) string {
	if b {
		return valueYes
	}
	return valueNo
}

// dayBucketOf turns a YNAB date ("2026-10-18") into the day-bucket key and the
// sortable order the framework's date machinery coarsens by prefix. Empty key
// when the value is not a date.
func dayBucketOf(date string) (key string, order int64) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", 0
	}
	return date, int64(d.Year())*10000 + int64(d.Month())*100 + int64(d.Day())
}

// amountBandOf buckets a signed milliunit amount by magnitude. The order makes
// the largest band sort first.
func amountBandOf(milliunits int64) (band string, order int64) {
	abs := milliunits
	if abs < 0 {
		abs = -abs
	}
	switch units := abs / 1000; {
	case units < 10:
		return bandUnder10, 1
	case units < 50:
		return band10To50, 2
	case units < 200:
		return band50To200, 3
	case units < 1000:
		return band200To1000, 4
	default:
		return band1000AndOver, 5
	}
}

// FacetCounts summarizes the transactions of a transaction-bearing node (the
// budget-wide section, one account, one category) in one request: it fetches
// the window the filter names and folds each row's facet values into one
// summary (RFC 0012 §4.1). Other node kinds report ok=false so the framework
// falls back.
//
// The date window is pushed down to the server: `date=2026-07` fetches only
// July. Without a date predicate the window is the short default one, so the
// result is marked incomplete — it says nothing about older history. A date
// predicate makes it complete for exactly that span, unless the open `payee`
// dimension was capped.
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"ynab plugin: FacetCounts requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	if !bearsTransactions(ref.kind) {
		return cutting_garden_plugins.FacetResult{}, false, nil
	}
	c, err := resolveClient(ref)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	budgetID, err := resolveBudgetID(ctx, c, ref)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	scope, err := transactionScope(ctx, c, budgetID, ref)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	window, err := windowFor(ref, filter)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	summary := cutting_garden_plugins.FacetSummary{}
	if window.empty() {
		// Contradictory date predicates: nothing can match, and that is a
		// complete answer. No request is sent.
		return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
	}
	txns, err := c.transactions(ctx, budgetID, scope, window.since, window.until)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	// The same set the listing shows (visibleTransactions), so a summary
	// always totals the nodes ListEnriched returns for the same filter.
	rest := withoutDate(filter)
	for _, v := range visibleTransactions(txns, window) {
		facets := transactionFacets(v.row)
		if !rest.Matches(facets) {
			continue
		}
		liftFacets(summary, facets)
	}

	complete := window.explicit
	var incomplete map[string]cutting_garden_plugins.FacetGap
	if total, dropped := capHistogram(summary[facetPayee], payeeTopN); dropped {
		complete = false
		// With an explicit window every other dimension is exact, so say which
		// one is not (RFC 0012 §5.1, cutting-garden#171). Under the default
		// window the whole summary is partial for a reason that is not a
		// dimension's, so the gap cannot be pinned on payee alone.
		if window.explicit {
			incomplete = map[string]cutting_garden_plugins.FacetGap{
				facetPayee: {Reason: fmt.Sprintf("top %d of %d payees", payeeTopN, total)},
			}
		}
	}
	return cutting_garden_plugins.FacetResult{
		Summary: summary, Complete: complete, Incomplete: incomplete,
	}, true, nil
}

// RefusesFacetFold says the framework fold must not take over where
// FacetCounts declined (cutting_garden_plugins.FacetFoldRefuser). Every node
// that does not list transactions is declined above (the account root, a
// budget, its accounts/categories/payees sections, a category group, a single
// transaction or payee), and the fold would walk those subtrees through
// ListRoots: descending every account and category container fetches its
// transactions, one request each, from the 200-per-hour budget — on a timer,
// since the host warms and refreshes the roots' summaries without a read. So a
// declined node stays "no facet summary available", with no request. This
// mirrors FacetCounts' decline test (a node that does not bear transactions);
// change them together.
func (Plugin) RefusesFacetFold(node *url.URL) bool {
	ref, err := classifyURI(node)
	return err != nil || !bearsTransactions(ref.kind)
}

// liftFacets folds one transaction's facet values into summary: +1 per
// (dimension, value key). The per-node "lift" of RFC 0012 §3.
func liftFacets(
	summary cutting_garden_plugins.FacetSummary,
	facets map[string][]cutting_garden_plugins.FacetValue,
) {
	for dim, values := range facets {
		hist := summary[dim]
		if hist == nil {
			hist = cutting_garden_plugins.FacetHistogram{}
			summary[dim] = hist
		}
		for _, v := range values {
			hist[v.Key]++
		}
	}
}

// capHistogram keeps only the top-n buckets of hist by descending count (ties
// broken by ascending key for determinism), deleting the rest in place. It
// reports how many buckets the histogram held before the cap and whether any
// was dropped. A nil or already-small histogram is left untouched.
func capHistogram(hist cutting_garden_plugins.FacetHistogram, n int) (total int, dropped bool) {
	total = len(hist)
	if total <= n {
		return total, false
	}
	type kv struct {
		key   string
		count int64
	}
	entries := make([]kv, 0, len(hist))
	for k, v := range hist {
		entries = append(entries, kv{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].key < entries[j].key
	})
	for _, e := range entries[n:] {
		delete(hist, e.key)
	}
	return total, true
}
