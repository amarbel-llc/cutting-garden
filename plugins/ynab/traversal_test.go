package ynab

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const (
	testToken    = "tok-test"
	testTokenEnv = "YNAB_TEST_TOKEN"
	uuidBudget   = "33333333-3333-3333-3333-333333333333"
)

func str(s string) *string { return &s }

// newFixture starts an in-memory YNAB server, wires the plugin at it through
// one configured account ("personal") and pins the clock to 2026-10-20, so the
// default 30-day window is 2026-09-20..now. The tree:
//
//	Consolidated (b1)            Archive (b2)
//	  accounts: Checking, Savings
//	  categories: Food{Groceries, Dining Out}, Bills{Rent}
//	  payees: p1 Corner Cafe, p2 Grocer, p3 Landlord, p4 Corner Cafe Downtown
//	          (unused), p5 "Transfer : Savings" (a transfer payee, to a2)
//	  transactions: t1 10-18 Checking/Dining Out (p1), t2 10-01 Checking/Groceries (p2),
//	                t3 08-01 Checking/Rent (p3, outside the window),
//	                t4 10-10 Savings/uncategorized (no payee)
func newFixture(t *testing.T) *ynabtestserver.Server {
	t.Helper()
	t.Setenv(testTokenEnv, testToken)
	cacheReset()
	t.Cleanup(cacheReset)
	srv := ynabtestserver.Start(testToken)
	t.Cleanup(srv.Close)

	acct := account("personal")
	acct.SessionURL = srv.BaseURL()
	setAccounts(t, acct)

	prev := now
	now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = prev })

	srv.AddBudget(ynabtestserver.Budget{ID: "b1", Name: "Consolidated"})
	srv.AddBudget(ynabtestserver.Budget{ID: "b2", Name: "Archive"})
	srv.AddAccount(ynabtestserver.Account{BudgetID: "b1", ID: "a1", Name: "Checking", Type: "checking", OnBudget: true})
	srv.AddAccount(ynabtestserver.Account{BudgetID: "b1", ID: "a2", Name: "Savings", Type: "savings", OnBudget: true})
	srv.AddCategoryGroup(ynabtestserver.CategoryGroup{BudgetID: "b1", ID: "g1", Name: "Food"})
	srv.AddCategoryGroup(ynabtestserver.CategoryGroup{BudgetID: "b1", ID: "g2", Name: "Bills"})
	srv.AddCategory(ynabtestserver.Category{BudgetID: "b1", GroupID: "g1", ID: "c1", Name: "Groceries"})
	srv.AddCategory(ynabtestserver.Category{BudgetID: "b1", GroupID: "g1", ID: "c2", Name: "Dining Out"})
	srv.AddCategory(ynabtestserver.Category{BudgetID: "b1", GroupID: "g2", ID: "c3", Name: "Rent"})
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p1", Name: "Corner Cafe"})
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p2", Name: "Grocer"})
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p3", Name: "Landlord"})
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p4", Name: "Corner Cafe Downtown"})
	srv.AddPayee(ynabtestserver.Payee{BudgetID: "b1", ID: "p5", Name: "Transfer : Savings", TransferAccountID: str("a2")})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "t1", Date: "2026-10-18", Amount: -12340, AmountFormatted: "-$12.34",
		Cleared: "cleared", AccountID: "a1", AccountName: "Checking",
		PayeeID: str("p1"), PayeeName: str("Corner Cafe"), CategoryID: str("c2"), CategoryName: str("Dining Out"),
	})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "t2", Date: "2026-10-01", Amount: -45000, AmountFormatted: "-$45.00",
		Cleared: "uncleared", AccountID: "a1", AccountName: "Checking",
		PayeeID: str("p2"), PayeeName: str("Grocer"), CategoryID: str("c1"), CategoryName: str("Groceries"),
	})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "t3", Date: "2026-08-01", Amount: -900000, AmountFormatted: "-$900.00",
		Cleared: "reconciled", AccountID: "a1", AccountName: "Checking",
		PayeeID: str("p3"), PayeeName: str("Landlord"), CategoryID: str("c3"), CategoryName: str("Rent"),
	})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "t4", Date: "2026-10-10", Amount: 100000, AmountFormatted: "$100.00",
		Cleared: "cleared", AccountID: "a2", AccountName: "Savings",
	})
	return srv
}

