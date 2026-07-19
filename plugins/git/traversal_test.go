package cutting_garden_plugin_git

import (
	"context"
	"sort"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// createBranch points a new branch ref at tip in the repo at dir, without
// checking it out — enough for ListRoots/ListContext to see it. dir must
// already be a repository built by buildRepo.
func createBranch(t *testing.T, dir, name, tip string) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	ref := plumbing.NewHashReference(
		plumbing.NewBranchReferenceName(name), plumbing.NewHash(tip),
	)
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("set branch %s: %v", name, err)
	}
}

func TestGitTypes_DeclaresSingleBranchLeaf(t *testing.T) {
	types := Plugin{}.Types()
	if len(types) != 1 {
		t.Fatalf("Types() = %v, want exactly one entry", types)
	}
	nt := types[0]
	if nt.Tag != typeBranch {
		t.Errorf("Tag = %q, want %q", nt.Tag, typeBranch)
	}
	if nt.Container {
		t.Error("a branch node MUST be a leaf (Container == false)")
	}
}

func TestGitDescribeFacets_DeclaresClosedDefaultDimension(t *testing.T) {
	facets := Plugin{}.DescribeFacets()
	if len(facets) != 1 || facets[0].Tag != typeBranch {
		t.Fatalf("DescribeFacets() = %+v, want one entry tagged %q", facets, typeBranch)
	}
	dims := facets[0].Dimensions
	if len(dims) != 1 {
		t.Fatalf("Dimensions = %v, want exactly one", dims)
	}
	d := dims[0]
	if d.Key != facetDefault {
		t.Errorf("dimension Key = %q, want %q", d.Key, facetDefault)
	}
	if d.Kind != cutting_garden_plugins.FacetCategorical {
		t.Errorf("dimension Kind = %q, want categorical", d.Kind)
	}
	if d.Values == nil {
		t.Error("the default dimension MUST declare a closed domain (RFC 0012 §2)")
	}
}

func TestListRoots_EndpointListsEveryBranchAsALeaf(t *testing.T) {
	dir, defaultBranch, tips := buildRepo(t, map[string]string{"f.txt": "v1"})
	createBranch(t, dir, "feature", tips[0])

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), mustParseURL(t, "git:"+dir),
	)
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}

	names := make([]string, 0, len(nodes))
	byName := map[string]cutting_garden_plugins.Node{}
	for _, n := range nodes {
		names = append(names, n.Name)
		byName[n.Name] = n
		if n.Type != typeBranch {
			t.Errorf("node %q Type = %q, want %q", n.Name, n.Type, typeBranch)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "feature" || names[1] != defaultBranch {
		t.Fatalf("branch names = %v, want [feature %s]", names, defaultBranch)
	}

	// The resolved default branch carries facetDefault=true; every other
	// branch carries facetDefault=false (RFC 0012 §3's informative-zero
	// posture for a closed dimension).
	if got := byName[defaultBranch].Facets[facetDefault]; len(got) != 1 || got[0].Key != "true" {
		t.Errorf("default branch %q facets[default] = %v, want [{true}]", defaultBranch, got)
	}
	if got := byName["feature"].Facets[facetDefault]; len(got) != 1 || got[0].Key != "false" {
		t.Errorf("feature branch facets[default] = %v, want [{false}]", got)
	}

	// Every listed node's URI MUST round-trip through remoteAndBranchFromArg
	// to the SAME (remote, branch) pair a `capture <node.URI>` would resolve
	// — the FDR 0014 "URI re-classifies as a capture root" contract.
	for _, n := range nodes {
		reparsed := mustParseURL(t, n.URIString())
		remote, branch, err := remoteAndBranchFromArg(reparsed)
		if err != nil {
			t.Fatalf("node %q URI %q does not re-parse: %v", n.Name, n.URIString(), err)
		}
		if remote != dir || branch != n.Name {
			t.Errorf("node %q URI %q round-trips to (%q, %q), want (%q, %q)",
				n.Name, n.URIString(), remote, branch, dir, n.Name)
		}
	}
}

func TestListRoots_BranchScopedNodeIsALeaf(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), mustParseURL(t, "git:"+dir+"#"+branch),
	)
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if nodes != nil {
		t.Errorf("ListRoots(branch-scoped) = %v, want nil (a leaf has no children)", nodes)
	}
}

func TestListRoots_NilNode_Errors(t *testing.T) {
	if _, err := (Plugin{}).ListRoots(context.Background(), nil); err == nil {
		t.Fatal("ListRoots(nil) did not error")
	}
}

