package cutting_garden_plugins

import (
	"reflect"
	"strings"
	"testing"
)

// The caldav shape (cutting-garden#413): a VTODO's missing STATUS is the alias
// `none` and implies needs-action; a VEVENT declares the same alias with no
// implication, so its `none` means only "no value".
var aliasSchema = []NodeTypeFacets{
	{Tag: "todo-v1", Dimensions: []FacetDimension{
		{Key: "status", FoldCase: true, NoValueAlias: "none", AliasImplies: []string{"needs-action"}},
		{Key: "component"},
	}},
	{Tag: "event-v1", Dimensions: []FacetDimension{
		{Key: "status", FoldCase: true, NoValueAlias: "none"},
	}},
}

func facets(kv ...string) map[string][]FacetValue {
	out := map[string][]FacetValue{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = append(out[kv[i]], FacetValue{Key: kv[i+1]})
	}
	return out
}

func TestNoValueAliases_ForMatching(t *testing.T) {
	a := NoValueAliasesOf(aliasSchema)
	for _, c := range []struct {
		name, tag string
		in, want  map[string][]FacetValue
	}{
		{
			"a task holding the alias also holds the implied value", "todo-v1",
			facets("status", "none", "component", "VTODO"),
			facets("status", "none", "status", "needs-action", "component", "VTODO"),
		},
		{
			"the alias compares under the dimension's fold", "todo-v1",
			facets("status", "NONE"), facets("status", "NONE", "status", "needs-action"),
		},
		{
			"a task with a real status is untouched", "todo-v1",
			facets("status", "completed"), facets("status", "completed"),
		},
		{
			"an event's alias implies nothing", "event-v1",
			facets("status", "none"), facets("status", "none"),
		},
		{
			"an undeclared type is untouched", "other-v1",
			facets("status", "none"), facets("status", "none"),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := a.ForMatching(c.tag, c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ForMatching = %v, want %v", got, c.want)
			}
		})
	}
}

// ForMatching never mutates the node's own facets: the counted value stays
// the alias alone.
func TestNoValueAliases_ForMatchingLeavesInputAlone(t *testing.T) {
	in := facets("status", "none")
	NoValueAliasesOf(aliasSchema).ForMatching("todo-v1", in)
	if !reflect.DeepEqual(in, facets("status", "none")) {
		t.Errorf("input mutated to %v", in)
	}
}

func TestFacetFilter_MatchesNode(t *testing.T) {
	a := NoValueAliasesOf(aliasSchema)
	filter := func(raw string) FacetFilter {
		f, err := ParseFacetFilter(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Validate(aliasSchema); err != nil {
			t.Fatal(err)
		}
		return f
	}
	for _, c := range []struct {
		filter, tag string
		node        map[string][]FacetValue
		want        bool
	}{
		{"status=needs-action", "todo-v1", facets("status", "none"), true},
		{"status=NEEDS-ACTION", "todo-v1", facets("status", "none"), true},
		{"status=none", "todo-v1", facets("status", "none"), true},
		{"status=completed", "todo-v1", facets("status", "none"), false},
		{"status=needs-action", "event-v1", facets("status", "none"), false},
		{"status=needs-action", "todo-v1", facets("status", "completed"), false},
	} {
		if got := filter(c.filter).MatchesNode(a, c.tag, c.node); got != c.want {
			t.Errorf("%s on %s %v: MatchesNode = %v, want %v", c.filter, c.tag, c.node, got, c.want)
		}
	}
}

func TestNoValueAliases_WithoutAliases(t *testing.T) {
	a := NoValueAliasesOf(aliasSchema)
	got := a.WithoutAliases("todo-v1", facets("status", "none", "component", "VTODO"))
	if want := facets("component", "VTODO"); !reflect.DeepEqual(got, want) {
		t.Errorf("WithoutAliases = %v, want %v", got, want)
	}
	if got := a.WithoutAliases("todo-v1", facets("status", "completed")); !reflect.DeepEqual(got, facets("status", "completed")) {
		t.Errorf("a real value was dropped: %v", got)
	}
}

func TestValidateNoValueAliases(t *testing.T) {
	dim := func(d FacetDimension) []NodeTypeFacets {
		return []NodeTypeFacets{{Tag: "t", Dimensions: []FacetDimension{d}}}
	}
	vals := func(keys ...string) []FacetValue {
		out := make([]FacetValue, len(keys))
		for i, k := range keys {
			out[i] = FacetValue{Key: k}
		}
		return out
	}
	for _, c := range []struct {
		name    string
		dims    []NodeTypeFacets
		wantErr string
	}{
		{"open alias with implication", aliasSchema, ""},
		{"implies without an alias", dim(FacetDimension{Key: "s", AliasImplies: []string{"x"}}), "without a no-value alias"},
		{"closed alias not in the domain", dim(FacetDimension{Key: "s", Values: vals("a"), NoValueAlias: "none"}), `"none"`},
		{"closed implied value not in the domain", dim(FacetDimension{Key: "s", Values: vals("none", "a"), NoValueAlias: "none", AliasImplies: []string{"b"}}), `"b"`},
		{"an empty implied value", dim(FacetDimension{Key: "s", NoValueAlias: "none", AliasImplies: []string{""}}), "empty"},
		{"an implied value twice", dim(FacetDimension{Key: "s", NoValueAlias: "none", AliasImplies: []string{"a", "a"}}), "more than once"},
		{"the alias implying itself", dim(FacetDimension{Key: "s", NoValueAlias: "none", AliasImplies: []string{"none"}}), "itself"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateNoValueAliases(c.dims)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("error = %v, want one naming %q", err, c.wantErr)
			}
		})
	}
}
