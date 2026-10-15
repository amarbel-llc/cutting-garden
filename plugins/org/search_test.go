package org

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func search(t *testing.T, root, query string, mode cutting_garden_plugins.SearchMode) []string {
	t.Helper()
	res, err := Plugin{}.Search(context.Background(), cutting_garden_plugins.SearchRequest{
		Root: mustURL(t, root), Query: query, Mode: mode,
	})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	if !res.Complete {
		t.Errorf("search %q: incomplete", query)
	}
	return uriStrings(res.Nodes)
}

func uriStrings(nodes []cutting_garden_plugins.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.URIString())
	}
	slices.Sort(out)
	return out
}

// walkMatches is the evaluator's honest walk: every node under root
// (root included) whose leaf body matches.
func walkMatches(t *testing.T, root *url.URL, match func(string) bool) []string {
	t.Helper()
	out := []string{}
	var visit func(u *url.URL)
	visit = func(u *url.URL) {
		if content, ok, err := (Plugin{}).ReadLeaf(context.Background(), u); err != nil {
			t.Fatal(err)
		} else if ok && match(string(content.Raw)) {
			out = append(out, u.String())
		}
		children, err := Plugin{}.ListRoots(context.Background(), u)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range children {
			visit(c.URI)
		}
	}
	visit(root)
	slices.Sort(out)
	return out
}

// Search and the walk agree on which nodes match (RFC 0016 §2), for both
// modes and for folder, file and headline roots.
func TestSearch_AgreesWithTheWalk(t *testing.T) {
	setup(t)
	for _, c := range []struct {
		root, query string
		mode        cutting_garden_plugins.SearchMode
	}{
		{"org://test/", "valve", cutting_garden_plugins.SearchSubstring},
		{"org://test/", "Ask about", cutting_garden_plugins.SearchSubstring},
		{"org://test/", "Seeds", cutting_garden_plugins.SearchSubstring}, // case-sensitive: no match
		{"org://test/", `(?i)seeds|passport`, cutting_garden_plugins.SearchRegex},
		{"org://test/_inbox.org", "quoted", cutting_garden_plugins.SearchSubstring},
		{"org://test/_inbox.org?id=abc-123", "appends", cutting_garden_plugins.SearchSubstring},
	} {
		t.Run(c.query, func(t *testing.T) {
			match := func(s string) bool { return strings.Contains(s, c.query) }
			if c.mode == cutting_garden_plugins.SearchRegex {
				match = regexp.MustCompile(c.query).MatchString
			}
			want := walkMatches(t, mustURL(t, c.root), match)
			if diff := cmp.Diff(want, search(t, c.root, c.query, c.mode)); diff != "" {
				t.Errorf("search vs walk (-walk +search):\n%s", diff)
			}
		})
	}
}

func TestSearch_FilterLimitAndRefusals(t *testing.T) {
	setup(t)
	ctx := context.Background()
	res, err := Plugin{}.Search(ctx, cutting_garden_plugins.SearchRequest{
		Root: mustURL(t, "org://test/"), Query: "a", Mode: cutting_garden_plugins.SearchSubstring,
		Filter: cutting_garden_plugins.FacetFilter{{Dimension: fieldState, Value: "TODO"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := uriStrings(res.Nodes); !slices.Equal(got, []string{"org://test/_inbox.org?h=Call+the+plumber"}) {
		t.Errorf("filtered = %v, want only the TODO headline", got)
	}

	res, err = Plugin{}.Search(ctx, cutting_garden_plugins.SearchRequest{
		Root: mustURL(t, "org://test/"), Query: "a", Mode: cutting_garden_plugins.SearchSubstring, Limit: 2,
	})
	if err != nil || len(res.Nodes) != 2 || res.Complete {
		t.Errorf("limited: %d nodes, complete=%v, err=%v; want 2, false", len(res.Nodes), res.Complete, err)
	}

	for _, req := range []cutting_garden_plugins.SearchRequest{
		{Root: mustURL(t, "org://test/"), Query: "", Mode: cutting_garden_plugins.SearchSubstring},
		{Root: mustURL(t, "org://test/"), Query: "(", Mode: cutting_garden_plugins.SearchRegex},
		{Root: mustURL(t, "org://test/"), Query: "x", Mode: "fuzzy"},
		{Query: "x", Mode: cutting_garden_plugins.SearchSubstring},
	} {
		if _, err := (Plugin{}).Search(ctx, req); err == nil {
			t.Errorf("%+v: want a bad request", req)
		}
	}
}
