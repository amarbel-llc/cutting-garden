package ynab

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func dateFilter(values ...string) cutting_garden_plugins.FacetFilter {
	var f cutting_garden_plugins.FacetFilter
	for _, v := range values {
		f = append(f, cutting_garden_plugins.FacetPredicate{Dimension: facetDate, Value: v})
	}
	return f
}

func dimFilter(dim, value string) cutting_garden_plugins.FacetPredicate {
	return cutting_garden_plugins.FacetPredicate{Dimension: dim, Value: value}
}

func counts(t *testing.T, raw string, filter cutting_garden_plugins.FacetFilter) cutting_garden_plugins.FacetResult {
	t.Helper()
	res, ok, err := (Plugin{}).FacetCounts(context.Background(), mustParseURL(t, raw), filter)
	if err != nil || !ok {
		t.Fatalf("FacetCounts(%s): ok=%v err=%v", raw, ok, err)
	}
	return res
}

func TestDescribeFacets_DeclaresTheTransactionDimensions(t *testing.T) {
	var dims []cutting_garden_plugins.FacetDimension
	for _, nt := range (Plugin{}).DescribeFacets() {
		if nt.Tag == typeTransaction {
			dims = nt.Dimensions
		} else if len(nt.Dimensions) > 0 {
			t.Errorf("%s declares dimensions %v; only transactions group", nt.Tag, nt.Dimensions)
		}
	}
	var keys []string
	for _, d := range dims {
		keys = append(keys, d.Key)
	}
	want := []string{
		facetDate, facetAccount, facetCategory, facetPayee, facetCleared,
		facetApproved, facetFlag, facetTransfer, facetAmountBand,
	}
	if !slices.Equal(keys, want) {
		t.Fatalf("dimensions = %v, want %v", keys, want)
	}
	byKey := map[string]cutting_garden_plugins.FacetDimension{}
	for _, d := range dims {
		byKey[d.Key] = d
	}
	if byKey[facetDate].Kind != cutting_garden_plugins.FacetDate {
		t.Errorf("date kind = %q, want %q", byKey[facetDate].Kind, cutting_garden_plugins.FacetDate)
	}
	if len(byKey[facetCleared].Values) != 3 || len(byKey[facetApproved].Values) != 2 {
		t.Errorf("closed domains wrong: cleared %v approved %v", byKey[facetCleared].Values, byKey[facetApproved].Values)
	}
	if byKey[facetPayee].Values != nil || byKey[facetFlag].Values != nil {
		t.Error("payee and flag are open domains and must declare no Values")
	}
}

// The hand-written listing schema must project the unified declaration: every
// unified field that has a stored counterpart, in the same order with the same
// labels. The four facet-only fields have none.
func TestDescribeListingFields_ConsistentWithUnified(t *testing.T) {
	facetOnly := map[string]bool{facetApproved: true, facetFlag: true, facetTransfer: true, facetAmountBand: true}
	var want []cutting_garden_plugins.ListingField
	for _, set := range unifiedFieldSets() {
		for _, codec := range set.Codecs {
			for _, f := range codec.Fields() {
				if !facetOnly[f.Key] {
					want = append(want, cutting_garden_plugins.ListingField{Key: f.Key, Label: f.Label})
				}
			}
		}
	}
	got := (Plugin{}).DescribeListingFields()
	if len(got) != 1 || got[0].Tag != typeTransaction {
		t.Fatalf("listing schema = %+v, want one transaction entry", got)
	}
	// Order differs by design (the display order is hand-picked); compare as sets.
	index := func(fs []cutting_garden_plugins.ListingField) map[string]string {
		m := map[string]string{}
		for _, f := range fs {
			m[f.Key] = f.Label
		}
		return m
	}
	g, w := index(got[0].Fields), index(want)
	if len(g) != len(got[0].Fields) {
		t.Errorf("duplicate listing keys: %+v", got[0].Fields)
	}
	if fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("listing fields %v do not match the unified declaration %v", g, w)
	}
}

