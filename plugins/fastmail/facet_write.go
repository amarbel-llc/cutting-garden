package fastmail

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.FacetWriteDescriber = (*Plugin)(nil)

// DescribeFacetWrites declares how each node type's facet dimensions map to
// writes (RFC 0012 §Write mapping, FDR 0023) — DERIVED from the unified
// declaration (FDR 0025 Option B): the thread's `tags` is the single
// write:many dimension (a full-set membership replacement onto its own
// stored field), and the read-only grouping dimensions (from, date,
// has_attachment) are explicit write:none so an edit to one fails loudly.
// This only DESCRIBES the writes; the apply side (NodeMutator +
// MembershipWriteApplier) is the following task, so until it lands an
// `organize -apply` naming tags is refused by the engine's NodeMutator gate.
func (Plugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return cutting_garden_plugins.DeriveNodeTypeFacetWrites(unifiedFieldSets())
}
