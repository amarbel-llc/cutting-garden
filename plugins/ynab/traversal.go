package ynab

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeBudget is a YNAB budget — a container of the three sections.
	typeBudget = "cutting_garden-ynab-budget-v1"
	// typeSection is one of a budget's three listing containers
	// (accounts/, categories/, transactions/).
	typeSection = "cutting_garden-ynab-section-v1"
	// typeAccount is a budget account — a container of its transactions.
	typeAccount = "cutting_garden-ynab-account-v1"
	// typeCategoryGroup is a category group — a container of its categories.
	typeCategoryGroup = "cutting_garden-ynab-category_group-v1"
	// typeCategory is a category — a container of its transactions.
	typeCategory = "cutting_garden-ynab-category-v1"
	// typeTransaction is one transaction — a leaf whose body is its JSON.
	typeTransaction = "cutting_garden-ynab-transaction-v1"
)

// mimeJSON is the content type of a transaction leaf's body.
const mimeJSON = "application/json"

// defaultWindowDays is how far back an unbounded transaction listing looks.
// YNAB has no paging, only date bounds (its own default is one year), so the
// plugin's pages are date windows and the default is deliberately short
// (FDR 0027 §Paging).
const defaultWindowDays = 30

// now is the clock, a package var solely so tests can pin the window.
var now = time.Now

// defaultSince is the since_date of the default window, YYYY-MM-DD in UTC
// (YNAB dates are UTC).
func defaultSince() string {
	return now().UTC().AddDate(0, 0, -defaultWindowDays).Format("2006-01-02")
}

// Types declares the node types the ynab tree is built from. Tags are
// hyphenated and horizontally versioned (FDR 0018).
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeBudget, Container: true},
		{Tag: typeSection, Container: true},
		{Tag: typeAccount, Container: true},
		{Tag: typeCategoryGroup, Container: true},
		{Tag: typeCategory, Container: true},
		{Tag: typeTransaction, Container: false, MimeType: mimeJSON},
	}
}

// ListRoots returns the immediate children of node:
//
//   - account root → the account's budgets (archived ones included).
//   - budget → its three sections.
//   - accounts/ → the budget's accounts; categories/ → its category groups;
//     transactions/ → its transactions in the default window.
//   - account → that account's transactions in the default window.
//   - category group → its categories; category → its transactions in the
//     default window.
//   - transaction → none (a leaf).
func (Plugin) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("ynab plugin: ListRoots requires a node URI")
	}
	ref, err := classifyURI(node)
	if err != nil {
		return nil, err
	}
	c, err := resolveClient(ref)
	if err != nil {
		return nil, err
	}

	if ref.kind == kindAccountRoot {
		return budgetNodes(ctx, c, ref)
	}
	if ref.kind == kindBudget {
		// Confirm the budget exists so a mistyped or ambiguous name fails
		// here, not one level down. An id or alias costs no request.
		budgetID, err := resolveBudgetID(ctx, c, ref)
		if err != nil {
			return nil, err
		}
		// A budget disambiguated by `?id=` has no room for that query on its
		// children, so they are minted under the resolved id as the segment.
		segment := ref.budget
		if ref.id != "" {
			segment = budgetID
		}
		return sectionNodes(ref, segment), nil
	}
	if ref.kind == kindTransaction {
		return nil, nil
	}

	budgetID, err := resolveBudgetID(ctx, c, ref)
	if err != nil {
		return nil, err
	}
	switch ref.kind {
	case kindAccounts:
		return accountNodes(ctx, c, ref, budgetID)
	case kindCategories:
		return categoryGroupNodes(ctx, c, ref, budgetID)
	case kindTransactions, kindAccount, kindCategory:
		scope, err := transactionScope(ctx, c, budgetID, ref)
		if err != nil {
			return nil, err
		}
		return transactionNodes(ctx, c, ref, budgetID, scope, dateWindow{since: defaultSince()}, nil)
	case kindCategoryGroup:
		group, err := resolveCategoryGroup(ctx, c, budgetID, ref)
		if err != nil {
			return nil, err
		}
		return categoryNodes(ref, group), nil
	}
	return nil, nil
}

// bearsTransactions reports whether a node kind lists transactions: the
// budget-wide section, one account, or one category. Only these have the
// transaction facets and the date window.
func bearsTransactions(kind nodeKind) bool {
	return kind == kindTransactions || kind == kindAccount || kind == kindCategory
}

