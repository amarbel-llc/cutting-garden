package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// feedFacetLister is a nebulous-like fake plugin: stories faceted by a
// FacetLabelled "feed" dimension (opaque feed ids resolved to titles) and a
// closed categorical "read" dimension — the RFC 0012 §6 fixture for the
// fleet's Q1 acceptance scenario (cutting-garden#124's last comment):
// "unread counts per feed" answered by ONE read_facets call.
type feedFacetLister struct {
	fakeLister
}

const (
	facetFeed = "feed"
	facetRead = "read"
)

func (feedFacetLister) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{
				Key:   facetFeed,
				Label: "Feed",
				Kind:  cutting_garden_plugins.FacetLabelled,
			},
			{
				Key:  facetRead,
				Kind: cutting_garden_plugins.FacetCategorical,
				Values: []cutting_garden_plugins.FacetValue{
					{Key: "read"}, {Key: "unread"},
				},
			},
		},
	}}
}

// feedFacetStories is the fixture corpus: (feed, read-state) per story.
var feedFacetStories = []struct{ feed, state string }{
	{"512", "unread"},
	{"512", "unread"},
	{"512", "read"},
	{"600", "unread"},
	{"600", "read"},
	{"600", "read"},
}

func (feedFacetLister) FacetCounts(
	_ context.Context, _ *url.URL, filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	summary := cutting_garden_plugins.FacetSummary{}
	for _, s := range feedFacetStories {
		facets := map[string][]cutting_garden_plugins.FacetValue{
			facetFeed: {{Key: s.feed}},
			facetRead: {{Key: s.state}},
		}
		if !filter.Matches(facets) {
			continue
		}
		for dim, values := range facets {
			hist := summary[dim]
			if hist == nil {
				hist = cutting_garden_plugins.FacetHistogram{}
				summary[dim] = hist
			}
			for _, v := range values {
				hist[v.Key]++
			}
		}
	}
	return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
}

var feedFacetLabels = map[string]string{"512": "Hacker News", "600": "lobste.rs"}

func (feedFacetLister) ResolveFacetLabels(
	_ context.Context, dimension string, keys []string,
) (map[string]string, error) {
	if dimension != facetFeed {
		return nil, nil
	}
	out := map[string]string{}
	for _, k := range keys {
		if lbl, ok := feedFacetLabels[k]; ok {
			out[k] = lbl
		}
	}
	return out, nil
}

// TestAcceptance_UnreadCountsPerFeedInOneCall pins #124's Q1 acceptance
// criterion: "unread counts per feed" on a nebulous-like plugin is
// expressible as ONE read_facets call with filter "read=unread" — exercised
// end to end through the real mcp tool surface (Tools -> Resources -> the
// fake FacetCounter+FacetLabeler plugin), with feed ids resolved to
// display names in the same response.
func TestAcceptance_UnreadCountsPerFeedInOneCall(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, feedFacetLister{}, nil
	}
	tools := newTools(r.roots, r)

	res, err := tools.CallTool(context.Background(), "read_facets",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"read=unread"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("read_facets errored: %+v", res.Content)
	}

	var view facetView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode read_facets output %q: %v", res.Content[0].Text, err)
	}

	// One call answers "unread counts per feed" directly: the feed
	// histogram, narrowed to unread stories.
	if got := view.Facets[facetFeed]["512"]; got != 2 {
		t.Errorf("unread count for feed 512 = %d, want 2", got)
	}
	if got := view.Facets[facetFeed]["600"]; got != 1 {
		t.Errorf("unread count for feed 600 = %d, want 1", got)
	}

	// Feed ids are resolved to display names in the SAME response
	// (RFC 0012 §7) — no second round trip needed to make the counts
	// human-readable.
	if got := view.Labels[facetFeed]["512"]; got != "Hacker News" {
		t.Errorf("labels[feed][512] = %q, want %q", got, "Hacker News")
	}
	if got := view.Labels[facetFeed]["600"]; got != "lobste.rs" {
		t.Errorf("labels[feed][600] = %q, want %q", got, "lobste.rs")
	}

	if view.Freshness != freshnessFresh {
		t.Errorf("freshness = %q, want %q (a filtered read is a direct, fresh compute)",
			view.Freshness, freshnessFresh)
	}

	// The unfiltered summary's "read" dimension is the alternative Q1 path
	// the phase brief names ("or the unfiltered summary's read dimension"):
	// serving the memoized whole-corpus summary directly answers
	// read-vs-unread totals without a filter at all.
	res, err = tools.CallTool(context.Background(), "read_facets",
		json.RawMessage(`{"uri":"faketest://h/work"}`))
	if err != nil {
		t.Fatalf("transport error (unfiltered): %v", err)
	}
	if res.IsError {
		t.Fatalf("unfiltered read_facets errored: %+v", res.Content)
	}
	var unfiltered facetView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &unfiltered); err != nil {
		t.Fatalf("decode unfiltered output: %v", err)
	}
	if got := unfiltered.Facets[facetRead]["unread"]; got != 3 {
		t.Errorf("unfiltered read[unread] = %d, want 3", got)
	}
	if got := unfiltered.Facets[facetRead]["read"]; got != 3 {
		t.Errorf("unfiltered read[read] = %d, want 3", got)
	}
}
