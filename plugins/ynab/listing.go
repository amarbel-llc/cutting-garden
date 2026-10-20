package ynab

import (
	"context"
	"fmt"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// DescribeListingFields declares the transaction node's display fields — the
// discoverability half of enrichment, symmetric with DescribeFacets: a consumer
// learns via describe_node_types which Node.Fields keys a transaction may
// carry. Hand-written (the SDK has no listing-field derivation helper) but a
// strict projection of the unified declaration, pinned by
// TestDescribeListingFields_ConsistentWithUnified. Nothing is Writable.
func (Plugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	return []cutting_garden_plugins.NodeTypeListingFields{{
		Tag: typeTransaction,
		Fields: []cutting_garden_plugins.ListingField{
			{Key: facetDate, Label: "Date"},
			{Key: facetAccount, Label: "Account"},
			{Key: facetCategory, Label: "Category"},
			{Key: facetPayee, Label: "Payee"},
			{Key: facetCleared, Label: "Cleared"},
			{Key: listingAmount, Label: "Amount"},
			{Key: listingMemo, Label: "Memo"},
		},
	}}
}

// transactionFields projects one transaction onto its listing fields. Empty
// values are omitted, matching the present-but-empty-omitted convention of the
// other plugins.
func transactionFields(t Transaction) map[string]any {
	fields := map[string]any{}
	put := func(key, value string) {
		if value != "" {
			fields[key] = value
		}
	}
	put(facetDate, t.Date)
	put(facetAccount, t.AccountName)
	if t.CategoryName != nil {
		put(facetCategory, *t.CategoryName)
	}
	if t.PayeeName != nil {
		put(facetPayee, *t.PayeeName)
	}
	put(facetCleared, t.Cleared)
	if t.AmountFormatted != "" {
		put(listingAmount, t.AmountFormatted)
	} else {
		put(listingAmount, fmt.Sprintf("%d", t.Amount))
	}
	if t.Memo != nil {
		put(listingMemo, *t.Memo)
	}
	return fields
}

// ListEnriched serves a transaction-bearing node's children enriched — each
// transaction with Facets and Fields populated and narrowed by filter — in the
// SAME set ListRoots returns for the same window (RFC 0012 §12.2). The date
// window is pushed down to the server: `date=2026-07` fetches July, not the
// default 30 days. It applies to the budget-wide section, one account and one
// category; every other kind reports ok=false so the framework falls back to
// ListRoots plus host-side handling.
func (Plugin) ListEnriched(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	if node == nil {
		return nil, false, errors.ErrorWithStackf("ynab plugin: ListEnriched requires a node URI")
	}
	ref, err := classifyURI(node)
	if err != nil {
		return nil, false, err
	}
	if !bearsTransactions(ref.kind) {
		return nil, false, nil
	}
	c, err := resolveClient(ref)
	if err != nil {
		return nil, false, err
	}
	budgetID, err := resolveBudgetID(ctx, c, ref)
	if err != nil {
		return nil, false, err
	}
	scope, err := transactionScope(ctx, c, budgetID, ref)
	if err != nil {
		return nil, false, err
	}
	window, err := windowFromFilter(filter)
	if err != nil {
		return nil, false, err
	}
	nodes, err := transactionNodes(
		ctx, c, ref, budgetID, scope, window.since, window.until, withoutDate(filter),
	)
	if err != nil {
		return nil, false, err
	}
	return nodes, true, nil
}