// transactionScope resolves which YNAB transactions endpoint a
// transaction-bearing node reads: "" for the budget-wide listing, else the
// per-account or per-category path, which serves a drill-down in one request
// instead of filtering the whole budget.
func transactionScope(
	ctx context.Context, c *client, budgetID string, ref nodeRef,
) (string, error) {
	switch ref.kind {
	case kindTransactions:
		return "", nil
	case kindAccount:
		account, err := resolveAccount(ctx, c, budgetID, ref)
		if err != nil {
			return "", err
		}
		return "accounts/" + url.PathEscape(account.ID), nil
	case kindCategory:
		group, err := resolveCategoryGroup(ctx, c, budgetID, ref)
		if err != nil {
			return "", err
		}
		category, err := resolveCategory(group, ref)
		if err != nil {
			return "", err
		}
		return "categories/" + url.PathEscape(category.ID), nil
	}
	return "", errors.ErrorWithStackf("ynab plugin: %d is not a transaction-bearing node", ref.kind)
}

// budgetNodes lists the account's budgets. Two budgets sharing a name are
// minted with their id as the path segment, since a deeper URI has no room
// for an `?id=` tiebreak on the budget.
func budgetNodes(
	ctx context.Context, c *client, ref nodeRef,
) ([]cutting_garden_plugins.Node, error) {
	budgets, err := c.budgets(ctx)
	if err != nil {
		return nil, err
	}
	dups := repeatedNames(budgets, func(b Budget) string { return b.Name })
	nodes := make([]cutting_garden_plugins.Node, 0, len(budgets))
	for _, b := range budgets {
		// The id is the segment when the name is shared, or when it would be
		// mistaken for an alias or an id on the way back in.
		segment := b.Name
		if dups[b.Name] || budgetAliases[b.Name] || isUUID(b.Name) {
			segment = b.ID
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  budgetURI(ref.account, segment),
			Name: b.Name,
			Type: typeBudget,
		})
	}
	return nodes, nil
}

// sectionNodes is a budget's three fixed listing containers, minted under
// budget (the name or id segment the children are addressed by).
func sectionNodes(ref nodeRef, budget string) []cutting_garden_plugins.Node {
	sections := []string{sectionAccounts, sectionCategories, sectionTransactions}
	nodes := make([]cutting_garden_plugins.Node, 0, len(sections))
	for _, s := range sections {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  sectionURI(ref.account, budget, s),
			Name: s,
			Type: typeSection,
		})
	}
	return nodes
}

func accountNodes(
	ctx context.Context, c *client, ref nodeRef, budgetID string,
) ([]cutting_garden_plugins.Node, error) {
	accounts, err := c.accounts(ctx, budgetID)
	if err != nil {
		return nil, err
	}
	accounts = liveAccounts(accounts)
	dups := repeatedNames(accounts, func(a Account) string { return a.Name })
	nodes := make([]cutting_garden_plugins.Node, 0, len(accounts))
	for _, a := range accounts {
		id := ""
		if dups[a.Name] {
			id = a.ID
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  accountNodeURI(ref.account, ref.budget, a.Name, id),
			Name: a.Name,
			Type: typeAccount,
		})
	}
	return nodes, nil
}

func categoryGroupNodes(
	ctx context.Context, c *client, ref nodeRef, budgetID string,
) ([]cutting_garden_plugins.Node, error) {
	groups, err := c.categoryGroups(ctx, budgetID)
	if err != nil {
		return nil, err
	}
	groups = liveGroups(groups)
	dups := repeatedNames(groups, func(g CategoryGroup) string { return g.Name })
	nodes := make([]cutting_garden_plugins.Node, 0, len(groups))
	for _, g := range groups {
		// A shared group name is minted as the group's id SEGMENT, not an
		// `?id=` tiebreak: the category URIs below it reuse the segment, and
		// their own `?id=` slot belongs to the category.
		segment := g.Name
		if dups[g.Name] {
			segment = g.ID
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  categoryGroupURI(ref.account, ref.budget, segment, ""),
			Name: g.Name,
			Type: typeCategoryGroup,
		})
	}
	return nodes, nil
}

