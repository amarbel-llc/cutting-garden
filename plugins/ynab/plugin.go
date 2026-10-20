// Package ynab is cutting-garden's YNAB (You Need A Budget) traversal and
// facet plugin (FDR 0027). Registered for the `ynab` URI scheme, it makes a
// YNAB account a `list` / `mcp` / trellis substrate over the YNAB REST API:
// its budgets, their accounts, category groups, categories and transactions.
// It is read-only except for a transaction's category, cleared state and memo
// (write.go).
//
// Like fastmail it is a scheme-only plugin: it implements none of
// capture/restore/diff and registers via MustRegisterScheme (init.go). The
// `ynab://<account>/` URL's host slot names a config account, not a server
// (the API host is fixed), and the tree walk, leaf read and facets are
// reached by type assertion after registration.
package ynab

import (
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// schemeYNAB is the single URI scheme this plugin claims.
const schemeYNAB = "ynab"

// typeTagCaptureReceipt is the RESERVED capture-receipt type-tag (FDR 0018).
// The plugin captures nothing, but Plugin.TypeTag must return something
// stable, and reserving it keeps a later receipt shape from colliding with
// another plugin's namespace.
const typeTagCaptureReceipt = "cutting_garden-capture_receipt-ynab-v1"

// Plugin is the YNAB traversal, facet and transaction-patch backend. It is a
// zero-size value: all account config lives in package state
// (configuredAccounts, see config.go).
type Plugin struct{}

// Schemes returns the single `ynab` scheme. It claims no bare transport
// scheme: a YNAB endpoint is not distinguishable from any other https URL,
// so it must be opted into explicitly.
func (Plugin) Schemes() []string { return []string{schemeYNAB} }

// TypeTag returns the reserved capture-receipt tag.
func (Plugin) TypeTag() string { return typeTagCaptureReceipt }

// Validate rejects a node URI whose scheme this plugin does not claim, or
// whose account-name host is not a configured account — both CALLER
// mistakes, so they classify as bad requests. It is structural only (no
// network), so it is safe to call during argument classification.
func (Plugin) Validate(u *url.URL, raw string) error {
	ref, err := classifyURI(u)
	if err != nil {
		return err
	}
	if _, ok := accountByName(ref.account); !ok {
		return errors.BadRequestf(
			"ynab plugin: unknown account %q in %q", ref.account, u.String(),
		)
	}
	return nil
}

// Interface conformance: the read capability set (FDR 0027 slice 1b). The
// SDK's RootProvider / RootLabeler / RootNamer embed RootLister, so they are
// satisfied only now that traversal (traversal.go) exists. Facets, listing
// fields and the unified field declaration land in slice 1c; capture /
// restore / diff are deliberately absent.
var (
	_ cutting_garden_plugins.RootLister   = (*Plugin)(nil)
	_ cutting_garden_plugins.RootProvider = (*Plugin)(nil)
	_ cutting_garden_plugins.RootLabeler  = (*Plugin)(nil)
	_ cutting_garden_plugins.RootNamer    = (*Plugin)(nil)
	_ cutting_garden_plugins.LeafReader   = (*Plugin)(nil)

	// The facet surface (slice 1c-2): the declaration derives from the unified
	// field model (unified.go); counting and enrichment push the date window
	// down to the server (window.go). FacetVersioner is deliberately absent —
	// YNAB has no single cheap change token (a delta request costs a request
	// and a rate-limit unit), so the framework falls back to its TTL.
	_ cutting_garden_plugins.FacetDescriber         = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter           = (*Plugin)(nil)
	_ cutting_garden_plugins.EnrichedLister         = (*Plugin)(nil)
	_ cutting_garden_plugins.ListingFieldsDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.UnifiedDescriber       = (*Plugin)(nil)

	// The write path (slice 2, cutting-garden#331): PatchNode on a transaction
	// (write.go) and the facet writes organize routes to it (facet_write.go).
	_ cutting_garden_plugins.NodeMutator         = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetWriteDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetWriteApplier   = (*Plugin)(nil)
)
