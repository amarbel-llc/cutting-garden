package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
)

// growingStore is a container whose children a BulkMutate create adds to:
// the shape of a source whose listing changes because of a write made
// through the same server.
type growingStore struct {
	children []string
}

func (*growingStore) Schemes() []string { return []string{"faketest"} }
func (*growingStore) TypeTag() string   { return "growing-v1" }

func (*growingStore) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{{Tag: "growing-leaf-v1"}}
}

func (g *growingStore) ListRoots(
	_ context.Context, u *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if u.Path != "/" {
		return nil, nil
	}
	nodes := make([]cutting_garden_plugins.Node, 0, len(g.children))
	for _, name := range g.children {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "faketest", Host: "h", Path: "/" + name},
			Name: name,
			Type: "growing-leaf-v1",
		})
	}
	return nodes, nil
}

func (g *growingStore) BulkMutate(
	_ context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	var result cutting_garden_plugins.BulkResult
	for _, op := range req.Ops {
		name := strings.TrimPrefix(op.TempID, "+")
		g.children = append(g.children, name)
		created := &url.URL{Scheme: "faketest", Host: "h", Path: "/" + name}
		result.AppliedNodes = append(result.AppliedNodes, created)
		result.Created = append(result.Created, cutting_garden_plugins.BulkCreated{
			TempID: op.TempID, URI: created,
		})
	}
	return result, nil
}

func growingResolve(g *growingStore) resolveFunc {
	return func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, g, nil
	}
}

// A node created through mutate_nodes shows up in the next list_nodes of
// its container through the same server (cutting-garden#302): the write
// drops the server's own memoized listing instead of serving it until the
// background refresh comes round.
func TestMutateNodes_NextListingSeesTheCreatedNode(t *testing.T) {
	store := &growingStore{children: []string{"a"}}
	root, _ := url.Parse("faketest://h/")
	resources := &Resources{
		roots:    []*url.URL{root},
		resolve:  growingResolve(store),
		facets:   newFacetCache(),
		listings: newListingCache(),
	}
	tools := &Tools{
		roots:         []*url.URL{root},
		resolveLister: growingResolve(store),
		resolveBulk: func(uriStr string) (*url.URL, cutting_garden_plugins.BulkMutator, error) {
			u, err := url.Parse(uriStr)
			return u, store, err
		},
		reader: resources,
		facets: resources,
	}

	list := func() string {
		t.Helper()
		res, err := tools.CallTool(context.Background(), "list_nodes",
			json.RawMessage(`{"uri":"faketest://h/"}`))
		if err != nil || res.IsError {
			t.Fatalf("list_nodes: %v %+v", err, res)
		}
		return res.Content[0].Text
	}

	if got := list(); strings.Contains(got, "faketest://h/b") {
		t.Fatalf("b listed before it was created: %s", got)
	}

	res, err := tools.CallTool(context.Background(), "mutate_nodes", json.RawMessage(
		`{"groups":[{"anchor":"faketest://h/","ops":[`+
			`{"kind":"create","id":"+b","type":"growing-leaf-v1","body":"{}"}]}]}`))
	if err != nil || res.IsError {
		t.Fatalf("mutate_nodes: %v %+v", err, resultText(res))
	}

	if got := list(); !strings.Contains(got, "faketest://h/b") {
		t.Errorf("the listing after the create omits b: %s", got)
	}
}

// Dropping after a write is scoped to the written source: a cached listing
// of another host stays served from the cache.
func TestListingCache_DropSourceKeepsOtherHosts(t *testing.T) {
	lc := newListingCache()
	lc.entries["faketest://h/"] = &listingCacheEntry{}
	lc.entries["faketest://h/cal/"] = &listingCacheEntry{}
	lc.entries["faketest://other/"] = &listingCacheEntry{}
	lc.entries["othertest://h/"] = &listingCacheEntry{}

	written, _ := url.Parse("faketest://h/cal/x.ics")
	lc.dropSource(written)

	for _, gone := range []string{"faketest://h/", "faketest://h/cal/"} {
		if _, ok := lc.entries[gone]; ok {
			t.Errorf("%s survived a write to its source", gone)
		}
	}
	for _, kept := range []string{"faketest://other/", "othertest://h/"} {
		if _, ok := lc.entries[kept]; !ok {
			t.Errorf("%s was dropped by a write to another source", kept)
		}
	}
}

func resultText(res *protocol.ToolCallResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].Text
}
