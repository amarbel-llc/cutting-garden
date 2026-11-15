package trellis_eval

import (
	"slices"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// aliasTree is the caldav shape of cutting-garden#413: a statusless task
// carries the alias `none`, which implies needs-action for a task and nothing
// for an event.
func aliasTree(t *testing.T) *fakeTree {
	status := func(v string) map[string][]cgp.FacetValue {
		return map[string][]cgp.FacetValue{"status": {{Key: v}}}
	}
	return &fakeTree{
		children: map[string][]cgp.Node{
			"fake://cal/": {
				node(t, "fake://cal/bare", "todo-v1", status("none")),
				node(t, "fake://cal/open", "todo-v1", status("needs-action")),
				node(t, "fake://cal/done", "todo-v1", status("completed")),
				node(t, "fake://cal/event", "event-v1", status("none")),
			},
		},
		facets: []cgp.NodeTypeFacets{
			{Tag: "todo-v1", Dimensions: []cgp.FacetDimension{{
				Key: "status", FoldCase: true, NoValueAlias: "none", AliasImplies: []string{"needs-action"},
			}}},
			{Tag: "event-v1", Dimensions: []cgp.FacetDimension{{Key: "status", FoldCase: true, NoValueAlias: "none"}}},
		},
	}
}

func TestEvaluate_NoValueAliasImplication(t *testing.T) {
	tree := aliasTree(t)
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"status=needs-action", []string{"fake://cal/bare", "fake://cal/open"}},
		{"status=NEEDS-ACTION", []string{"fake://cal/bare", "fake://cal/open"}},
		{"status=none", []string{"fake://cal/bare", "fake://cal/event"}},
		{"^status=needs-action", []string{"fake://cal/done", "fake://cal/event"}},
		{"status=[needs-action, completed]", []string{"fake://cal/bare", "fake://cal/done", "fake://cal/open"}},
	} {
		t.Run(c.query, func(t *testing.T) {
			if got := evalURIs(t, tree, "fake://cal/", c.query); !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
