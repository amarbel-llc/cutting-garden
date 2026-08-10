package organize

import (
	"context"
	"net/url"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// fakeLister is a minimal RootLister + FacetDescriber for exercising the
// terminal machinery without a real plugin: ListRoots returns a fixed node set
// (ignoring the URI, since the tests query a single level) and DescribeFacets
// declares the given schema.
type fakeLister struct {
	dims  []cgp.NodeTypeFacets
	nodes []cgp.Node
}

func (f *fakeLister) Schemes() []string                    { return []string{"fake"} }
func (f *fakeLister) TypeTag() string                      { return "fake" }
func (f *fakeLister) Types() []cgp.NodeType                { return nil }
func (f *fakeLister) DescribeFacets() []cgp.NodeTypeFacets { return f.dims }

func (f *fakeLister) ListRoots(context.Context, *url.URL) ([]cgp.Node, error) {
	// Return copies so annotation does not mutate the fixture across calls.
	out := make([]cgp.Node, len(f.nodes))
	copy(out, f.nodes)
	return out, nil
}

func taskDims(terminal ...string) []cgp.NodeTypeFacets {
	return []cgp.NodeTypeFacets{{
		Tag: "task",
		Dimensions: []cgp.FacetDimension{
			{Key: "status", Kind: cgp.FacetCategorical, TerminalValues: terminal},
		},
	}}
}

func statusNode(t *testing.T, name, status string) cgp.Node {
	return cgp.Node{
		URI:    mustURL(t, "fake://cal/"+name),
		Type:   "task",
		Facets: map[string][]cgp.FacetValue{"status": {{Key: status}}},
	}
}

// TestEffectiveQuery pins the one composition rule (cutting-garden#214): append
// `_terminal=no` by default; skip it when the plugin has no terminal concept, the
// user includes terminal, or the user's query already references `_terminal`.
func TestEffectiveQuery(t *testing.T) {
	terminal := &fakeLister{dims: taskDims("COMPLETED", "CANCELLED")}
	plain := &fakeLister{dims: taskDims()} // no terminal values

	cases := []struct {
		name    string
		lister  cgp.RootLister
		query   string
		include bool
		want    string
	}{
		{"default no query", terminal, "", false, "_terminal=no"},
		{"default with query appends", terminal, "status=NEEDS-ACTION", false, "status=NEEDS-ACTION _terminal=no"},
		{"include drops the clause", terminal, "", true, ""},
		{"include drops it with a query", terminal, "status=NEEDS-ACTION", true, "status=NEEDS-ACTION"},
		{"explicit _terminal wins", terminal, "_terminal=yes", false, "_terminal=yes"},
		{"no terminal concept", plain, "", false, ""},
	}
	for _, c := range cases {
		got := effectiveQuery(c.lister, c.query, c.include)
		if got != c.want {
			t.Errorf("%s: effectiveQuery = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestTerminalLister_AnnotatesAndDeclares pins the decorator: it stamps each
// node's `_terminal` facet from the terminal values and declares the synthetic
// dimension so the evaluator routes `_terminal=…` to the facet path.
func TestTerminalLister_AnnotatesAndDeclares(t *testing.T) {
	f := &fakeLister{
		dims:  taskDims("COMPLETED", "CANCELLED"),
		nodes: []cgp.Node{statusNode(t, "done", "COMPLETED"), statusNode(t, "active", "NEEDS-ACTION")},
	}
	tl := withTerminal(f)

	// _terminal is declared on the task type.
	var found bool
	for _, nt := range tl.(cgp.FacetDescriber).DescribeFacets() {
		for _, d := range nt.Dimensions {
			if d.Key == terminalDim {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("_terminal dimension not declared on the task type")
	}

	nodes, err := tl.ListRoots(context.Background(), mustURL(t, "fake://cal"))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	byName := map[string]string{}
	for _, n := range nodes {
		if v := n.Facets[terminalDim]; len(v) == 1 {
			byName[n.URIString()] = v[0].Key
		}
	}
	if byName["fake://cal/done"] != terminalYes {
		t.Errorf("COMPLETED node _terminal = %q, want yes", byName["fake://cal/done"])
	}
	if byName["fake://cal/active"] != terminalNo {
		t.Errorf("NEEDS-ACTION node _terminal = %q, want no", byName["fake://cal/active"])
	}
}

// TestSelectNodes_ExcludesTerminal drives the exclusion end to end through the
// evaluator: `_terminal=no` (the default effective query) drops the COMPLETED
// task and keeps the active one, with no evaluator change — the decorator makes
// `_terminal` a matchable facet.
func TestSelectNodes_ExcludesTerminal(t *testing.T) {
	f := &fakeLister{
		dims:  taskDims("COMPLETED", "CANCELLED"),
		nodes: []cgp.Node{statusNode(t, "done", "COMPLETED"), statusNode(t, "active", "NEEDS-ACTION")},
	}

	got, err := selectNodes(context.Background(), f, mustURL(t, "fake://cal"), "_terminal=no")
	if err != nil {
		t.Fatalf("selectNodes: %v", err)
	}
	if len(got) != 1 || got[0].URIString() != "fake://cal/active" {
		t.Fatalf("selectNodes(_terminal=no) = %+v, want just the active task", got)
	}

	// _terminal=yes is the inverse — only the done one.
	only, err := selectNodes(context.Background(), f, mustURL(t, "fake://cal"), "_terminal=yes")
	if err != nil {
		t.Fatalf("selectNodes: %v", err)
	}
	if len(only) != 1 || only[0].URIString() != "fake://cal/done" {
		t.Fatalf("selectNodes(_terminal=yes) = %+v, want just the done task", only)
	}
}