func list(t *testing.T, raw string) []cutting_garden_plugins.Node {
	t.Helper()
	nodes, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, raw))
	if err != nil {
		t.Fatalf("ListRoots(%s): %v", raw, err)
	}
	return nodes
}

func nodeNames(nodes []cutting_garden_plugins.Node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func nodeURIs(nodes []cutting_garden_plugins.Node) []string {
	uris := make([]string, len(nodes))
	for i, n := range nodes {
		uris[i] = n.URI.String()
	}
	return uris
}

func TestListRoots_AccountRootListsBudgetsInOneRequest(t *testing.T) {
	srv := newFixture(t)
	nodes := list(t, "ynab://personal/")
	if got, want := nodeNames(nodes), []string{"Consolidated", "Archive"}; !slices.Equal(got, want) {
		t.Errorf("budgets = %v, want %v", got, want)
	}
	if got, want := nodeURIs(nodes)[0], "ynab://personal/Consolidated/"; got != want {
		t.Errorf("first budget URI = %q, want %q", got, want)
	}
	if n := srv.Requests(); n != 1 {
		t.Errorf("account root cost %d requests, want 1", n)
	}
}

func TestListRoots_BudgetListsItsSections(t *testing.T) {
	srv := newFixture(t)
	nodes := list(t, "ynab://personal/Consolidated/")
	if got, want := nodeNames(nodes), []string{"accounts", "categories", "transactions", "payees"}; !slices.Equal(got, want) {
		t.Errorf("sections = %v, want %v", got, want)
	}
	// A name costs one request (the budget list) to prove the budget exists;
	// an alias or an id costs none.
	if n := srv.Requests(); n != 1 {
		t.Errorf("section listing by name cost %d requests, want 1", n)
	}
	before := srv.Requests()
	list(t, "ynab://personal/last-used/")
	list(t, "ynab://personal/"+uuidBudget+"/")
	if n := srv.Requests() - before; n != 0 {
		t.Errorf("section listing by alias/id cost %d requests, want 0", n)
	}
}

// A budget that does not exist (or whose name is ambiguous) fails at the
// budget level, not one level down.
func TestListRoots_BudgetListingRejectsUnknownAndAmbiguousNames(t *testing.T) {
	srv := newFixture(t)
	_, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, "ynab://personal/Nonesuch/"))
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("unknown budget: want a 400, got %v", err)
	}
	srv.AddBudget(ynabtestserver.Budget{ID: "b3", Name: "Consolidated"})
	cacheReset() // the budget list the first call cached predates b3
	_, err = (Plugin{}).ListRoots(context.Background(), mustParseURL(t, "ynab://personal/Consolidated/"))
	if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), "?id=") {
		t.Errorf("ambiguous budget: want a 400 naming ?id=, got %v", err)
	}
	if got := nodeNames(list(t, "ynab://personal/Consolidated/?id=b3")); len(got) != 4 {
		t.Errorf("?id= tiebreak at the budget level: sections = %v", got)
	}
}

// A budget whose name collides with an API alias is minted by id, or the name
// would resolve to the account's default budget instead.
func TestListRoots_BudgetNamedLikeAnAliasIsMintedByID(t *testing.T) {
	srv := newFixture(t)
	srv.AddBudget(ynabtestserver.Budget{ID: "b4", Name: "default"})
	srv.AddBudget(ynabtestserver.Budget{ID: "b5", Name: uuidBudget})
	uris := nodeURIs(list(t, "ynab://personal/"))
	for _, want := range []string{"ynab://personal/b4/", "ynab://personal/b5/"} {
		if !slices.Contains(uris, want) {
			t.Errorf("budget URIs %v missing %q", uris, want)
		}
	}
	for _, bad := range []string{"ynab://personal/default/", "ynab://personal/" + uuidBudget + "/"} {
		if slices.Contains(uris, bad) {
			t.Errorf("budget URIs %v contain the alias-shaped %q", uris, bad)
		}
	}
}

