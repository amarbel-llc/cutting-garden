package trellis_eval

import (
	"context"
	stderrors "errors"
	"net/url"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// searchTree is the sample tree plus a Searcher whose answer the test
// scripts, counting searches and leaf reads.
type searchTree struct {
	*fakeTree
	hits     []string
	complete bool
	err      error

	searches []cgp.SearchRequest
	reads    int
}

var _ cgp.Searcher = (*searchTree)(nil)

func (s *searchTree) Search(_ context.Context, req cgp.SearchRequest) (cgp.SearchResult, error) {
	s.searches = append(s.searches, req)
	if s.err != nil {
		return cgp.SearchResult{}, s.err
	}
	var nodes []cgp.Node
	for _, h := range s.hits {
		u, _ := url.Parse(h)
		nodes = append(nodes, cgp.Node{URI: u})
	}
	return cgp.SearchResult{Nodes: nodes, Complete: s.complete}, nil
}

func (s *searchTree) ReadLeaf(ctx context.Context, u *url.URL) (cgp.LeafContent, bool, error) {
	s.reads++
	return s.fakeTree.ReadLeaf(ctx, u)
}

func TestSearch_CompleteResultReplacesLeafReads(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), hits: []string{"fake://cal/personal/e1"}, complete: true}
	got := evalURIs(t, tree, "fake://cal/", "!calendar-v1 -> _body*=standup")
	if !equalStrings(got, []string{"fake://cal/personal/e1"}) {
		t.Errorf("got %v", got)
	}
	if tree.reads != 0 {
		t.Errorf("leaf reads = %d, want 0 with a complete search", tree.reads)
	}
	if len(tree.searches) != 1 {
		t.Fatalf("searches = %d, want 1 (memoized across nodes)", len(tree.searches))
	}
	req := tree.searches[0]
	if req.Root.String() != "fake://cal/" || req.Query != "standup" || req.Mode != cgp.SearchSubstring {
		t.Errorf("request = %+v, want the anchor, the query and substring mode", req)
	}

	// Negation reads the same set the other way.
	if got := evalURIs(t, tree, "fake://cal/personal/", "^_body*=standup"); !equalStrings(
		got, []string{"fake://cal/personal/t1"}) {
		t.Errorf("negated: got %v", got)
	}
}

// An incomplete result confirms its hits but cannot rule a node out: the
// rest are read, so a match the index missed is still found.
func TestSearch_IncompleteResultFallsBackForMisses(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), hits: nil, complete: false}
	got := evalURIs(t, tree, "fake://cal/personal/", "_body*=standup")
	if !equalStrings(got, []string{"fake://cal/personal/e1"}) {
		t.Errorf("got %v", got)
	}
	if tree.reads != 2 {
		t.Errorf("leaf reads = %d, want 2 (both misses re-checked)", tree.reads)
	}
}

func TestSearch_UnsupportedModeFallsBackToWalk(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), err: cgp.ErrSearchModeUnsupported}
	got := evalURIs(t, tree, "fake://cal/personal/", `_body~="SUMMARY:s.*up"`)
	if !equalStrings(got, []string{"fake://cal/personal/e1"}) {
		t.Errorf("got %v", got)
	}
	if len(tree.searches) != 1 || tree.searches[0].Mode != cgp.SearchRegex {
		t.Errorf("searches = %+v, want one regex attempt", tree.searches)
	}
}

func TestSearch_ErrorFailsTheQuery(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), err: stderrors.New("index offline")}
	q, err := trellis.Parse("_body*=standup")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Evaluate(context.Background(), q, mustURL(t, "fake://cal/personal/"), tree)
	if err == nil || !strings.Contains(err.Error(), "index offline") {
		t.Errorf("err = %v, want the search failure", err)
	}
}

// Operators search does not express (equality, prefix) never call it.
func TestSearch_OnlyContainsAndRegexAreAccelerated(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), complete: true}
	evalURIs(t, tree, "fake://cal/personal/", `_body^=BEGIN`)
	if len(tree.searches) != 0 {
		t.Errorf("searches = %d, want 0 for ^=", len(tree.searches))
	}
}
