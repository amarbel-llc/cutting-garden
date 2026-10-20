package ynab

import (
	"context"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const txnPrefix = "ynab://personal/Consolidated/transactions/"

func patchTxn(t *testing.T, id, body string) ([]string, error) {
	t.Helper()
	return (Plugin{}).PatchNode(context.Background(), mustParseURL(t, txnPrefix+id), strings.NewReader(body))
}

func mustPatch(t *testing.T, id, body string) []string {
	t.Helper()
	applied, err := patchTxn(t, id, body)
	if err != nil {
		t.Fatalf("PatchNode(%s, %s): %v", id, body, err)
	}
	return applied
}

func mustRefuse(t *testing.T, srv *ynabtestserver.Server, id, body string) {
	t.Helper()
	before := srv.Writes()
	_, err := patchTxn(t, id, body)
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("PatchNode(%s, %s): want a 400 refusal, got %v", id, body, err)
	}
	if srv.Writes() != before {
		t.Errorf("PatchNode(%s, %s) was refused but a write reached the server", id, body)
	}
}

func TestPatchNode_CategoryByBareName(t *testing.T) {
	srv := newFixture(t)
	if got := mustPatch(t, "t2", `{"category":"Dining Out"}`); !slices.Equal(got, []string{"category"}) {
		t.Errorf("applied = %v, want [category]", got)
	}
	saved, _ := srv.TransactionByID("b1", "t2")
	if saved.CategoryID == nil || *saved.CategoryID != "c2" || *saved.CategoryName != "Dining Out" {
		t.Errorf("server holds category %v/%v, want c2/Dining Out", saved.CategoryID, saved.CategoryName)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want exactly 1", srv.Writes())
	}
}

func TestPatchNode_CategoryByPathAndByID(t *testing.T) {
	srv := newFixture(t)
	mustPatch(t, "t2", `{"category":"Bills/Rent"}`)
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.CategoryID != "c3" {
		t.Errorf("Group/Category resolved to %v, want c3", *saved.CategoryID)
	}
	mustPatch(t, "t2", `{"category":"c1"}`)
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.CategoryID != "c1" {
		t.Errorf("an id resolved to %v, want c1", *saved.CategoryID)
	}
}

// A bare name shared by two groups is refused with the qualified candidates, so
// a write never lands on a guess; the qualified form works.
func TestPatchNode_AmbiguousCategoryNameIsRefused(t *testing.T) {
	srv := newFixture(t)
	srv.AddCategoryGroup(ynabtestserver.CategoryGroup{BudgetID: "b1", ID: "g-fun", Name: "Fun"})
	srv.AddCategory(ynabtestserver.Category{BudgetID: "b1", GroupID: "g-fun", ID: "c-fun", Name: "Dining Out"})
	cacheReset()

	_, err := patchTxn(t, "t2", `{"category":"Dining Out"}`)
	if err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("want a 400 for an ambiguous name, got %v", err)
	}
	for _, want := range []string{"Food/Dining Out", "Fun/Dining Out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name candidate %q: %v", want, err)
		}
	}
	if srv.Writes() != 0 {
		t.Error("an ambiguous name must write nothing")
	}
	mustPatch(t, "t2", `{"category":"Fun/Dining Out"}`)
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.CategoryID != "c-fun" {
		t.Errorf("Fun/Dining Out resolved to %v, want c-fun", *saved.CategoryID)
	}
}

