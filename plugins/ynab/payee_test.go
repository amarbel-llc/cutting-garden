package ynab

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const payeePrefix = "ynab://personal/Consolidated/payees/"

func patchPayeeNode(t *testing.T, id, body string) ([]string, error) {
	t.Helper()
	return (Plugin{}).PatchNode(context.Background(), mustParseURL(t, payeePrefix+id), strings.NewReader(body))
}

func mustRename(t *testing.T, id, body string) []string {
	t.Helper()
	applied, err := patchPayeeNode(t, id, body)
	if err != nil {
		t.Fatalf("PatchNode(payee %s, %s): %v", id, body, err)
	}
	return applied
}

func mustRefusePayee(t *testing.T, srv *ynabtestserver.Server, id, body string) {
	t.Helper()
	before := srv.Writes()
	_, err := patchPayeeNode(t, id, body)
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("PatchNode(payee %s, %s): want a 400 refusal, got %v", id, body, err)
	}
	if srv.Writes() != before {
		t.Errorf("PatchNode(payee %s, %s) was refused but a write reached the server", id, body)
	}
}

func TestClassifyURI_Payees(t *testing.T) {
	ref, err := classifyURI(mustParseURL(t, "ynab://personal/Consolidated/payees/"))
	if err != nil || ref.kind != kindPayees || ref.budget != "Consolidated" {
		t.Errorf("payees/ = %+v, %v; want kindPayees under Consolidated", ref, err)
	}
	ref, err = classifyURI(mustParseURL(t, "ynab://personal/Consolidated/payees/p1"))
	if err != nil || ref.kind != kindPayee || ref.payeeID != "p1" {
		t.Errorf("payees/p1 = %+v, %v; want kindPayee p1", ref, err)
	}
	if _, err := classifyURI(mustParseURL(t, "ynab://personal/Consolidated/payees/p1/x")); err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("payees/p1/x: want a 400, got %v", err)
	}
	// ?date= belongs to transaction-bearing nodes only.
	if _, err := classifyURI(mustParseURL(t, "ynab://personal/Consolidated/payees/?date=2026-07")); err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("payees/?date=: want a 400, got %v", err)
	}
}

// Payees list as leaves addressed by id (a rename must not move the URI), named
// by the payee; a deleted payee is not listed.
func TestListRoots_PayeesAreLeavesAddressedByID(t *testing.T) {
	srv := newFixture(t)
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p-gone", Name: "Gone Cafe", Deleted: true})
	cacheReset()

	nodes := list(t, payeePrefix)
	wantNames := []string{"Corner Cafe", "Grocer", "Landlord", "Corner Cafe Downtown", "Transfer : Savings"}
	if got := nodeNames(nodes); !slices.Equal(got, wantNames) {
		t.Errorf("payees = %v, want %v", got, wantNames)
	}
	if got, want := nodeURIs(nodes)[0], payeePrefix+"p1"; got != want {
		t.Errorf("first payee URI = %q, want %q", got, want)
	}
	for _, n := range nodes {
		if n.Type != typePayee {
			t.Errorf("%s has type %q, want %q", n.Name, n.Type, typePayee)
		}
	}
	// A payee is a leaf: nothing lists beneath it.
	if got := list(t, payeePrefix+"p1"); len(got) != 0 {
		t.Errorf("a payee listed %d children, want none", len(got))
	}
}

