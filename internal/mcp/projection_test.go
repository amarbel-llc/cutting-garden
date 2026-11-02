package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// projLister is a tagged, faceted, field-declaring lister whose ListRoots and
// ListEnriched agree, so the filter path and the trellis query path return the
// same nodes and a projection can be compared across them.
type projLister struct{ taggedEnrichedLister }

func facetKeys(keys []string) []cutting_garden_plugins.FacetValue {
	out := make([]cutting_garden_plugins.FacetValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, cutting_garden_plugins.FacetValue{Key: k})
	}
	return out
}

func projNodes(host string) []cutting_garden_plugins.Node {
	mk := func(name, summary string, cats []string, status string) cutting_garden_plugins.Node {
		return cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "faketest", Host: host, Path: "/work/" + name},
			Name: name,
			Type: "test-object-v1",
			Facets: map[string][]cutting_garden_plugins.FacetValue{
				"status":   {{Key: status}},
				"priority": {{Key: "3_unspecified"}},
				// categories is a facet, a listing field and the tag set,
				// as in caldav.
				"categories": facetKeys(cats),
			},
			Fields: map[string]any{"summary": summary, "due": "2026-11-20", "categories": cats},
		}
	}
	return []cutting_garden_plugins.Node{
		mk("a.ics", "Buy milk", []string{"work", "errand"}, "NEEDS-ACTION"),
		mk("b.ics", "Call back", nil, "COMPLETED"),
	}
}

func (projLister) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node.Path == "/work" {
		return projNodes(node.Host), nil
	}
	return fakeLister{}.ListRoots(ctx, node)
}

func (projLister) ListEnriched(
	_ context.Context, node *url.URL, _ cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	if node.Path != "/work" {
		return nil, false, nil // the calendars level: decline, ListRoots serves it
	}
	return projNodes(node.Host), true, nil
}

func (projLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "status"}, {Key: "priority"}, {Key: "categories"},
		},
	}}
}

func (projLister) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	return []cutting_garden_plugins.NodeTypeListingFields{{
		Tag: "test-object-v1",
		Fields: []cutting_garden_plugins.ListingField{
			{Key: "summary"}, {Key: "due"}, {Key: "categories"},
		},
	}}
}

func listProj(t *testing.T, args string) (string, bool) {
	t.Helper()
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(projLister{})
	res, err := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(args))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	return res.Content[0].Text, res.IsError
}

