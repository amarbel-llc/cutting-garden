package cutting_garden_plugin_git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	git "github.com/go-git/go-git/v5"
)

// TestRestoreProtocol_RebuildsCheckedOutClone is the hermetic restore
// round-trip (no `git` binary): capture a repo, restore it, and verify via
// go-git that the result is a clean working clone on the preserved branch
// at the captured tip, with file contents materialized.
func TestRestoreProtocol_RebuildsCheckedOutClone(t *testing.T) {
	dir, branch, tips := buildRepo(t, map[string]string{
		"f.txt":     "hello\n",
		"sub/g.txt": "nested\n",
	})
	tip := tips[0]

	store := newMemStore(t)
	res, err := captureProtocol(context.Background(), capture_plugin.NewBlobStoreWriter(store), dir, branch)
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		RawDest:       dest,
	}); err != nil {
		t.Fatalf("RestoreProtocol: %v", err)
	}

	repo, err := git.PlainOpen(dest)
	if err != nil {
		t.Fatalf("open restored repo: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("restored HEAD: %v", err)
	}
	if head.Name().Short() != branch {
		t.Errorf("restored branch = %q, want %q", head.Name().Short(), branch)
	}
	if head.Hash().String() != tip {
		t.Errorf("restored tip = %q, want %q", head.Hash().String(), tip)
	}

	// Working tree materialized with the captured contents.
	if got, err := os.ReadFile(filepath.Join(dest, "f.txt")); err != nil || string(got) != "hello\n" {
		t.Errorf("restored f.txt = %q (err %v), want %q", got, err, "hello\n")
	}
	if got, err := os.ReadFile(filepath.Join(dest, "sub", "g.txt")); err != nil || string(got) != "nested\n" {
		t.Errorf("restored sub/g.txt = %q (err %v), want %q", got, err, "nested\n")
	}

	// A clean checkout: no diff between worktree, index, and HEAD.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	status, err := wt.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.IsClean() {
		t.Errorf("restored worktree not clean:\n%s", status)
	}
}

// TestRestoreProtocol_RejectsExistingDestination guards the precondition
// that restore never writes into an existing path.
func TestRestoreProtocol_RejectsExistingDestination(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})
	store := newMemStore(t)
	res, err := captureProtocol(context.Background(), capture_plugin.NewBlobStoreWriter(store), dir, branch)
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}

	dest := t.TempDir() // already exists
	err = (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		RawDest:       dest,
	})
	if err == nil {
		t.Fatal("expected error for existing destination, got nil")
	}
}
