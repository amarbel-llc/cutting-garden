package trellis_eval

import (
	"context"
	"net/url"
	"sort"
	"strings"
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

func evalURIs(t *testing.T, lister cgp.RootLister, anchor, query string) []string {
	t.Helper()
	q, err := trellis.Parse(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	nodes, err := Evaluate(context.Background(), q, mustURL(t, anchor), lister)
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
		{
			name:   "OR-alternatives over type",
			anchor: "fake://cal/personal/",
			query:  "[!event-v1, !todo-v1]",
			want:   []string{"fake://cal/personal/e1", "fake://cal/personal/t1"},
		},
		{
			name:   "OR-alternatives over facet",
			anchor: "fake://cal/personal/",
			query:  "[component=VEVENT, component=VTODO]",
			want:   []string{"fake://cal/personal/e1", "fake://cal/personal/t1"},
		},
		{
			name:   "regex on a leaf field (anchored)",
			anchor: "fake://cal/personal/",
			query:  `summary~="^stand"`,
			want:   []string{"fake://cal/personal/e1"},
		},
		{
			name:   "regex on a leaf field (substring)",
			anchor: "fake://cal/personal/",
			query:  "summary~=milk",
			want:   []string{"fake://cal/personal/t1"},
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

// graphTree builds a depth-3 tree with a shared (two-parent) node, exercising
// the reverse and closure combinators and their URI-dedup:
//
//	fake://g/                     (anchor)
//	  ├─ fake://g/a       group-v1
//	  │    └─ fake://g/a/x    mid-v1
//	  │         ├─ fake://g/a/x/leaf1   leaf-v1
//	  │         └─ fake://g/shared      shared-v1
//	  └─ fake://g/b       group-v1
//	       └─ fake://g/b/y    mid-v1
//	            ├─ fake://g/b/y/leaf2   leaf-v1
//	            └─ fake://g/shared      shared-v1   (same node, two parents)
func graphTree(t *testing.T) *fakeTree {
	shared := node(t, "fake://g/shared", "shared-v1", nil)
	return &fakeTree{
		children: map[string][]cgp.Node{
			"fake://g/": {
				node(t, "fake://g/a", "group-v1", nil),
				node(t, "fake://g/b", "group-v1", nil),
			},
			"fake://g/a":   {node(t, "fake://g/a/x", "mid-v1", nil)},
			"fake://g/b":   {node(t, "fake://g/b/y", "mid-v1", nil)},
			"fake://g/a/x": {node(t, "fake://g/a/x/leaf1", "leaf-v1", nil), shared},
			"fake://g/b/y": {node(t, "fake://g/b/y/leaf2", "leaf-v1", nil), shared},
		},
	}
}

// TestEvaluate_GraphTraversal exercises slice-2a's untyped graph combinators —
// reverse `<-`, forward closure `->>`, backward closure `<<-` — including the
// two-parent (DAG) inversion and the anchor-boundary limitation (a query cannot
// reverse above its anchor).
func TestEvaluate_GraphTraversal(t *testing.T) {
	tree := graphTree(t)

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "reverse one hop to parents",
			query: "!group-v1 -> !mid-v1 <- !group-v1",
			want:  []string{"fake://g/a", "fake://g/b"},
		},
		{
			name:  "reverse yields both parents of a shared node",
			query: "!group-v1 -> !mid-v1 -> !shared-v1 <- !mid-v1",
			want:  []string{"fake://g/a/x", "fake://g/b/y"},
		},
		{
			name:  "reverse above the anchor yields nothing",
			query: "!group-v1 <- !group-v1",
			want:  nil,
		},
		{
			name:  "forward closure descends every level",
			query: "!group-v1 ->> !leaf-v1",
			want:  []string{"fake://g/a/x/leaf1", "fake://g/b/y/leaf2"},
		},
		{
			name:  "forward closure dedups a shared descendant",
			query: "!group-v1 ->> !shared-v1",
			want:  []string{"fake://g/shared"},
		},
		{
			name:  "backward closure ascends every level",
			query: "!group-v1 -> !mid-v1 -> !leaf-v1 <<- !group-v1",
			want:  []string{"fake://g/a", "fake://g/b"},
		},
		{
			name:  "closure from a terminal frontier is empty",
			query: "!group-v1 -> !mid-v1 -> !leaf-v1 ->> !leaf-v1",
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalURIs(t, tree, "fake://g/", tc.query)
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
		"!a -[!x]-> !b",    // typed forward
		"!a <-[!x]- !b",    // typed backward
		"!a -[!x]->> !b",   // typed closure (reserved)
		`summary ~= "("`,   // invalid ~= regex (unbalanced paren)
		"!event-v1+",       // non-`:` sigil
		"!calendar-v1 [+]", // version subpath
		"@blake2b256-abc",  // object-identity term
		"-> !calendar-v1",  // leading combinator (default anchor)
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

// urlp parses a known-valid URL for a test fixture, panicking on the
// impossible error so the fixture methods stay signature-clean (no *testing.T).
func urlp(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// enrichOnlyTree models a caldav-shaped plugin whose plain ListRoots is
// metadata-only (Facets empty) while ListEnriched populates Facets — the exact
// shape that made facet predicates silently fail before cutting-garden#212. It
// declares `component` as a facet dimension and serves the enriched listing
// only at the calendar level, declining at the home (mirroring caldav's
// level-scoping, RFC 0012 §12.2).
type enrichOnlyTree struct{}

var (
	_ cgp.RootLister     = enrichOnlyTree{}
	_ cgp.EnrichedLister = enrichOnlyTree{}
	_ cgp.FacetDescriber = enrichOnlyTree{}
)

func (enrichOnlyTree) Schemes() []string { return []string{"enr"} }
func (enrichOnlyTree) TypeTag() string   { return "enr-v1" }

func (enrichOnlyTree) Types() []cgp.NodeType {
	return []cgp.NodeType{
		{Tag: "calendar-v1", Container: true},
		{Tag: "todo-v1"},
		{Tag: "event-v1"},
	}
}

func (enrichOnlyTree) DescribeFacets() []cgp.NodeTypeFacets {
	return []cgp.NodeTypeFacets{
		{Tag: "todo-v1", Dimensions: []cgp.FacetDimension{{Key: "component"}}},
		{Tag: "event-v1", Dimensions: []cgp.FacetDimension{{Key: "component"}}},
	}
}

// ListRoots is metadata-only: the objects carry no Facets, mirroring caldav's
// hrefs-only listing (the reason a facet predicate matched nothing pre-#212).
func (enrichOnlyTree) ListRoots(_ context.Context, u *url.URL) ([]cgp.Node, error) {
	switch u.String() {
	case "enr://cal/":
		return []cgp.Node{{URI: urlp("enr://cal/work/"), Type: "calendar-v1"}}, nil
	case "enr://cal/work/":
		return []cgp.Node{
			{URI: urlp("enr://cal/work/t1"), Type: "todo-v1"},
			{URI: urlp("enr://cal/work/e1"), Type: "event-v1"},
		}, nil
	default:
		return nil, nil
	}
}

// ListEnriched populates Facets, but only at the calendar level — it declines
// (ok==false) at the home, where the children are calendars rather than the
// enrichable object unit, exactly as caldav does.
func (enrichOnlyTree) ListEnriched(
	_ context.Context, u *url.URL, _ cgp.FacetFilter,
) ([]cgp.Node, bool, error) {
	if u.String() != "enr://cal/work/" {
		return nil, false, nil
	}
	comp := func(v string) map[string][]cgp.FacetValue {
		return map[string][]cgp.FacetValue{"component": {{Key: v}}}
	}
	return []cgp.Node{
		{URI: urlp("enr://cal/work/t1"), Type: "todo-v1", Facets: comp("VTODO")},
		{URI: urlp("enr://cal/work/e1"), Type: "event-v1", Facets: comp("VEVENT")},
	}, true, nil
}

// TestEvaluate_FacetPredicateUsesEnrichedListing pins cutting-garden#212: a
// facet predicate matches against a plugin whose Facets are populated only by
// ListEnriched (not plain ListRoots), because the evaluator now prefers the
// enriched listing. Before the fix this returned empty — the silent,
// confidently-wrong result an agent could not distinguish from "no matches".
func TestEvaluate_FacetPredicateUsesEnrichedListing(t *testing.T) {
	tree := enrichOnlyTree{}

	// Facet predicate over the calendar's objects: only the VTODO matches,
	// which is only possible if the walk consulted the enriched listing.
	got := evalURIs(t, tree, "enr://cal/work/", "component=VTODO")
	if want := []string{"enr://cal/work/t1"}; !equalStrings(got, want) {
		t.Errorf("facet predicate: got %v, want %v", got, want)
	}

	// The enriched-listing decline at the home level still falls back to
	// ListRoots, so a type predicate over the calendars matches.
	got = evalURIs(t, tree, "enr://cal/", "!calendar-v1")
	if want := []string{"enr://cal/work/"}; !equalStrings(got, want) {
		t.Errorf("fallback at declined level: got %v, want %v", got, want)
	}
}

// TestEvaluate_LeafFieldPrefersInlineFields pins the Node.Fields cheap-path
// (cutting-garden#211): a leaf-field predicate matches off the inline Fields an
// enriched listing populated, without a per-node ReadLeaf. The fixture node
// carries Fields but has NO leaf entry, so a match proves the inline path was
// taken — a ReadLeaf would find nothing.
func TestEvaluate_LeafFieldPrefersInlineFields(t *testing.T) {
	tree := &fakeTree{
		children: map[string][]cgp.Node{
			"fake://c/": {{
				URI:    mustURL(t, "fake://c/x"),
				Type:   "obj-v1",
				Fields: map[string]any{"summary": "standup", "due": "20260101"},
			}},
		},
	}

	if got := evalURIs(t, tree, "fake://c/", "summary=standup"); !equalStrings(
		got, []string{"fake://c/x"},
	) {
		t.Errorf("inline Fields match: got %v, want [fake://c/x]", got)
	}
	// An ordering operator against an inline field works off Fields alone.
	if got := evalURIs(t, tree, "fake://c/", "due<=20260601"); !equalStrings(
		got, []string{"fake://c/x"},
	) {
		t.Errorf("inline Fields ordering: got %v, want [fake://c/x]", got)
	}
	// A present-but-unmatched inline field returns false without a leaf fetch.
	if got := evalURIs(t, tree, "fake://c/", "summary=nope"); len(got) != 0 {
		t.Errorf("inline Fields non-match: got %v, want empty", got)
	}
}

// TestEvaluate_DateFacetEqPrefixMatches pins the date-granularity uniformity
// decision (cutting-garden#230): a `=` facet predicate on a FacetDate-kind
// dimension hierarchy-prefix-matches by validated shape, exactly as
// FacetFilter does for `list --filter` and the mcp read_facets filter — a
// shape-valid YYYY / YYYY-MM / YYYY-MM-DD value matches a bucket key that
// equals it or extends it at a `-` boundary. Everything else keeps exact
// trellis semantics: non-shape values, non-date dimensions (even with
// date-shaped keys), and every other operator (`^=` stays raw prefix).
func TestEvaluate_DateFacetEqPrefixMatches(t *testing.T) {
	tree := &fakeTree{
		children: map[string][]cgp.Node{
			"fake://d/": {
				node(t, "fake://d/t1", "todo-v1", map[string][]cgp.FacetValue{
					"date_due": {{Key: "2026-09-10"}},
					// A categorical dimension whose key is coincidentally
					// date-shaped: shape alone must not trigger prefixing.
					"status": {{Key: "2026-01-15"}},
				}),
			},
		},
		facets: []cgp.NodeTypeFacets{
			{Tag: "todo-v1", Dimensions: []cgp.FacetDimension{
				{Key: "date_due", Kind: cgp.FacetDate},
				{Key: "status", Kind: cgp.FacetCategorical},
			}},
		},
	}

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "month prefix matches the day-precise key",
			query: "date_due=2026-09",
			want:  []string{"fake://d/t1"},
		},
		{
			name:  "year prefix matches the day-precise key",
			query: "date_due=2026",
			want:  []string{"fake://d/t1"},
		},
		{
			name:  "exact day still matches",
			query: "date_due=2026-09-10",
			want:  []string{"fake://d/t1"},
		},
		{
			name:  "sibling month does not match",
			query: "date_due=2026-08",
			want:  nil,
		},
		{
			name:  "non-shape value degrades to exact matching",
			query: "date_due=2026-0",
			want:  nil,
		},
		{
			name:  "categorical dimension never prefix-matches",
			query: "status=2026",
			want:  nil,
		},
		{
			name:  "categorical exact equality still matches",
			query: "status=2026-01-15",
			want:  []string{"fake://d/t1"},
		},
		{
			name:  "raw `^=` prefix operator is untouched",
			query: "date_due^=2026-0",
			want:  []string{"fake://d/t1"},
		},
		{
			name:  "negation composes with the prefix semantics",
			query: "^date_due=2026-09",
			want:  nil,
		},
		{
			// `!=` is `=`'s symmetric negation on a date dimension: the
			// day-precise key falls inside the containing month, so no
			// bucket key escapes the bucket and the node does not match.
			name:  "!= against the containing month does not match",
			query: "date_due!=2026-09",
			want:  nil,
		},
		{
			name:  "!= against a sibling month matches",
			query: "date_due!=2026-08",
			want:  []string{"fake://d/t1"},
		},
		{
			// A categorical dimension keeps raw `!=` semantics even with a
			// date-shaped key: exact inequality, no containment.
			name:  "categorical != exact key does not match",
			query: "status!=2026-01-15",
			want:  nil,
		},
		{
			name:  "categorical != coarser prefix stays raw inequality",
			query: "status!=2026-01",
			want:  []string{"fake://d/t1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalURIs(t, tree, "fake://d/", tc.query)
			if !equalStrings(got, tc.want) {
				t.Errorf("query %q: got %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// tagTree is a RootLister + UnifiedDescriber test double whose obj-v1 nodes
// carry a single FieldTag dimension, exercising the bare-identifier (tag) term.
// The dimension key and its declared interpreter are fixture parameters so one
// shape pins both the naive (exact) and dodder-hyphen (transitive) match lanes,
// and — with WithTagsInterpreter — the config-override precedence.
type tagTree struct {
	children    map[string][]cgp.Node
	dim         string // the FieldTag dimension key (also the Node.Facets key)
	interpreter string // the field's declared interpreter ("" -> naive default)
}

var (
	_ cgp.RootLister       = (*tagTree)(nil)
	_ cgp.UnifiedDescriber = (*tagTree)(nil)
)

func (*tagTree) Schemes() []string     { return []string{"tag"} }
func (*tagTree) TypeTag() string       { return "tag-v1" }
func (*tagTree) Types() []cgp.NodeType { return []cgp.NodeType{{Tag: "obj-v1"}} }

func (f *tagTree) ListRoots(_ context.Context, u *url.URL) ([]cgp.Node, error) {
	return f.children[u.String()], nil
}

func (f *tagTree) DescribeUnified() []cgp.NodeTypeUnifiedFields {
	return []cgp.NodeTypeUnifiedFields{{
		Tag: "obj-v1",
		Codecs: []cgp.Codec{cgp.IdentityCodec{Field: cgp.UnifiedField{
			Key:         f.dim,
			Kind:        cgp.FieldTag,
			MultiValued: true,
			Interpreter: f.interpreter,
		}}},
	}}
}

// tagFacets builds a node's membership in one tag dimension — each tag becomes a
// FacetValue.Key under the dimension, the shape matchTag reads.
func tagFacets(dim string, tags ...string) map[string][]cgp.FacetValue {
	vals := make([]cgp.FacetValue, 0, len(tags))
	for _, tag := range tags {
		vals = append(vals, cgp.FacetValue{Key: tag})
	}
	return map[string][]cgp.FacetValue{dim: vals}
}

// TestEvaluate_BareTagMatchesThroughInterpreter pins #231 slice 3: a
// bare-identifier term matches through the node's tag-dimension interpreter —
// exact under naive, transitive under dodder-hyphen, with the [tags] config
// override (WithTagsInterpreter) winning over a field's declared default.
func TestEvaluate_BareTagMatchesThroughInterpreter(t *testing.T) {
	// naive (the field default): exact set membership only. `work` matches the
	// exactly-tagged node, never the segment-extended `work-urgent`.
	naive := &tagTree{
		dim: "categories",
		children: map[string][]cgp.Node{
			"tag://r/": {
				node(t, "tag://r/a", "obj-v1", tagFacets("categories", "work")),
				node(t, "tag://r/b", "obj-v1", tagFacets("categories", "work-urgent")),
				node(t, "tag://r/c", "obj-v1", nil),
			},
		},
	}
	if got := evalURIs(t, naive, "tag://r/", "work"); !equalStrings(
		got, []string{"tag://r/a"},
	) {
		t.Errorf("naive bare tag: got %v, want [tag://r/a] (exact, not work-urgent)", got)
	}
	if got := evalURIs(t, naive, "tag://r/", "nope"); len(got) != 0 {
		t.Errorf("naive bare tag no-match: got %v, want empty", got)
	}

	// dodder-hyphen (declared via the field's Interpreter): transitive along the
	// segment path. `project` matches both the segment-extended and the exact
	// node; `pro` matches neither (not a segment boundary).
	hyphen := &tagTree{
		dim:         "labels",
		interpreter: "dodder-hyphen",
		children: map[string][]cgp.Node{
			"tag://h/": {
				node(t, "tag://h/a", "obj-v1", tagFacets("labels", "project-client-acme")),
				node(t, "tag://h/b", "obj-v1", tagFacets("labels", "project")),
				node(t, "tag://h/c", "obj-v1", tagFacets("labels", "other")),
			},
		},
	}
	if got := evalURIs(t, hyphen, "tag://h/", "project"); !equalStrings(
		got, []string{"tag://h/a", "tag://h/b"},
	) {
		t.Errorf("dodder-hyphen transitive: got %v, want [a b]", got)
	}
	if got := evalURIs(t, hyphen, "tag://h/", "pro"); len(got) != 0 {
		t.Errorf("dodder-hyphen non-boundary: got %v, want empty (pro !~ project)", got)
	}

	// Config override: a naive-default dimension matches transitively when the
	// global [tags] override selects dodder-hyphen (RFC 0019 §4) — and stays
	// exact-only without it.
	naiveDefault := &tagTree{
		dim: "categories", // Interpreter "" -> naive default
		children: map[string][]cgp.Node{
			"tag://o/": {
				node(t, "tag://o/a", "obj-v1", tagFacets("categories", "project-client-acme")),
			},
		},
	}
	q, err := trellis.Parse("project")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, err := Evaluate(
		context.Background(), q, mustURL(t, "tag://o/"), naiveDefault,
		WithTagsInterpreter("dodder-hyphen"),
	)
	if err != nil {
		t.Fatalf("evaluate with override: %v", err)
	}
	if len(nodes) != 1 || nodes[0].URIString() != "tag://o/a" {
		t.Errorf("override transitive: got %v, want [tag://o/a]", nodes)
	}
	if got := evalURIs(t, naiveDefault, "tag://o/", "project"); len(got) != 0 {
		t.Errorf("naive default without override: got %v, want empty (exact only)", got)
	}
}

// TestEvaluate_BareTagUnknownOverrideErrors pins that an unknown [tags]
// interpreter override surfaces as a loud bad request through
// matchBasic -> matchTag -> resolveTagInterpreter -> Evaluate, naming the bad
// value, rather than being swallowed or silently defaulted (RFC 0019 §3).
func TestEvaluate_BareTagUnknownOverrideErrors(t *testing.T) {
	tree := &tagTree{
		dim: "categories",
		children: map[string][]cgp.Node{
			"tag://e/": {node(t, "tag://e/a", "obj-v1", tagFacets("categories", "work"))},
		},
	}
	q, err := trellis.Parse("work")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Evaluate(
		context.Background(), q, mustURL(t, "tag://e/"), tree,
		WithTagsInterpreter("bogus"),
	)
	if err == nil {
		t.Fatal("unknown override interpreter: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error %q does not name the bad interpreter %q", err.Error(), "bogus")
	}
}

// TestEvaluate_BareTagWithoutUnifiedFieldsMatchesNothing pins that a plugin
// declaring no unified fields (no UnifiedDescriber) has no tag dimension, so a
// bare tag term is valid but matches nothing rather than erroring — fakeTree
// implements FacetDescriber but not UnifiedDescriber.
func TestEvaluate_BareTagWithoutUnifiedFieldsMatchesNothing(t *testing.T) {
	tree := sampleTree(t)
	if got := evalURIs(t, tree, "fake://cal/personal/", "anytag"); len(got) != 0 {
		t.Errorf("bare tag without unified fields: got %v, want empty", got)
	}
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
