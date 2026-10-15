package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// boundedLister is fakeLister whose /work listing reports itself partial,
// as caldav's date-windowed event listing does (cutting-garden#266).
type boundedLister struct{ fakeLister }

const boundedReason = "2 of 3 events lie outside the listed window"

func (b boundedLister) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node != nil && node.Path == "/work" {
		cutting_garden_plugins.ReportPartialListing(ctx, node, boundedReason)
	}
	return b.fakeLister.ListRoots(ctx, node)
}

func boundedResolve(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
	u, _, err := fakeResolve(uriStr)
	if err != nil {
		return nil, nil, err
	}
	return u, boundedLister{}, nil
}

func assertPartial(t *testing.T, lv listingVersion) {
	t.Helper()
	if lv.Complete == nil || *lv.Complete ||
		len(lv.Partial) != 1 || lv.Partial[0].URI != "faketest://h/work" ||
		lv.Partial[0].Reason != boundedReason {
		t.Errorf("want complete=false with the /work report, got complete=%v partial=%+v",
			lv.Complete, lv.Partial)
	}
}

// TestListing_PartialSurvivesCache pins that a container read reports a
// bounded listing as incomplete — on the computing read and on the cache
// hit after it, when the plugin is not called again.
func TestListing_PartialSurvivesCache(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	r.resolve = boundedResolve

	for _, read := range []string{"cold", "cached"} {
		got, err := r.ReadResource(context.Background(), "faketest://h/work")
		if err != nil {
			t.Fatalf("%s ReadResource: %v", read, err)
		}
		var lv listingView
		if err := json.Unmarshal([]byte(got.Contents[0].Text), &lv); err != nil {
			t.Fatalf("decode listing %q: %v", got.Contents[0].Text, err)
		}
		assertPartial(t, lv.listingVersion)
	}

	got, err := r.ReadResource(context.Background(), "faketest://h/")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	var lv listingView
	if err := json.Unmarshal([]byte(got.Contents[0].Text), &lv); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if lv.Complete != nil || lv.Partial != nil {
		t.Errorf("a complete listing must omit the fields: %q", got.Contents[0].Text)
	}
}

// TestListNodesQuery_ReportsPartialListing pins that a trellis query which
// walked a bounded listing says its result may be incomplete.
func TestListNodesQuery_ReportsPartialListing(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(boundedLister{})

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/","query":"!test-calendar-v1 -> !test-object-v1"}`))
	if err != nil || res.IsError {
		t.Fatalf("query: err=%v content=%+v", err, res.Content)
	}
	var view queriedListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode %q: %v", res.Content[0].Text, err)
	}
	assertPartial(t, view.listingVersion)
}
