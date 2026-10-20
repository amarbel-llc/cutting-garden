package ynab

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// DescribeFacetWrites declares how each facet dimension maps to a write
// (RFC 0012 §Write mapping, FDR 0023) — DERIVED from the unified declaration
// (FDR 0025): `category` and `cleared` are write:one onto their own fields,
// and every other grouping dimension is an explicit write:none so an organize
// edit to one fails loudly. `cleared` offers only cleared and uncleared as
// move targets (reconciled is read-only, see write.go).
func (Plugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return cutting_garden_plugins.DeriveNodeTypeFacetWrites(unifiedFieldSets())
}

// BuildFacetWritePatch builds the PatchNode body that moves a transaction into
// toBucket of a writable dimension, so organize re-categorizes by moving a
// line under another category heading. The body is the same shape PatchNode
// takes from a caller directly; the writes happen in PatchNode, which owns the
// safety checks. An empty toBucket is a clear, and neither dimension is
// clearable.
func (Plugin) BuildFacetWritePatch(
	_ context.Context,
	_ cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	toBucket string,
) ([]byte, error) {
	switch write.DimensionKey {
	case facetCategory:
		if toBucket == "" || toBucket == valueUncategorized {
			return nil, errors.BadRequestf(
				"ynab plugin: %q is not a YNAB category; to uncategorize, move the "+
					"transaction under the \"Uncategorized\" category", valueUncategorized,
			)
		}
		return json.Marshal(map[string]string{facetCategory: toBucket})
	case facetCleared:
		if toBucket == "" {
			return nil, errors.BadRequestf("ynab plugin: cleared cannot be cleared; use %q or %q",
				writeCleared, writeUncleared)
		}
		return json.Marshal(map[string]string{facetCleared: toBucket})
	}
	return nil, errors.BadRequestf("ynab plugin: dimension %q is not writable", write.DimensionKey)
}
