package node_view

import (
	"context"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// pathLister has no NodeIDer: its ids are the host+path default.
type pathLister struct{}

func (pathLister) Schemes() []string                        { return []string{"cal"} }
func (pathLister) TypeTag() string                          { return "cal-v1" }
func (pathLister) Types() []cutting_garden_plugins.NodeType { return nil }

func (pathLister) ListRoots(
	context.Context, *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

// queryLister's nodes carry their identity in the query, like fastmail's
// threads: `T1` ↔ `q://acct/box/?n=T1`.
type queryLister struct{ pathLister }

func (queryLister) RelativeNodeID(nodeURI, anchor string) (string, bool) {
	u, err := url.Parse(nodeURI)
	if err != nil || u.Query().Get("n") == "" {
		return "", false
	}
	return u.Query().Get("n"), true
}

func (queryLister) ResolveNodeID(id, anchor string) (string, bool) {
	return anchor + "?n=" + id, true
}

func TestNodeURIFor_RoundTripsRelativeIDFor(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		lister         cutting_garden_plugins.RootLister
		anchor, nodeID string
		want           string
	}{
		{"path default", pathLister{}, "cal://host/cal/", "task1.ics", "cal://host/cal/task1.ics"},
		{"nested path", pathLister{}, "cal://host/", "cal/task1.ics", "cal://host/cal/task1.ics"},
		{"opaque anchor", pathLister{}, "cal:https://host/cal/", "task1.ics", "cal:https://host/cal/task1.ics"},
		{"full uri under anchor", pathLister{}, "cal://host/cal/", "cal://host/cal/x.ics", "cal://host/cal/x.ics"},
		{"plugin id", queryLister{}, "q://acct/box/", "T1", "q://acct/box/?n=T1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := NodeURIFor(testCase.lister, testCase.nodeID, testCase.anchor)
			if err != nil {
				t.Fatalf("NodeURIFor: %v", err)
			}
			if got.String() != testCase.want {
				t.Errorf("got %q, want %q", got, testCase.want)
			}
			// A relative id must come back exactly; a full URI comes back as
			// the id it shortens to.
			want := RelativeIDFor(testCase.lister, testCase.nodeID, testCase.anchor)
			if back := RelativeIDFor(testCase.lister, got.String(), testCase.anchor); back != want {
				t.Errorf("RelativeIDFor(%q) = %q, want %q", got, back, want)
			}
		})
	}
}

func TestNodeURIFor_Refusals(t *testing.T) {
	for name, testCase := range map[string]struct{ anchor, nodeID string }{
		"empty id":          {"cal://host/cal/", ""},
		"outside anchor":    {"cal://host/cal/", "cal://host/other/x.ics"},
		"the anchor itself": {"cal://host/cal/", "cal://host/cal/"},
		"schemeless anchor": {"host/cal/", "x.ics"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := NodeURIFor(pathLister{}, testCase.nodeID, testCase.anchor); err == nil {
				t.Errorf("resolved to %q", got)
			}
		})
	}
}
