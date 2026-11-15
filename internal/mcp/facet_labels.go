package mcp

import (
	"context"
	"sort"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// attachLabels resolves display names for FacetLabelled dimensions present in
// view.Facets, via the plugin's OPTIONAL FacetLabeler capability (RFC 0012
// §7), and sets view.Labels. Shared by the resources/read implicit facet
// surface (facetContent) and the read_facets tool (Resources.ReadFacets) so
// both project the SAME labels.
//
// Label resolution is presentation-only and non-fatal (RFC 0012 §7, §9): a
// plugin without FacetLabeler, or a resolver error for one dimension, simply
// leaves that dimension's labels absent — the caller falls back to raw keys
// — and never fails the read/tool call. A dimension is resolved only when
// BOTH its FacetDescriber declaration says Kind == FacetLabelled AND it is
// actually present in the served summary, so resolution work (and any
// failure) is scoped to what is shown, mirroring §8's "resolve only shown
// rows" truncation rule.
func attachLabels(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	view *facetView,
) {
	if view == nil || len(view.Facets) == 0 {
		return
	}
	labeler, ok := lister.(cutting_garden_plugins.FacetLabeler)
	if !ok {
		return
	}
	describer, ok := lister.(cutting_garden_plugins.FacetDescriber)
	if !ok {
		return
	}

	labelled := labelledDimensionKeys(describer)
	var labels map[string]map[string]string
	var joined map[string][]facetLabelledRow
	for dim, hist := range view.Facets {
		if !labelled[dim] {
			continue
		}
		keys := make([]string, 0, len(hist))
		for k := range hist {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		resolved, err := labeler.ResolveFacetLabels(ctx, dim, keys)
		if err != nil || len(resolved) == 0 {
			continue
		}
		dimLabels := make(map[string]string, len(keys))
		for _, k := range keys {
			if lbl, ok := resolved[k]; ok && lbl != "" {
				dimLabels[k] = lbl
			}
		}
		if len(dimLabels) == 0 {
			continue
		}
		if labels == nil {
			labels = map[string]map[string]string{}
		}
		labels[dim] = dimLabels
		if joined == nil {
			joined = map[string][]facetLabelledRow{}
		}
		joined[dim] = labelledRows(hist, dimLabels)
	}
	view.Labels = labels
	view.FacetsLabelled = joined
}

// labelledRows joins a dimension's histogram with its resolved labels into
// {key, label, count} rows ordered by count descending, then key.
func labelledRows(
	hist cutting_garden_plugins.FacetHistogram,
	labels map[string]string,
) []facetLabelledRow {
	rows := make([]facetLabelledRow, 0, len(hist))
	for k, n := range hist {
		rows = append(rows, facetLabelledRow{Key: k, Label: labels[k], Count: n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

// labelledDimensionKeys collects the dimension keys a plugin declares
// Kind == FacetLabelled, across every node type in its facet schema.
func labelledDimensionKeys(
	describer cutting_garden_plugins.FacetDescriber,
) map[string]bool {
	out := map[string]bool{}
	for _, ntf := range describer.DescribeFacets() {
		for _, dim := range ntf.Dimensions {
			if dim.Kind == cutting_garden_plugins.FacetLabelled {
				out[dim.Key] = true
			}
		}
	}
	return out
}
