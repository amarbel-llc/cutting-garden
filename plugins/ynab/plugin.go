// Package ynab is cutting-garden's YNAB (You Need A Budget) traversal and
// facet plugin (FDR 0027). Registered for the `ynab` URI scheme, it makes a
// YNAB account a read-only `list` / `mcp` / trellis substrate over the YNAB
// REST API: its budgets, their accounts, category groups, categories and
// transactions.
//
// Like fastmail it is a scheme-only plugin: it implements none of
// capture/restore/diff and registers via MustRegisterScheme (init.go). The
// `ynab://<account>/` URL's host slot names a config account, not a server
// (the API host is fixed), and the tree walk, leaf read and facets are
// reached by type assertion after registration.
package ynab

import (
	"net/url"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// schemeYNAB is the single URI scheme this plugin claims.
const schemeYNAB = "ynab"

// typeTagCaptureReceipt is the RESERVED capture-receipt type-tag (FDR 0018).
// The plugin captures nothing, but Plugin.TypeTag must return something
// stable, and reserving it keeps a later receipt shape from colliding with
// another plugin's namespace.
const typeTagCaptureReceipt = "cutting_garden-capture_receipt-ynab-v1"

// Plugin is the read-only YNAB traversal and facet backend. It is a
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

// Interface conformance is deliberately NOT asserted yet (FDR 0027 slice 1a).
// The SDK's RootProvider / RootLabeler / RootNamer all embed RootLister, so
// the plugin does not satisfy them — and no-argument roots do not surface it —
// until the client and traversal land (slice 1b). Roots, RootLabels and
// RootNames are implemented and tested in config.go ahead of that.
