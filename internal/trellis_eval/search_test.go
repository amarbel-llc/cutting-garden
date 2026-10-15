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

func evalWords(t *testing.T, lister cgp.RootLister, query string) ([]string, []cgp.PartialListing, error) {
	t.Helper()
	q, err := trellis.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	ctx, partial := cgp.WithPartialListings(context.Background())
	nodes, err := Evaluate(ctx, q, mustURL(t, "fake://cal/personal/"), lister)
	var uris []string
	for _, n := range nodes {
		uris = append(uris, n.URIString())
	}
	return uris, partial.All(), err
}

// `_words` is answered only by the plugin's words search: hits match, a
// complete answer rules the rest out with no leaf read.
func TestWords_AnsweredBySearch(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), hits: []string{"fake://cal/personal/t1"}, complete: true}
	got, partial, err := evalWords(t, tree, `_words=["milk", "bread"]`)
	if err != nil || !equalStrings(got, []string{"fake://cal/personal/t1"}) || len(partial) != 0 {
		t.Errorf("got %v partial=%v err=%v", got, partial, err)
	}
	if tree.reads != 0 || tree.searches[0].Mode != cgp.SearchWords {
		t.Errorf("reads=%d searches=%+v, want 0 reads and a words search", tree.reads, tree.searches)
	}
}

// With no walk to top it up, an incomplete words answer is used as it is
// and the query is reported partial.
func TestWords_IncompleteIsReportedPartial(t *testing.T) {
	tree := &searchTree{fakeTree: sampleTree(t), hits: []string{"fake://cal/personal/t1"}, complete: false}
	got, partial, err := evalWords(t, tree, `_words=milk`)
	if err != nil || !equalStrings(got, []string{"fake://cal/personal/t1"}) {
		t.Fatalf("got %v err=%v", got, err)
	}
	if len(partial) != 1 || !strings.Contains(partial[0].Reason, `"milk"`) {
		t.Errorf("partial = %+v, want one report naming the word", partial)
	}
}

func TestWords_Refusals(t *testing.T) {
	for name, c := range map[string]struct {
		lister cgp.RootLister
		query  string
		want   string
	}{
		"no searcher":     {sampleTree(t), `_words=milk`, "does not"},
		"mode declined":   {&searchTree{fakeTree: sampleTree(t), err: cgp.ErrSearchModeUnsupported}, `_words=milk`, "does not"},
		"not an equality": {&searchTree{fakeTree: sampleTree(t), complete: true}, `_words*=mil`, "takes `=`"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := evalWords(t, c.lister, c.query)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to mention %q", err, c.want)
			}
		})
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