// rawNodes decodes a listing wrapper's nodes as generic maps, so the test sees
// exactly which keys the wire carries.
func rawNodes(t *testing.T, text string) []map[string]any {
	t.Helper()
	var w struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(text), &w); err != nil {
		t.Fatalf("decode: %v (%q)", err, text)
	}
	return w.Nodes
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasKeys(m map[string]any, want ...string) bool {
	if len(m) != len(want) {
		return false
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func TestListNodesFields_FilterPath(t *testing.T) {
	text, isErr := listProj(t,
		`{"uri":"faketest://h/work","filter":"status=NEEDS-ACTION","fields":["summary","priority","tags"]}`)
	if isErr {
		t.Fatalf("errored: %s", text)
	}
	nodes := rawNodes(t, text)
	if len(nodes) == 0 {
		t.Fatalf("no nodes: %s", text)
	}
	n := nodes[0]
	// uri, name, type always; container and mimeType dropped unless listed.
	if !hasKeys(n, "uri", "name", "type", "facets", "fields", "tags") {
		t.Fatalf("keys = %v", keysOf(n))
	}
	if got := n["fields"].(map[string]any); !hasKeys(got, "summary") {
		t.Errorf("fields = %v, want only summary", got)
	}
	if got := n["facets"].(map[string]any); !hasKeys(got, "priority") {
		t.Errorf("facets = %v, want only priority", got)
	}
}

func TestListNodesFields_FacetAndFieldNameBoth(t *testing.T) {
	// "status" is a facet dimension only; "categories" is a facet, a field and
	// the tag set: each requested name appears exactly once.
	text, isErr := listProj(t, `{"uri":"faketest://h/work","fields":["status","categories"]}`)
	if isErr {
		t.Fatalf("errored (unfiltered + fields uses the enriched path): %s", text)
	}
	n := rawNodes(t, text)[0]
	if !hasKeys(n, "uri", "name", "type", "facets", "fields") {
		t.Fatalf("keys = %v", keysOf(n))
	}
	if !hasKeys(n["facets"].(map[string]any), "status") || !hasKeys(n["fields"].(map[string]any), "categories") {
		t.Errorf("facets=%v fields=%v: categories must appear once, under fields", n["facets"], n["fields"])
	}
}

func TestListNodesFields_CategoriesAndTagsEachOnce(t *testing.T) {
	text, isErr := listProj(t, `{"uri":"faketest://h/work","fields":["categories","tags"]}`)
	if isErr {
		t.Fatalf("errored: %s", text)
	}
	n := rawNodes(t, text)[0]
	if !hasKeys(n, "uri", "name", "type", "fields", "tags") {
		t.Fatalf("keys = %v (want no facets: categories lives under fields)", keysOf(n))
	}
	if !hasKeys(n["fields"].(map[string]any), "categories") {
		t.Errorf("fields = %v", n["fields"])
	}
}

// A field added to nodeView must reach a projected view too: the slim marshal
// differs from the plain one by the container key only.
func TestNodeViewSlimMarshalDropsOnlyContainer(t *testing.T) {
	v := nodeView{
		URI: "u", Name: "n", Type: "t", Container: true, MimeType: "m",
		Facets: map[string][]string{"f": {"x"}}, Fields: map[string]any{"k": 1}, Tags: []string{"t"},
	}
	plain, _ := json.Marshal(v)
	v.slim = true
	slim, _ := json.Marshal(v)
	var p, s map[string]any
	if err := json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(slim, &s); err != nil {
		t.Fatal(err)
	}
	delete(p, "container")
	if !reflect.DeepEqual(p, s) {
		t.Errorf("plain minus container %v\nslim %v", p, s)
	}
}

// The same projection gives identical views on the filter path and the query
// path.
func TestListNodesFields_QueryPathMatchesFilterPath(t *testing.T) {
	fields := `"fields":["summary","status","tags"]`
	filtered, isErr := listProj(t, `{"uri":"faketest://h/work",`+fields+`,"filter":"priority=3_unspecified"}`)
	if isErr {
		t.Fatalf("filter path errored: %s", filtered)
	}
	queried, isErr := listProj(t,
		`{"uri":"faketest://h/","query":"!test-calendar-v1 -> !test-object-v1",`+fields+`}`)
	if isErr {
		t.Fatalf("query path errored: %s", queried)
	}
	if a, b := rawNodes(t, filtered), rawNodes(t, queried); !reflect.DeepEqual(a, b) {
		t.Errorf("filter path %v\nquery path  %v", a, b)
	}
}

func TestListNodesFields_Refusals(t *testing.T) {
	cases := []struct{ name, args, want string }{
		{"empty array is refused, not 'all'", `{"uri":"faketest://h/work","fields":[]}`, "empty"},
		{"unknown name lists the valid ones", `{"uri":"faketest://h/work","fields":["nope"]}`, "summary"},
		{"unknown name is named", `{"uri":"faketest://h/work","fields":["summary","nope"]}`, `"nope"`},
		{"bare plus fields", `{"uri":"faketest://h/work","bare":true,"fields":["summary"]}`, "bare"},
		{"fields without a uri", `{"fields":["summary"]}`, "uri"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, isErr := listProj(t, c.args)
			if !isErr || !strings.Contains(text, c.want) {
				t.Fatalf("isError=%v text=%q; want an error containing %q", isErr, text, c.want)
			}
		})
	}
	text, _ := listProj(t, `{"uri":"faketest://h/work","fields":["nope"]}`)
	for _, valid := range []string{"summary", "due", "categories", "status", "priority", "tags"} {
		if !strings.Contains(text, valid) {
			t.Errorf("the error does not list valid name %q: %s", valid, text)
		}
	}
}

// Without fields, the output is unchanged: the full enriched view keeps
// container and mimeType.
func TestListNodesFields_AbsentLeavesDefaultShape(t *testing.T) {
	text, isErr := listProj(t, `{"uri":"faketest://h/work","filter":"status=NEEDS-ACTION"}`)
	if isErr {
		t.Fatalf("errored: %s", text)
	}
	n := rawNodes(t, text)[0]
	if _, ok := n["container"]; !ok {
		t.Errorf("default view lost container: %v", keysOf(n))
	}
}
