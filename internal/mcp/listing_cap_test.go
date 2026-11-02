package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

func capViews3() []nodeView {
	return []nodeView{
		{URI: "u/a", Name: "a", Type: "t"},
		{URI: "u/b", Name: "b", Type: "t"},
		{URI: "u/c", Name: "c", Type: "t"},
	}
}

func TestCapViews_Boundaries(t *testing.T) {
	views := capViews3()
	two := nodeCost(views[0]) + nodeCost(views[1])

	t.Run("exactly at the cap keeps those nodes", func(t *testing.T) {
		page, tr := capViews(views, 0, 0, two)
		if len(page) != 2 || !tr.Truncated || tr.Shown != 2 || tr.Total != 3 {
			t.Errorf("page=%d trailer=%+v", len(page), tr)
		}
	})
	t.Run("one byte over drops the last", func(t *testing.T) {
		page, tr := capViews(views, 0, 0, two-1)
		if len(page) != 1 || tr.Shown != 1 || tr.Total != 3 {
			t.Errorf("page=%d trailer=%+v", len(page), tr)
		}
	})
	t.Run("a first node larger than the cap is still returned", func(t *testing.T) {
		page, tr := capViews(views, 0, 0, 1)
		if len(page) != 1 || !tr.Truncated || tr.Shown != 1 || tr.Total != 3 {
			t.Errorf("page=%d trailer=%+v", len(page), tr)
		}
	})
	t.Run("everything fits: no trailer", func(t *testing.T) {
		page, tr := capViews(views, 0, 0, 1<<20)
		if len(page) != 3 || tr.Truncated || tr.Hint != "" {
			t.Errorf("page=%d trailer=%+v", len(page), tr)
		}
	})
	t.Run("an explicit limit disables the cap", func(t *testing.T) {
		page, tr := capViews(views, 0, 3, 1)
		if len(page) != 3 || tr.Truncated {
			t.Errorf("page=%d trailer=%+v", len(page), tr)
		}
	})
	t.Run("offset applies first; total is the true match count", func(t *testing.T) {
		page, tr := capViews(views, 1, 0, 1)
		if len(page) != 1 || page[0].Name != "b" || tr.Total != 3 || !strings.Contains(tr.Hint, "offset=2") {
			t.Errorf("page=%v trailer=%+v", page, tr)
		}
	})
}

// bigLister returns two nodes, the first with a field far over the cap.
type bigLister struct{ projLister }

func (bigLister) ListEnriched(
	_ context.Context, node *url.URL, _ cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	if node.Path != "/work" {
		return nil, false, nil // decline: ListRoots serves the calendars level
	}
	nodes := projNodes(node.Host)
	nodes[0].Fields["summary"] = strings.Repeat("x", listingByteCap+1000)
	return nodes, true, nil
}

func (b bigLister) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node.Path == "/work" {
		nodes, _, err := b.ListEnriched(ctx, node, nil)
		return nodes, err
	}
	return b.projLister.ListRoots(ctx, node)
}

func TestListNodesFiltered_CapsAndSaysSo(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(bigLister{})

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"priority=3_unspecified"}`))
	if err != nil || res.IsError {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	var w struct {
		Nodes     []map[string]any `json:"nodes"`
		Truncated bool             `json:"truncated"`
		Shown     int              `json:"shown"`
		Total     int              `json:"total"`
		Hint      string           `json:"hint"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &w); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(w.Nodes) != 1 || !w.Truncated || w.Shown != 1 || w.Total != 2 ||
		!strings.Contains(w.Hint, "limit") || !strings.Contains(w.Hint, "fields") {
		t.Errorf("nodes=%d wrapper=%+v", len(w.Nodes), w)
	}

	// An explicit limit takes the caller's word for the size.
	res, _ = tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"priority=3_unspecified","limit":2}`))
	if !strings.Contains(res.Content[0].Text, `"b.ics"`) || strings.Contains(res.Content[0].Text, `"truncated"`) {
		t.Errorf("explicit limit was capped or lacks the second node")
	}
}

// The query path caps the same way.
func TestListNodesQuery_CapsAndSaysSo(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(bigLister{})
	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/","query":"!test-calendar-v1 -> !test-object-v1"}`))
	if err != nil || res.IsError {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content[0].Text, `"truncated":true`) {
		t.Errorf("query path not capped: %.200s", res.Content[0].Text)
	}
}
