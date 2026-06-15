package cutting_garden_plugin_git

import (
	"fmt"
	"os"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
)

var _ cutting_garden_plugins.ProtocolRestorePlugin = (*Plugin)(nil)

// ProtocolKind reports the receipt kind this plugin restores and diffs.
func (Plugin) ProtocolKind() string { return captureKind }

// RestoreProtocol reconstitutes a git receipt's preserved branch at its
// recorded tip — entirely in-process via go-git. The destination decides
// the shape:
//
//   - a bare local path (no URL scheme): a fresh working clone is inflated
//     there, checked out to the branch (restoreLocalClone);
//   - a URL (any scheme — ssh://, https://, git://, file://): the branch
//     is pushed to that remote (restoreRemotePush).
//
// Either way the captured objects are loaded back through the bridge,
// which re-verifies each recreated oid against the captured one.
func (Plugin) RestoreProtocol(req cutting_garden_plugins.ProtocolRestoreRequest) error {
	payload, meta, err := loadReceiptPayload(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return err
	}

	branch := meta.Branch
	if branch == "" {
		branch = "main"
	}

	if req.Dest != nil && req.Dest.Scheme != "" {
		return restoreRemotePush(req, payload.Refs, meta.Tip, branch)
	}
	return restoreLocalClone(req, payload.Refs, meta.Tip, branch)
}

// restoreLocalClone inflates a fresh working clone at a local path: init a
// repository, write every object back, point the preserved branch at the
// tip, aim HEAD at it, and hard-reset the working tree from the tip.
func restoreLocalClone(
	req cutting_garden_plugins.ProtocolRestoreRequest,
	refs []capture_plugin.Ref,
	tipStr, branch string,
) error {
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

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return errors.Wrapf(err, "git plugin: create restore destination %s", dest)
	}
	repo, err := git.PlainInit(dest, false)
	if err != nil {
		return errors.Wrapf(err, "git plugin: init repository at %s", dest)
	}

	if err := writeObjectsToStorer(repo.Storer, req.BlobStore, refs); err != nil {
		return err
	}

	branchRef := plumbing.NewBranchReferenceName(branch)
	tip := plumbing.NewHash(tipStr)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, tip)); err != nil {
		return errors.Wrapf(err, "git plugin: set branch %s to %s", branch, tipStr)
	}
	if err := repo.Storer.SetReference(
		plumbing.NewSymbolicReference(plumbing.HEAD, branchRef),
	); err != nil {
		return errors.Wrapf(err, "git plugin: point HEAD at %s", branch)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return errors.Wrap(err)
	}
	if err := wt.Reset(&git.ResetOptions{Commit: tip, Mode: git.HardReset}); err != nil {
		return errors.Wrapf(err, "git plugin: check out %s", tipStr)
	}
	return nil
}

// restoreRemotePush pushes the preserved branch to a remote: load the
// captured objects into an in-memory store, point the branch at the tip,
// and push that ref to the destination URL (auth selected by scheme).
// Inflates a REMOTE branch the same way restoreLocalClone inflates a local
// one — over any go-git transport.
func restoreRemotePush(
	req cutting_garden_plugins.ProtocolRestoreRequest,
	refs []capture_plugin.Ref,
	tipStr, branch string,
) error {
	st := memory.NewStorage()
	if err := writeObjectsToStorer(st, req.BlobStore, refs); err != nil {
		return err
	}

	branchRef := plumbing.NewBranchReferenceName(branch)
	if err := st.SetReference(
		plumbing.NewHashReference(branchRef, plumbing.NewHash(tipStr)),
	); err != nil {
		return errors.Wrapf(err, "git plugin: set branch %s to %s", branch, tipStr)
	}

	auth, err := authMethod(req.RawDest)
	if err != nil {
		return err
	}
	rem := git.NewRemote(st, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{req.RawDest},
	})
	refspec := config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))
	if perr := rem.PushContext(req.Context, &git.PushOptions{
		RefSpecs: []config.RefSpec{refspec},
		Auth:     auth,
	}); perr != nil && perr != git.NoErrAlreadyUpToDate {
		return errors.Wrapf(perr, "git plugin: push %s to %s", branch, req.RawDest)
	}
	return nil
}

// assertDestAbsent refuses a local restore destination that already exists,
// mirroring the filesystem plugin's destination precondition. (Remote push
// destinations are existing repositories, so this applies only to the
// local-clone path.)
func assertDestAbsent(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return errors.ErrorWithStackf(
			"%s: destination already exists\n"+
				"hint: choose a destination that does not exist", dest,
		)
	} else if !os.IsNotExist(err) {
		return errors.Wrapf(err, "stat %q", dest)
	}
	return nil
}
