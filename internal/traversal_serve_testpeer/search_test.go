package traversal_serve_testpeer

import (
	"context"
	stderrors "errors"
	"net/url"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// TestWireSearchIndistinguishableFromLinked is RFC 0016 §6's conformance
// bar: nodes.search over the spawned peer answers exactly as the linked
// Searcher — the same nodes and completeness for substring searches with a
// filter and a limit, the same decline for regex (-32001 on the wire), and
// a rejected empty query.
func TestWireSearchIndistinguishableFromLinked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Setenv(mainModeEnv, "1")

	linked := NewPlugin()
	wire := traversal_serve.NewWirePlugin(traversal_serve.PluginSpec{
		Name:    "cgtest-search",
		Command: []string{selfExecutable(t)},
		Schemes: []string{Scheme},
	})
	defer func() { _ = wire.Close() }()

	root, err := url.Parse(RootBox)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []cutting_garden_plugins.SearchRequest{
		{Root: root, Query: "body", Mode: cutting_garden_plugins.SearchSubstring},
		{Root: root, Query: "comment", Mode: cutting_garden_plugins.SearchSubstring},
		{
			Root: root, Query: "body", Mode: cutting_garden_plugins.SearchSubstring,
			Filter: cutting_garden_plugins.FacetFilter{{Dimension: "state", Value: "open"}},
		},
		{Root: root, Query: "body", Mode: cutting_garden_plugins.SearchSubstring, Limit: 1},
	} {
		want, err := linked.Search(ctx, req)
		if err != nil {
			t.Fatalf("linked %+v: %v", req, err)
		}
		got, err := wire.Search(ctx, req)
		if err != nil {
			t.Fatalf("wire %+v: %v", req, err)
		}
		if len(want.Nodes) == 0 {
			t.Fatalf("%+v: the fixture should match something", req)
		}
		requireNodesEqual(t, req.Query, want.Nodes, got.Nodes)
		if got.Complete != want.Complete {
			t.Errorf("%+v: complete wire %v, linked %v", req, got.Complete, want.Complete)
		}
	}

	regex := cutting_garden_plugins.SearchRequest{Root: root, Query: "b.dy", Mode: cutting_garden_plugins.SearchRegex}
	if _, err := wire.Search(ctx, regex); !stderrors.Is(err, cutting_garden_plugins.ErrSearchModeUnsupported) {
		t.Errorf("wire regex: err = %v, want ErrSearchModeUnsupported (-32001)", err)
	}

	empty := cutting_garden_plugins.SearchRequest{Root: root, Mode: cutting_garden_plugins.SearchSubstring}
	if _, err := wire.Search(ctx, empty); err == nil ||
		stderrors.Is(err, cutting_garden_plugins.ErrSearchModeUnsupported) {
		t.Errorf("wire empty query: err = %v, want an invalid-params failure", err)
	}
}
