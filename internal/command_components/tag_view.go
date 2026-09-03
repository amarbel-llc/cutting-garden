package command_components

import (
	"slices"
	"sort"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// This file is the ONE home of the framework-side tag-view wiring (native
// tags slice 2, design G12): resolving a plugin's designated tag dimension,
// its interpreter (field default + [tags] override), and the node →
// SortKey-ordered-tag-set presenter. organize (box atoms), `list -format
// json`, and the mcp enriched listing all resolve tags through these — never
// their own copies. Moved here from internal/organize so the read-only
// consumers reach the same resolution without importing organize (which
// imports this package).

// DescribedTagDims collects the plugin's TAG-dimension keys — the
// UnifiedField.Kind == FieldTag fields declared via DescribeUnified
// (FDR 0025). The FacetDimension surface derives a tag field to
// FacetCategorical (facet_derive), so the unified declaration is the ONLY
// place a tag dimension is distinguishable from a plain categorical one; a
// plugin without the UnifiedDescriber capability has no tag dimensions.
// Deduplicated, first-declared order — organize's parseGroupSpec resolves an
// unqualified namespace arg against the first.
func DescribedTagDims(lister cutting_garden_plugins.RootLister) []string {
	d, ok := lister.(cutting_garden_plugins.UnifiedDescriber)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	for _, set := range d.DescribeUnified() {
		for _, c := range set.Codecs {
			for _, f := range c.Fields() {
				if f.Kind != cutting_garden_plugins.FieldTag || seen[f.Key] {
					continue
				}
				seen[f.Key] = true
				keys = append(keys, f.Key)
			}
		}
	}
	return keys
}

// FirstTagDim returns the plugin's DESIGNATED tag dimension — the first
// declared FieldTag key (DescribedTagDims order) — or "" when none is
// declared. The single home of the "first declared wins" rule, shared by
// organize's resolveTagDimension (a tag grouping's dimension), its apply-time
// tag-atom wiring (the dimension box atoms write to), and the node-view tag
// presenters below.
func FirstTagDim(lister cutting_garden_plugins.RootLister) string {
	if dims := DescribedTagDims(lister); len(dims) > 0 {
		return dims[0]
	}
	return ""
}

// InterpreterForDimension resolves the tag interpreter a dimension uses
// (RFC 0019 §4 selection): the field's plugin-declared default (its
// UnifiedField.Interpreter, read via the optional UnifiedDescriber capability)
// with the global [tags] config override layered on top — the override wins,
// per ResolveTagInterpreter. A lister that declares no unified fields, no
// field for the dimension, or an empty declared interpreter defaults the
// field-default to "naive" (the RFC 0019 §4 default), NOT an error; only an
// unknown interpreter NAME (from either source) is the loud bad request
// ResolveTagInterpreter raises. The resolved name is returned alongside the
// interpreter so a caller can name it in an error (e.g. a naive interpreter
// rejecting a namespace grouping).
func InterpreterForDimension(
	lister cutting_garden_plugins.RootLister, dim string, tagsOverride string,
) (cutting_garden_plugins.TagInterpreter, string, error) {
	name := resolveTagInterpreterName(declaredOrNaive(lister, dim), tagsOverride)
	// name is already fully resolved (default + override), so the empty
	// override here is a pass-through — ResolveTagInterpreter only performs
	// the registry lookup.
	interp, err := ResolveTagInterpreter(name, "")
	return interp, name, err
}

// declaredOrNaive is the field-default half of the RFC 0019 §4 precedence:
// the interpreter the plugin declares for dim's unified field, defaulting to
// "naive" when the capability, the field, or the declaration is absent.
func declaredOrNaive(lister cutting_garden_plugins.RootLister, dim string) string {
	if describer, ok := lister.(cutting_garden_plugins.UnifiedDescriber); ok {
		return orNaive(declaredTagInterpreter(describer, dim))
	}
	return "naive"
}

// orNaive applies the RFC 0019 §4 default: an empty (absent) interpreter
// declaration means "naive". The one home of that rule, shared by the
// per-dimension resolution (declaredOrNaive) and the per-type discovery
// (TypeTagSets).
func orNaive(name string) string {
	if name == "" {
		return "naive"
	}
	return name
}

// declaredTagInterpreter returns the interpreter a plugin declares for the
// dimension's unified field — the first field whose Key == dim across the node
// types' codecs — or "" when no such field is declared (or it names no
// interpreter). A tag field's interpreter is a property of the dimension, so the
// first Key match is authoritative; caller defaults "" to naive.
func declaredTagInterpreter(
	describer cutting_garden_plugins.UnifiedDescriber, dim string,
) string {
	for _, nt := range describer.DescribeUnified() {
		for _, codec := range nt.Codecs {
			for _, field := range codec.Fields() {
				if field.Key == dim {
					return field.Interpreter
				}
			}
		}
	}
	return ""
}

// UnifiedTagPresenter returns the node → rendered-tag-set function (design
// G1/G12): the type's designated FieldTag field's values (PresentUnifiedTags,
// stored order), ordered by the resolved interpreter's SortKey. nil for a
// plugin without the UnifiedDescriber capability — no tag dimension, no tags.
// The presented slice is cloned before sorting: the codec's Format output must
// never be reordered in place (it may alias plugin state).
func UnifiedTagPresenter(
	lister cutting_garden_plugins.RootLister,
	interp cutting_garden_plugins.TagInterpreter,
) func(cutting_garden_plugins.Node) []string {
	d, ok := lister.(cutting_garden_plugins.UnifiedDescriber)
	if !ok {
		return nil
	}
	byType := map[string][]cutting_garden_plugins.Codec{}
	for _, set := range d.DescribeUnified() {
		byType[set.Tag] = set.Codecs
	}
	return func(n cutting_garden_plugins.Node) []string {
		tags := slices.Clone(
			cutting_garden_plugins.PresentUnifiedTags(byType[n.Type], n),
		)
		if interp != nil {
			sort.SliceStable(tags, func(i, j int) bool {
				return interp.SortKey(tags[i]) < interp.SortKey(tags[j])
			})
		}
		return tags
	}
}

// NodeTagsPresenter is the node-view composition (design G12): resolve the
// lister's designated tag dimension and its interpreter (field default +
// [tags] override), and return the SortKey-ordered tag presenter the `list
// -format json` and mcp enriched-listing views render into a top-level `tags`
// array. A (nil, nil) return means the plugin declares no tag dimension —
// the views omit the key entirely. The only error is an unknown interpreter
// NAME (a config typo), surfaced loudly rather than degrading to unsorted.
//
// This assumes ONE plugin-wide designated tag dimension (FirstTagDim; the
// G6 v1 invariant — every type's tag field is the same dimension), so one
// resolved interpreter orders every node's tags. Per-type interpreters
// would need a per-type presenter map, cf. TypeTagSets' per-type
// resolution.
func NodeTagsPresenter(
	lister cutting_garden_plugins.RootLister, tagsOverride string,
) (func(cutting_garden_plugins.Node) []string, error) {
	dim := FirstTagDim(lister)
	if dim == "" {
		return nil, nil
	}
	interp, _, err := InterpreterForDimension(lister, dim, tagsOverride)
	if err != nil {
		return nil, err
	}
	return UnifiedTagPresenter(lister, interp), nil
}

// TagSet names one node type's designated tag set for schema discovery
// (describe_node_types' `tag_set`, design G12): the FieldTag dimension key
// bare tag terms address, and the interpreter NAME resolved for it (field
// default + [tags] override — the same precedence InterpreterForDimension
// applies, minus the registry lookup: discovery reports the resolution, the
// consuming paths validate it).
type TagSet struct {
	Field       string `json:"field"`
	Interpreter string `json:"interpreter"`
}

// TypeTagSets maps each node type tag that declares a FieldTag dimension to
// its resolved TagSet. Empty (never nil-vs-empty significant) for a plugin
// without the UnifiedDescriber capability or with no tag declarations.
//
// Unlike NodeTagsPresenter (one plugin-wide interpreter via FirstTagDim,
// the G6 v1 invariant), this resolves PER TYPE — for today's plugins the
// two agree because every type declares the same tag dimension; a future
// multi-tag-dim plugin would need NodeTagsPresenter to grow a matching
// per-type presenter map before the views could diverge honestly.
func TypeTagSets(
	lister cutting_garden_plugins.RootLister, tagsOverride string,
) map[string]TagSet {
	d, ok := lister.(cutting_garden_plugins.UnifiedDescriber)
	if !ok {
		return nil
	}
	out := map[string]TagSet{}
	for _, set := range d.DescribeUnified() {
		for _, c := range set.Codecs {
			for _, f := range c.Fields() {
				if f.Kind != cutting_garden_plugins.FieldTag {
					continue
				}
				if _, dup := out[set.Tag]; dup {
					// A second FieldTag on one type is a declaration error
					// (G6 v1) surfaced loudly by organize's generate-time
					// validation; discovery keeps the first-declared set.
					continue
				}
				out[set.Tag] = TagSet{
					Field: f.Key,
					Interpreter: resolveTagInterpreterName(
						orNaive(f.Interpreter), tagsOverride,
					),
				}
			}
		}
	}
	return out
}
