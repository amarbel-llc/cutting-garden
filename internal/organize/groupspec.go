package organize

import (
	"slices"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// groupKind classifies how a --group-by spelling resolves (native tags design
// G10). The zero value is groupKindField — a plain facet/field dimension — so a
// groupSpec constructed directly (the apply/group tests) is a field grouping
// unless marked otherwise.
type groupKind int

const (
	// groupKindField groups by a facet/field dimension (`status=`, `priority=`,
	// `date_due=(month)`).
	groupKindField groupKind = iota
	// groupKindTagWhole groups by the type's whole tag set (`(tags)`): one bucket
	// per raw tag value.
	groupKindTagWhole
	// groupKindTagNamespace groups by a NAMESPACE within the tag set (bare
	// `project`): Dim is the tag dimension, Namespace the segment prefix.
	groupKindTagNamespace
)

// groupSpec is a resolved grouping: the ONE spelling shared by the --group-by
// flag, the `_group-by` envelope directive, and the dimension heading (design
// G10):
//
//	(tags)             the type's whole tag set        Kind=tagWhole
//	project            tag namespace `project`         Kind=tagNamespace
//	status=            field grouping                  Kind=field
//	date_due=(month)   date field at month granularity Kind=field, Granularity
//
// Dim is the facet dimension the grouping reads and writes. For a FIELD grouping
// it is the spelled field; for a TAG grouping it is the plugin's designated tag
// dimension (caldav `categories`), which the spelling never names — it is
// resolved from the plugin schema at generate time (parseGroupSpec) and again at
// apply time (resolveTagDimension), since `_group-by = (tags)` carries only the
// spelling.
//
// A date field's granularity is resolved ONCE at generate time (the `(month)`
// qualifier, else `[organize] date_granularity`, else day) and persisted
// verbatim in the dimension heading (`# date_due=(month)`), so a later --apply
// coarsens live values identically WITHOUT consulting config.
type groupSpec struct {
	Dim         string
	Granularity cgp.DateGranularity // "" for a non-date dimension
	Kind        groupKind
	Namespace   string // set only for groupKindTagNamespace
}

// grouped reports whether the spec names a grouping at all: the zero spec (a
// document with neither a `# <dim>=` heading nor a `_group-by` directive) does
// not.
func (s groupSpec) grouped() bool {
	return s.Kind != groupKindField || s.Dim != ""
}

// String renders the canonical spelling — the provenance form, the `_group-by`
// value for a tag grouping, and the dimension heading term for a field
// grouping. It is exactly what parseGroupTerm reads back.
func (s groupSpec) String() string {
	switch s.Kind {
	case groupKindTagNamespace:
		return trellis.QuoteIfNeeded(s.Namespace)
	case groupKindTagWhole:
		return "(tags)"
	default:
		if s.Granularity == "" {
			return trellis.QuoteIfNeeded(s.Dim) + "="
		}
		return trellis.QuoteIfNeeded(s.Dim) + "=(" + string(s.Granularity) + ")"
	}
}

// groupByEncoding renders the `_group-by` envelope value: the spelling itself
// for a TAG grouping (`(tags)`, `project`), whose hoisted body has no dimension
// heading to recover it from. A field grouping returns "" — its spelling IS the
// `# <dim>=` heading, so a field document carries no `_group-by`.
func (s groupSpec) groupByEncoding() string {
	if s.Kind == groupKindField {
		return ""
	}
	return s.String()
}

// parseGroupByEncoding reads a `_group-by` envelope value back as a TAG
// groupSpec. Dim stays EMPTY — the spelling does not name the tag dimension;
// apply fills it from the plugin (resolveTagDimension). A field spelling here
// is a bad request: a field grouping's spelling is its heading, never the
// envelope.
func parseGroupByEncoding(enc string) (groupSpec, error) {
	gt, err := parseGroupTerm(enc)
	if err != nil {
		return groupSpec{}, errors.BadRequestf("organize: `- %s = %s`: %w", fieldGroupBy, enc, err)
	}
	switch gt.kind {
	case groupTermTags:
		return groupSpec{Kind: groupKindTagWhole}, nil
	case groupTermBare:
		return groupSpec{Kind: groupKindTagNamespace, Namespace: gt.name}, nil
	default:
		return groupSpec{}, errors.BadRequestf(
			"organize: `- %s = %s` spells a field grouping; a field grouping's "+
				"spelling is its `# %s` heading, not the envelope", fieldGroupBy, enc, enc,
		)
	}
}

// groupTermKind is the syntactic shape of one group-by spelling, before it is
// resolved against a plugin schema.
type groupTermKind int

const (
	// groupTermTags is the `(tags)` qualifier term.
	groupTermTags groupTermKind = iota
	// groupTermBare is a bare or quoted identifier: ALWAYS a tag namespace
	// (design G9 — bare is a tag; a field needs an operator).
	groupTermBare
	// groupTermField is `<dim>=` (a field, qualifier empty) or
	// `<dim>=(<qualifier>)` (a field with a granularity qualifier).
	groupTermField
)

// groupTerm is the parsed shape of a group-by spelling (flag, `_group-by`, or
// dimension heading).
type groupTerm struct {
	kind      groupTermKind
	name      string // the namespace (bare) or field name (field)
	qualifier string // the `(x)` qualifier of a field term; "" when absent
}

// parseGroupTerm reads one group-by spelling with the trellis term parser
// (design G13 — one grammar). The shapes:
//
//   - `(tags)`           → a QualifierBasicTerm named tags
//   - `project`          → a bare / quoted identifier (a tag namespace)
//   - `date_due=(month)` → a FieldPred whose single value is a Qualifier
//   - `status=`          → a PARTIAL term: field name + `=` + no value
//
// The partial `status=` is NOT a trellis term — RFC 0015 §Headings keeps the
// `PartialTerm` deliberately out of the grammar (a grammar-level spelling would
// be context-sensitive), so the `=` suffix is split off here and the field NAME
// alone is parsed by trellis (a bare or quoted identifier). Every other shape is
// one whole trellis term.
//
// The retired spellings are rejected with a hint naming the new one: the
// `dim:granularity` suffix (`date_due:month`, which trellis lexes as ONE
// identifier under the strict sigil rule) and the `dim/namespace` envelope
// encoding (`categories/project`).
func parseGroupTerm(spelling string) (groupTerm, error) {
	if name, ok := strings.CutSuffix(spelling, "="); ok && name != "" {
		if err := rejectLegacySpelling(name); err != nil {
			return groupTerm{}, err
		}
		field, err := parsePlainIdent(name)
		if err != nil {
			return groupTerm{}, errors.BadRequestf(
				"the name before `=` must be a bare or quoted field name: %w", err,
			)
		}
		return groupTerm{kind: groupTermField, name: field}, nil
	}

	if err := rejectLegacySpelling(spelling); err != nil {
		return groupTerm{}, err
	}
	t, err := trellis.ParseTerm(spelling)
	if err != nil {
		return groupTerm{}, err
	}
	if t.Negate || t.Exact {
		return groupTerm{}, errors.BadRequestf(
			"a `^`/`=` prefix is a query decoration, not a grouping",
		)
	}
	switch b := t.Basic.(type) {
	case trellis.QualifierBasicTerm:
		if b.Qualifier.Name != "tags" {
			return groupTerm{}, errors.BadRequestf(
				"unknown qualifier; the only bare qualifier is `(tags)` (the type's whole tag set)",
			)
		}
		return groupTerm{kind: groupTermTags}, nil
	case trellis.FieldPredBasicTerm:
		return parseFieldGroupTerm(b.FieldPred)
	case trellis.IdentBasicTerm:
		if b.Sigil == nil {
			return groupTerm{kind: groupTermBare, name: b.Ident.Name}, nil
		}
	case trellis.QuotedRefBasicTerm:
		if b.Sigil == nil {
			return groupTerm{kind: groupTermBare, name: b.Ref.Value}, nil
		}
	}
	return groupTerm{}, errors.BadRequestf(
		"expected `(tags)`, a tag namespace (`project`), a field (`status=`), or a " +
			"date field with a granularity (`date_due=(month)`)",
	)
}

// parseFieldGroupTerm projects a parsed FieldPred onto the field group-by
// shape: `=` with exactly one Qualifier value (`date_due=(month)`). A literal
// value (`status=x`) is a query predicate, not a grouping.
func parseFieldGroupTerm(fp trellis.FieldPred) (groupTerm, error) {
	if fp.Op != trellis.FieldOpEq || fp.List {
		return groupTerm{}, errors.BadRequestf(
			"a field grouping is `%s=` (or `%s=(granularity)` for a date field); "+
				"`%s` is a query operator", fp.Field.Name, fp.Field.Name, fp.Op,
		)
	}
	q, ok := fp.Values[0].(trellis.Qualifier)
	if !ok {
		return groupTerm{}, errors.BadRequestf(
			"`%s=<value>` is a query predicate; group by the field with `%s=`, or at "+
				"a granularity with `%s=(year|month|day)`",
			fp.Field.Name, fp.Field.Name, fp.Field.Name,
		)
	}
	return groupTerm{kind: groupTermField, name: fp.Field.Name, qualifier: q.Name}, nil
}

// rejectLegacySpelling refuses the pre-G10 spellings with a hint: a
// `:granularity` suffix (`date_due:month`) and a `/`-joined namespace encoding
// (`categories/project`). Both lex as a single identifier under the strict sigil
// rule, so they would otherwise fail later as an unknown name.
func rejectLegacySpelling(s string) error {
	if dim, gran, ok := strings.Cut(s, ":"); ok && dim != "" && gran != "" && !strings.ContainsAny(s, "\"'") {
		return errors.BadRequestf(
			"the `dim:granularity` spelling is retired; spell a date granularity as `%s=(%s)`",
			dim, gran,
		)
	}
	if dim, ns, ok := strings.Cut(s, "/"); ok && dim != "" && ns != "" && !strings.ContainsAny(s, "\"'") {
		return errors.BadRequestf(
			"the `dim/namespace` spelling is retired; a bare name is a tag namespace "+
				"(`%s`), and `(tags)` is the whole tag set", ns,
		)
	}
	return nil
}

// parsePlainIdent parses s as one sigil-free bare or quoted identifier term,
// returning its decoded text.
func parsePlainIdent(s string) (string, error) {
	t, err := trellis.ParseTerm(s)
	if err != nil {
		return "", err
	}
	if !t.Negate && !t.Exact {
		switch b := t.Basic.(type) {
		case trellis.IdentBasicTerm:
			if b.Sigil == nil {
				return b.Ident.Name, nil
			}
		case trellis.QuotedRefBasicTerm:
			if b.Sigil == nil {
				return b.Ref.Value, nil
			}
		}
	}
	return "", errors.BadRequestf("%q is not a bare or quoted identifier", s)
}

// parseGroupSpec resolves a --group-by spelling against the plugin's declared
// schema (design G10):
//
//   - `(tags)` → the type's whole tag set; an error when the plugin declares
//     no tag dimension.
//   - a bare name → a tag NAMESPACE, never a field (G9). With no tag dimension
//     it fails loudly, suggesting `<name>=` when a field of that name exists.
//     (A namespace that matches nothing at generate time is checked after
//     grouping — rejectEmptyNamespace.)
//   - `<dim>=` → the field; `<dim>=(<granularity>)` → a date field at that
//     granularity. A bare `<dim>=` on a FacetDate dimension resolves
//     configDefault, then day.
//
// tagDims are the plugin's TAG-dimension keys (UnifiedField.Kind == FieldTag),
// which the FacetDimension surface cannot distinguish — the derived facet kind of
// a tag field is FacetCategorical (facet_derive). dims may be nil (no
// FacetDescriber) — then a field name is taken on trust, but a granularity
// qualifier is rejected (no schema says the field is a date).
func parseGroupSpec(
	spelling string,
	dims []cgp.NodeTypeFacets,
	tagDims []string,
	configDefault string,
) (groupSpec, error) {
	gt, err := parseGroupTerm(spelling)
	if err != nil {
		return groupSpec{}, errors.BadRequestf("organize: --group-by %s: %w", spelling, err)
	}
	switch gt.kind {
	case groupTermTags:
		if len(tagDims) == 0 {
			return groupSpec{}, errors.BadRequestf(
				"organize: --group-by (tags): the plugin declares no tag dimension to group by",
			)
		}
		return groupSpec{Dim: tagDims[0], Kind: groupKindTagWhole}, nil

	case groupTermBare:
		if slices.Contains(tagDims, gt.name) {
			// The retired whole-dimension spelling (`categories`): a bare name is a
			// namespace, and no tag sits under a namespace named after the
			// dimension itself.
			return groupSpec{}, errors.BadRequestf(
				"organize: --group-by %s: a bare name is a tag namespace, and %q names "+
					"the tag dimension itself; group by the whole tag set with `(tags)`",
				spelling, gt.name,
			)
		}
		if len(tagDims) == 0 {
			if _, declared := findDim(dims, gt.name); declared {
				return groupSpec{}, errors.BadRequestf(
					"organize: --group-by %s: a bare name is a tag namespace, but the "+
						"plugin declares no tag dimension; to group by the %q field spell it "+
						"`%s=`", spelling, gt.name, spelling,
				)
			}
			return groupSpec{}, errors.BadRequestf(
				"organize: --group-by %s: a bare name is a tag namespace, but the "+
					"plugin declares no tag dimension", spelling,
			)
		}
		// caldav declares exactly one tag dimension; if a plugin ever declares
		// several, the first-declared owns an unqualified namespace arg — genuine
		// cross-dimension ambiguity is out of scope for this slice.
		return groupSpec{Dim: tagDims[0], Namespace: gt.name, Kind: groupKindTagNamespace}, nil

	default:
		return resolveFieldDim(spelling, gt, dims, configDefault)
	}
}

// resolveFieldDim resolves a field group term against the schema: the field
// must be declared (when a schema is in hand), a granularity qualifier is legal
// only on a FacetDate dimension, and a bare `<dim>=` on a date dimension takes
// configDefault, then day.
func resolveFieldDim(
	spelling string, gt groupTerm, dims []cgp.NodeTypeFacets, configDefault string,
) (groupSpec, error) {
	d, declared := findDim(dims, gt.name)
	if dims != nil && !declared {
		return groupSpec{}, errors.BadRequestf(
			"organize: --group-by %s: %q is not a declared field dimension", spelling, gt.name,
		)
	}
	if gt.qualifier != "" {
		g, ok := cgp.ParseDateGranularity(gt.qualifier)
		if !ok {
			return groupSpec{}, errors.BadRequestf(
				"organize: --group-by %s: granularity %q is not one of year, month, day",
				spelling, gt.qualifier,
			)
		}
		if !declared || d.Kind != cgp.FacetDate {
			return groupSpec{}, errors.BadRequestf(
				"organize: --group-by %s: %q is not a date dimension; a `(granularity)` "+
					"qualifier applies only to date dimensions", spelling, gt.name,
			)
		}
		return groupSpec{Dim: gt.name, Granularity: g}, nil
	}
	if declared && d.Kind == cgp.FacetDate {
		if g, ok := cgp.ParseDateGranularity(configDefault); ok {
			return groupSpec{Dim: gt.name, Granularity: g}, nil
		}
		return groupSpec{Dim: gt.name, Granularity: cgp.GranularityDay}, nil
	}
	return groupSpec{Dim: gt.name}, nil
}

// rejectEmptyNamespace is the generate-time half of the bare-name rule: a
// namespace grouping that bucketed NOTHING, when a field of the same name
// exists, almost certainly meant the field — fail loudly suggesting `<name>=`
// rather than emitting an all-ungrouped document.
func rejectEmptyNamespace(spec groupSpec, doc document, dims []cgp.NodeTypeFacets) error {
	if spec.Kind != groupKindTagNamespace || doc.hasBuckets() {
		return nil
	}
	if _, declared := findDim(dims, spec.Namespace); !declared {
		return nil
	}
	return errors.BadRequestf(
		"organize: --group-by %s: no tag is under the %q namespace, but %q is a "+
			"field dimension; to group by the field spell it `%s=`",
		spec, spec.Namespace, spec.Namespace, spec,
	)
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
