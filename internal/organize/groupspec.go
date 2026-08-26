package organize

import (
	"slices"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// groupKind classifies how a --group-by spelling resolves (RFC 0019 tags
// slice 3, cutting-garden#231). The zero value is groupKindField — a plain
// facet/field dimension, today's only behavior — so a groupSpec constructed
// directly (the persisted-heading parse in document.go, the apply/group tests)
// is a field grouping unless the resolver marks it otherwise.
type groupKind int

const (
	// groupKindField groups by a facet/field dimension (status, date_due,
	// priority) — the pre-tags behavior, unchanged.
	groupKindField groupKind = iota
	// groupKindTagWhole groups by a TAG dimension as a whole (categories): one
	// bucket per raw tag value.
	groupKindTagWhole
	// groupKindTagNamespace groups by a NAMESPACE within a tag dimension: Dim is
	// the tag dimension (categories), Namespace the segment prefix (project).
	groupKindTagNamespace
)

// groupSpec is a parsed --group-by / document-heading dimension spelling:
// the facet dimension plus, for a FacetDate dimension, the bucket
// granularity (cutting-garden#230). Non-date dimensions never carry one.
//
// Kind records which resolution the spelling took (RFC 0019 tags slice 3): a
// field grouping, the tag dimension as a whole, or a namespace within it. For a
// tag-namespace grouping Dim is the TAG DIMENSION (e.g. categories) and
// Namespace is the segment prefix (e.g. project); Granularity is only ever set
// on a date FIELD dimension.
//
// The spec is resolved ONCE at generate time (config-then-day for a bare
// date spelling) and persisted verbatim in the document's dimension heading
// (`date_due:month=`), so a later --apply coarsens live values identically
// WITHOUT consulting config — config may change between generate and apply.
type groupSpec struct {
	Dim         string
	Granularity cgp.DateGranularity // "" for a non-date dimension
	Kind        groupKind
	Namespace   string // set only for groupKindTagNamespace
}

// String renders the canonical spelling — the document heading term and
// provenance form. A field grouping spells `dim` (or `dim:granularity` for a
// date dim); a tag whole-dimension grouping spells the bare tag dimension; a
// tag-namespace grouping spells the bare namespace. (B3 will move the persisted
// form to a `_group_by` envelope; this stays a sensible bare canonical form.)
func (s groupSpec) String() string {
	switch s.Kind {
	case groupKindTagNamespace:
		return s.Namespace
	case groupKindTagWhole:
		return s.Dim
	default:
		if s.Granularity == "" {
			return s.Dim
		}
		return s.Dim + ":" + string(s.Granularity)
	}
}

// groupByEncoding renders the self-describing `_group_by` envelope value for a
// TAG grouping (RFC 0019 tags slice 3 B3): `<dim>` for a whole-dimension
// grouping, `<dim>/<namespace>` for a namespace grouping — the `/` presence is
// what distinguishes the two on parse, so the CLI's bare `project` still
// round-trips to a namespace spec. A field grouping returns "" (it carries no
// `_group_by` directive; its dimension lives in a `# <dim>=` heading), keeping a
// field document byte-identical to the pre-tags dialect.
func (s groupSpec) groupByEncoding() string {
	switch s.Kind {
	case groupKindTagNamespace:
		return s.Dim + "/" + s.Namespace
	case groupKindTagWhole:
		return s.Dim
	default:
		return ""
	}
}

// parseGroupByEncoding reconstructs the tag groupSpec a `_group_by` envelope
// value encodes, WITHOUT re-resolving against the plugin schema (the encoding is
// self-describing, RFC 0019 tags slice 3 B3): a `/` separates a namespace
// grouping (`<dim>/<namespace>`, groupKindTagNamespace) from a whole-dimension
// one (`<dim>`, groupKindTagWhole).
func parseGroupByEncoding(enc string) groupSpec {
	if dim, namespace, hasNamespace := strings.Cut(enc, "/"); hasNamespace {
		return groupSpec{Dim: dim, Namespace: namespace, Kind: groupKindTagNamespace}
	}
	return groupSpec{Dim: enc, Kind: groupKindTagWhole}
}

// parseGroupSpec resolves a --group-by spelling against the plugin's declared
// schema (RFC 0019 tags slice 3, cutting-garden#231):
//
//   - A trailing `=` FORCES the field reading: the token before it MUST be a
//     declared facet/field dimension (error naming it otherwise) —
//     disambiguates a name that is both a tag namespace and a field.
//   - A `:granularity` suffix is the existing date-field path, unchanged.
//   - A bare arg resolves, in order: the TAG dimension itself → whole-dimension
//     tag grouping; a declared facet/field dimension → field grouping (today's
//     behavior, including a bare date dim's granularity default); an
//     otherwise-unrecognized arg WHEN a tag dimension exists → tag-namespace
//     grouping (Dim = the tag dimension, Namespace = the arg); nothing
//     recognizable and no tag dimension → the field reading (today's silent
//     fall-through for an unknown bare dimension).
//
// tagDims are the plugin's TAG-dimension keys (UnifiedField.Kind == FieldTag),
// which the FacetDimension surface cannot distinguish — the derived facet kind
// of a tag field is FacetCategorical (facet_derive), so the unified declaration
// is the only place a tag dimension is visible. dims may be nil (no
// FacetDescriber) — then any suffix is rejected (no schema says it's a date).
func parseGroupSpec(
	spelling string,
	dims []cgp.NodeTypeFacets,
	tagDims []string,
	configDefault string,
) (groupSpec, error) {
	// A trailing `=` forces the field reading and requires a declared dimension.
	if forced, ok := strings.CutSuffix(spelling, "="); ok {
		return resolveFieldDim(forced, dims, configDefault, true)
	}

	// A `:granularity` suffix is a date-field spelling — the existing path.
	if _, _, hasSuffix := strings.Cut(spelling, ":"); hasSuffix {
		return resolveFieldDim(spelling, dims, configDefault, false)
	}

	// Bare arg. The tag dimension itself groups whole-dimension; a declared
	// facet/field dimension keeps the field reading; an unrecognized arg becomes
	// a tag NAMESPACE when a tag dimension exists.
	if slices.Contains(tagDims, spelling) {
		return groupSpec{Dim: spelling, Kind: groupKindTagWhole}, nil
	}
	if _, declared := findDim(dims, spelling); declared {
		return resolveFieldDim(spelling, dims, configDefault, false)
	}
	if len(tagDims) > 0 {
		// caldav declares exactly one tag dimension; if a plugin ever declares
		// several, the first-declared owns an unqualified namespace arg — genuine
		// cross-dimension ambiguity is out of scope for this slice.
		return groupSpec{
			Dim:       tagDims[0],
			Namespace: spelling,
			Kind:      groupKindTagNamespace,
		}, nil
	}
	return resolveFieldDim(spelling, dims, configDefault, false)
}

// resolveFieldDim resolves the field/facet reading of a spelling — the
// pre-tags logic verbatim, plus a requireDeclared guard for the forced-field
// (`dim=`) case. A `:granularity` suffix is legal only on a FacetDate
// dimension; a bare date dimension takes configDefault, then day.
func resolveFieldDim(
	spelling string,
	dims []cgp.NodeTypeFacets,
	configDefault string,
	requireDeclared bool,
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
	if requireDeclared && !declared {
		return groupSpec{}, errors.BadRequestf(
			"organize: --group-by %q= forces a field reading, but %q is not a "+
				"declared facet dimension", dim, dim,
		)
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
