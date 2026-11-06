package traversal_serve

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// datedPlugin declares a date-kind facet and records the filter its
// EnrichedLister receives.
type datedPlugin struct {
	got cutting_garden_plugins.FacetFilter
}

func (*datedPlugin) Schemes() []string { return []string{"dated"} }
func (*datedPlugin) TypeTag() string   { return "dated-v1" }

func (*datedPlugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{{Tag: "dated-leaf-v1"}}
}

func (*datedPlugin) ListRoots(context.Context, *url.URL) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

func (*datedPlugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "dated-leaf-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "date_start", Kind: cutting_garden_plugins.FacetDate},
		},
	}}
}

func (p *datedPlugin) ListEnriched(
	_ context.Context, _ *url.URL, filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	p.got = filter
	return nil, true, nil
}

func listWithFilter(t *testing.T, s *server, dimension, value string) error {
	t.Helper()
	params, err := json.Marshal(NodesListParams{
		URI:    "dated://h/",
		Filter: []PredicateView{{Dimension: dimension, Value: value}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.handleNodesList(context.Background(), params)
	return err
}

// The wire server validates a filter against its own plugin's schema
// (cutting-garden#239): that arms a date predicate for prefix matching,
// which never crosses the wire, and refuses an undeclared dimension as a
// caller fault.
func TestWireServer_ValidatesTheFilter(t *testing.T) {
	plugin := &datedPlugin{}
	s, err := newServer(ServeConfig{Plugin: plugin})
	if err != nil {
		t.Fatal(err)
	}

	if err := listWithFilter(t, s, "date_start", "2026-08"); err != nil {
		t.Fatalf("a declared date filter was refused: %v", err)
	}
	day := map[string][]cutting_garden_plugins.FacetValue{"date_start": {{Key: "2026-08-15"}}}
	if !plugin.got.Matches(day) {
		t.Error("date_start=2026-08 did not prefix-match 2026-08-15 inside the plugin")
	}

	err = listWithFilter(t, s, "no_such_dimension", "x")
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Errorf("an undeclared dimension = %#v, want an invalid-params RPC error", err)
	}
}
