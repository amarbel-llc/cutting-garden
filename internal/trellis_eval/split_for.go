package trellis_eval

import (
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// SplitFilterFor splits a caller's one-step filter against lister's declared
// facet schema and validates the pushdown (cutting-garden#161, #388). It is
// the one place a plugin-bound filter is parsed and checked, shared by the
// mcp read/write tools and the `mutate` command so a sweep filter means the
// same thing wherever it is spelled.
func SplitFilterFor(
	raw string, lister cgp.RootLister,
) (cgp.FacetFilter, Residual, error) {
	var schema []cgp.NodeTypeFacets
	if fd, ok := lister.(cgp.FacetDescriber); ok {
		schema = fd.DescribeFacets()
	}
	if len(schema) == 0 {
		schema = nil
	}
	pushdown, residual, _, err := SplitFilter(raw, schema)
	if err != nil {
		return nil, Residual{}, err
	}
	if err := pushdown.Validate(schema); err != nil {
		return nil, Residual{}, err
	}
	if err := residual.ValidateFields(lister); err != nil {
		return nil, Residual{}, err
	}
	return pushdown, residual, nil
}
