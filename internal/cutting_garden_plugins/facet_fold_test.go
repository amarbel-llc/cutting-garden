package cutting_garden_plugins

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

// foldLister is a tree-backed RootLister with a declared schema and no
// FacetCounter: the shape RFC 0012 §4.2 hands to the framework fold.
type foldLister struct {
	tree      map[string][]Node
	dims      []NodeTypeFacets
	listCalls int
	listErr   error
}

func (*foldLister) Schemes() []string { return []string{"fold"} }
func (*foldLister) TypeTag() string   { return "cutting_garden-test-v1" }

func (*foldLister) Types() []NodeType {
	return []NodeType{{Tag: "dir", Container: true}, {Tag: "item"}}
}

func (l *foldLister) DescribeFacets() []NodeTypeFacets { return l.dims }

func (l *foldLister) ListRoots(_ context.Context, node *url.URL) ([]Node, error) {
	l.listCalls++
	if l.listErr != nil {
		return nil, l.listErr
	}
	return l.tree[node.String()], nil
}

func foldNode(uri, typ string, facets map[string][]FacetValue) Node {
	u, err := url.Parse(uri)
	if err != nil {
		panic(err)
	}
	return Node{URI: u, Name: uri, Type: typ, Facets: facets}
}

func kind(keys ...string) map[string][]FacetValue {
	values := make([]FacetValue, len(keys))
	for i, key := range keys {
		values[i] = FacetValue{Key: key}
	}
	return map[string][]FacetValue{"kind": values}
}

var foldDims = []NodeTypeFacets{{
	Tag:        "item",
	Dimensions: []FacetDimension{{Key: "kind", Kind: FacetCategorical, Multi: true}},
}}

func newFoldTree() *foldLister {
	return &foldLister{
		dims: foldDims,
		tree: map[string][]Node{
			"fold://t/": {
				foldNode("fold://t/a", "item", kind("x")),
				foldNode("fold://t/d/", "dir", nil),
			},
			"fold://t/d/": {
				foldNode("fold://t/d/b", "item", kind("x", "y")),
				foldNode("fold://t/d/c", "item", kind("y")),
			},
		},
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestFoldFacets_HoistsDescendantsAcrossContainers(t *testing.T) {
	l := newFoldTree()
	result, ok, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), nil)
	if err != nil || !ok {
		t.Fatalf("FoldFacets = ok %v, err %v", ok, err)
	}
	want := FacetSummary{"kind": {"x": 2, "y": 2}}
	if !reflect.DeepEqual(result.Summary, want) {
		t.Errorf("Summary = %v, want %v", result.Summary, want)
	}
	if !result.IsComplete() {
		t.Error("a fully-walked fold must be complete")
	}
}

func TestFoldFacets_FilterNarrowsCountsButStillDescends(t *testing.T) {
	l := newFoldTree()
	filter := FacetFilter{{Dimension: "kind", Value: "y"}}
	result, _, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), filter)
	if err != nil {
		t.Fatal(err)
	}
	// Only d/b and d/c match; the unmatched container d/ was still descended.
	want := FacetSummary{"kind": {"x": 1, "y": 2}}
	if !reflect.DeepEqual(result.Summary, want) {
		t.Errorf("Summary = %v, want %v", result.Summary, want)
	}
}

func TestFoldFacets_IgnoresUndeclaredDimensions(t *testing.T) {
	l := newFoldTree()
	l.tree["fold://t/"][0].Facets["secret"] = []FacetValue{{Key: "z"}}
	result, _, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := result.Summary["secret"]; present {
		t.Error("an undeclared dimension must be ignored (RFC 0012 §2)")
	}
}

func TestFoldFacets_NoSchemaDeclinesWithoutWalking(t *testing.T) {
	l := newFoldTree()
	l.dims = nil
	_, ok, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), nil)
	if err != nil || ok {
		t.Fatalf("FoldFacets = ok %v, err %v; want a decline", ok, err)
	}
	if l.listCalls != 0 {
		t.Errorf("a schema-less fold made %d ListRoots calls, want 0", l.listCalls)
	}
}

func TestFoldFacets_ListErrorAborts(t *testing.T) {
	l := newFoldTree()
	boom := errors.New("backend down")
	l.listErr = boom
	_, _, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v (RFC 0012 §9: no silently partial summary)", err, boom)
	}
}

