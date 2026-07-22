package traversal_serve_testpeer

import (
	"context"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	uri, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return uri
}

// TestFixedTreeShape pins the deterministic tree: roots, child order,
// leaf content, and the leaf/container listing distinction.
func TestFixedTreeShape(t *testing.T) {
	plugin := NewPlugin()
	ctx := context.Background()

	roots, err := plugin.Roots(ctx)
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 1 || roots[0].String() != RootBox {
		t.Fatalf("Roots = %v, want [%s]", roots, RootBox)
	}

	boxChildren, err := plugin.ListRoots(ctx, mustParseURL(t, RootBox))
	if err != nil {
		t.Fatalf("ListRoots(box): %v", err)
	}
	wantOrder := []string{LeafAlpha, LeafBeta, NestedBox}
	if len(boxChildren) != len(wantOrder) {
		t.Fatalf("box children = %+v, want %d", boxChildren, len(wantOrder))
	}
	for i, want := range wantOrder {
		if got := boxChildren[i].URI.String(); got != want {
			t.Errorf("box child[%d] = %q, want %q", i, got, want)
		}
	}
	if boxChildren[2].Type != ContainerType {
		t.Errorf("nested type = %q, want %q", boxChildren[2].Type, ContainerType)
	}

	alpha := boxChildren[0]
	if got := alpha.Facets["state"]; len(got) != 1 || got[0].Key != "open" {
		t.Errorf("alpha state = %+v, want [open]", got)
	}
	if got := alpha.Facets["month"]; len(got) != 1 || got[0].Order != 202606 {
		t.Errorf("alpha month = %+v, want order 202606", got)
	}
	if got := alpha.Facets["tag"]; len(got) != 2 {
		t.Errorf("alpha tags = %+v, want the multi-valued pair", got)
	}

	nestedChildren, err := plugin.ListRoots(ctx, mustParseURL(t, NestedBox))
	if err != nil {
		t.Fatalf("ListRoots(nested): %v", err)
	}
	if len(nestedChildren) != 1 || nestedChildren[0].URI.String() != LeafGamma {
		t.Fatalf("nested children = %+v, want [%s]", nestedChildren, LeafGamma)
	}

	leafChildren, err := plugin.ListRoots(ctx, mustParseURL(t, LeafAlpha))
	if err != nil || len(leafChildren) != 0 {
		t.Errorf("ListRoots(leaf) = %+v, %v; want empty, nil", leafChildren, err)
	}

	content, ok, err := plugin.ReadLeaf(ctx, mustParseURL(t, LeafAlpha))
	if err != nil || !ok {
		t.Fatalf("ReadLeaf(alpha) = ok %t, err %v; want true, nil", ok, err)
	}
	if string(content.Raw) != "alpha body\n" || content.RawMimeType != LeafMimeType {
		t.Errorf("alpha content = %q %q", content.Raw, content.RawMimeType)
	}

	if _, ok, err := plugin.ReadLeaf(
		ctx, mustParseURL(t, RootBox),
	); ok || err != nil {
		t.Errorf("ReadLeaf(container) = ok %t, err %v; want false, nil", ok, err)
	}
}