func TestReadLeaf_Payee(t *testing.T) {
	srv := newFixture(t)
	u := mustParseURL(t, payeePrefix+"p1")
	leaf, ok, err := (Plugin{}).ReadLeaf(context.Background(), u)
	if err != nil || !ok {
		t.Fatalf("ReadLeaf(payee) = %v, %v", ok, err)
	}
	var got Payee
	if err := json.Unmarshal(leaf.Raw, &got); err != nil || got.ID != "p1" || got.Name != "Corner Cafe" {
		t.Errorf("payee body = %s (%v), want p1 / Corner Cafe", leaf.Raw, err)
	}
	if leaf.RawMimeType != mimeJSON {
		t.Errorf("mime = %q, want %q", leaf.RawMimeType, mimeJSON)
	}
	// After a listing the payee is already in hand: reading it costs no request.
	list(t, payeePrefix)
	before := srv.Requests()
	if _, ok, err := (Plugin{}).ReadLeaf(context.Background(), u); err != nil || !ok {
		t.Fatalf("ReadLeaf after a listing = %v, %v", ok, err)
	}
	if n := srv.Requests() - before; n != 0 {
		t.Errorf("reading a listed payee cost %d requests, want 0", n)
	}
	// An unknown payee is a caller mistake.
	if _, _, err := (Plugin{}).ReadLeaf(context.Background(), mustParseURL(t, payeePrefix+"nope")); err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("unknown payee: want a 400, got %v", err)
	}
}

func TestRenamePayee(t *testing.T) {
	srv := newFixture(t)
	if got := mustRename(t, "p1", `{"name":"Corner Café"}`); !slices.Equal(got, []string{"name"}) {
		t.Errorf("applied = %v, want [name]", got)
	}
	if saved, _ := srv.PayeeByID("b1", "p1"); saved.Name != "Corner Café" {
		t.Errorf("server holds payee name %q, want Corner Café", saved.Name)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want exactly 1", srv.Writes())
	}
}

// A rename must not leave a listing cached before it serving the old name, on
// the payee list OR on the transactions that carry the payee's name.
func TestRenamePayee_InvalidatesTheCachesSoReadsSeeIt(t *testing.T) {
	newFixture(t)
	if got := nodeNames(list(t, payeePrefix))[0]; got != "Corner Cafe" {
		t.Fatalf("first payee starts as %q", got)
	}
	payeeOf := func() string {
		for _, n := range list(t, txnPrefix) {
			if strings.HasSuffix(n.URI.Path, "/t1") {
				return n.Facets[facetPayee][0].Key
			}
		}
		t.Fatal("t1 not listed")
		return ""
	}
	if got := payeeOf(); got != "Corner Cafe" {
		t.Fatalf("t1 starts under payee %q", got)
	}

	mustRename(t, "p1", `{"name":"Corner Café"}`)

	if got := nodeNames(list(t, payeePrefix))[0]; got != "Corner Café" {
		t.Errorf("the payee list reads %q after the rename (a stale list was served)", got)
	}
	if got := payeeOf(); got != "Corner Café" {
		t.Errorf("t1 reads under payee %q after the rename (a stale listing was served)", got)
	}
}

func TestRenamePayee_AlreadyNamedSoWritesNothing(t *testing.T) {
	srv := newFixture(t)
	applied := mustRename(t, "p1", `{"name":"Corner Cafe"}`)
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}
	if srv.Writes() != 0 {
		t.Errorf("a no-op sent %d write(s)", srv.Writes())
	}
}

func TestRenamePayee_UnrecognizedKeysOnlyCostNothing(t *testing.T) {
	srv := newFixture(t)
	applied := mustRename(t, "p1", `{"transfer_account_id":"a1","deleted":true}`)
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}
	if n := srv.Requests(); n != 0 {
		t.Errorf("an unrecognized body cost %d requests, want 0", n)
	}
}