func TestFoldFacets_CancelledContextAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := FoldFacets(ctx, newFoldTree(), mustParse(t, "fold://t/"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFoldFacets_ListBoundMarksResultPartial(t *testing.T) {
	// A chain of FacetFoldListLimit+5 nested containers: the fold must stop at
	// its ListRoots bound and say so rather than present a partial walk as
	// exhaustive (RFC 0012 §8).
	l := &foldLister{dims: foldDims, tree: map[string][]Node{}}
	depth := FacetFoldListLimit + 5
	for i := 0; i < depth; i++ {
		here := fmt.Sprintf("fold://t/%d/", i)
		next := fmt.Sprintf("fold://t/%d/", i+1)
		l.tree[here] = []Node{
			foldNode(here+"leaf", "item", kind("x")),
			foldNode(next, "dir", nil),
		}
	}
	result, ok, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/0/"), nil)
	if err != nil || !ok {
		t.Fatalf("FoldFacets = ok %v, err %v", ok, err)
	}
	if result.IsComplete() {
		t.Error("a fold that hit its bound must not report Complete")
	}
	if l.listCalls != FacetFoldListLimit {
		t.Errorf("ListRoots calls = %d, want exactly the bound %d", l.listCalls, FacetFoldListLimit)
	}
	if got := result.Summary["kind"]["x"]; got != int64(FacetFoldListLimit) {
		t.Errorf("counted %d leaves, want %d (one per listed container)", got, FacetFoldListLimit)
	}
}

func TestFoldFacets_CycleIsWalkedOnce(t *testing.T) {
	l := &foldLister{dims: foldDims, tree: map[string][]Node{
		"fold://t/": {
			foldNode("fold://t/a", "item", kind("x")),
			foldNode("fold://t/loop/", "dir", nil),
		},
		"fold://t/loop/": {
			foldNode("fold://t/loop/b", "item", kind("x")),
			foldNode("fold://t/", "dir", nil), // back to the start
			foldNode("fold://t/loop/", "dir", nil),
		},
	}}
	result, _, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Summary["kind"]["x"]; got != 2 {
		t.Errorf("counted %d, want 2 (a, loop/b once each)", got)
	}
	if l.listCalls != 2 {
		t.Errorf("ListRoots calls = %d, want 2 (each container once)", l.listCalls)
	}
	if !result.IsComplete() {
		t.Error("a cycle that was cut short deliberately is still a complete walk")
	}
}

func TestFoldFacets_VisitBoundMarksResultPartial(t *testing.T) {
	children := make([]Node, FacetFoldVisitLimit+3)
	for i := range children {
		children[i] = foldNode(fmt.Sprintf("fold://t/n%d", i), "item", kind("x"))
	}
	l := &foldLister{dims: foldDims, tree: map[string][]Node{"fold://t/": children}}
	result, _, err := FoldFacets(context.Background(), l, mustParse(t, "fold://t/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsComplete() {
		t.Error("a fold that hit its visit bound must not report Complete")
	}
	if got := result.Summary["kind"]["x"]; got != int64(FacetFoldVisitLimit) {
		t.Errorf("counted %d, want the visit bound %d", got, FacetFoldVisitLimit)
	}
}

// countingCounterLister adds a one-shot FacetCounter to foldLister.
type countingCounterLister struct {
	*foldLister
	result FacetResult
	ok     bool
	calls  int
}

func (c *countingCounterLister) FacetCounts(
	context.Context, *url.URL, FacetFilter,
) (FacetResult, bool, error) {
	c.calls++
	return c.result, c.ok, nil
}

func TestResolveFacetCounts_OneShotAnswerWinsAndSkipsTheWalk(t *testing.T) {
	c := &countingCounterLister{
		foldLister: newFoldTree(),
		result:     FacetResult{Summary: FacetSummary{"kind": {"only": 7}}, Complete: true},
		ok:         true,
	}
	result, ok, err := ResolveFacetCounts(context.Background(), c, mustParse(t, "fold://t/"), nil)
	if err != nil || !ok {
		t.Fatalf("ResolveFacetCounts = ok %v, err %v", ok, err)
	}
	if result.Summary["kind"]["only"] != 7 {
		t.Errorf("Summary = %v, want the plugin's one-shot answer", result.Summary)
	}
	if c.listCalls != 0 {
		t.Errorf("the fold ran (%d ListRoots calls) although the plugin answered", c.listCalls)
	}
}

func TestResolveFacetCounts_DeclineFallsBackToFold(t *testing.T) {
	c := &countingCounterLister{foldLister: newFoldTree(), ok: false}
	result, ok, err := ResolveFacetCounts(context.Background(), c, mustParse(t, "fold://t/"), nil)
	if err != nil || !ok {
		t.Fatalf("ResolveFacetCounts = ok %v, err %v", ok, err)
	}
	if c.calls != 1 {
		t.Errorf("FacetCounts calls = %d, want 1", c.calls)
	}
	want := FacetSummary{"kind": {"x": 2, "y": 2}}
	if !reflect.DeepEqual(result.Summary, want) {
		t.Errorf("Summary = %v, want the fold's %v", result.Summary, want)
	}
}

// refusingCounterLister is a decliner that forbids the fold for some nodes.
type refusingCounterLister struct {
	*countingCounterLister
	refuse map[string]bool
}

func (r *refusingCounterLister) RefusesFacetFold(node *url.URL) bool {
	return r.refuse[node.String()]
}

func TestResolveFacetCounts_ARefusedFoldDeclinesWithoutWalking(t *testing.T) {
	c := &refusingCounterLister{
		countingCounterLister: &countingCounterLister{foldLister: newFoldTree(), ok: false},
		refuse:                map[string]bool{"fold://t/": true},
	}
	_, ok, err := ResolveFacetCounts(context.Background(), c, mustParse(t, "fold://t/"), nil)
	if err != nil || ok {
		t.Fatalf("ResolveFacetCounts = ok %v, err %v; want a clean decline", ok, err)
	}
	if c.listCalls != 0 {
		t.Errorf("a refused fold made %d ListRoots calls, want 0", c.listCalls)
	}

	// A node the refuser does not name still folds.
	_, ok, err = ResolveFacetCounts(context.Background(), c, mustParse(t, "fold://t/d/"), nil)
	if err != nil || !ok {
		t.Fatalf("an unrefused node: ok %v, err %v; want the fold's summary", ok, err)
	}
}

func TestCanSummarizeFacets(t *testing.T) {
	if !CanSummarizeFacets(newFoldTree()) {
		t.Error("a FacetDescriber can be folded")
	}
	if CanSummarizeFacets(typesOnlyLister{}) {
		t.Error("a lister with neither FacetCounter nor FacetDescriber has nothing to summarize")
	}
}