// TestFacetCountsFoldsSubtree pins the recursive fold: the root summary
// spans nested's leaf, multi-valued tags count each value, and a filter
// narrows the fold.
func TestFacetCountsFoldsSubtree(t *testing.T) {
	plugin := NewPlugin()
	ctx := context.Background()

	result, ok, err := plugin.FacetCounts(
		ctx, mustParseURL(t, RootBox), nil,
	)
	if err != nil || !ok || !result.Complete {
		t.Fatalf("FacetCounts(box) = ok %t, complete %t, err %v",
			ok, result.Complete, err)
	}
	if result.Summary["state"]["open"] != 2 ||
		result.Summary["state"]["closed"] != 1 {
		t.Errorf("state histogram = %+v", result.Summary["state"])
	}
	if result.Summary["tag"]["b"] != 2 || result.Summary["tag"]["c"] != 1 {
		t.Errorf("tag histogram = %+v", result.Summary["tag"])
	}
	if result.Summary["month"]["2026-07"] != 2 {
		t.Errorf("month histogram = %+v", result.Summary["month"])
	}

	// The §13 breakdown attributes DESCENDANTS only: exactly one node
	// (gamma) lives under the nested box, so its count is 1 — not 2,
	// which is what counting the container's own trivial empty-filter
	// self-match would produce (the box's direct leaves belong to no
	// child container and appear nowhere). Pins the phantom-self-match
	// bug the #173 review caught (cutting-garden#173).
	wantBreakdown := []cutting_garden_plugins.FacetContainerBreakdown{
		{URI: NestedBox, Name: "nested", Count: 1},
	}
	if !reflect.DeepEqual(result.ByContainer, wantBreakdown) {
		t.Errorf("byContainer = %+v, want %+v",
			result.ByContainer, wantBreakdown)
	}
	if result.ByContainerTruncated {
		t.Error("byContainerTruncated = true, want false")
	}

	filtered, ok, err := plugin.FacetCounts(
		ctx, mustParseURL(t, RootBox),
		cutting_garden_plugins.FacetFilter{
			{Dimension: "state", Value: "open"},
		},
	)
	if err != nil || !ok {
		t.Fatalf("filtered FacetCounts = ok %t, err %v", ok, err)
	}
	if filtered.Summary["state"]["open"] != 2 ||
		filtered.Summary["state"]["closed"] != 0 {
		t.Errorf("filtered state histogram = %+v", filtered.Summary["state"])
	}
	if filtered.Summary["feed"]["f2"] != 0 || filtered.Summary["feed"]["f1"] != 2 {
		t.Errorf("filtered feed histogram = %+v", filtered.Summary["feed"])
	}
	// Under the filter, gamma (open) still matches: the breakdown carries
	// the SAME filter as the summary (RFC 0012 §13 attribution scope).
	wantFiltered := []cutting_garden_plugins.FacetContainerBreakdown{
		{URI: NestedBox, Name: "nested", Count: 1},
	}
	if !reflect.DeepEqual(filtered.ByContainer, wantFiltered) {
		t.Errorf("filtered byContainer = %+v, want %+v",
			filtered.ByContainer, wantFiltered)
	}

	// A leaf is not summarized: the decline, not an error.
	if _, ok, err := plugin.FacetCounts(
		ctx, mustParseURL(t, LeafAlpha), nil,
	); ok || err != nil {
		t.Errorf("FacetCounts(leaf) = ok %t, err %v; want false, nil", ok, err)
	}
}

// TestFacetVersionDeterministicAndMutationSensitive pins the token
// contract: equal across fresh instances, changed by any mutation.
func TestFacetVersionDeterministicAndMutationSensitive(t *testing.T) {
	ctx := context.Background()
	box := mustParseURL(t, RootBox)

	first, ok, err := NewPlugin().FacetVersion(ctx, box)
	if err != nil || !ok {
		t.Fatalf("FacetVersion = ok %t, err %v", ok, err)
	}

	plugin := NewPlugin()
	second, _, _ := plugin.FacetVersion(ctx, box)
	if first != second {
		t.Errorf("fresh tokens differ: %q vs %q", first, second)
	}

	delta := mustParseURL(t, RootBox+"/delta")
	if err := plugin.CreateNode(
		ctx, delta, strings.NewReader("x"), LeafType,
	); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	mutated, _, _ := plugin.FacetVersion(ctx, box)
	if mutated == second {
		t.Errorf("token unchanged after mutation: %q", mutated)
	}
}