func TestAmountBandOf_Boundaries(t *testing.T) {
	cases := []struct {
		milliunits int64
		band       string
	}{
		{0, bandUnder10},
		{9_999, bandUnder10},
		{-9_999, bandUnder10},
		{10_000, band10To50},
		{-49_999, band10To50},
		{50_000, band50To200},
		{199_999, band50To200},
		{200_000, band200To1000},
		{-999_999, band200To1000},
		{1_000_000, band1000AndOver},
		{-5_000_000, band1000AndOver},
	}
	for _, tc := range cases {
		if got, _ := amountBandOf(tc.milliunits); got != tc.band {
			t.Errorf("amountBandOf(%d) = %q, want %q", tc.milliunits, got, tc.band)
		}
	}
	_, small := amountBandOf(500)
	_, large := amountBandOf(5_000_000)
	if small >= large {
		t.Errorf("a larger band must carry a larger order: %d vs %d", small, large)
	}
}

func TestTransactionFacets(t *testing.T) {
	keysOf := func(f map[string][]cutting_garden_plugins.FacetValue, dim string) []string {
		var ks []string
		for _, v := range f[dim] {
			ks = append(ks, v.Key)
		}
		return ks
	}

	plain := transactionFacets(Transaction{
		Date: "2026-10-18", Amount: -12340, Cleared: "cleared", Approved: true,
		AccountName: "Checking", PayeeName: str("Corner Cafe"), CategoryName: str("Dining Out"),
		FlagColor: str("blue"),
	})
	want := map[string]string{
		facetDate: "2026-10-18", facetAccount: "Checking", facetCategory: "Dining Out",
		facetPayee: "Corner Cafe", facetCleared: "cleared", facetApproved: valueYes,
		facetFlag: "blue", facetTransfer: valueNo, facetAmountBand: band10To50,
	}
	for dim, key := range want {
		if got := keysOf(plain, dim); !slices.Equal(got, []string{key}) {
			t.Errorf("%s = %v, want [%s]", dim, got, key)
		}
	}
	if order := plain[facetDate][0].Order; order != 20261018 {
		t.Errorf("date order = %d, want 20261018", order)
	}

	bare := transactionFacets(Transaction{Date: "2026-10-10", Amount: 100000, TransferAccountID: str("a2")})
	if got := keysOf(bare, facetCategory); !slices.Equal(got, []string{valueUncategorized}) {
		t.Errorf("a transaction with no category should group as %q, got %v", valueUncategorized, got)
	}
	if got := keysOf(bare, facetTransfer); !slices.Equal(got, []string{valueYes}) {
		t.Errorf("transfer = %v, want yes", got)
	}
	for _, absent := range []string{facetPayee, facetFlag, facetAccount, facetCleared} {
		if _, ok := bare[absent]; ok {
			t.Errorf("%s should be absent when the row has no value: %v", absent, bare[absent])
		}
	}
}

func TestDateBucketBounds(t *testing.T) {
	cases := []struct{ in, first, last string }{
		{"2026", "2026-01-01", "2026-12-31"},
		{"2026-07", "2026-07-01", "2026-07-31"},
		{"2026-02", "2026-02-01", "2026-02-28"},
		{"2028-02", "2028-02-01", "2028-02-29"},
		{"2026-07-15", "2026-07-15", "2026-07-15"},
	}
	for _, tc := range cases {
		first, last, err := dateBucketBounds(tc.in)
		if err != nil || first != tc.first || last != tc.last {
			t.Errorf("dateBucketBounds(%q) = %q, %q, %v; want %q, %q", tc.in, first, last, err, tc.first, tc.last)
		}
	}
	for _, bad := range []string{"", "26", "2026-13", "2026-07-32", "last-week", "2026/07"} {
		if _, _, err := dateBucketBounds(bad); err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("dateBucketBounds(%q): want a 400, got %v", bad, err)
		}
	}
}

