package traversal_serve_testpeer

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// TestFacetWriteDeclarationIsValid pins that the fixture's own declaration
// passes the same cross-check the host applies (a linked plugin's tests
// assert ValidateFacetWrites against their own schema).
func TestFacetWriteDeclarationIsValid(t *testing.T) {
	plugin := NewPlugin()
	if err := cutting_garden_plugins.ValidateFacetWrites(
		plugin.DescribeFacets(), plugin.DescribeFacetWrites(),
	); err != nil {
		t.Fatalf("ValidateFacetWrites: %v", err)
	}
}

// TestPatchMovesDeclaredFacets pins that the host-built shapes move the
// node's facet membership: {"state": bucket} replaces the one dimension,
// {"tags": [set]} replaces the many dimension wholesale, [] clears it.
func TestPatchMovesDeclaredFacets(t *testing.T) {
	plugin := NewPlugin()
	ctx := context.Background()
	alpha := mustParseURL(t, LeafAlpha)

	facetsOfAlpha := func() map[string][]cutting_garden_plugins.FacetValue {
		t.Helper()
		nodes, err := plugin.ListRoots(ctx, mustParseURL(t, RootBox))
		if err != nil {
			t.Fatalf("ListRoots: %v", err)
		}
		return nodes[0].Facets
	}

	for _, tc := range []struct {
		body      string
		dimension string
		want      []cutting_garden_plugins.FacetValue
	}{
		{`{"state":"closed"}`, "state", []cutting_garden_plugins.FacetValue{{Key: "closed"}}},
		{`{"tags":["z","b"]}`, "tag", []cutting_garden_plugins.FacetValue{{Key: "z"}, {Key: "b"}}},
		{`{"tags":[]}`, "tag", []cutting_garden_plugins.FacetValue{}},
	} {
		if _, err := plugin.PatchNode(ctx, alpha, strings.NewReader(tc.body)); err != nil {
			t.Fatalf("PatchNode(%s): %v", tc.body, err)
		}
		if got := facetsOfAlpha()[tc.dimension]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("after %s: %s = %+v, want %+v", tc.body, tc.dimension, got, tc.want)
		}
	}
}

// TestPatchRejectsUnusableFacetWriteValues pins the RFC 0013 rule for a
// recognized key with an unusable value: a bad request (-32602 on the
// wire), with the node left untouched.
func TestPatchRejectsUnusableFacetWriteValues(t *testing.T) {
	plugin := NewPlugin()
	ctx := context.Background()
	alpha := mustParseURL(t, LeafAlpha)

	for _, body := range []string{
		`{"state":5}`, `{"state":""}`, `{"tags":"a"}`, `{"tags":[1]}`,
	} {
		_, err := plugin.PatchNode(ctx, alpha, strings.NewReader(body))
		if err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("PatchNode(%s): err = %v, want a bad request", body, err)
		}
	}

	nodes, err := plugin.ListRoots(ctx, mustParseURL(t, RootBox))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if got := nodes[0].Facets["state"]; !reflect.DeepEqual(
		got, []cutting_garden_plugins.FacetValue{{Key: "open"}},
	) {
		t.Errorf("state after rejected patches = %+v, want untouched open", got)
	}
}

// TestTrailingSlashContainerListsLikeBare pins readKey: organize re-queries
// at its document's anchor, the listed URIs' common prefix — which, for
// this path-shaped tree, carries a trailing `/`.
func TestTrailingSlashContainerListsLikeBare(t *testing.T) {
	plugin := NewPlugin()
	ctx := context.Background()

	bare, err := plugin.ListRoots(ctx, mustParseURL(t, RootBox))
	if err != nil {
		t.Fatalf("ListRoots bare: %v", err)
	}
	slashed, err := plugin.ListRoots(ctx, mustParseURL(t, RootBox+"/"))
	if err != nil {
		t.Fatalf("ListRoots slashed: %v", err)
	}
	if len(bare) == 0 || !reflect.DeepEqual(bare, slashed) {
		t.Errorf("slashed listing %+v differs from bare %+v", slashed, bare)
	}
}

// TestStateFilePersistsAcrossInstances pins StateFileEnv: a mutation in one
// peer instance is visible to the next instance started against the same
// file — the property the CLI-level organize bats lane needs, since every
// host invocation spawns a fresh peer.
func TestStateFilePersistsAcrossInstances(t *testing.T) {
	t.Setenv(StateFileEnv, filepath.Join(t.TempDir(), "state.json"))
	ctx := context.Background()

	first, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv: %v", err)
	}
	if _, err := first.Plugin.(*TreePlugin).PatchNode(
		ctx, mustParseURL(t, LeafBeta), strings.NewReader(`{"state":"open"}`),
	); err != nil {
		t.Fatalf("PatchNode: %v", err)
	}

	second, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv (second): %v", err)
	}
	nodes, err := second.Plugin.(*TreePlugin).ListRoots(ctx, mustParseURL(t, RootBox))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if got := nodes[1].Facets["state"]; !reflect.DeepEqual(
		got, []cutting_garden_plugins.FacetValue{{Key: "open"}},
	) {
		t.Errorf("beta state in the second instance = %+v, want open", got)
	}
	if len(nodes) != 4 || nodes[2].URIString() != NestedBox {
		t.Errorf("tree shape not restored: %+v", nodes)
	}
}
