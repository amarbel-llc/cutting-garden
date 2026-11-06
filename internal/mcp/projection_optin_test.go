package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// optInLister adds a heavy `notes` listing field, declared OptIn, to projLister's
// nodes (cutting-garden#386): it is carried on every node but only a request
// that names it gets it back.
type optInLister struct{ projLister }

func withNotes(nodes []cutting_garden_plugins.Node) []cutting_garden_plugins.Node {
	for _, n := range nodes {
		n.Fields["notes"] = "long body of " + n.Name
	}
	return nodes
}

func (l optInLister) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	nodes, err := l.projLister.ListRoots(ctx, node)
	if node.Path == "/work" {
		withNotes(nodes)
	}
	return nodes, err
}

func (l optInLister) ListEnriched(
	ctx context.Context, node *url.URL, f cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	nodes, ok, err := l.projLister.ListEnriched(ctx, node, f)
	if ok {
		withNotes(nodes)
	}
	return nodes, ok, err
}

func (optInLister) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	return []cutting_garden_plugins.NodeTypeListingFields{{
		Tag: "test-object-v1",
		Fields: []cutting_garden_plugins.ListingField{
			{Key: "summary"},
			{Key: "due"},
			{Key: "categories"},
			{Key: "notes", Label: "Notes", OptIn: true},
		},
	}}
}

func listOptIn(t *testing.T, args string) []map[string]any {
	t.Helper()
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(optInLister{})
	res, err := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(args))
	if err != nil || res.IsError {
		t.Fatalf("list_nodes: %v %v", err, res.Content[0].Text)
	}
	return rawNodes(t, res.Content[0].Text)
}

func fieldsOf(t *testing.T, n map[string]any) map[string]any {
	t.Helper()
	f, _ := n["fields"].(map[string]any)
	return f
}

// An OptIn field never rides the default enriched listing (a body per child
// would swamp it).
func TestListNodes_OptInFieldOmittedByDefault(t *testing.T) {
	nodes := listOptIn(t, `{"uri":"faketest://h/work","filter":"status=NEEDS-ACTION"}`)
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}
	if _, ok := fieldsOf(t, nodes[0])["notes"]; ok {
		t.Errorf("the default listing carries the opt-in notes field: %v", fieldsOf(t, nodes[0]))
	}
	if _, ok := fieldsOf(t, nodes[0])["summary"]; !ok {
		t.Errorf("the default listing lost an ordinary field: %v", fieldsOf(t, nodes[0]))
	}
}

// The container read and unfiltered list_nodes share enrichedNodeViews; it must
// not edit the node's own Fields map, which the plugin caches.
func TestEnrichedNodeViews_OptInDropIsACopy(t *testing.T) {
	nodes := withNotes(projNodes("h"))
	views, err := enrichedNodeViews(optInLister{}, nodes, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := views[0].Fields["notes"]; ok {
		t.Errorf("view fields = %v, want notes dropped", views[0].Fields)
	}
	if nodes[0].Fields["notes"] == nil {
		t.Error("enrichedNodeViews edited the node's own Fields")
	}
}

// Naming it under fields returns it.
func TestListNodes_OptInFieldReturnedWhenProjected(t *testing.T) {
	nodes := listOptIn(t, `{"uri":"faketest://h/work","fields":["summary","notes"]}`)
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}
	f := fieldsOf(t, nodes[0])
	if f["notes"] != "long body of a.ics" {
		t.Errorf("fields = %v, want notes projected", f)
	}
}
