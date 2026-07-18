package cutting_garden_plugin_file

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestRoots_WorkingDirectory(t *testing.T) {
	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 intrinsic root, got %d", len(roots))
	}
	cwd, _ := os.Getwd()
	if roots[0].Scheme != "file" || roots[0].Path != cwd {
		t.Errorf("root = %s, want file://%s", roots[0], cwd)
	}
}

func TestListRoots_OneLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: dir},
	)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]string, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n.Type
	}
	if byName["sub"] != typeDirectory {
		t.Errorf("sub type = %q, want %q", byName["sub"], typeDirectory)
	}
	if byName["f.txt"] != typeFile {
		t.Errorf("f.txt type = %q, want %q", byName["f.txt"], typeFile)
	}
}

func TestListRoots_LeafHasNoChildren(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: f},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("leaf yielded %d children, want 0", len(nodes))
	}
}

// TestListRoots_OneLevelOnly pins the lazy, one-level-per-call contract
// (FDR 0014): a grandchild file must NOT appear in a listing of the
// top-level directory — only sub/ itself does, as a container the caller
// would descend into with a second call.
func TestListRoots_OneLevelOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "sub", "grandchild.txt"), []byte("x"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: dir},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1 (only sub/, not its contents): %+v", len(nodes), nodes)
	}
	if nodes[0].Name != "sub" || nodes[0].Type != typeDirectory {
		t.Errorf("nodes[0] = %+v, want sub/ directory", nodes[0])
	}
}

// TestListRoots_HiddenDotfilesIncluded pins the deliberate choice to
// include dotfiles: this lists the user's own working tree, not a shared
// or untrusted corpus.
func TestListRoots_HiddenDotfilesIncluded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: dir},
	)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range nodes {
		if n.Name == ".hidden" {
			found = true
		}
	}
	if !found {
		t.Errorf("dotfile omitted from listing: %+v", nodes)
	}
}

// TestListRoots_SymlinkedDirectoryIsLeafNotFollowed pins the documented
// symlink posture (FDR 0014's open question, resolved here): a symlinked
// directory classifies as typeFile (a leaf-like dead end), not
// typeDirectory, so ListRoots never follows it.
func TestListRoots_SymlinkedDirectoryIsLeafNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "realdir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "inside.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linkdir")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: dir},
	)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]cutting_garden_plugins.Node, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}
	linkNode, ok := byName["linkdir"]
	if !ok {
		t.Fatal("linkdir missing from listing")
	}
	if linkNode.Type != typeFile {
		t.Errorf("linkdir type = %q, want %q (leaf, not followed)", linkNode.Type, typeFile)
	}

	// A subsequent ListRoots call on the symlink's own URI must not
	// enumerate the target's children (i.e. it behaves as a leaf, not a
	// container) — the "does not follow" contract, not merely a labeling
	// quirk.
	descend, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: link},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(descend) != 0 {
		t.Errorf("ListRoots on a symlinked dir returned %d children, want 0 (leaf)", len(descend))
	}
}

// TestListRoots_FileFacetsPopulated pins the RFC 0012 §1 "same enumeration"
// rule end to end: a regular file's node carries extension/size_band/month
// facets computed from the SAME os.ReadDir/stat pass, while a directory
// entry carries none (facets are declared only for the file leaf type).
func TestListRoots_FileFacetsPopulated(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "report.TXT"), []byte("hello"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	nodes, err := (Plugin{}).ListRoots(
		context.Background(), &url.URL{Scheme: "file", Path: dir},
	)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]cutting_garden_plugins.Node, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}

	file := byName["report.TXT"]
	if len(file.Facets[facetExtension]) != 1 || file.Facets[facetExtension][0].Key != "txt" {
		t.Errorf("report.TXT extension facet = %+v, want [txt]", file.Facets[facetExtension])
	}
	if len(file.Facets[facetSizeBand]) != 1 || file.Facets[facetSizeBand][0].Key != sizeBandTiny {
		t.Errorf("report.TXT size_band facet = %+v, want [tiny]", file.Facets[facetSizeBand])
	}
	if _, ok := file.Facets[facetMonth]; !ok {
		t.Errorf("report.TXT missing month facet: %+v", file.Facets)
	}

	sub := byName["sub"]
	if sub.Facets != nil {
		t.Errorf("directory node carries facets: %+v, want nil", sub.Facets)
	}
}
