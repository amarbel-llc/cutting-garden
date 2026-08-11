package trellis_eval

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// fakeResolver maps a bare origin token to a canonical anchor URL (modelling a
// plugin's alias/URI canonicalization, e.g. caldav:fastmail -> its home URL) and
// serves the whole resolved subtree from one lister.
type fakeResolver struct {
	lister  cgp.RootLister
	anchors map[string]string // origin token -> canonical anchor URL
}

func (r fakeResolver) Resolve(uri string) (*url.URL, cgp.RootLister, error) {
	target, ok := r.anchors[uri]
	if !ok {
		return nil, nil, fmt.Errorf("fakeResolver: unknown origin %q", uri)
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, nil, err
	}
	return u, r.lister, nil
}

func sampleResolver(tree cgp.RootLister) fakeResolver {
	return fakeResolver{
		lister: tree,
		anchors: map[string]string{
			"fake:cal":      "fake://cal/",
			"fake:personal": "fake://cal/personal/",
		},
	}
}

func evalResolvingURIs(t *testing.T, resolver OriginResolver, query string) []string {
	t.Helper()
	q, err := trellis.Parse(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	nodes, err := EvaluateResolving(context.Background(), q, resolver)
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

// TestEvaluateResolving_Origin pins the origin-in-expression mode
// (cutting-garden#37): the leading URI resolves to the anchor via the injected
// resolver and the remainder evaluates from it. A bare origin lists the anchor's
// children; `origin -> …` filters and walks exactly as an explicit-anchor query.
func TestEvaluateResolving_Origin(t *testing.T) {
	resolver := sampleResolver(sampleTree(t))

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "bare origin lists the anchor's children",
			query: "fake:cal",
			want:  []string{"fake://cal/personal/", "fake://cal/work/"},
		},
		{
			name:  "origin then type predicate over children",
			query: "fake:cal -> !calendar-v1",
			want:  []string{"fake://cal/personal/", "fake://cal/work/"},
		},
		{
			name:  "deep origin then facet predicate",
			query: "fake:personal -> component=VEVENT",
			want:  []string{"fake://cal/personal/e1"},
		},
		{
			name:  "origin then multi-step walk",
			query: "fake:cal -> !calendar-v1 -> !event-v1",
			want:  []string{"fake://cal/personal/e1"},
		},
		{
			name:  "deep origin then type predicate",
			query: "fake:personal -> !todo-v1",
			want:  []string{"fake://cal/personal/t1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalResolvingURIs(t, resolver, tc.query)
			if !equalStrings(got, tc.want) {
				t.Errorf("query %q: got %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestEvaluateResolving_MatchesExplicitAnchor pins the mode equivalence: an
// origin-in-expression query resolves to the same nodes the explicit-anchor
// Evaluate returns over the same anchor and remainder (`origin -> step` ==
// Evaluate(anchor=origin, "step")).
func TestEvaluateResolving_MatchesExplicitAnchor(t *testing.T) {
	tree := sampleTree(t)
	resolver := sampleResolver(tree)

	got := evalResolvingURIs(t, resolver, "fake:personal -> component=VEVENT")
	want := evalURIs(t, tree, "fake://cal/personal/", "component=VEVENT")
	if !equalStrings(got, want) {
		t.Errorf("origin-mode %v != explicit-anchor %v", got, want)
	}
}

// TestEvaluateResolving_Rejects pins origin-mode validation: a malformed or
// out-of-slice origin query is a loud bad request, never a silent mismatch.
func TestEvaluateResolving_Rejects(t *testing.T) {
	resolver := sampleResolver(sampleTree(t))

	cases := []struct {
		name    string
		query   string
		wantSub string
	}{
		{"non-forward combinator after origin", "fake:cal ->> !event-v1", "non-forward"},
		{"multi-term origin step", "fake:cal !calendar-v1", "single URI term"},
		{"negated origin", "^fake:cal -> !calendar-v1", "cannot be negated"},
		{"version sigil on origin", "fake:cal+ -> !calendar-v1", "version sigil"},
		{"unresolvable origin", "fake:unknown -> !calendar-v1", "unknown origin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := trellis.Parse(tc.query)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.query, err)
			}
			if _, err = EvaluateResolving(context.Background(), q, resolver); err == nil {
				t.Fatalf("query %q: expected an error", tc.query)
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("query %q: error %q does not contain %q", tc.query, err.Error(), tc.wantSub)
			}
		})
	}
}
