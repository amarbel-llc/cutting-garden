package mcp

import (
	"encoding/json"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/node_view"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// list_nodes `fields`: a projection of the enriched view.
// uri, name and type are always kept; container and mimeType are dropped
// unless listed; each other name selects a declared listing field
// (Node.Fields), a facet dimension (Node.Facets) or "tags". A name that is
// valid for any returned node type is accepted and simply absent on nodes
// that lack it; a name valid for none is a bad request listing the valid
// ones. It changes the output only: which nodes come back is the filter's or
// the query's business.

const (
	projectContainer = "container"
	projectMimeType  = "mimeType"
	projectTags      = "tags"
)

// MarshalJSON writes a projected (slim) view without the container and
// mimeType keys the projection dropped; any other view marshals as before.
func (v nodeView) MarshalJSON() ([]byte, error) {
	type plain nodeView
	if !v.slim {
		return json.Marshal(plain(v))
	}
	// Marshal as any view and drop the one key a projection omits, so a field
	// added to nodeView later cannot vanish from projected views only.
	b, err := json.Marshal(plain(v))
	if err != nil || v.keepContainer != nil {
		return b, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	delete(m, "container")
	return json.Marshal(m)
}

// validProjectionNames is every name the projection accepts for these nodes:
// the declared listing fields and facet dimensions of each returned node
// type (every declared type when nothing was returned), plus the always-valid
// container, mimeType and, when the listing presents tags, tags.
func validProjectionNames(
	lister cutting_garden_plugins.RootLister,
	nodes []cutting_garden_plugins.Node,
	hasTags bool,
) []string {
	types := map[string]bool{}
	for _, n := range nodes {
		types[n.Type] = true
	}
	all := len(types) == 0
	names := map[string]bool{projectContainer: true, projectMimeType: true}
	if hasTags {
		names[projectTags] = true
	}
	if d, ok := lister.(cutting_garden_plugins.ListingFieldsDescriber); ok {
		for _, nt := range d.DescribeListingFields() {
			if all || types[nt.Tag] {
				for _, f := range nt.Fields {
					names[f.Key] = true
				}
			}
		}
	}
	if d, ok := lister.(cutting_garden_plugins.FacetDescriber); ok {
		for _, nt := range d.DescribeFacets() {
			if all || types[nt.Tag] {
				for _, dim := range nt.Dimensions {
					names[dim.Key] = true
				}
			}
		}
	}
	out := make([]string, 0, len(names))
	for k := range names {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// projectedNodeViews projects nodes onto the enriched view restricted to
// fields. fields MUST be non-empty (the caller refuses an empty array).
func projectedNodeViews(
	lister cutting_garden_plugins.RootLister,
	nodes []cutting_garden_plugins.Node,
	tagsOverride string,
	fields []string,
) ([]nodeView, error) {
	presentTags, err := node_view.NodeTagsPresenter(lister, tagsOverride)
	if err != nil {
		return nil, err
	}
	valid := validProjectionNames(lister, nodes, presentTags != nil)
	var unknown []string
	for _, f := range fields {
		if !slices.Contains(valid, f) {
			unknown = append(unknown, `"`+f+`"`)
		}
	}
	if len(unknown) > 0 {
		return nil, errors.BadRequestf(
			"list_nodes: fields %s not valid for these nodes; valid names: %s",
			strings.Join(unknown, ", "), strings.Join(valid, ", "))
	}
	want := map[string]bool{}
	for _, f := range fields {
		want[f] = true
	}

	declared := declaredListingFields(lister)
	views := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		v := enrichedNodeView(lister, n, presentTags)
		v.slim = true
		if want[projectContainer] {
			c := v.Container
			v.keepContainer = &c
		}
		if !want[projectMimeType] {
			v.MimeType = ""
		}
		if !want[projectTags] {
			v.Tags = nil
		}
		// A requested name is emitted ONCE: under fields when the node's type
		// declares it as a listing field, else under facets. (categories is
		// both a facet and a field in caldav; tags is its own key.)
		asField := map[string]bool{}
		for k := range want {
			if declared[n.Type][k] {
				asField[k] = true
			}
		}
		wantFacet := map[string]bool{}
		for k := range want {
			if !asField[k] {
				wantFacet[k] = true
			}
		}
		v.Facets = keepKeys(v.Facets, wantFacet)
		v.Fields = keepKeys(v.Fields, asField)
		views = append(views, v)
	}
	return views, nil
}

// declaredListingFields maps a node type tag to its declared listing-field
// keys.
func declaredListingFields(lister cutting_garden_plugins.RootLister) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if d, ok := lister.(cutting_garden_plugins.ListingFieldsDescriber); ok {
		for _, nt := range d.DescribeListingFields() {
			set := map[string]bool{}
			for _, f := range nt.Fields {
				set[f.Key] = true
			}
			out[nt.Tag] = set
		}
	}
	return out
}

// keepKeys is m restricted to the wanted keys (nil when none remain, so the
// key is omitted from the view).
func keepKeys[V any](m map[string]V, want map[string]bool) map[string]V {
	var out map[string]V
	for k, v := range m {
		if want[k] {
			if out == nil {
				out = map[string]V{}
			}
			out[k] = v
		}
	}
	return out
}
