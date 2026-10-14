// Package org is cutting-garden's read-only Org (org-mode) traversal and
// facet plugin (FDR 0026). Registered for the `org` URI scheme, it makes a
// WebDAV folder of `.org` files — the one Orgzly syncs through Fastmail
// Files — browsable over `list` / `mcp` / `read_facets` and trellis queries:
// account → folder → file → headline → child headlines.
//
// Like fastmail it is scheme-only (MustRegisterScheme): no capture,
// restore or diff. Slice 1 never writes; the narrow "quote" write is
// slice 2 (#309). Parsing lives in the orgtext leaf package.
package org

import (
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const schemeOrg = "org"

// typeTagCaptureReceipt is reserved for an eventual archival capture
// (FDR 0026 later slices); Slice 1 captures nothing.
const typeTagCaptureReceipt = "cutting_garden-capture_receipt-org-v1"

// Node type tags (FDR 0018 horizontal versioning).
const (
	typeFolder   = "cutting_garden-org-folder-v1"
	typeFile     = "cutting_garden-org-file-v1"
	typeHeadline = "cutting_garden-org-headline-v1"
)

// Plugin is the zero-size org plugin; account config lives in package
// state (config.go).
type Plugin struct{}

func init() {
	cutting_garden_plugins.MustRegisterScheme(Plugin{})
}

// Schemes returns the single `org` scheme.
func (Plugin) Schemes() []string { return []string{schemeOrg} }

// TypeTag returns the reserved capture-receipt tag.
func (Plugin) TypeTag() string { return typeTagCaptureReceipt }

// Validate rejects a URI this plugin cannot classify or whose account is
// not configured — caller mistakes, so bad requests. No network.
func (Plugin) Validate(u *url.URL, _ string) error {
	ref, err := classifyURI(u)
	if err != nil {
		return err
	}
	if _, ok := accountByName(ref.account); !ok {
		return errors.BadRequestf("org plugin: unknown account %q in %q", ref.account, u.String())
	}
	return nil
}

// Types declares the three node types. All are containers: a folder holds
// folders and files, a file holds its top-level headlines, a headline its
// children — and a file or headline also serves its own body (ReadLeaf).
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeFolder, Container: true},
		{Tag: typeFile, Container: true},
		{Tag: typeHeadline, Container: true},
	}
}

var (
	_ cutting_garden_plugins.RootLister             = (*Plugin)(nil)
	_ cutting_garden_plugins.RootProvider           = (*Plugin)(nil)
	_ cutting_garden_plugins.RootLabeler            = (*Plugin)(nil)
	_ cutting_garden_plugins.RootNamer              = (*Plugin)(nil)
	_ cutting_garden_plugins.LeafReader             = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetDescriber         = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter           = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetVersioner         = (*Plugin)(nil)
	_ cutting_garden_plugins.ListingFieldsDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.UnifiedDescriber       = (*Plugin)(nil)
	_ cutting_garden_plugins.NodeIDer               = (*Plugin)(nil)
)
