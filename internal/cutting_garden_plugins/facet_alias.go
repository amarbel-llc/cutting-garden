package cutting_garden_plugins

import (
	"fmt"
	"maps"
	"strings"
)

// NoValueAliases indexes a plugin's declared no-value aliases (RFC 0012 §2,
// cutting-garden#413) by node type and dimension key: the one place every
// matcher (FacetFilter.MatchesNode, a plugin's own pushdown, trellis) and every
// value-partitioning consumer (organize) reads them, so they agree. The zero
// value declares nothing and changes nothing.
type NoValueAliases map[string]map[string]FacetDimension

// NoValueAliasesOf indexes the dimensions of types that declare a no-value
// alias.
func NoValueAliasesOf(types []NodeTypeFacets) NoValueAliases {
	var out NoValueAliases
	for _, t := range types {
		for _, d := range t.Dimensions {
			if d.NoValueAlias == "" {
				continue
			}
			if out == nil {
				out = NoValueAliases{}
			}
			if out[t.Tag] == nil {
				out[t.Tag] = map[string]FacetDimension{}
			}
			out[t.Tag][d.Key] = d
		}
	}
	return out
}

// NoValueAliasesFor probes lister's FacetDescriber; a plugin without one
// declares no aliases.
func NoValueAliasesFor(lister RootLister) NoValueAliases {
	fd, ok := lister.(FacetDescriber)
	if !ok {
		return nil
	}
	return NoValueAliasesOf(fd.DescribeFacets())
}

// holdsAlias reports whether values hold d's alias, under d's fold.
func (d FacetDimension) holdsAlias(values []FacetValue) bool {
	return containsFacetValue(values, d.NoValueAlias, d.FoldCase)
}

// ForMatching returns a node's facets as a matcher sees them: for each of the
// type's aliased dimensions the node holds the alias of, its AliasImplies
// values are added. facets itself is never mutated, and is returned as is when
// nothing is implied, so a count (which lifts the node's own facets) still
// counts the alias once.
func (a NoValueAliases) ForMatching(tag string, facets map[string][]FacetValue) map[string][]FacetValue {
	var out map[string][]FacetValue
	for key, d := range a[tag] {
		if len(d.AliasImplies) == 0 || !d.holdsAlias(facets[key]) {
			continue
		}
		if out == nil {
			out = maps.Clone(facets)
		}
		values := append([]FacetValue(nil), facets[key]...)
		for _, implied := range d.AliasImplies {
			if !containsFacetValue(values, implied, d.FoldCase) {
				values = append(values, FacetValue{Key: implied})
			}
		}
		out[key] = values
	}
	if out == nil {
		return facets
	}
	return out
}

// WithoutAliases returns a node's facets as a value-partitioning consumer sees
// them: a dimension whose only value is the type's alias is dropped, so the
// node reads as valueless for it (organize's no-value section). facets itself
// is never mutated.
func (a NoValueAliases) WithoutAliases(tag string, facets map[string][]FacetValue) map[string][]FacetValue {
	var out map[string][]FacetValue
	for key, d := range a[tag] {
		values := facets[key]
		if len(values) != 1 || !d.holdsAlias(values) {
			continue
		}
		if out == nil {
			out = maps.Clone(facets)
		}
		delete(out, key)
	}
	if out == nil {
		return facets
	}
	return out
}

// MatchesNode reports whether a node of type tag matches f, honoring the
// type's no-value alias implications (RFC 0012 §6): the match a listing and a
// fold use. Matches alone treats an alias as a plain value.
func (f FacetFilter) MatchesNode(a NoValueAliases, tag string, facets map[string][]FacetValue) bool {
	return f.Matches(a.ForMatching(tag, facets))
}

// ValidateNoValueAliases checks the no-value alias declarations of types: an
// implication needs an alias; on a CLOSED dimension the alias and every
// implied value are declared values; an implied value is non-empty, listed
// once, and not the alias itself. Linked plugins are checked by a unit test
// against their DescribeFacets; a wire plugin at bring-up.
func ValidateNoValueAliases(types []NodeTypeFacets) error {
	for _, t := range types {
		for _, d := range t.Dimensions {
			if err := validateNoValueAlias(d); err != nil {
				return fmt.Errorf("no-value alias: type %q dimension %q %s", t.Tag, d.Key, err)
			}
		}
	}
	return nil
}

func validateNoValueAlias(d FacetDimension) error {
	if d.NoValueAlias == "" {
		if len(d.AliasImplies) > 0 {
			return fmt.Errorf("declares implied values without a no-value alias")
		}
		return nil
	}
	if d.Values != nil && !containsFacetValue(d.Values, d.NoValueAlias, d.FoldCase) {
		return fmt.Errorf("alias %q is not one of the dimension's declared values", d.NoValueAlias)
	}
	seen := map[string]bool{}
	for _, implied := range d.AliasImplies {
		folded := implied
		if d.FoldCase {
			folded = strings.ToLower(implied)
		}
		switch {
		case implied == "":
			return fmt.Errorf("names an empty implied value")
		case seen[folded]:
			return fmt.Errorf("names implied value %q more than once", implied)
		case implied == d.NoValueAlias || (d.FoldCase && strings.EqualFold(implied, d.NoValueAlias)):
			return fmt.Errorf("alias %q implies itself", implied)
		case d.Values != nil && !containsFacetValue(d.Values, implied, d.FoldCase):
			return fmt.Errorf("implied value %q is not one of the dimension's declared values", implied)
		}
		seen[folded] = true
	}
	return nil
}
