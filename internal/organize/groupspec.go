package organize

import (
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// groupSpec is a parsed --group-by / document-heading dimension spelling:
// the facet dimension plus, for a FacetDate dimension, the bucket
// granularity (cutting-garden#230). Non-date dimensions never carry one.
//
// The spec is resolved ONCE at generate time (config-then-day for a bare
// date spelling) and persisted verbatim in the document's dimension heading
// (`date_due:month=`), so a later --apply coarsens live values identically
// WITHOUT consulting config — config may change between generate and apply.
type groupSpec struct {
	Dim         string
	Granularity cgp.DateGranularity // "" for a non-date dimension
}

// String renders the canonical spelling ("date_due:month", or just the
// dimension) — the document heading term and provenance form.
func (s groupSpec) String() string {
	if s.Granularity == "" {
		return s.Dim
	}
	return s.Dim + ":" + string(s.Granularity)
}

// parseGroupSpec resolves a spelling against the plugin's declared schema:
// a `dim:granularity` suffix is legal only on a FacetDate dimension; a bare
// date dimension takes configDefault, then day. dims may be nil (no
// FacetDescriber) — then any suffix is rejected (no schema says it's a date).
func parseGroupSpec(
	spelling string, dims []cgp.NodeTypeFacets, configDefault string,
) (groupSpec, error) {
	dim, suffix, hasSuffix := strings.Cut(spelling, ":")
	d, declared := findDim(dims, dim)
	if hasSuffix {
		g, ok := cgp.ParseDateGranularity(suffix)
		if !ok {
			return groupSpec{}, errors.BadRequestf(
				"organize: granularity %q is not one of year, month, day", suffix,
			)
		}
		if !declared || d.Kind != cgp.FacetDate {
			return groupSpec{}, errors.BadRequestf(
				"organize: dimension %q is not a date dimension; a :granularity "+
					"suffix applies only to date dimensions", dim,
			)
		}
		return groupSpec{Dim: dim, Granularity: g}, nil
	}
	if declared && d.Kind == cgp.FacetDate {
		if g, ok := cgp.ParseDateGranularity(configDefault); ok {
			return groupSpec{Dim: dim, Granularity: g}, nil
		}
		return groupSpec{Dim: dim, Granularity: cgp.GranularityDay}, nil
	}
	return groupSpec{Dim: dim}, nil
}

// findDim looks a dimension key up across the declared node-type schemas,
// returning its first declaration. Organize groups one dimension across every
// type, so a key declared with different kinds on different types would be a
// plugin-schema inconsistency, not a case to arbitrate here.
func findDim(dims []cgp.NodeTypeFacets, key string) (cgp.FacetDimension, bool) {
	return cgp.FindFacetDimension(dims, key)
}

// coarsenBucket coarsens a live bucket value to the document's granularity by
// prefix truncation — the apply-side twin of groupNodes' generate-side
// coarsening. Identity for a non-date spec (g == ""), and identity for any
// value that is not itself a shape-valid date bucket (YYYY / YYYY-MM /
// YYYY-MM-DD): a non-ISO key from a (wire) plugin lands verbatim in its own
// observed bucket instead of being blind-sliced into garbage ("20260815"
// month-truncated would be "2026-08"-lengthed nonsense like "2026081").
func coarsenBucket(v string, g cgp.DateGranularity) string {
	if g == "" {
		return v
	}
	if _, ok := cgp.ParseDateBucket(v); !ok {
		return v
	}
	return cgp.TruncateDateKey(v, g)
}