func TestWindowFromFilter(t *testing.T) {
	newFixture(t) // pins the clock: the default window starts 2026-09-20
	w, err := windowFromFilter(nil)
	if err != nil || w.explicit || w.since != "2026-09-20" || w.until != "" {
		t.Errorf("default window = %+v, %v", w, err)
	}
	w, err = windowFromFilter(dateFilter("2026-07"))
	if err != nil || !w.explicit || w.since != "2026-07-01" || w.until != "2026-07-31" {
		t.Errorf("month window = %+v, %v", w, err)
	}
	// Two date predicates intersect.
	w, err = windowFromFilter(dateFilter("2026", "2026-07-15"))
	if err != nil || w.since != "2026-07-15" || w.until != "2026-07-15" {
		t.Errorf("intersected window = %+v, %v", w, err)
	}
	// Non-date predicates do not move the window.
	w, err = windowFromFilter(cutting_garden_plugins.FacetFilter{dimFilter(facetCleared, "cleared")})
	if err != nil || w.explicit {
		t.Errorf("a cleared predicate changed the window: %+v, %v", w, err)
	}
	// A URI window intersects the filter's predicates.
	w, err = windowFor(nodeRef{date: "2026"}, dateFilter("2026-07"))
	if err != nil || w.since != "2026-07-01" || w.until != "2026-07-31" || !w.explicit {
		t.Errorf("uri 2026 ∩ filter 2026-07 = %+v, %v", w, err)
	}
	w, err = windowFor(nodeRef{date: "2026-07"}, nil)
	if err != nil || w.since != "2026-07-01" || w.until != "2026-07-31" {
		t.Errorf("uri 2026-07 = %+v, %v", w, err)
	}
	if _, err := windowFromFilter(dateFilter("yesterday")); err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("a malformed date predicate must be a 400, got %v", err)
	}
}

func TestFacetCounts_DefaultWindowIsIncomplete(t *testing.T) {
	newFixture(t)
	res := counts(t, "ynab://personal/Consolidated/transactions/", nil)
	if res.Complete {
		t.Error("a default-window summary must not claim to cover the whole history")
	}
	if got := res.Summary[facetCleared]; got["cleared"] != 2 || got["uncleared"] != 1 || got["reconciled"] != 0 {
		t.Errorf("cleared = %v, want cleared 2 uncleared 1 (t3 is outside the window)", got)
	}
	if got := res.Summary[facetCategory]; got["Dining Out"] != 1 || got["Groceries"] != 1 || got[valueUncategorized] != 1 {
		t.Errorf("category = %v", got)
	}
	if got := res.Summary[facetDate]; len(got) != 3 {
		t.Errorf("date = %v, want 3 day buckets", got)
	}
}

func TestFacetCounts_DatePredicateIsPushedDownAndComplete(t *testing.T) {
	srv := newFixture(t)
	res := counts(t, "ynab://personal/Consolidated/transactions/", dateFilter("2026-08"))
	if !res.Complete {
		t.Error("an explicit month window is complete for that month")
	}
	if got := res.Summary[facetCategory]; got["Rent"] != 1 || len(got) != 1 {
		t.Errorf("August category = %v, want only Rent (t3)", got)
	}
	// One request for the budget lookup, one for the window itself.
	if n := srv.Requests(); n != 2 {
		t.Errorf("an August summary by budget name cost %d requests, want 2", n)
	}

	year := counts(t, "ynab://personal/Consolidated/transactions/", dateFilter("2026"))
	if got := year.Summary[facetDate]; len(got) != 4 {
		t.Errorf("2026 date buckets = %v, want 4 (t1..t4)", got)
	}
}

func TestFacetCounts_NarrowsByTheOtherPredicates(t *testing.T) {
	newFixture(t)
	res := counts(t, "ynab://personal/Consolidated/transactions/", cutting_garden_plugins.FacetFilter{
		{Dimension: facetDate, Value: "2026-10"},
		dimFilter(facetAccount, "Checking"),
	})
	if got := res.Summary[facetPayee]; len(got) != 2 || got["Corner Cafe"] != 1 || got["Grocer"] != 1 {
		t.Errorf("payees = %v, want the two Checking transactions", got)
	}
	if got := res.Summary[facetAccount]; got["Savings"] != 0 {
		t.Errorf("Savings leaked through the account filter: %v", got)
	}
}

func TestFacetCounts_DrillDownScopes(t *testing.T) {
	newFixture(t)
	acct := counts(t, "ynab://personal/Consolidated/accounts/Checking/", dateFilter("2026"))
	if got := acct.Summary[facetAccount]; len(got) != 1 || got["Checking"] != 3 {
		t.Errorf("Checking 2026 account facet = %v, want only Checking x3", got)
	}
	cat := counts(t, "ynab://personal/Consolidated/categories/Food/Dining%20Out/", dateFilter("2026"))
	if got := cat.Summary[facetCategory]; len(got) != 1 || got["Dining Out"] != 1 {
		t.Errorf("Dining Out category facet = %v", got)
	}
}