func TestPatchNode_MemoSetAndClear(t *testing.T) {
	srv := newFixture(t)
	if got := mustPatch(t, "t2", `{"memo":"weekly shop"}`); !slices.Equal(got, []string{"memo"}) {
		t.Errorf("applied = %v", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); saved.Memo == nil || *saved.Memo != "weekly shop" {
		t.Errorf("memo = %v, want set", saved.Memo)
	}
	if got := mustPatch(t, "t2", `{"memo":null}`); !slices.Equal(got, []string{"memo"}) {
		t.Errorf("clearing applied = %v", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); saved.Memo != nil {
		t.Errorf("memo = %q, want cleared", *saved.Memo)
	}
}

func TestPatchNode_ClearedToggles(t *testing.T) {
	srv := newFixture(t)
	if got := mustPatch(t, "t2", `{"cleared":"cleared"}`); !slices.Equal(got, []string{"cleared"}) {
		t.Errorf("applied = %v", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); saved.Cleared != "cleared" {
		t.Errorf("cleared = %q", saved.Cleared)
	}
}

// Several fields are ONE write, and applied is in a fixed order.
func TestPatchNode_SeveralFieldsAreOneWrite(t *testing.T) {
	srv := newFixture(t)
	got := mustPatch(t, "t2", `{"memo":"m","cleared":"cleared","category":"Rent"}`)
	if !slices.Equal(got, []string{"category", "cleared", "memo"}) {
		t.Errorf("applied = %v, want [category cleared memo]", got)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want 1", srv.Writes())
	}
}

// Values already in place apply nothing and send nothing, and say so with a
// non-nil EMPTY applied (cutting-garden#182).
func TestPatchNode_AlreadyAsRequestedWritesNothing(t *testing.T) {
	srv := newFixture(t)
	applied := mustPatch(t, "t1", `{"category":"Dining Out","cleared":"cleared"}`)
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}
	if srv.Writes() != 0 {
		t.Errorf("a no-op sent %d write(s)", srv.Writes())
	}
}

// A body naming nothing recognized costs no request at all, and reports honestly.
func TestPatchNode_UnrecognizedKeysOnlyCostNothing(t *testing.T) {
	srv := newFixture(t)
	applied := mustPatch(t, "t2", `{"payee":"Someone Else","amount":1}`)
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}
	if n := srv.Requests(); n != 0 {
		t.Errorf("an unrecognized body cost %d requests, want 0", n)
	}
}

func TestPatchNode_RefusesWhatItMustNotWrite(t *testing.T) {
	srv := newFixture(t)
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "tsplit", Date: "2026-10-19", Amount: -3000, AmountFormatted: "-$3.00",
		Cleared: "cleared", AccountID: "a1", AccountName: "Checking", CategoryName: str("Split"),
		Subtransactions: []any{map[string]any{"id": "leg-x", "transaction_id": "tsplit"}},
	})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "txfer", Date: "2026-10-19", Amount: -5000, AmountFormatted: "-$5.00",
		Cleared: "cleared", AccountID: "a1", AccountName: "Checking", TransferAccountID: str("a2"),
	})
	cacheReset()

	cases := map[string]struct{ id, body string }{
		"a split's category":           {"tsplit", `{"category":"Rent"}`},
		"a transfer's category":        {"txfer", `{"category":"Rent"}`},
		"a reconciled cleared state":   {"t3", `{"cleared":"uncleared"}`},
		"setting reconciled":           {"t2", `{"cleared":"reconciled"}`},
		"an unknown cleared value":     {"t2", `{"cleared":"maybe"}`},
		"an unknown category":          {"t2", `{"category":"Nonesuch"}`},
		"an empty category":            {"t2", `{"category":""}`},
		"a null category":              {"t2", `{"category":null}`},
		"a numeric category":           {"t2", `{"category":5}`},
		"a numeric memo":               {"t2", `{"memo":5}`},
		"a numeric cleared":            {"t2", `{"cleared":1}`},
		"an overlong memo":             {"t2", `{"memo":"` + strings.Repeat("x", maxMemoRunes+1) + `"}`},
		"invalid JSON":                 {"t2", `{"memo":`},
		"an empty body":                {"t2", ``},
		"a body that is not an object": {"t2", `["category"]`},
		"an unknown transaction":       {"nope", `{"memo":"x"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) { mustRefuse(t, srv, tc.id, tc.body) })
	}
}

// A refusal on one field refuses the whole patch: nothing partial is written.
func TestPatchNode_OneBadFieldWritesNone(t *testing.T) {
	srv := newFixture(t)
	mustRefuse(t, srv, "t3", `{"memo":"fine","cleared":"uncleared"}`) // t3 is reconciled
	if saved, _ := srv.TransactionByID("b1", "t3"); saved.Memo != nil {
		t.Errorf("the memo was written despite the refused cleared change: %q", *saved.Memo)
	}
}

func TestPatchNode_OnlyTransactionNodesArePatchable(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{
		"ynab://personal/",
		"ynab://personal/Consolidated/",
		"ynab://personal/Consolidated/accounts/Checking/",
		"ynab://personal/Consolidated/categories/Food/Dining%20Out/",
	} {
		_, err := (Plugin{}).PatchNode(context.Background(), mustParseURL(t, raw), strings.NewReader(`{"memo":"x"}`))
		if err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("PatchNode(%s): want a 400, got %v", raw, err)
		}
	}
}

func TestMutatorVerbsOtherThanPatchAreUnsupported(t *testing.T) {
	newFixture(t)
	u := mustParseURL(t, txnPrefix+"t2")
	if err := (Plugin{}).CreateNode(context.Background(), u, strings.NewReader(`{}`), typeTransaction); !errors.Is400BadRequest(err) {
		t.Errorf("CreateNode: want a 400, got %v", err)
	}
	if err := (Plugin{}).PutNode(context.Background(), u, strings.NewReader(`{}`)); !errors.Is400BadRequest(err) {
		t.Errorf("PutNode: want a 400, got %v", err)
	}
	if err := (Plugin{}).DeleteNode(context.Background(), u); !errors.Is400BadRequest(err) {
		t.Errorf("DeleteNode: want a 400, got %v", err)
	}
}

// A server that answers success without applying the change must not read as
// success (cutting-garden#188).
func TestPatchNode_AWriteTheServerIgnoredIsAnError(t *testing.T) {
	srv := newFixture(t)
	srv.IgnoreWrites(true)
	_, err := patchTxn(t, "t2", `{"category":"Dining Out"}`)
	if err == nil {
		t.Fatal("a write YNAB did not apply was reported as success")
	}
	if errors.Is400BadRequest(err) {
		t.Errorf("an unapplied write is a plugin/server failure, not a caller mistake: %v", err)
	}
	if !strings.Contains(err.Error(), "not reflected") {
		t.Errorf("error does not say the write was not reflected: %v", err)
	}
}

// After a write, a listing cached before it must not serve the old value.
func TestPatchNode_InvalidatesTheCacheSoReadsSeeTheWrite(t *testing.T) {
	newFixture(t)
	category := func() string {
		for _, n := range list(t, txnPrefix) {
			if strings.HasSuffix(n.URI.Path, "/t2") {
				return n.Facets[facetCategory][0].Key
			}
		}
		t.Fatal("t2 not listed")
		return ""
	}
	if got := category(); got != "Groceries" {
		t.Fatalf("t2 starts as %q, want Groceries", got)
	}
	mustPatch(t, "t2", `{"category":"Rent"}`)
	if got := category(); got != "Rent" {
		t.Errorf("t2 reads as %q after the write, want Rent (a stale listing was served)", got)
	}
}

// Request economy: patching costs a fresh read of the transaction (the guards
// must not trust a cached listing), the category lookup and the write.
func TestPatchNode_AfterAListingCostsThreeRequests(t *testing.T) {
	srv := newFixture(t)
	list(t, txnPrefix)
	before := srv.Requests()
	mustPatch(t, "t2", `{"category":"Rent"}`)
	if n := srv.Requests() - before; n != 3 {
		t.Errorf("patching a listed transaction cost %d requests, want 3 (fresh read + category tree + write)", n)
	}
}

func TestDescribeFacetWrites_CategoryAndClearedAreTheOnlyWritableDimensions(t *testing.T) {
	writes := (Plugin{}).DescribeFacetWrites()
	if err := cutting_garden_plugins.ValidateFacetWrites((Plugin{}).DescribeFacets(), writes); err != nil {
		t.Fatalf("write mappings do not cross-check against the facets: %v", err)
	}
	byKey := map[string]cutting_garden_plugins.FacetWrite{}
	for _, set := range writes {
		if set.Tag == typeTransaction {
			for _, w := range set.Writes {
				byKey[w.DimensionKey] = w
			}
		}
	}
	for _, key := range []string{facetCategory, facetCleared} {
		if byKey[key].Mode != cutting_garden_plugins.FacetWriteOne || byKey[key].Field != key {
			t.Errorf("%s write = %+v, want write:one onto its own field", key, byKey[key])
		}
	}
	for _, key := range []string{facetDate, facetAccount, facetPayee, facetApproved, facetFlag, facetTransfer, facetAmountBand} {
		if byKey[key].Mode != cutting_garden_plugins.FacetWriteNone {
			t.Errorf("%s must be an explicit write:none, got %+v", key, byKey[key])
		}
	}
	if got := byKey[facetCleared].Values; !slices.Equal(got, []string{"cleared", "uncleared"}) {
		t.Errorf("cleared move targets = %v; reconciled must not be offered", got)
	}
}

func TestBuildFacetWritePatch(t *testing.T) {
	ctx := context.Background()
	build := func(dim, bucket string) ([]byte, error) {
		return (Plugin{}).BuildFacetWritePatch(ctx, cutting_garden_plugins.Node{},
			cutting_garden_plugins.FacetWrite{DimensionKey: dim}, bucket)
	}
	if got, err := build(facetCategory, "Dining Out"); err != nil || string(got) != `{"category":"Dining Out"}` {
		t.Errorf("category body = %s, %v", got, err)
	}
	if got, err := build(facetCleared, "uncleared"); err != nil || string(got) != `{"cleared":"uncleared"}` {
		t.Errorf("cleared body = %s, %v", got, err)
	}
	for name, tc := range map[string]struct{ dim, bucket string }{
		"the synthetic uncategorized bucket": {facetCategory, valueUncategorized},
		"clearing the category":              {facetCategory, ""},
		"clearing cleared":                   {facetCleared, ""},
		"a read-only dimension":              {facetAccount, "Savings"},
	} {
		if _, err := build(tc.dim, tc.bucket); err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("%s: want a 400, got %v", name, err)
		}
	}
}

// The body organize builds is exactly what PatchNode accepts.
func TestBuildFacetWritePatch_FeedsPatchNode(t *testing.T) {
	srv := newFixture(t)
	body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), cutting_garden_plugins.Node{},
		cutting_garden_plugins.FacetWrite{DimensionKey: facetCategory}, "Rent")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustPatch(t, "t2", string(body)); !slices.Equal(got, []string{"category"}) {
		t.Errorf("applied = %v", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.CategoryID != "c3" {
		t.Errorf("category = %v, want c3", *saved.CategoryID)
	}
}
