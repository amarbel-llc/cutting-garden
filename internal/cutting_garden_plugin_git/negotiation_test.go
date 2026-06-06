package cutting_garden_plugin_git

import (
	"context"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/memory"
)

// countingStorer wraps an in-memory storer and counts SetEncodedObject
// calls. A fetch into a non-pack-writing storer explodes the received
// pack object-by-object via SetEncodedObject, so the count gained across
// a fetch is exactly the number of objects that crossed the wire — the
// observable that distinguishes a delta transfer from a full one.
type countingStorer struct {
	*memory.Storage
	sets int
}

func (c *countingStorer) SetEncodedObject(o plumbing.EncodedObject) (plumbing.Hash, error) {
	c.sets++
	return c.Storage.SetEncodedObject(o)
}

func fetchBranch(t *testing.T, st storage.Storer, dir, branch string) {
	t.Helper()
	remote := git.NewRemote(st, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{dir},
	})
	err := remote.FetchContext(context.Background(), &git.FetchOptions{
		RefSpecs: []config.RefSpec{
			config.RefSpec("+refs/heads/" + branch + ":refs/remotes/origin/" + branch),
		},
		Tags: git.NoTags,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		t.Fatalf("fetch %s: %v", branch, err)
	}
}

// TestSeededStorer_FetchTransfersOnlyDelta is the Stage-2 risk validation:
// go-git, given a storer seeded with a prior snapshot's objects plus a ref
// at the prior tip, negotiates that tip as a `have` and fetches only the
// delta to the new tip. This is the load-bearing assumption behind
// fewest-ops diff (property 2) and incremental capture (property 4).
//
// The fixture: commit A (one file) then commit B (same file changed).
// closure(A) = {commitA, treeA, blobV1} (3); closure(B) = closure(A) +
// {commitB, treeB, blobV2} (6); the fast-forward delta is exactly 3.
func TestSeededStorer_FetchTransfersOnlyDelta(t *testing.T) {
	dir, branch, tips := buildRepo(t, map[string]string{"f.txt": "v1"})
	tipA := tips[0]

	// Capture closure(A) the production way, then read back its refs/tip.
	store := newMemStore(t)
	res, err := captureProtocol(
		context.Background(), capture_plugin.NewBlobStoreWriter(store), dir, branch, cutting_garden_plugins.NopReporter{})
	if err != nil {
		t.Fatalf("capture A: %v", err)
	}
	payload, meta, err := loadReceiptPayload(store, res.ReceiptDigest)
	if err != nil {
		t.Fatalf("load payload: %v", err)
	}
	if meta.Tip != tipA {
		t.Fatalf("captured tip %q, want %q", meta.Tip, tipA)
	}

	// Advance the branch to commit B.
	tipB := appendCommit(t, dir, map[string]string{"f.txt": "v2"})

	// Seeded fetch: storer already holds closure(A) and advertises A as a
	// have, so the server should send only the delta.
	seeded := &countingStorer{Storage: memory.NewStorage()}
	if err := populateNegotiationStorer(seeded, store, payload.Refs, meta.Tip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	beforeFetch := seeded.sets
	fetchBranch(t, seeded, dir, branch)
	deltaObjs := seeded.sets - beforeFetch

	// Cold fetch: empty storer, no haves → the server sends the full
	// closure of B.
	cold := &countingStorer{Storage: memory.NewStorage()}
	fetchBranch(t, cold, dir, branch)
	fullObjs := cold.sets

	if deltaObjs >= fullObjs {
		t.Fatalf("seeded fetch transferred %d objects, not fewer than the cold "+
			"fetch's %d — go-git did not negotiate the seeded tip as a have",
			deltaObjs, fullObjs)
	}
	if deltaObjs != 3 {
		t.Errorf("delta fetch transferred %d objects, want 3 (commitB, treeB, blobV2)", deltaObjs)
	}
	if fullObjs != 6 {
		t.Errorf("cold fetch transferred %d objects, want 6 (full closure of B)", fullObjs)
	}

	// The seeded storer can now reconstruct B: the new commit is present.
	if _, err := seeded.EncodedObject(plumbing.CommitObject, plumbing.NewHash(tipB)); err != nil {
		t.Fatalf("commit B missing from seeded storer after fetch: %v", err)
	}
}
