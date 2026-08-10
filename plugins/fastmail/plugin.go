// Package fastmail is cutting-garden's READ-ONLY Fastmail (JMAP) traversal
// and facet plugin. Registered for the `fastmail` URI scheme, it makes a
// Fastmail account a first-class `list` / `mcp` substrate over JMAP
// (RFC 8620 core + RFC 8621 mail): the account's mailbox/tag tree, its
// threads, and their messages are traversable and faceted, with the raw
// RFC 5322 bytes fetched lazily on an explicit read.
//
// It is the FIRST in-tree scheme-only (read-only) plugin: it implements
// none of capture/restore/diff and so registers via MustRegisterScheme
// (init.go) rather than the capability registries. The tree walk
// (RootLister/RootProvider), leaf read (LeafReader), and read-only facets
// (FacetDescriber/FacetCounter/FacetVersioner) are all probed by type
// assertion after registration.
//
// Slice 1 is read-only. Tag writes over `organize` (Slice 2) are gated on
// the organize framework growing `write:many` apply; archival
// capture/restore/diff (Slice 3) is a separate follow-on. See FDR 0024.
//
// The plugin mirrors plugins/caldav for the network + RFC 0007
// account-config + traversal + facet pattern. It carries no MCP write
// surface and no mail parser: a message is an opaque message/rfc822 blob
// alongside its structured JMAP Email JSON, exactly the shape the read
// surface wants.
package fastmail

import (
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// schemeFastmail is the single URI scheme this plugin claims. It is
// vendor-named on purpose (FDR 0024): the horizon data types
// (masked-email, notes) are Fastmail-specific JMAP, not portable standard
// JMAP, so a generic `jmap:` sibling is a later possibility but the
// shipped scheme is `fastmail`.
const schemeFastmail = "fastmail"

// typeTagCaptureReceipt is the RESERVED capture-receipt type-tag for the
// eventual archival capture path (Slice 3). It is unused in Slice 1 — the
// plugin captures nothing — but Plugin.TypeTag must return something
// stable, and reserving the tag now keeps the Slice 3 receipt shape from
// colliding with any other plugin's namespace (FDR 0018).
const typeTagCaptureReceipt = "cutting_garden-capture_receipt-fastmail-v1"

// Plugin is the read-only Fastmail (JMAP) traversal and facet backend. It
// is a zero-size value: all account config lives in package state
// (configuredAccounts, see config.go), injected once at startup, so the
// plugin carries no per-instance state and the registry can hold it by
// value.
type Plugin struct{}

// Schemes returns the single `fastmail` scheme. Like caldav and jira it
// claims no bare transport scheme: the JMAP API host is fixed and a
// Fastmail endpoint is not distinguishable by host from any other https
// URL, so it must be opted into explicitly.
func (Plugin) Schemes() []string { return []string{schemeFastmail} }

// TypeTag returns the reserved capture-receipt tag. Slice 1 registers via
// MustRegisterScheme and captures nothing, so this value is never emitted
// on the wire yet; it is reserved for Slice 3's archival receipts.
func (Plugin) TypeTag() string { return typeTagCaptureReceipt }

// Validate rejects a node URI whose scheme this plugin does not claim, or
// whose account-name host is not a configured account — both CALLER
// mistakes (a malformed node URI the caller can fix), so they classify as
// bad requests (errors.Is400BadRequest true) rather than plugin failures
// that invite a futile retry. It is structural only — no network — so it
// is safe to call during arg classification.
func (Plugin) Validate(u *url.URL, raw string) error {
	ref, err := classifyURI(u)
	if err != nil {
		return err
	}
	if _, ok := accountByName(ref.account); !ok {
		return errors.BadRequestf(
			"fastmail plugin: unknown account %q in %q", ref.account, u.String(),
		)
	}
	return nil
}

// Interface conformance: the read-only capability set (FDR 0024). Writes
// (FacetWrite*, FieldWriteApplier) and capture/restore/diff are Slice 2/3
// and are deliberately absent.
var (
	_ cutting_garden_plugins.RootLister             = (*Plugin)(nil)
	_ cutting_garden_plugins.RootProvider           = (*Plugin)(nil)
	_ cutting_garden_plugins.RootLabeler            = (*Plugin)(nil)
	_ cutting_garden_plugins.LeafReader             = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetDescriber         = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter           = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetVersioner         = (*Plugin)(nil)
	_ cutting_garden_plugins.EnrichedLister         = (*Plugin)(nil)
	_ cutting_garden_plugins.ListingFieldsDescriber = (*Plugin)(nil)
)