// TestMutationsRoundTripInMemory pins the write surface: create appends
// to the parent listing, patch merges into the structured view, put
// replaces raw, delete removes (recursively), and the guards hold
// (strict create, declared type, existing parent, empty patch body is a
// bad request).
func TestMutationsRoundTripInMemory(t *testing.T) {
	plugin := NewPlugin()
	ctx := context.Background()
	delta := mustParseURL(t, RootBox+"/delta")

	if err := plugin.CreateNode(
		ctx, delta, strings.NewReader("delta body"), LeafType,
	); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	children, err := plugin.ListRoots(ctx, mustParseURL(t, RootBox))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if len(children) != 4 || children[3].URI.String() != delta.String() {
		t.Fatalf("children after create = %+v, want delta appended", children)
	}

	// Strict create: the same URI again is an error.
	if err := plugin.CreateNode(
		ctx, delta, strings.NewReader("again"), LeafType,
	); err == nil {
		t.Error("re-create succeeded, want strict-create error")
	}

	// An undeclared type is a bad request.
	other := mustParseURL(t, RootBox+"/other")
	if err := plugin.CreateNode(
		ctx, other, nil, "cgtest-unknown-v1",
	); !errors.Is400BadRequest(err) {
		t.Errorf("undeclared type: err = %v, want bad request", err)
	}

	// A parentless create is a bad request.
	orphan := mustParseURL(t, "cgtest://fixture/nowhere/orphan")
	if err := plugin.CreateNode(
		ctx, orphan, nil, LeafType,
	); !errors.Is400BadRequest(err) {
		t.Errorf("orphan create: err = %v, want bad request", err)
	}

	applied, err := plugin.PatchNode(
		ctx, delta, strings.NewReader(`{"note":"patched"}`),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if !slices.Equal(applied, []string{"note"}) {
		t.Errorf("applied = %#v, want [note] (cutting-garden#182)", applied)
	}

	content, ok, err := plugin.ReadLeaf(ctx, delta)
	if err != nil || !ok {
		t.Fatalf("ReadLeaf(delta) = ok %t, err %v", ok, err)
	}
	structured, isMap := content.Structured.(map[string]any)
	if !isMap || structured["note"] != "patched" ||
		structured["title"] != "delta" {
		t.Errorf("patched structured = %+v", content.Structured)
	}
	if string(content.Raw) != "delta body" {
		t.Errorf("raw after patch = %q, want untouched", content.Raw)
	}

	if _, err := plugin.PatchNode(
		ctx, delta, strings.NewReader(""),
	); !errors.Is400BadRequest(err) {
		t.Errorf("empty patch: err = %v, want bad request", err)
	}

	if err := plugin.PutNode(
		ctx, delta, strings.NewReader("replaced"),
	); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	content, _, _ = plugin.ReadLeaf(ctx, delta)
	if string(content.Raw) != "replaced" {
		t.Errorf("raw after put = %q, want replaced", content.Raw)
	}

	missing := mustParseURL(t, RootBox+"/missing")
	if err := plugin.PutNode(
		ctx, missing, strings.NewReader("x"),
	); err == nil {
		t.Error("put on a missing node succeeded")
	}

	if err := plugin.DeleteNode(ctx, delta); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, ok, _ := plugin.ReadLeaf(ctx, delta); ok {
		t.Error("deleted leaf still readable")
	}
	children, _ = plugin.ListRoots(ctx, mustParseURL(t, RootBox))
	if len(children) != 3 {
		t.Errorf("children after delete = %+v, want the original 3", children)
	}

	// Recursive delete: removing nested takes gamma with it.
	if err := plugin.DeleteNode(ctx, mustParseURL(t, NestedBox)); err != nil {
		t.Fatalf("DeleteNode(nested): %v", err)
	}
	if _, ok, _ := plugin.ReadLeaf(ctx, mustParseURL(t, LeafGamma)); ok {
		t.Error("gamma survived its container's deletion")
	}
}

// TestConfigAppliesTOMLToServedPlugin pins the RFC 0007 passthrough
// probe: Config wires ConfigApply to the fresh served instance, and the
// received TOML is readable back through cfg.Plugin.
func TestConfigAppliesTOMLToServedPlugin(t *testing.T) {
	cfg := Config()

	plugin, isTree := cfg.Plugin.(*TreePlugin)
	if !isTree {
		t.Fatalf("cfg.Plugin is %T, want *TreePlugin", cfg.Plugin)
	}

	if _, ok := plugin.ReceivedConfigTOML(); ok {
		t.Error("fresh config reports a received TOML")
	}

	const configTOML = "[cgtest]\nfixture = \"v1\"\n"
	if err := cfg.ConfigApply(configTOML); err != nil {
		t.Fatalf("ConfigApply: %v", err)
	}

	received, ok := plugin.ReceivedConfigTOML()
	if !ok || received != configTOML {
		t.Errorf("received = %q, %t; want %q, true", received, ok, configTOML)
	}
}

// TestResolveFacetLabelsFixedMap pins the labelled dimension: known
// keys resolve from the fixed map, unknown keys and dimensions are
// simply absent.
func TestResolveFacetLabelsFixedMap(t *testing.T) {
	plugin := NewPlugin()

	labels, err := plugin.ResolveFacetLabels(
		context.Background(), "feed", []string{"f1", "f2", "zz"},
	)
	if err != nil {
		t.Fatalf("ResolveFacetLabels: %v", err)
	}
	if labels["f1"] != "Feed One" || labels["f2"] != "Feed Two" {
		t.Errorf("labels = %+v", labels)
	}
	if _, found := labels["zz"]; found {
		t.Errorf("unknown key resolved: %+v", labels)
	}

	labels, err = plugin.ResolveFacetLabels(
		context.Background(), "state", []string{"open"},
	)
	if err != nil || len(labels) != 0 {
		t.Errorf("unlabelled dimension = %+v, %v; want empty, nil", labels, err)
	}
}
