package cutting_garden_plugin_git

import (
	"context"
	"os"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.ProtocolRestorePlugin = (*Plugin)(nil)

// ProtocolKind reports the receipt kind this plugin restores and diffs.
func (Plugin) ProtocolKind() string { return captureKind }

// RestoreProtocol rebuilds a working git clone from a git receipt merkle
// tree, checked out to the preserved branch. It reads every object leaf
// referenced by the payload node back into a fresh repository's object
// database (verifying each recreated oid matches the captured one), then
// creates the branch at the recorded tip and checks out the working
// tree.
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

	if err := runGit(req.Context, "", "init", "-q", "-b", branch, "--", dest); err != nil {
		return err
	}

	if err := writeObjects(req.Context, req.BlobStore, dest, payload.Refs); err != nil {
		return err
	}

	// Create the preserved branch at the recorded tip, then check out
	// the working tree from it.
	if err := runGit(req.Context, dest, "update-ref", "refs/heads/"+branch, meta.Tip); err != nil {
		return err
	}
	if err := runGit(req.Context, dest, "reset", "--hard"); err != nil {
		return err
	}

	return nil
}

// writeObjects reads each git object leaf from store and writes it back
// into the repository at dest via `git hash-object -w`, verifying the
// recreated oid equals the captured oid (the reference alias).
func writeObjects(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	dest string,
	refs []capture_plugin.Ref,
) error {
	for _, ref := range refs {
		gitType := gitTypeFromObjectType(ref.TypeString)
		if gitType == "" {
			return errors.ErrorWithStackf(
				"git plugin: payload reference %q has non-object type %q",
				ref.Alias, ref.TypeString)
		}

		if err := writeOneObject(ctx, store, dest, ref, gitType); err != nil {
			return err
		}
	}
	return nil
}

func writeOneObject(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	dest string,
	ref capture_plugin.Ref,
	gitType string,
) (err error) {
	var id markl.Id
	if err = id.Set(ref.Digest); err != nil {
		return errors.Wrapf(err, "parse object blob id %q", ref.Digest)
	}
	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return errors.Wrapf(err, "open object blob %s", ref.Digest)
	}
	defer errors.DeferredCloser(&err, reader)

	out, err := gitInput(ctx, dest, reader,
		"hash-object", "-t", gitType, "-w", "--stdin")
	if err != nil {
		return err
	}

	got := strings.TrimSpace(out)
	if got != ref.Alias {
		return errors.ErrorWithStackf(
			"git plugin: object integrity mismatch: captured oid %q, "+
				"recreated oid %q (type %s)", ref.Alias, got, gitType)
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