func TestFacetCounts_OnlyTransactionBearingNodesSummarize(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{
		"ynab://personal/",
		"ynab://personal/Consolidated/",
		"ynab://personal/Consolidated/accounts/",
		"ynab://personal/Consolidated/categories/Food/",
		"ynab://personal/Consolidated/transactions/t1",
	} {
		_, ok, err := (Plugin{}).FacetCounts(context.Background(), mustParseURL(t, raw), nil)
		if err != nil || ok {
			t.Errorf("FacetCounts(%s) = ok %v, err %v; want ok=false", raw, ok, err)
		}
	}
}

func TestFacetCounts_CapsThePayeeDimensionAndSaysSo(t *testing.T) {
	srv := newFixture(t)
	for i := 0; i < payeeTopN+5; i++ {
		srv.AddTransaction(ynabtestserver.Transaction{
			BudgetID: "b1", ID: fmt.Sprintf("p%02d", i), Date: "2026-10-05", Amount: -1000,
			AccountID: "a1", AccountName: "Checking", PayeeName: str(fmt.Sprintf("Payee %02d", i)),
		})
	}
	cacheReset()
	res := counts(t, "ynab://personal/Consolidated/transactions/", dateFilter("2026-10"))
	if got := len(res.Summary[facetPayee]); got != payeeTopN {
		t.Errorf("payee buckets = %d, want the top %d", got, payeeTopN)
	}
	if res.Complete {
		t.Error("a capped payee dimension must mark the summary incomplete")
	}
	// The gap is pinned on payee alone (RFC 0012 §5.1, cutting-garden#171): the
	// explicit month is exact for every other dimension, and a consumer can read
	// that off the result instead of distrusting the whole summary.
	if got := res.IncompleteDimensions(); !slices.Equal(got, []string{facetPayee}) {
		t.Fatalf("incomplete dimensions = %v, want [payee]", got)
	}
	gap := res.Incomplete[facetPayee]
	if gap.Declined || gap.Reason != fmt.Sprintf("top %d of %d payees", payeeTopN, payeeTopN+7) {
		t.Errorf("payee gap = %+v, want partial, %q", gap, fmt.Sprintf("top %d of %d payees", payeeTopN, payeeTopN+7))
	}
	if res.IsComplete() {
		t.Error("IsComplete must be false when a dimension is incomplete")
	}
}

// Under the default window the whole summary is partial because it does not
// reach back through history, which is not a dimension's gap: attributing it to
// payee alone would tell a consumer the other dimensions cover the history.
func TestFacetCounts_DefaultWindowDoesNotPinTheGapOnPayee(t *testing.T) {
	srv := newFixture(t)
	for i := 0; i < payeeTopN+5; i++ {
		srv.AddTransaction(ynabtestserver.Transaction{
			BudgetID: "b1", ID: fmt.Sprintf("p%02d", i), Date: "2026-10-05", Amount: -1000,
			AccountID: "a1", AccountName: "Checking", PayeeName: str(fmt.Sprintf("Payee %02d", i)),
		})
	}
	cacheReset()
	res := counts(t, "ynab://personal/Consolidated/transactions/", nil)
	if res.Complete || res.IsComplete() {
		t.Error("a default-window summary is partial")
	}
	if len(res.Incomplete) != 0 {
		t.Errorf("incomplete = %v, want none: the whole summary is partial, not one dimension", res.Incomplete)
	}
}

func TestListRoots_TransactionNodesCarryFacetsAndFields(t *testing.T) {
	newFixture(t)
	var cafe *cutting_garden_plugins.Node
	for _, n := range list(t, "ynab://personal/Consolidated/transactions/") {
		if n.URI.Path == "/Consolidated/transactions/t1" {
			n := n
			cafe = &n
		}
	}
	if cafe == nil {
		t.Fatal("t1 not listed")
	}
	if got := cafe.Facets[facetCategory]; len(got) != 1 || got[0].Key != "Dining Out" {
		t.Errorf("t1 category facet = %v", got)
	}
	if cafe.Fields[facetPayee] != "Corner Cafe" || cafe.Fields[listingAmount] != "-$12.34" ||
		cafe.Fields[facetDate] != "2026-10-18" {
		t.Errorf("t1 fields = %v", cafe.Fields)
	}
}

