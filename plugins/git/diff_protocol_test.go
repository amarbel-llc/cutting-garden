package cutting_garden_plugin_git

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func captureToReceipt(t *testing.T, store blob_stores.BlobStoreInitialized, dir, branch string) string {
	t.Helper()
	res, err := captureProtocol(context.Background(), capture_plugin.NewBlobStoreWriter(store), dir, branch, cutting_garden_plugins.NopReporter{})
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}
	return res.ReceiptDigest
}

func runDiffProtocol(t *testing.T, store blob_stores.BlobStoreInitialized, receiptDigest, dir, branch string) []string {
	t.Helper()
	arg := "git:" + dir + "#" + branch
	res, err := (Plugin{}).DiffProtocol(cutting_garden_plugins.ProtocolDiffRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: receiptDigest,
		Source:        mustParseURL(t, arg),
		RawSource:     arg,
	})
	if err != nil {
		t.Fatalf("DiffProtocol: %v", err)
	}
	return res.Differences
}

func countAddedDeleted(diffs []string) (added, deleted int) {
	for _, d := range diffs {
		switch {
		case strings.HasPrefix(d, "A "):
			added++
		case strings.HasPrefix(d, "D "):
			deleted++
		}
	}
	return added, deleted
}

// forceBranch repoints a branch at an arbitrary commit (a force-push /
// rewind), bypassing fast-forward rules — the way upstream history is
// rewritten between captures.
func forceBranch(t *testing.T, dir, branch, hash string) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), plumbing.NewHash(hash))
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("SetReference: %v", err)
	}
}

// forceBranchToOrphan rewrites branch onto a fresh root commit whose object
// closure is disjoint from the prior history — the worst case for diff
// (the captured tip is useless as a negotiation have), which the old code
// handled with a full re-clone and the new code resolves in-memory.
func forceBranchToOrphan(t *testing.T, dir, branch string) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	s := repo.Storer
	sig := object.Signature{
		Name:  "fixture",
		Email: "fixture@example.com",
		When:  time.Unix(1_600_000_200, 0).UTC(),
	}

	setObj := func(what string, enc interface {
		Encode(plumbing.EncodedObject) error
	},
	) plumbing.Hash {
		o := &plumbing.MemoryObject{}
		if err := enc.Encode(o); err != nil {
			t.Fatalf("encode %s: %v", what, err)
		}
		h, serr := s.SetEncodedObject(o)
		if serr != nil {
			t.Fatalf("store %s: %v", what, serr)
		}
		return h
	}

	blob := &plumbing.MemoryObject{}
	blob.SetType(plumbing.BlobObject)
	if _, err := blob.Write([]byte("divergent")); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	bh, err := s.SetEncodedObject(blob)
	if err != nil {
		t.Fatalf("store blob: %v", err)
	}

	th := setObj("tree", &object.Tree{
		Entries: []object.TreeEntry{{Name: "g.txt", Mode: filemode.Regular, Hash: bh}},
	})
	ch := setObj("commit", &object.Commit{
		Author:    sig,
		Committer: sig,
		Message:   "divergent root\n",
		TreeHash:  th,
	})

	forceBranch(t, dir, branch, ch.String())
}

// TestDiffProtocol_CleanWhenTipUnchanged: an unchanged tip is detected by
// the cheap ref-advertisement probe alone — no object transfer, no drift.
func TestDiffProtocol_CleanWhenTipUnchanged(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})
	store := newMemStore(t)
	rid := captureToReceipt(t, store, dir, branch)

	if diffs := runDiffProtocol(t, store, rid, dir, branch); len(diffs) != 0 {
		t.Fatalf("expected no drift, got %v", diffs)
	}
}

// TestDiffProtocol_FastForwardReportsAdditions: a fast-forward yields only
// additions (the new commit, root tree, and blob) under the M tip line.
func TestDiffProtocol_FastForwardReportsAdditions(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})
	store := newMemStore(t)
	rid := captureToReceipt(t, store, dir, branch)

	appendCommit(t, dir, map[string]string{"f.txt": "v2"})

	diffs := runDiffProtocol(t, store, rid, dir, branch)
	if len(diffs) == 0 || !strings.HasPrefix(diffs[0], "M ") {
		t.Fatalf("expected leading M tip line, got %v", diffs)
	}
	added, deleted := countAddedDeleted(diffs)
	if added != 3 || deleted != 0 {
		t.Errorf("fast-forward diff added=%d deleted=%d, want 3/0: %v", added, deleted, diffs)
	}
}

// TestDiffProtocol_RewindReportsDeletions: a branch rewound to an earlier
// commit yields only deletions (the dropped commit, tree, and blob) — no
// fetch is needed (the live tip is already held) and no full clone.
func TestDiffProtocol_RewindReportsDeletions(t *testing.T) {
	dir, branch, tips := buildRepo(
		t,
		map[string]string{"f.txt": "v1"},
		map[string]string{"f.txt": "v2"},
	)
	store := newMemStore(t)
	rid := captureToReceipt(t, store, dir, branch) // captures the B tip

	forceBranch(t, dir, branch, tips[0]) // rewind to A

	diffs := runDiffProtocol(t, store, rid, dir, branch)
	if len(diffs) == 0 || !strings.HasPrefix(diffs[0], "M ") {
		t.Fatalf("expected leading M tip line, got %v", diffs)
	}
	added, deleted := countAddedDeleted(diffs)
	if added != 0 || deleted != 3 {
		t.Errorf("rewind diff added=%d deleted=%d, want 0/3: %v", added, deleted, diffs)
	}
}

// TestDiffProtocol_DivergentReportsAddsAndDeletes is the headline Stage-2
// case: a force-push to an unrelated history. The captured tip shares
// nothing with the live tip, so the symmetric difference is both the new
// closure (additions) and the entire old closure (deletions) — computed
// in-memory with no full re-clone.
func TestDiffProtocol_DivergentReportsAddsAndDeletes(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})
	store := newMemStore(t)
	rid := captureToReceipt(t, store, dir, branch) // captures A (closure 3)

	forceBranchToOrphan(t, dir, branch) // disjoint closure C (3)

	diffs := runDiffProtocol(t, store, rid, dir, branch)
	if len(diffs) == 0 || !strings.HasPrefix(diffs[0], "M ") {
		t.Fatalf("expected leading M tip line, got %v", diffs)
	}
	added, deleted := countAddedDeleted(diffs)
	if added != 3 || deleted != 3 {
		t.Errorf("divergent diff added=%d deleted=%d, want 3/3: %v", added, deleted, diffs)
	}
}