// The per-category endpoint returns a split's leg as a hybrid row with its
// own id; the node must address the readable PARENT transaction.
func TestListRoots_CategoryTransactionsAddressTheParentOfASplitLeg(t *testing.T) {
	srv := newFixture(t)
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "leg1", Date: "2026-10-19", Amount: -3000, AmountFormatted: "-$3.00",
		AccountID: "a1", AccountName: "Checking", PayeeName: str("Warehouse"),
		CategoryID: str("c2"), CategoryName: str("Dining Out"),
		Type: "subtransaction", ParentTransactionID: str("split1"), HybridOnly: true,
	})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "split1", Date: "2026-10-19", Amount: -9000, AmountFormatted: "-$9.00",
		AccountID: "a1", AccountName: "Checking", PayeeName: str("Warehouse"),
		CategoryName: str("Split"),
	})

	nodes := list(t, "ynab://personal/Consolidated/categories/Food/Dining%20Out/")
	uris := nodeURIs(nodes)
	want := "ynab://personal/Consolidated/transactions/split1"
	if !slices.Contains(uris, want) {
		t.Fatalf("category transactions %v do not address the parent %q", uris, want)
	}
	if slices.Contains(uris, "ynab://personal/Consolidated/transactions/leg1") {
		t.Errorf("category transactions mint the unreadable leg id: %v", uris)
	}
	i := slices.Index(uris, want)
	if !strings.HasSuffix(nodes[i].Name, "(split)") {
		t.Errorf("leg node name %q does not say it is a split leg", nodes[i].Name)
	}
	if _, ok, err := (Plugin{}).ReadLeaf(context.Background(), mustParseURL(t, want)); err != nil || !ok {
		t.Errorf("the parent URI must be readable: ok=%v err=%v", ok, err)
	}
}

// Two category groups sharing a name: every URI the plugin mints must list.
func TestListRoots_DuplicateCategoryGroupNamesStayNavigable(t *testing.T) {
	srv := newFixture(t)
	srv.AddCategoryGroup(ynabtestserver.CategoryGroup{BudgetID: "b1", ID: "g3", Name: "Food"})
	srv.AddCategory(ynabtestserver.Category{BudgetID: "b1", GroupID: "g3", ID: "c9", Name: "Snacks"})

	groups := list(t, "ynab://personal/Consolidated/categories/")
	uris := nodeURIs(groups)
	for _, want := range []string{
		"ynab://personal/Consolidated/categories/g1/",
		"ynab://personal/Consolidated/categories/g3/",
		"ynab://personal/Consolidated/categories/Bills/",
	} {
		if !slices.Contains(uris, want) {
			t.Errorf("group URIs %v missing %q", uris, want)
		}
	}
	for _, g := range groups {
		for _, c := range list(t, g.URI.String()) {
			// Following the minted category URI must not hit an ambiguity error.
			list(t, c.URI.String())
		}
	}
	if got := nodeNames(list(t, "ynab://personal/Consolidated/categories/g3/")); !slices.Equal(got, []string{"Snacks"}) {
		t.Errorf("second Food group categories = %v", got)
	}
}

func TestListRoots_Accounts(t *testing.T) {
	newFixture(t)
	nodes := list(t, "ynab://personal/Consolidated/accounts/")
	if got, want := nodeNames(nodes), []string{"Checking", "Savings"}; !slices.Equal(got, want) {
		t.Errorf("accounts = %v, want %v", got, want)
	}
	if got, want := nodeURIs(nodes)[0], "ynab://personal/Consolidated/accounts/Checking/"; got != want {
		t.Errorf("account URI = %q, want %q", got, want)
	}
}

