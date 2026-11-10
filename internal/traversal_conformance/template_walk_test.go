package traversal_conformance

import (
	"fmt"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// A root listing more leaves than the walk's list budget, ahead of the
// container holding the templated node: the walk must spend its lists on
// containers only, so it still reaches and checks the templated node.
func TestWalkTemplatedNodesListsContainersOnly(t *testing.T) {
	const root = "fake://root"
	types := []cutting_garden_plugins.NodeType{
		{Tag: "leaf", Container: false},
		{Tag: "box", Container: true},
		{Tag: "issue", Container: true, URITemplate: "fake://root/box/issue-{n}"},
	}

	var children []traversal_serve.NodeView
	for i := 0; i < templateWalkLists+8; i++ {
		children = append(children, traversal_serve.NodeView{
			URI: fmt.Sprintf("fake://root/leaf-%d", i), Type: "leaf",
		})
	}
	children = append(children, traversal_serve.NodeView{URI: "fake://root/box", Type: "box"})

	listed := map[string][]traversal_serve.NodeView{
		root:              children,
		"fake://root/box": {{URI: "fake://root/box/issue-1", Type: "issue"}},
	}
	var listCalls []string
	list := func(uri string) ([]traversal_serve.NodeView, bool) {
		listCalls = append(listCalls, uri)
		return listed[uri], true
	}

	var checkedURIs []string
	lists, checked := walkTemplatedNodes([]string{root}, list, types,
		func(node traversal_serve.NodeView, _ string, _, _ int) bool {
			checkedURIs = append(checkedURIs, node.URI)
			return true
		},
	)

	if checked != 1 || len(checkedURIs) != 1 || checkedURIs[0] != "fake://root/box/issue-1" {
		t.Fatalf("checked %d %v, want the one templated node; listed %d: %v",
			checked, checkedURIs, lists, listCalls)
	}
	for _, uri := range listCalls {
		if uri != root && uri != "fake://root/box" && uri != "fake://root/box/issue-1" {
			t.Errorf("listed %s, a leaf: the walk lists containers only", uri)
		}
	}
}
