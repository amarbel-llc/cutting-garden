package trellis_eval

import (
	"context"
	"net/url"
	"sort"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// fakeTree is an in-memory RootLister + LeafReader + FacetDescriber test
// double: a fixed child map, a leaf-content map, and a declared facet schema.
type fakeTree struct {
	children map[string][]cgp.Node
	leaves   map[string]cgp.LeafContent
	facets   []cgp.NodeTypeFacets
}

var (
	_ cgp.RootLister     = (*fakeTree)(nil)
	_ cgp.LeafReader     = (*fakeTree)(nil)
	_ cgp.FacetDescriber = (*fakeTree)(nil)
)

func (*fakeTree) Schemes() []string { return []string{"fake"} }
func (*fakeTree) TypeTag() string   { return "fake-v1" }

func (*fakeTree) Types() []cgp.NodeType {
	return []cgp.NodeType{{Tag: "calendar-v1"}, {Tag: "event-v1"}, {Tag: "todo-v1"}}
}

func (f *fakeTree) ListRoots(_ context.Context, u *url.URL) ([]cgp.Node, error) {
	return f.children[u.String()], nil
}

func (f *fakeTree) ReadLeaf(
	_ context.Context, u *url.URL,
) (cgp.LeafContent, bool, error) {
	c, ok := f.leaves[u.String()]
	return c, ok, nil
}

func (f *fakeTree) DescribeFacets() []cgp.NodeTypeFacets { return f.facets }

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

func node(t *testing.T, uri, typ string, facets map[string][]cgp.FacetValue) cgp.Node {
	return cgp.Node{URI: mustURL(t, uri), Type: typ, Facets: facets}
}

// sampleTree builds a small caldav-shaped tree:
//
//	fake://cal/                    (anchor: calendar-home)
//	  ├─ fake://cal/personal/      calendar-v1
//	  │    ├─ fake://cal/personal/e1   event-v1  {component:VEVENT, year:2026}
//	  │    └─ fake://cal/personal/t1   todo-v1   {component:VTODO}
//	  └─ fake://cal/work/          calendar-v1  (no children)
func sampleTree(t *testing.T) *fakeTree {
	comp := func(v string) map[string][]cgp.FacetValue {
		return map[string][]cgp.FacetValue{"component": {{Key: v}}}
	}
	e1Facets := map[string][]cgp.FacetValue{
		"component": {{Key: "VEVENT"}},
		"year":      {{Key: "2026"}},
	}
	return &fakeTree{
		children: map[string][]cgp.Node{
			"fake://cal/": {
				node(t, "fake://cal/personal/", "calendar-v1", nil),
				node(t, "fake://cal/work/", "calendar-v1", nil),
			},
			"fake://cal/personal/": {
				node(t, "fake://cal/personal/e1", "event-v1", e1Facets),
				node(t, "fake://cal/personal/t1", "todo-v1", comp("VTODO")),
			},
			// fake://cal/work/ has no entry → no children.
		},
		leaves: map[string]cgp.LeafContent{
			"fake://cal/personal/e1": {
				Structured:  map[string]any{"summary": "standup", "dtstart": "20260720"},
				Raw:         []byte("BEGIN:VEVENT\nSUMMARY:standup\nEND:VEVENT"),
				RawMimeType: "text/calendar",
			},
			"fake://cal/personal/t1": {
				Structured:  map[string]any{"summary": "buy milk", "due": "20260101"},
				Raw:         []byte("BEGIN:VTODO\nSUMMARY:buy milk\nEND:VTODO"),
				RawMimeType: "text/calendar",
			},
		},
		facets: []cgp.NodeTypeFacets{
			{Tag: "event-v1", Dimensions: []cgp.FacetDimension{{Key: "component"}, {Key: "year"}}},
			{Tag: "todo-v1", Dimensions: []cgp.FacetDimension{{Key: "component"}}},
		},
	}
}

func evalURIs(t *testing.T, tree *fakeTree, anchor, query string) []string {
	t.Helper()
	q, err := trellis.Parse(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	nodes, err := Evaluate(context.Background(), q, mustURL(t, anchor), tree)
	if err != nil {
		t.Fatalf("evaluate %q: %v", query, err)
	}
	got := make([]string, 0, len(nodes))
	for _, n := range nodes {
		got = append(got, n.URIString())
	}
	sort.Strings(got)
	return got
}

func TestEvaluate_Matches(t *testing.T) {
	tree := sampleTree(t)

	cases := []struct {
		name   string
		anchor string
		query  string
		want   []string
	}{
		{
			name:   "type predicate over anchor children",
			anchor: "fake://cal/",
			query:  "!calendar-v1",
			want:   []string{"fake://cal/personal/", "fake://cal/work/"},
		},
		{
			name:   "forward walk then type predicate",
			anchor: "fake://cal/",
			query:  "!calendar-v1 -> !event-v1",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "facet field predicate after walk",
			anchor: "fake://cal/",
			query:  "!calendar-v1 -> component=VEVENT",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "numeric facet field",
			anchor: "fake://cal/",
			query:  "!calendar-v1 -> year=2026",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "leaf structured field via fetch",
			anchor: "fake://cal/personal/",
			query:  "summary=standup",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "leaf ordering operator",
			anchor: "fake://cal/personal/",
			query:  "due<=20260601",
			want:   []string{"fake://cal/personal/t1"},
		},
		{
			name:   "body substring match",
			anchor: "fake://cal/personal/",
			query:  "_body*=standup",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "negated facet predicate",
			anchor: "fake://cal/personal/",
			query:  "^component=VEVENT",
			want:   []string{"fake://cal/personal/t1"},
		},
		{
			name:   "ANDed terms in one step",
			anchor: "fake://cal/personal/",
			query:  "!event-v1 year=2026",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "existential subpath keeps calendars with an event child",
			anchor: "fake://cal/",
			query:  "!calendar-v1 [-> !event-v1]",
			want:   []string{"fake://cal/personal/"},
		},
		{
			name:   "empty subpath keeps calendars with any child",
			anchor: "fake://cal/",
			query:  "!calendar-v1 [->]",
			want:   []string{"fake://cal/personal/"},
		},
		{
			name:   "latest sigil is a no-op",
			anchor: "fake://cal/personal/",
			query:  "!event-v1:",
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "no match yields empty",
			anchor: "fake://cal/personal/",
			query:  "component=NOPE",
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalURIs(t, tree, tc.anchor, tc.query)
			if !equalStrings(got, tc.want) {
				t.Errorf("query %q: got %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestEvaluate_Rejects pins that every deferred grammar form fails fast with
// an error rather than silently mismatching.
func TestEvaluate_Rejects(t *testing.T) {
	tree := sampleTree(t)

	queries := []string{
		"!a <- !b",            // reverse combinator
		"!a ->> !b",           // forward closure
		"!a <<- !b",           // backward closure
		"!a -[!x]-> !b",       // typed forward
		"component ~= x",      // regex operator
		"!event-v1+",          // non-`:` sigil
		"!calendar-v1 [+]",    // version subpath
		"!calendar-v1 [a, b]", // OR-alternatives
		"sometag",             // bare identifier (tag) predicate
		"@blake2b256-abc",     // object-identity term
		"-> !calendar-v1",     // leading combinator (default anchor)
	}

	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			q, err := trellis.Parse(query)
			if err != nil {
				// A form the grammar itself rejects is already fine; this
				// test targets grammar-valid-but-deferred forms.
				t.Skipf("parse rejected %q before evaluation: %v", query, err)
			}
			if _, err := Evaluate(context.Background(), q, mustURL(t, "fake://cal/"), tree); err == nil {
				t.Errorf("query %q: expected a rejection, got nil error", query)
			}
		})
	}
}

// TestEvaluate_DegradesWithoutCapabilities pins that a plugin exposing only
// RootLister (no LeafReader, no FacetDescriber) does not error on a
// leaf/field predicate — the predicate simply does not match.
func TestEvaluate_DegradesWithoutCapabilities(t *testing.T) {
	rootOnly := &rootOnlyTree{children: map[string][]cgp.Node{
		"fake://r/": {node(t, "fake://r/a", "thing-v1", nil)},
	}}
	q, err := trellis.Parse("summary=x")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, err := Evaluate(context.Background(), q, mustURL(t, "fake://r/"), rootOnly)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("got %d nodes, want 0 (leaf field unresolvable without LeafReader)", len(nodes))
	}
}

// rootOnlyTree implements only RootLister — no LeafReader, no FacetDescriber.
type rootOnlyTree struct{ children map[string][]cgp.Node }

var _ cgp.RootLister = (*rootOnlyTree)(nil)

func (*rootOnlyTree) Schemes() []string     { return []string{"fake"} }
func (*rootOnlyTree) TypeTag() string       { return "fake-v1" }
func (*rootOnlyTree) Types() []cgp.NodeType { return []cgp.NodeType{{Tag: "thing-v1"}} }
func (f *rootOnlyTree) ListRoots(_ context.Context, u *url.URL) ([]cgp.Node, error) {
	return f.children[u.String()], nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