func TestListRoots_CategoryGroupsThenCategories(t *testing.T) {
	newFixture(t)
	groups := list(t, "ynab://personal/Consolidated/categories/")
	if got, want := nodeNames(groups), []string{"Food", "Bills"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
	cats := list(t, "ynab://personal/Consolidated/categories/Food/")
	if got, want := nodeNames(cats), []string{"Groceries", "Dining Out"}; !slices.Equal(got, want) {
		t.Errorf("categories = %v, want %v", got, want)
	}
	if got, want := nodeURIs(cats)[1], "ynab://personal/Consolidated/categories/Food/Dining%20Out/"; got != want {
		t.Errorf("category URI = %q, want %q", got, want)
	}
}

func TestListRoots_TransactionsDefaultToTheLast30Days(t *testing.T) {
	newFixture(t)
	nodes := list(t, "ynab://personal/Consolidated/transactions/")
	want := []string{
		"2026-10-18 Corner Cafe -$12.34",
		"2026-10-01 Grocer -$45.00",
		"2026-10-10 $100.00", // no payee
	}
	if !slices.Equal(nodeNames(nodes), want) {
		t.Errorf("transactions = %v, want %v (t3, dated 2026-08-01, is outside the window)", nodeNames(nodes), want)
	}
	if got, want := nodeURIs(nodes)[0], "ynab://personal/Consolidated/transactions/t1"; got != want {
		t.Errorf("transaction URI = %q, want %q", got, want)
	}
	for _, n := range nodes {
		if n.Type != typeTransaction {
			t.Errorf("node %q type = %q", n.Name, n.Type)
		}
	}
}

func TestListRoots_AccountDrillDownUsesThePerAccountEndpoint(t *testing.T) {
	newFixture(t)
	nodes := list(t, "ynab://personal/Consolidated/accounts/Checking/")
	want := []string{"2026-10-18 Corner Cafe -$12.34", "2026-10-01 Grocer -$45.00"}
	if !slices.Equal(nodeNames(nodes), want) {
		t.Errorf("Checking transactions = %v, want %v", nodeNames(nodes), want)
	}
}

func TestListRoots_CategoryDrillDown(t *testing.T) {
	newFixture(t)
	nodes := list(t, "ynab://personal/Consolidated/categories/Food/Dining%20Out/")
	if got, want := nodeNames(nodes), []string{"2026-10-18 Corner Cafe -$12.34"}; !slices.Equal(got, want) {
		t.Errorf("Dining Out transactions = %v, want %v", got, want)
	}
}

func TestListRoots_TransactionLeafHasNoChildren(t *testing.T) {
	newFixture(t)
	if nodes := list(t, "ynab://personal/Consolidated/transactions/t1"); len(nodes) != 0 {
		t.Errorf("a transaction leaf listed %d children", len(nodes))
	}
}

func TestListRoots_EveryNodeTypeIsDeclared(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{
		"ynab://personal/",
		"ynab://personal/Consolidated/",
		"ynab://personal/Consolidated/accounts/",
		"ynab://personal/Consolidated/categories/",
		"ynab://personal/Consolidated/categories/Food/",
		"ynab://personal/Consolidated/transactions/",
	} {
		for _, n := range list(t, raw) {
			if _, ok := cutting_garden_plugins.NodeTypeFor(Plugin{}, n.Type); !ok {
				t.Errorf("%s: node %q has undeclared type %q", raw, n.Name, n.Type)
			}
		}
	}
}

func TestListRoots_BudgetAliasAndIDPassThroughWithoutALookup(t *testing.T) {
	srv := newFixture(t)
	srv.AddBudget(ynabtestserver.Budget{ID: uuidBudget, Name: "Uuid"})
	srv.AddAccount(ynabtestserver.Account{BudgetID: uuidBudget, ID: "u1", Name: "Cash"})

	before := srv.Requests()
	if got := nodeNames(list(t, "ynab://personal/last-used/accounts/")); !slices.Equal(got, []string{"Checking", "Savings"}) {
		t.Errorf("last-used accounts = %v", got)
	}
	if n := srv.Requests() - before; n != 1 {
		t.Errorf("last-used accounts cost %d requests, want 1 (no budget lookup)", n)
	}

	before = srv.Requests()
	if got := nodeNames(list(t, "ynab://personal/"+uuidBudget+"/accounts/")); !slices.Equal(got, []string{"Cash"}) {
		t.Errorf("uuid accounts = %v", got)
	}
	if n := srv.Requests() - before; n != 1 {
		t.Errorf("uuid accounts cost %d requests, want 1 (no budget lookup)", n)
	}
}

func TestListRoots_DuplicateBudgetNamesMintTheIDAsTheSegment(t *testing.T) {
	srv := newFixture(t)
	srv.AddBudget(ynabtestserver.Budget{ID: "b3", Name: "Consolidated"})
	srv.AddAccount(ynabtestserver.Account{BudgetID: "b3", ID: "a9", Name: "Offshore"})

	uris := nodeURIs(list(t, "ynab://personal/"))
	for _, want := range []string{"ynab://personal/b1/", "ynab://personal/b3/", "ynab://personal/Archive/"} {
		if !slices.Contains(uris, want) {
			t.Errorf("budget URIs %v missing %q", uris, want)
		}
	}

	// The id segment resolves; the ambiguous name is refused with a hint.
	if got := nodeNames(list(t, "ynab://personal/b3/accounts/")); !slices.Equal(got, []string{"Offshore"}) {
		t.Errorf("b3 accounts = %v", got)
	}
	_, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, "ynab://personal/Consolidated/accounts/"))
	if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), "?id=") {
		t.Errorf("ambiguous budget name: want a 400 naming ?id=, got %v", err)
	}
}

