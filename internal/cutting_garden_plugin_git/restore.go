package cutting_garden_plugin_git

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

var _ cutting_garden_plugins.ProtocolRestorePlugin = (*Plugin)(nil)

// ProtocolKind reports the receipt kind this plugin restores and diffs.
func (Plugin) ProtocolKind() string { return captureKind }

// RestoreProtocol rebuilds a working git clone from a git receipt merkle
// tree, checked out to the preserved branch — entirely in-process via
// go-git (no `git` binary). Every captured object is written back into a
// fresh repository's object database (loadEncodedObject verifies each
// recreated oid against the captured one, an integrity check), the
// preserved branch is pointed at the recorded tip, and the working tree is
// materialized from it.
func (Plugin) RestoreProtocol(req cutting_garden_plugins.ProtocolRestoreRequest) error {
	dest := req.RawDest
	if dest == "" && req.Dest != nil {
		dest = req.Dest.Path
	}
	if dest == "" {
		return errors.ErrorWithStackf("git plugin: empty restore destination")
	}
	if err := assertDestAbsent(dest); err != nil {
		return err
	}

	payload, meta, err := loadReceiptPayload(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return err
	}

	branch := meta.Branch
	if branch == "" {
		branch = "main"
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return errors.Wrapf(err, "git plugin: create restore destination %s", dest)
	}
	repo, err := git.PlainInit(dest, false)
	if err != nil {
		return errors.Wrapf(err, "git plugin: init repository at %s", dest)
	}

	// Write every captured object back into the object database. The bridge
	// re-verifies each object's git oid, so a corrupted blob is rejected
	// here rather than silently restored.
	for _, ref := range payload.Refs {
		obj, lerr := loadEncodedObject(req.BlobStore, ref)
		if lerr != nil {
			return lerr
		}
		if _, serr := repo.Storer.SetEncodedObject(obj); serr != nil {
			return errors.Wrapf(serr, "git plugin: write object %s", ref.Alias)
		}
	}

	// Point the preserved branch at the recorded tip and aim HEAD at it,
	// then materialize the working tree from the tip commit.
	branchRef := plumbing.NewBranchReferenceName(branch)
	tip := plumbing.NewHash(meta.Tip)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, tip)); err != nil {
		return errors.Wrapf(err, "git plugin: set branch %s to %s", branch, meta.Tip)
	}
	if err := repo.Storer.SetReference(
		plumbing.NewSymbolicReference(plumbing.HEAD, branchRef)); err != nil {
		return errors.Wrapf(err, "git plugin: point HEAD at %s", branch)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return errors.Wrap(err)
	}
	if err := wt.Reset(&git.ResetOptions{Commit: tip, Mode: git.HardReset}); err != nil {
		return errors.Wrapf(err, "git plugin: check out %s", meta.Tip)
	}
	return nil
}

// assertDestAbsent refuses a restore destination that already exists,
// mirroring the filesystem plugin's destination precondition.
func assertDestAbsent(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return errors.ErrorWithStackf(
			"%s: destination already exists\n"+
				"hint: choose a destination that does not exist", dest)
	} else if !os.IsNotExist(err) {
		return errors.Wrapf(err, "stat %q", dest)
	}
	return nil
}
