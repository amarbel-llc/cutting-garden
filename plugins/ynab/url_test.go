package ynab

import (
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func TestClassifyURI_Kinds(t *testing.T) {
	cases := []struct {
		raw  string
		want nodeRef
	}{
		{"ynab://personal/", nodeRef{kind: kindAccountRoot}},
		{"ynab://personal", nodeRef{kind: kindAccountRoot}},
		{"ynab://personal/Consolidated/", nodeRef{kind: kindBudget, budget: "Consolidated"}},
		{"ynab://personal/Consolidated/accounts/", nodeRef{kind: kindAccounts, budget: "Consolidated"}},
		{"ynab://personal/Consolidated/categories/", nodeRef{kind: kindCategories, budget: "Consolidated"}},
		{"ynab://personal/Consolidated/transactions/", nodeRef{kind: kindTransactions, budget: "Consolidated"}},
		{
			"ynab://personal/Consolidated/accounts/Checking/",
			nodeRef{kind: kindAccount, budget: "Consolidated", accountName: "Checking"},
		},
		{
			"ynab://personal/Consolidated/categories/Food/",
			nodeRef{kind: kindCategoryGroup, budget: "Consolidated", group: "Food"},
		},
		{
			"ynab://personal/Consolidated/categories/Food/Dining%20Out/",
			nodeRef{kind: kindCategory, budget: "Consolidated", group: "Food", category: "Dining Out"},
		},
		{
			"ynab://personal/Consolidated/transactions/abc-123",
			nodeRef{kind: kindTransaction, budget: "Consolidated", txnID: "abc-123"},
		},
		{
			"ynab://personal/Consolidated/accounts/Checking/?id=acct-1",
			nodeRef{kind: kindAccount, budget: "Consolidated", accountName: "Checking", id: "acct-1"},
		},
		{"ynab://personal/last-used/", nodeRef{kind: kindBudget, budget: "last-used"}},
		{
			"ynab://personal/Consolidated/transactions/?date=2026-07",
			nodeRef{kind: kindTransactions, budget: "Consolidated", date: "2026-07"},
		},
		{
			"ynab://personal/Consolidated/accounts/Checking/?id=a1&date=2026",
			nodeRef{kind: kindAccount, budget: "Consolidated", accountName: "Checking", id: "a1", date: "2026"},
		},
	}
	for _, tc := range cases {
		got, err := classifyURI(mustParseURL(t, tc.raw))
		if err != nil {
			t.Errorf("classify %q: %v", tc.raw, err)
			continue
		}
		want := tc.want
		want.account = "personal"
		if got != want {
			t.Errorf("classify %q:\n got  %+v\n want %+v", tc.raw, got, want)
		}
	}
}

func TestClassifyURI_Rejections(t *testing.T) {
	for _, raw := range []string{
		"caldav://personal/",                             // wrong scheme
		"ynab:///Consolidated/",                          // empty account
		"ynab://personal/B/payees/",                      // unknown section
		"ynab://personal/B/accounts/A/extra/",            // too deep under accounts
		"ynab://personal/B/categories/G/C/extra/",        // too deep under categories
		"ynab://personal/B/transactions/t1/extra",        // too deep under transactions
		"ynab://personal/B/transactions/?date=yesterday", // malformed window
		"ynab://personal/B/categories/?date=2026",        // window on a node with no transactions
		"ynab://personal/B/?date=2026",                   // window on a budget
	} {
		_, err := classifyURI(mustParseURL(t, raw))
		if err == nil {
			t.Errorf("classify %q = nil error, want rejection", raw)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("classify %q: not a bad request: %v", raw, err)
		}
	}
}

func TestURIMinting_Spelling(t *testing.T) {
	cases := []struct{ got, want string }{
		{accountRootURI("personal").String(), "ynab://personal/"},
		{budgetURI("personal", "Consolidated").String(), "ynab://personal/Consolidated/"},
		{budgetURI("personal", "Big Budget").String(), "ynab://personal/Big%20Budget/"},
		{sectionURI("personal", "B", sectionTransactions).String(), "ynab://personal/B/transactions/"},
		{accountNodeURI("personal", "B", "Checking", "").String(), "ynab://personal/B/accounts/Checking/"},
		{accountNodeURI("personal", "B", "Checking", "a1").String(), "ynab://personal/B/accounts/Checking/?id=a1"},
		{categoryGroupURI("personal", "B", "Food", "").String(), "ynab://personal/B/categories/Food/"},
		{categoryURI("personal", "B", "Food", "Dining Out", "").String(), "ynab://personal/B/categories/Food/Dining%20Out/"},
		{transactionURI("personal", "B", "t1").String(), "ynab://personal/B/transactions/t1"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("minted %q, want %q", tc.got, tc.want)
		}
	}
}

// Every minted URI classifies back to the node it was minted for, including a
// name that carries a slash (it must stay one segment).
func TestURIMinting_RoundTrips(t *testing.T) {
	ref, err := classifyURI(categoryURI("personal", "B/C", "Food & Drink", "Dining/Out", "c9"))
	if err != nil {
		t.Fatal(err)
	}
	want := nodeRef{
		kind: kindCategory, account: "personal", budget: "B/C",
		group: "Food & Drink", category: "Dining/Out", id: "c9",
	}
	if ref != want {
		t.Errorf("round-trip:\n got  %+v\n want %+v", ref, want)
	}

	ref, err = classifyURI(transactionURI("personal", "B", "t1"))
	if err != nil || ref.kind != kindTransaction || ref.txnID != "t1" {
		t.Fatalf("transaction round-trip: %+v err=%v", ref, err)
	}
}
