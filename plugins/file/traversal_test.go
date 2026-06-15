package cutting_garden_plugin_file

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
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