func TestRenamePayee_RefusesWhatItMustNotWrite(t *testing.T) {
	srv := newFixture(t)
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p-gone", Name: "Gone Cafe", Deleted: true})
	cacheReset()

	cases := map[string]struct{ id, body string }{
		// YNAB does not document what a rename onto an existing name does, so the
		// plugin never asks: exact, and differing only in case or padding.
		"a name another payee holds":       {"p1", `{"name":"Grocer"}`},
		"a name differing only in case":    {"p1", `{"name":"grocer"}`},
		"a name differing only in padding": {"p1", `{"name":"  Grocer "}`},
		"a transfer payee":                 {"p5", `{"name":"Savings Transfers"}`},
		"an empty name":                    {"p1", `{"name":""}`},
		"a blank name":                     {"p1", `{"name":"   "}`},
		"a null name":                      {"p1", `{"name":null}`},
		"a numeric name":                   {"p1", `{"name":5}`},
		"invalid JSON":                     {"p1", `{"name":`},
		"an empty body":                    {"p1", ``},
		"a body that is not an object":     {"p1", `["name"]`},
		"an unknown payee":                 {"nope", `{"name":"X"}`},
		"a deleted payee":                  {"p-gone", `{"name":"X"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) { mustRefusePayee(t, srv, tc.id, tc.body) })
	}
}

// The refusal names the payee it would collide with and where merging happens.
func TestRenamePayee_CollisionNamesTheOtherPayee(t *testing.T) {
	srv := newFixture(t)
	_, err := patchPayeeNode(t, "p1", `{"name":"Grocer"}`)
	if err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("want a 400, got %v", err)
	}
	for _, want := range []string{"Grocer", "p2", "merging payees is done in YNAB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if srv.Writes() != 0 {
		t.Error("a collision must write nothing")
	}
}

// A deleted payee does not hold its name: a live payee may take it.
func TestRenamePayee_ADeletedPayeesNameIsFree(t *testing.T) {
	srv := newFixture(t)
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p-gone", Name: "Old Cafe", Deleted: true})
	cacheReset()
	mustRename(t, "p1", `{"name":"Old Cafe"}`)
	if saved, _ := srv.PayeeByID("b1", "p1"); saved.Name != "Old Cafe" {
		t.Errorf("payee name = %q, want Old Cafe", saved.Name)
	}
}

// A case-only change to the payee's OWN name is not a collision with itself.
func TestRenamePayee_CaseOnlyChangeOfItsOwnName(t *testing.T) {
	srv := newFixture(t)
	mustRename(t, "p1", `{"name":"CORNER CAFE"}`)
	if saved, _ := srv.PayeeByID("b1", "p1"); saved.Name != "CORNER CAFE" {
		t.Errorf("payee name = %q, want CORNER CAFE", saved.Name)
	}
}

func TestRenamePayee_AWriteTheServerIgnoredIsAnError(t *testing.T) {
	srv := newFixture(t)
	srv.IgnoreWrites(true)
	_, err := patchPayeeNode(t, "p1", `{"name":"Corner Café"}`)
	if err == nil {
		t.Fatal("a rename YNAB did not apply was reported as success")
	}
	if errors.Is400BadRequest(err) {
		t.Errorf("an unapplied write is a plugin/server failure, not a caller mistake: %v", err)
	}
	if !strings.Contains(err.Error(), "not reflected") {
		t.Errorf("error does not say the write was not reflected: %v", err)
	}
}

// Request economy: a rename is a fresh payee list (the guards must see names as
// they are now) and the write, nothing else, when the budget is an alias.
func TestRenamePayee_CostsTwoRequests(t *testing.T) {
	srv := newFixture(t)
	list(t, payeePrefix)
	before := srv.Requests()
	u := mustParseURL(t, "ynab://personal/last-used/payees/p1")
	if _, err := (Plugin{}).PatchNode(context.Background(), u, strings.NewReader(`{"name":"Corner Café"}`)); err != nil {
		t.Fatal(err)
	}
	if n := srv.Requests() - before; n != 2 {
		t.Errorf("renaming a listed payee cost %d requests, want 2 (fresh payee list + write)", n)
	}
}

func TestOnlyTransactionAndPayeeNodesArePatchable_PayeesSection(t *testing.T) {
	newFixture(t)
	_, err := (Plugin{}).PatchNode(context.Background(), mustParseURL(t, payeePrefix), strings.NewReader(`{"name":"x"}`))
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("PatchNode(payees/): want a 400, got %v", err)
	}
}

// --- a transaction's payee ---

func TestPatchNode_PayeeByNameAndByID(t *testing.T) {
	srv := newFixture(t)
	if got := mustPatch(t, "t2", `{"payee":"Landlord"}`); !slices.Equal(got, []string{"payee"}) {
		t.Errorf("applied = %v, want [payee]", got)
	}
	saved, _ := srv.TransactionByID("b1", "t2")
	if saved.PayeeID == nil || *saved.PayeeID != "p3" || *saved.PayeeName != "Landlord" {
		t.Errorf("server holds payee %v/%v, want p3/Landlord", saved.PayeeID, saved.PayeeName)
	}
	mustPatch(t, "t2", `{"payee":"p4"}`)
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.PayeeID != "p4" || *saved.PayeeName != "Corner Cafe Downtown" {
		t.Errorf("an id resolved to %v/%v, want p4/Corner Cafe Downtown", *saved.PayeeID, *saved.PayeeName)
	}
	if srv.Writes() != 2 {
		t.Errorf("writes = %d, want 2", srv.Writes())
	}
}

// A transaction with no payee may be given one.
func TestPatchNode_PayeeOnATransactionWithNone(t *testing.T) {
	srv := newFixture(t)
	if got := mustPatch(t, "t4", `{"payee":"Grocer"}`); !slices.Equal(got, []string{"payee"}) {
		t.Errorf("applied = %v, want [payee]", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t4"); saved.PayeeID == nil || *saved.PayeeID != "p2" {
		t.Errorf("payee id = %v, want p2", saved.PayeeID)
	}
}

// A payee that does not exist is refused, never created: sent as a name, YNAB
// would mint a payee for any typo.
func TestPatchNode_AnUnknownPayeeIsRefusedNotCreated(t *testing.T) {
	srv := newFixture(t)
	before := srv.PayeeCount("b1")
	mustRefuse(t, srv, "t2", `{"payee":"Corner Cafee"}`)
	if got := srv.PayeeCount("b1"); got != before {
		t.Errorf("payees went from %d to %d: a refused patch minted one", before, got)
	}
	_, err := patchTxn(t, "t2", `{"payee":"Corner Cafee"}`)
	if err == nil || !strings.Contains(err.Error(), "does not create payees") {
		t.Errorf("the refusal does not say payees are not created: %v", err)
	}
}

func TestPatchNode_AnAmbiguousPayeeNameIsRefusedWithTheIDs(t *testing.T) {
	srv := newFixture(t)
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p-dup", Name: "Grocer"})
	cacheReset()

	_, err := patchTxn(t, "t1", `{"payee":"Grocer"}`)
	if err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("want a 400 for an ambiguous name, got %v", err)
	}
	for _, want := range []string{"p2", "p-dup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name candidate %q: %v", want, err)
		}
	}
	if srv.Writes() != 0 {
		t.Error("an ambiguous name must write nothing")
	}
	mustPatch(t, "t1", `{"payee":"p-dup"}`) // the id disambiguates
	if saved, _ := srv.TransactionByID("b1", "t1"); *saved.PayeeID != "p-dup" {
		t.Errorf("payee = %v, want p-dup", *saved.PayeeID)
	}
}

func TestPatchNode_PayeeRefusals(t *testing.T) {
	srv := newFixture(t)
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "txfer", Date: "2026-10-19", Amount: -5000, AmountFormatted: "-$5.00",
		Cleared: "cleared", AccountID: "a1", AccountName: "Checking", TransferAccountID: str("a2"),
		PayeeID: str("p5"), PayeeName: str("Transfer : Savings"),
	})
	cacheReset()

	cases := map[string]struct{ id, body string }{
		"a transfer payee as the target":       {"t2", `{"payee":"Transfer : Savings"}`},
		"a transfer payee by id":               {"t2", `{"payee":"p5"}`},
		"repointing a transfer's own payee":    {"txfer", `{"payee":"Grocer"}`},
		"an empty payee":                       {"t2", `{"payee":""}`},
		"a blank payee":                        {"t2", `{"payee":"  "}`},
		"a null payee":                         {"t2", `{"payee":null}`},
		"a numeric payee":                      {"t2", `{"payee":5}`},
		"a payee on an unknown transaction":    {"nope", `{"payee":"Grocer"}`},
		"a payee beside a refused cleared set": {"t3", `{"payee":"Grocer","cleared":"uncleared"}`}, // t3 is reconciled
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) { mustRefuse(t, srv, tc.id, tc.body) })
	}
	// A reconciled transaction's PAYEE is not locked, only its cleared state:
	// the refusal above came from the cleared field, and nothing was written.
	if saved, _ := srv.TransactionByID("b1", "t3"); *saved.PayeeID != "p3" {
		t.Errorf("t3's payee moved to %v despite the refused patch", *saved.PayeeID)
	}
}

func TestPatchNode_AlreadyThatPayeeWritesNothing(t *testing.T) {
	srv := newFixture(t)
	applied := mustPatch(t, "t1", `{"payee":"Corner Cafe"}`)
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}
	if srv.Writes() != 0 {
		t.Errorf("a no-op sent %d write(s)", srv.Writes())
	}
}

// Every field, in one write, with the applied report in a fixed order.
func TestPatchNode_PayeeWithTheOtherFieldsIsOneWrite(t *testing.T) {
	srv := newFixture(t)
	got := mustPatch(t, "t2", `{"payee":"Landlord","memo":"m","cleared":"cleared","category":"Rent"}`)
	if !slices.Equal(got, []string{"category", "cleared", "memo", "payee"}) {
		t.Errorf("applied = %v, want [category cleared memo payee]", got)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want 1", srv.Writes())
	}
}

func TestPatchNode_APayeeWriteTheServerIgnoredIsAnError(t *testing.T) {
	srv := newFixture(t)
	srv.IgnoreWrites(true)
	_, err := patchTxn(t, "t2", `{"payee":"Landlord"}`)
	if err == nil || errors.Is400BadRequest(err) || !strings.Contains(err.Error(), "not reflected") {
		t.Errorf("want a non-400 'not reflected' error, got %v", err)
	}
}

// After a payee write, a listing cached before it must not serve the old payee.
func TestPatchNode_PayeeInvalidatesTheCache(t *testing.T) {
	newFixture(t)
	payeeOf := func() string {
		for _, n := range list(t, txnPrefix) {
			if strings.HasSuffix(n.URI.Path, "/t2") {
				return n.Facets[facetPayee][0].Key
			}
		}
		t.Fatal("t2 not listed")
		return ""
	}
	if got := payeeOf(); got != "Grocer" {
		t.Fatalf("t2 starts under payee %q", got)
	}
	mustPatch(t, "t2", `{"payee":"Landlord"}`)
	if got := payeeOf(); got != "Landlord" {
		t.Errorf("t2 reads under payee %q after the write (a stale listing was served)", got)
	}
}

// A payee cleanup in one organize pass: the body organize builds from a payee
// heading is exactly what PatchNode takes.
func TestBuildFacetWritePatch_PayeeFeedsPatchNode(t *testing.T) {
	srv := newFixture(t)
	body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), cutting_garden_plugins.Node{},
		cutting_garden_plugins.FacetWrite{DimensionKey: facetPayee}, "Landlord")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustPatch(t, "t2", string(body)); !slices.Equal(got, []string{"payee"}) {
		t.Errorf("applied = %v", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.PayeeID != "p3" {
		t.Errorf("payee = %v, want p3", *saved.PayeeID)
	}
}

// TestPayeeURI_EscapesTheID pins that a payee id with a reserved rune stays one
// segment.
func TestPayeeURI_EscapesTheID(t *testing.T) {
	u := payeeURI("personal", "Consolidated", "a/b c")
	if got := u.EscapedPath(); got != "/Consolidated/payees/a%2Fb%20c" {
		t.Errorf("escaped path = %q", got)
	}
	back, err := url.Parse(u.String())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := classifyURI(back)
	if err != nil || ref.kind != kindPayee || ref.payeeID != "a/b c" {
		t.Errorf("round trip = %+v, %v", ref, err)
	}
}