// categoryNodes lists a group's categories from the group already in hand.
func categoryNodes(ref nodeRef, group CategoryGroup) []cutting_garden_plugins.Node {
	categories := liveCategories(group.Categories)
	dups := repeatedNames(categories, func(c Category) string { return c.Name })
	nodes := make([]cutting_garden_plugins.Node, 0, len(categories))
	for _, c := range categories {
		id := ""
		if dups[c.Name] {
			id = c.ID
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  categoryURI(ref.account, ref.budget, ref.group, c.Name, id),
			Name: c.Name,
			Type: typeCategory,
		})
	}
	return nodes
}

// transactionNodes lists the transactions in [since, until] as leaf nodes
// carrying their Facets and listing Fields (all derived from the rows already
// in hand: no per-node fetch, RFC 0012's cheapness rule), keeping only those
// that satisfy filter (nil keeps all). scope is "" for the whole budget, or
// the per-account / per-category endpoint path.
func transactionNodes(
	ctx context.Context, c *client, ref nodeRef, budgetID, scope string,
	window dateWindow, filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, error) {
	if window.empty() {
		return nil, nil
	}
	txns, err := c.transactions(ctx, budgetID, scope, window.since, window.until)
	if err != nil {
		return nil, err
	}
	visible := visibleTransactions(txns, window)
	nodes := make([]cutting_garden_plugins.Node, 0, len(visible))
	for _, v := range visible {
		facets := transactionFacets(v.row)
		if !filter.Matches(facets) {
			continue
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    transactionURI(ref.account, ref.budget, v.id),
			Name:   v.name,
			Type:   typeTransaction,
			Facets: facets,
			Fields: transactionFields(v.row),
		})
	}
	return nodes, nil
}

// visibleTransaction is one transaction as a node sees it: the id it is
// addressed by, its display name, and the row its facets derive from.
type visibleTransaction struct {
	id, name string
	row      Transaction
}

// visibleTransactions is THE set both the listing and the counting path
// describe (RFC 0012 §12.2), so a summary always totals what the listing shows:
// rows inside the window (re-checked locally, since the date predicate is
// dropped once pushed down), not deleted, addressable, and one per node.
//
// The per-category endpoint returns a split's leg as its own row (Type
// "subtransaction") whose id is NOT readable as a transaction, so a leg is
// addressed by its real parent and labelled as a split leg; two legs of one
// split in the same category share that parent and are ONE node. A leg whose
// parent is missing cannot be addressed at all and is skipped. The first row
// wins, so a split's facet values come from its first leg in the category.
func visibleTransactions(txns []Transaction, window dateWindow) []visibleTransaction {
	out := make([]visibleTransaction, 0, len(txns))
	seen := make(map[string]bool, len(txns))
	for _, t := range txns {
		if t.Deleted || !window.contains(t.Date) {
			continue
		}
		id, name := t.ID, transactionName(t)
		if t.Type == "subtransaction" {
			if t.ParentTransactionID == nil || *t.ParentTransactionID == "" {
				continue
			}
			id, name = *t.ParentTransactionID, name+" (split)"
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, visibleTransaction{id: id, name: name, row: t})
	}
	return out
}

// transactionName is a transaction's display label: date, payee, amount.
func transactionName(t Transaction) string {
	parts := []string{t.Date}
	if t.PayeeName != nil && *t.PayeeName != "" {
		parts = append(parts, *t.PayeeName)
	}
	parts = append(parts, amountLabel(t))
	return strings.Join(parts, " ")
}

// amountLabel is a transaction's amount as shown: YNAB's own formatted string
// (in the budget's currency format) when the response carries one, else the
// milliunits as plain units. It never shows raw milliunits, which next to
// formatted rows would read as a thousandfold error.
func amountLabel(t Transaction) string {
	if t.AmountFormatted != "" {
		return t.AmountFormatted
	}
	return formatMilliunits(t.Amount)
}

// formatMilliunits renders a milliunit amount as units with two decimals, or
// three when the amount has a sub-cent part ("-12340" → "-12.34", "1" →
// "0.001"). It carries no currency symbol: the response did not say which.
func formatMilliunits(m int64) string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	whole, frac := m/1000, m%1000
	if frac%10 == 0 {
		return fmt.Sprintf("%s%d.%02d", sign, whole, frac/10)
	}
	return fmt.Sprintf("%s%d.%03d", sign, whole, frac)
}