func TestReadLeaf_BranchScopedNodeReturnsResolvedTip(t *testing.T) {
	dir, branch, tips := buildRepo(t, map[string]string{"f.txt": "v1"})

	content, ok, err := (Plugin{}).ReadLeaf(
		context.Background(), mustParseURL(t, "git:"+dir+"#"+branch),
	)
	if err != nil {
		t.Fatalf("ReadLeaf: %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(branch-scoped) ok = false, want true")
	}
	got, ok := content.Structured.(map[string]string)
	if !ok {
		t.Fatalf("Structured = %#v, want map[string]string", content.Structured)
	}
	if got["tip"] != tips[0] {
		t.Errorf("tip = %q, want %q", got["tip"], tips[0])
	}
	if got["branch"] != branch {
		t.Errorf("branch = %q, want %q", got["branch"], branch)
	}
}

func TestReadLeaf_EndpointIsNotALeaf(t *testing.T) {
	dir, _, _ := buildRepo(t, map[string]string{"f.txt": "v1"})

	content, ok, err := (Plugin{}).ReadLeaf(
		context.Background(), mustParseURL(t, "git:"+dir),
	)
	if err != nil {
		t.Fatalf("ReadLeaf: %v", err)
	}
	if ok {
		t.Errorf("ReadLeaf(endpoint) ok = true, content = %+v, want false (a container, not a leaf)", content)
	}
}

func TestReadLeaf_NilNode_Errors(t *testing.T) {
	if _, _, err := (Plugin{}).ReadLeaf(context.Background(), nil); err == nil {
		t.Fatal("ReadLeaf(nil) did not error")
	}
}

// TestFacetCounts_EndpointSummarizesDefaultDimension pins the RFC 0012 §4.1
// one-shot path (cutting-garden#124): the declared "default" dimension,
// previously undeclared-by-nobody (no FacetCounter and the §4.2 framework
// fold unimplemented), now actually surfaces — with informative zeros for
// its closed two-value domain even when every non-default bucket is 0.
func TestFacetCounts_EndpointSummarizesDefaultDimension(t *testing.T) {
	dir, _, tips := buildRepo(t, map[string]string{"f.txt": "v1"})
	createBranch(t, dir, "feature", tips[0])
	createBranch(t, dir, "another", tips[0])

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), mustParseURL(t, "git:"+dir), nil,
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if !ok {
		t.Fatal("FacetCounts ok = false, want true for the endpoint")
	}
	if !result.Complete {
		t.Error("Complete = false, want true (one list-remote call, no cap)")
	}
	hist := result.Summary[facetDefault]
	if hist["true"] != 1 {
		t.Errorf("default[true] = %d, want 1 (exactly one default branch)", hist["true"])
	}
	if hist["false"] != 2 {
		t.Errorf("default[false] = %d, want 2 (feature, another)", hist["false"])
	}
}

// TestFacetCounts_FilterNarrowsToMatchingBranches pins RFC 0012 §6: a filter
// on facetDefault restricts the counted set to matching branches, while the
// closed domain's informative-zero bucket stays present.
func TestFacetCounts_FilterNarrowsToMatchingBranches(t *testing.T) {
	dir, _, tips := buildRepo(t, map[string]string{"f.txt": "v1"})
	createBranch(t, dir, "feature", tips[0])

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), mustParseURL(t, "git:"+dir),
		cutting_garden_plugins.FacetFilter{{Dimension: facetDefault, Value: "true"}},
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if !ok {
		t.Fatal("FacetCounts ok = false, want true")
	}
	hist := result.Summary[facetDefault]
	if hist["true"] != 1 {
		t.Errorf("filtered default[true] = %d, want 1", hist["true"])
	}
	if hist["false"] != 0 {
		t.Errorf("filtered default[false] = %d, want 0 (feature excluded by the filter)", hist["false"])
	}
}

// TestFacetCounts_BranchScopedNodeDeclines pins that a leaf node has no
// facets of its own — the framework falls back to nothing for a leaf,
// mirroring ListRoots's branch-scoped-node-has-no-children posture.
func TestFacetCounts_BranchScopedNodeDeclines(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})

	_, ok, err := (Plugin{}).FacetCounts(
		context.Background(), mustParseURL(t, "git:"+dir+"#"+branch), nil,
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if ok {
		t.Error("FacetCounts(branch-scoped) ok = true, want false (a leaf has no facets)")
	}
}

func TestFacetCounts_NilNode_Errors(t *testing.T) {
	if _, _, err := (Plugin{}).FacetCounts(context.Background(), nil, nil); err == nil {
		t.Fatal("FacetCounts(nil) did not error")
	}
}
