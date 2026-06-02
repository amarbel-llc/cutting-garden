package cutting_garden_plugin_git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// TestRestoreProtocol_PushesToRemote is the hermetic proof of property 6
// (restore inflates a REMOTE branch): capture a repo, then restore to a
// URL destination — a bare "remote" repo served over the in-process
// file:// transport — and verify the branch lands at the captured tip with
// the objects pushed. No `git` binary, no network; the same push path runs
// for ssh:// / https:// destinations, only the transport (and auth) differ.
func TestRestoreProtocol_PushesToRemote(t *testing.T) {
	dir, branch, tips := buildRepo(t, map[string]string{"f.txt": "hello\n"})
	tip := tips[0]

	store := newMemStore(t)
	res, err := captureProtocol(
		context.Background(), capture_plugin.NewBlobStoreWriter(store), dir, branch)
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}

	// A bare repository stands in for the remote.
	bare := filepath.Join(t.TempDir(), "remote.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	rawDest := "file://" + bare
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		Dest:          mustParseURL(t, rawDest),
		RawDest:       rawDest,
	}); err != nil {
		t.Fatalf("RestoreProtocol (push): %v", err)
	}

	// The bare remote now carries the preserved branch at the captured tip,
	// with the tip commit (and its graph) present.
	remote, err := git.PlainOpen(bare)
	if err != nil {
		t.Fatalf("open bare remote: %v", err)
	}
	ref, err := remote.Reference(plumbing.NewBranchReferenceName(branch), false)
	if err != nil {
		t.Fatalf("remote branch %s missing: %v", branch, err)
	}
	if ref.Hash().String() != tip {
		t.Errorf("pushed branch tip = %q, want %q", ref.Hash().String(), tip)
	}
	if _, err := remote.CommitObject(plumbing.NewHash(tip)); err != nil {
		t.Errorf("tip commit not pushed: %v", err)
	}
}
