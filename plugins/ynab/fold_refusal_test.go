package ynab

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.FacetFoldRefuser = Plugin{}

// The framework fold must never run on a node ynab's FacetCounts declines:
// descending an account or category container fetches its transactions, one
// request each from the 200-per-hour budget, and the host summarizes the
// configured roots at startup and again on a timer with no user read. Every
// declined node class costs zero requests and answers "no summary".
func TestResolveFacetCounts_DecliningNodesCostNoRequests(t *testing.T) {
	for _, raw := range []string{
		"ynab://personal/",
		"ynab://personal/Consolidated/",
		"ynab://personal/Consolidated/accounts/",
		"ynab://personal/Consolidated/categories/",
		"ynab://personal/Consolidated/payees/",
		"ynab://personal/Consolidated/payees/p1",
		"ynab://personal/Consolidated/transactions/t1",
	} {
		srv := newFixture(t)
		cacheReset()
		result, ok, err := cutting_garden_plugins.ResolveFacetCounts(
			context.Background(), Plugin{}, mustParseURL(t, raw), nil,
		)
		if err != nil || ok {
			t.Errorf("%s: ok=%v err=%v result=%+v, want a clean decline", raw, ok, err, result)
		}
		if n := srv.Requests(); n != 0 {
			t.Errorf("%s: the declined node cost %d requests, want 0", raw, n)
		}
	}
}

// Transaction-bearing nodes are still summarized one-shot.
func TestResolveFacetCounts_TransactionNodesStillSummarize(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{
		"ynab://personal/Consolidated/transactions/",
		"ynab://personal/Consolidated/accounts/Checking",
	} {
		_, ok, err := cutting_garden_plugins.ResolveFacetCounts(
			context.Background(), Plugin{}, mustParseURL(t, raw), nil,
		)
		if err != nil || !ok {
			t.Errorf("%s: ok=%v err=%v, want a summary", raw, ok, err)
		}
	}
}
