package mcp

// cutting-garden#203: a container listing carries the FacetVersion snapshot
// token so a caller can compare two listings of the same container and know
// whether they read the same underlying data. These tests pin that the token
// (and its freshness) rides both the shared producer (ReadResource /
// list_nodes' default path, via renderNodeViews) and the filtered tool path,
// is present only when the plugin is a FacetVersioner, and is stripped on the
// bare path.

import (
	"context"
	"encoding/json"
	"testing"
)

// TestListing_VersionPresentForVersioner: a FacetVersioner plugin's container
// listing carries version (== the token), a versionComputedAt timestamp, and
// a fresh freshness on a cold compute — beside the nodes, not replacing them.
func TestListing_VersionPresentForVersioner(t *testing.T) {
	lister := &countingEnrichedLister{token: "snap-1"}
	r := newEnrichedCacheResources(t, lister)

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}

	var lv listingView
	if err := json.Unmarshal([]byte(got.Contents[0].Text), &lv); err != nil {
		t.Fatalf("decode listing %q: %v", got.Contents[0].Text, err)
	}

	if lv.Version != "snap-1" {
		t.Errorf("version = %q, want the FacetVersion token snap-1", lv.Version)
	}
	if lv.VersionComputedAt == "" {
		t.Error("versionComputedAt is empty; a versioned listing must timestamp it")
	}
	if lv.Freshness != freshnessFresh {
		t.Errorf("freshness = %q, want %q on a cold compute", lv.Freshness, freshnessFresh)
	}
	if len(lv.Nodes) != 1 {
		t.Errorf("nodes = %d, want 1 (the version rides BESIDE the nodes)", len(lv.Nodes))
	}
}

// TestListing_VersionReflectsToken: the version IS the plugin's token, so two
// listings whose plugin reports different tokens carry different versions —
// the cross-call comparison the feature exists for.
func TestListing_VersionReflectsToken(t *testing.T) {
	first := listingNodesVersion(t, "tok-A")
	second := listingNodesVersion(t, "tok-B")

	if first == second {
		t.Fatalf("both listings reported version %q; a moved token must change it",
			first)
	}
	if first != "tok-A" || second != "tok-B" {
		t.Errorf("versions = (%q, %q), want (tok-A, tok-B)", first, second)
	}
}

// listingNodesVersion reads a container from a versioner serving token and
// returns the listing's version field.
func listingNodesVersion(t *testing.T, token string) string {
	t.Helper()
	r := newEnrichedCacheResources(t, &countingEnrichedLister{token: token})
	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	var lv listingView
	if err := json.Unmarshal([]byte(got.Contents[0].Text), &lv); err != nil {
		t.Fatalf("decode listing: %v", err)
	}

	return lv.Version
}

// TestListing_VersionAbsentWithoutVersioner: a plugin that is not a
// FacetVersioner yields a listing with NO version fields at all (all
// omitempty) — honest absence, not a fabricated token.
func TestListing_VersionAbsentWithoutVersioner(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}

	var lv listingView
	if err := json.Unmarshal([]byte(got.Contents[0].Text), &lv); err != nil {
		t.Fatalf("decode listing %q: %v", got.Contents[0].Text, err)
	}

	if lv.Version != "" || lv.VersionComputedAt != "" || lv.Freshness != "" {
		t.Errorf("a non-versioner listing carried a version: %+v", lv.listingVersion)
	}
	if len(lv.Nodes) == 0 {
		t.Error("expected the container's children in the wrapper")
	}
}

// TestListNodesFiltered_CarriesVersion: the filtered tool path (which computes
// fresh, bypassing the listing cache) resolves the token too, so a filtered
// listing is comparable across calls exactly like the unfiltered one.
func TestListNodesFiltered_CarriesVersion(t *testing.T) {
	lister := &countingEnrichedLister{token: "fsnap-1"}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(lister)

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"status=CONFIRMED"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("filtered list_nodes errored: %+v", res.Content)
	}

	var view filteredListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode %q: %v", res.Content[0].Text, err)
	}

	if view.Version != "fsnap-1" {
		t.Errorf("filtered listing version = %q, want fsnap-1", view.Version)
	}
	if view.Freshness != freshnessFresh {
		t.Errorf("filtered freshness = %q, want fresh", view.Freshness)
	}
	// The filter metadata still rides alongside the new version block.
	if !view.FilterApplied {
		t.Error("filterApplied = false, want true (the versioner applied it)")
	}
}

// TestPaginateListingText_PaginatesWrapperAndKeepsVersion pins the #203
// regression fix: paginateListingText must slice the {nodes,version} wrapper's
// nodes (the pre-#203 code decoded the text as a bare array, which now fails,
// silently returning the WHOLE listing — limit/offset ignored). The version
// block rides through unchanged: a page of a listing is still that snapshot.
func TestPaginateListingText_PaginatesWrapperAndKeepsVersion(t *testing.T) {
	text := `{
  "nodes": [
    {"uri":"x://1"},
    {"uri":"x://2"},
    {"uri":"x://3"}
  ],
  "version": "v-9",
  "versionComputedAt": "2026-07-28T00:00:00Z",
  "freshness": "fresh"
}`

	got, err := paginateListingText(text, 0, 2)
	if err != nil {
		t.Fatalf("paginateListingText: %v", err)
	}

	var lv listingView
	if err := json.Unmarshal([]byte(got), &lv); err != nil {
		t.Fatalf("decode %q: %v", got, err)
	}

	if len(lv.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2 (limit applied to the wrapper's nodes)",
			len(lv.Nodes))
	}
	if lv.Version != "v-9" || lv.Freshness != "fresh" ||
		lv.VersionComputedAt == "" {
		t.Errorf("version block not preserved through pagination: %+v",
			lv.listingVersion)
	}
}

// TestPaginateListingText_LeafObjectUnchanged: a non-listing text (a leaf
// object read, no nodes key) is returned verbatim — pagination is a listing
// concern, and the wrapper decode must not mangle a leaf body.
func TestPaginateListingText_LeafObjectUnchanged(t *testing.T) {
	text := `{"summary":"a task","status":"open"}`

	got, err := paginateListingText(text, 0, 2)
	if err != nil {
		t.Fatalf("paginateListingText: %v", err)
	}
	if got != text {
		t.Errorf("leaf object was modified: %q -> %q", text, got)
	}
}

// TestListNodesFilteredBare_OmitsVersion: bare is the stripped-output path, so
// even a filtered bare listing carries no version — the rule "the version
// rides the enriched listing only" holds across filter and bare together.
func TestListNodesFilteredBare_OmitsVersion(t *testing.T) {
	lister := &countingEnrichedLister{token: "bsnap-1"}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(lister)

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"status=CONFIRMED","bare":true}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("filtered bare list_nodes errored: %+v", res.Content)
	}

	var view filteredListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("decode %q: %v", res.Content[0].Text, err)
	}

	if view.Version != "" {
		t.Errorf("bare filtered listing carried version %q; bare strips it", view.Version)
	}
}
