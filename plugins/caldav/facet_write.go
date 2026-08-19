package caldav

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.FacetWriteDescriber = (*Plugin)(nil)

// DescribeFacetWrites declares how each object leaf type's facet dimensions map
// to writes (RFC 0012 §Write mapping, FDR 0023) — DERIVED from the unified
// field-codec declaration (FDR 0025 Option B): each type's GROUPABLE fields
// project into legacy FacetWrites via the SDK helper, a writable field to a
// write:one through its Source, a read-only one to an explicit write:none. The
// write-side substance lives on the fields in unifiedFieldSets: the
// reschedule-by-move date buckets (year, month) target the component's primary
// date property, status carries the per-component RFC 5545 enum as its
// pre-rendered bucket list, priority's band list falls out of its closed value
// domain, and the identity/derived/volatile dimensions (component, due_band,
// timezone) stay read-only. The write completions themselves (period splice,
// band→PRIORITY) live in the codecs' Parse, never in the framework — this only
// describes them.
func (Plugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return cutting_garden_plugins.DeriveNodeTypeFacetWrites(unifiedFieldSets())
}