func TestListRoots_DuplicateAccountNamesMintAnIDTiebreak(t *testing.T) {
	srv := newFixture(t)
	srv.AddAccount(ynabtestserver.Account{BudgetID: "b1", ID: "a3", Name: "Checking"})
	srv.AddTransaction(ynabtestserver.Transaction{
		BudgetID: "b1", ID: "t5", Date: "2026-10-19", Amount: -1000, AmountFormatted: "-$1.00",
		AccountID: "a3", AccountName: "Checking", PayeeName: str("Second"),
	})

	uris := nodeURIs(list(t, "ynab://personal/Consolidated/accounts/"))
	for _, want := range []string{
		"ynab://personal/Consolidated/accounts/Checking/?id=a1",
		"ynab://personal/Consolidated/accounts/Checking/?id=a3",
	} {
		if !slices.Contains(uris, want) {
			t.Errorf("account URIs %v missing %q", uris, want)
		}
	}
	got := nodeNames(list(t, "ynab://personal/Consolidated/accounts/Checking/?id=a3"))
	if !slices.Equal(got, []string{"2026-10-19 Second -$1.00"}) {
		t.Errorf("second Checking transactions = %v", got)
	}
}

func TestListRoots_UnknownNamesAreBadRequests(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{
		"ynab://personal/Nonesuch/accounts/",
		"ynab://personal/Consolidated/accounts/Nonesuch/",
		"ynab://personal/Consolidated/categories/Nonesuch/",
		"ynab://personal/Consolidated/categories/Food/Nonesuch/",
	} {
		_, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, raw))
		if err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("ListRoots(%s): want a 400, got %v", raw, err)
		}
	}
}

func TestListRoots_RejectedTokenSurfacesAsATokenProblem(t *testing.T) {
	newFixture(t)
	t.Setenv(testTokenEnv, "wrong")
	_, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, "ynab://personal/"))
	if err == nil {
		t.Fatal("want an error for a rejected token")
	}
	if errors.Is400BadRequest(err) {
		t.Errorf("a rejected token is not a caller mistake: %v", err)
	}
	if !strings.Contains(err.Error(), "access token was rejected") {
		t.Errorf("error does not name the token: %v", err)
	}
}

func TestReadLeaf_TransactionReturnsItsJSON(t *testing.T) {
	newFixture(t)
	content, ok, err := (Plugin{}).ReadLeaf(
		context.Background(), mustParseURL(t, "ynab://personal/Consolidated/transactions/t1"),
	)
	if err != nil || !ok {
		t.Fatalf("ReadLeaf: ok=%v err=%v", ok, err)
	}
	txn, isTxn := content.Structured.(Transaction)
	if !isTxn || txn.ID != "t1" || txn.Amount != -12340 {
		t.Errorf("Structured = %#v", content.Structured)
	}
	if content.RawMimeType != mimeJSON {
		t.Errorf("RawMimeType = %q", content.RawMimeType)
	}
	var raw Transaction
	if err := json.Unmarshal(content.Raw, &raw); err != nil || raw.ID != "t1" {
		t.Errorf("Raw is not the transaction JSON: %v %s", err, content.Raw)
	}
}

func TestReadLeaf_OtherNodeKindsAreNotLeaves(t *testing.T) {
	newFixture(t)
	for _, raw := range []string{
		"ynab://personal/",
		"ynab://personal/Consolidated/",
		"ynab://personal/Consolidated/accounts/Checking/",
	} {
		_, ok, err := (Plugin{}).ReadLeaf(context.Background(), mustParseURL(t, raw))
		if err != nil || ok {
			t.Errorf("ReadLeaf(%s) = ok %v, err %v; want not a leaf", raw, ok, err)
		}
	}
}

func TestReadLeaf_UnknownTransactionIsABadRequest(t *testing.T) {
	newFixture(t)
	_, _, err := (Plugin{}).ReadLeaf(
		context.Background(), mustParseURL(t, "ynab://personal/Consolidated/transactions/nope"),
	)
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("want a 400 for an unknown transaction, got %v", err)
	}
}