func enriched(t *testing.T, raw string, filter cutting_garden_plugins.FacetFilter) []cutting_garden_plugins.Node {
	t.Helper()
	nodes, ok, err := (Plugin{}).ListEnriched(context.Background(), mustParseURL(t, raw), filter)
	if err != nil || !ok {
		t.Fatalf("ListEnriched(%s): ok=%v err=%v", raw, ok, err)
	}
	return nodes
}

func TestListEnriched_DefaultWindowMatchesListRoots(t *testing.T) {
	newFixture(t)
	const raw = "ynab://personal/Consolidated/transactions/"
	if got, want := nodeURIs(enriched(t, raw, nil)), nodeURIs(list(t, raw)); !slices.Equal(got, want) {
		t.Errorf("enriched %v != listed %v", got, want)
	}
}

func TestListEnriched_PushesTheDatePredicateDown(t *testing.T) {
	srv := newFixture(t)
	nodes := enriched(t, "ynab://personal/Consolidated/transactions/", dateFilter("2026-08"))
	if got := nodeNames(nodes); !slices.Equal(got, []string{"2026-08-01 Landlord -$900.00"}) {
		t.Errorf("August = %v, want only the 2026-08-01 rent", got)
	}
	if n := srv.Requests(); n != 2 {
		t.Errorf("an August listing cost %d requests, want 2 (budget lookup + the window)", n)
	}
}

func TestListEnriched_FiltersOnOtherDimensions(t *testing.T) {
	newFixture(t)
	nodes := enriched(t, "ynab://personal/Consolidated/transactions/", cutting_garden_plugins.FacetFilter{
		dimFilter(facetCleared, "cleared"),
	})
	if got, want := nodeNames(nodes), []string{"2026-10-18 Corner Cafe -$12.34", "2026-10-10 $100.00"}; !slices.Equal(got, want) {
		t.Errorf("cleared = %v, want %v", got, want)
	}
}

func TestListEnriched_OtherNodeKindsFallBack(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{"ynab://personal/", "ynab://personal/Consolidated/", "ynab://personal/Consolidated/accounts/"} {
		_, ok, err := (Plugin{}).ListEnriched(context.Background(), mustParseURL(t, raw), nil)
		if err != nil || ok {
			t.Errorf("ListEnriched(%s) = ok %v, err %v; want ok=false", raw, ok, err)
		}
	}
}

// Two legs of one split in the same category share a parent: they are ONE node
// in both the listing and the summary.
func TestListing_TwoSplitLegsInOneCategoryAreOneNodeEverywhere(t *testing.T) {
	srv := newFixture(t)
	for _, id := range []string{"legA", "legB"} {
		srv.AddTransaction(ynabtestserver.Transaction{
			BudgetID: "b1", ID: id, Date: "2026-10-19", Amount: -3000, AmountFormatted: "-$3.00",
			AccountID: "a1", AccountName: "Checking", PayeeName: str("Warehouse"),
			CategoryID: str("c2"), CategoryName: str("Dining Out"),
			Type: "subtransaction", ParentTransactionID: str("split1"), HybridOnly: true,
		})
	}
	cacheReset()
	const raw = "ynab://personal/Consolidated/categories/Food/Dining%20Out/"
	parents := 0
	for _, u := range nodeURIs(list(t, raw)) {
		if u == "ynab://personal/Consolidated/transactions/split1" {
			parents++
		}
	}
	if parents != 1 {
		t.Errorf("the split parent was listed %d times, want 1", parents)
	}
	// The summary totals exactly the nodes the listing shows (RFC 0012 §12.2):
	// t1 plus the split once, not once per leg.
	res := counts(t, raw, dateFilter("2026-10"))
	if got := res.Summary[facetCategory]["Dining Out"]; got != 2 {
		t.Errorf("Dining Out count = %d, want 2 (t1 + the split once)", got)
	}
	if listed := len(enriched(t, raw, dateFilter("2026-10"))); int64(listed) != res.Summary[facetCategory]["Dining Out"] {
		t.Errorf("listing shows %d nodes but the summary counts %d", listed, res.Summary[facetCategory]["Dining Out"])
	}
}
