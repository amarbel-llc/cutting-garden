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
		if _, err := resolveBudgetID(ctx, c, ref); err != nil {
			return nil, err
		}
		return sectionNodes(ref), nil
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
		return transactionNodes(ctx, c, ref, budgetID, scope, defaultSince(), "", nil)
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

// sectionNodes is a budget's three fixed listing containers.
func sectionNodes(ref nodeRef) []cutting_garden_plugins.Node {
	sections := []string{sectionAccounts, sectionCategories, sectionTransactions}
	nodes := make([]cutting_garden_plugins.Node, 0, len(sections))
	for _, s := range sections {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  sectionURI(ref.account, ref.budget, s),
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
	ctx context.Context, c *client, ref nodeRef, budgetID, scope, since, until string,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, error) {
	txns, err := c.transactions(ctx, budgetID, scope, since, until)
	if err != nil {
		return nil, err
	}
	nodes := make([]cutting_garden_plugins.Node, 0, len(txns))
	seen := make(map[string]bool, len(txns))
	for _, t := range txns {
		if t.Deleted {
			continue
		}
		facets := transactionFacets(t)
		if !filter.Matches(facets) {
			continue
		}
		// The per-category endpoint returns a split's leg as its own row
		// (Type "subtransaction") whose id is NOT readable as a transaction;
		// address the real parent transaction instead and say it is a leg.
		// Two legs of one split in the same category share that parent, so a
		// listing keeps the first (the counting path still counts each leg).
		id, name := t.ID, transactionName(t)
		if t.Type == "subtransaction" && t.ParentTransactionID != nil {
			id, name = *t.ParentTransactionID, name+" (split)"
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    transactionURI(ref.account, ref.budget, id),
			Name:   name,
			Type:   typeTransaction,
			Facets: facets,
			Fields: transactionFields(t),
		})
	}
	return nodes, nil
}

// transactionName is a transaction's display label: date, payee, amount.
func transactionName(t Transaction) string {
	parts := []string{t.Date}
	if t.PayeeName != nil && *t.PayeeName != "" {
		parts = append(parts, *t.PayeeName)
	}
	if t.AmountFormatted != "" {
		parts = append(parts, t.AmountFormatted)
	} else {
		parts = append(parts, fmt.Sprintf("%d", t.Amount))
	}
	return strings.Join(parts, " ")
}
