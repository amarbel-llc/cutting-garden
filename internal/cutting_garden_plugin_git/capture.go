package cutting_garden_plugin_git

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// objectEntryMode is the placeholder permission stamped on every git
// object entry. Git objects are content, not files with meaningful
// filesystem permissions; the value only needs to round-trip stably
// through the receipt.
const objectEntryMode = 0o644

// CaptureRoot mirrors one branch of a git remote into the destination
// blob store as a content-addressed merkle tree: every git object
// reachable from the branch tip — each commit, tree, and blob — is
// stored individually as its own blob, preserving git's native object
// graph (and its dedup: an unchanged object keeps its oid and stores
// once). A `ref.txt` entry records the tip commit oid (the merkle root
// pointer). Any failure before the per-object walk collapses into a
// single sink.Failure on rawArg.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	remote, branch, err := remoteAndBranchFromArg(req.Source)
	if err != nil {
		req.Sink.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}
	source := canonicalSource(remote, branch)

	entries, failures, err := extractBranch(req.Context, req.BlobStore, remote, branch, source)
	if err != nil {
		req.Sink.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}

	for _, f := range failures {
		req.Sink.Failure(f.path, f.err)
	}
	for i := range entries {
		req.Sink.Entry(entries[i])
	}
	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: len(failures),
	}
}

// artifactFailure pairs an object label with the error that kept it out
// of the blob store. Capture routes these to the sink; diff aggregates
// them into a single error.
type artifactFailure struct {
	path string
	err  error
}

// withBareClone clones the single branch bare into a scratch dir,
// resolves the branch name (from HEAD when branch is empty) and its tip
// commit oid, then invokes fn with the clone dir, the resolved branch,
// and the tip. The scratch dir is removed afterward. Shared by the
// EntryV1 extraction (extractBranch) and the RFC 0002 protocol capture.
func withBareClone(
	ctx context.Context,
	remote, branch string,
	fn func(cloneDir, resolvedBranch, tip string) error,
) (err error) {
	cloneDir, err := os.MkdirTemp("", "cg-git-clone-*")
	if err != nil {
		return errors.Wrap(err)
	}
	defer errors.Deferred(&err, func() error { return os.RemoveAll(cloneDir) })

	cloneArgs := []string{"clone", "--bare", "--single-branch", "--no-tags"}
	if branch != "" {
		cloneArgs = append(cloneArgs, "--branch", branch)
	}
	cloneArgs = append(cloneArgs, "--", remote, cloneDir)
	if err = runGit(ctx, "", cloneArgs...); err != nil {
		return err
	}

	resolvedBranch := branch
	if resolvedBranch == "" {
		out, serr := gitOutput(ctx, cloneDir, "symbolic-ref", "--short", "HEAD")
		if serr != nil {
			return serr
		}
		resolvedBranch = strings.TrimSpace(out)
		if resolvedBranch == "" {
			return errors.ErrorWithStackf(
				"git plugin: could not resolve default branch on %q", remote)
		}
	}

	tipOut, rerr := gitOutput(ctx, cloneDir, "rev-parse", "refs/heads/"+resolvedBranch)
	if rerr != nil {
		return rerr
	}
	tip := strings.TrimSpace(tipOut)
	if tip == "" {
		return errors.ErrorWithStackf(
			"git plugin: empty tip for branch %q on %q", resolvedBranch, remote)
	}

	return fn(cloneDir, resolvedBranch, tip)
}

// extractBranch clones the single branch bare and stores every git
// object (plus the ref.txt tip pointer) into store, returning one
// EntryV1 per stored object. Per-object write failures accumulate in
// failures (the walk continues); err is reserved for hard failures
// (clone refused, branch unresolvable, cat-file crash) that abort the
// whole capture. Shared by CaptureRoot and the diff rescan path.
func extractBranch(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	remote, branch, source string,
) (entries []capture_receipt.EntryV1, failures []artifactFailure, err error) {
	err = withBareClone(ctx, remote, branch, func(cloneDir, _, tip string) error {
		// The tip pointer doubles as the merkle root reference and the
		// diff freshness key.
		refID, refSize, rwErr := plugin_blob_io.WriteReaderBlob(ctx, store, strings.NewReader(tip+"\n"))
		if rwErr != nil {
			return errors.Wrap(rwErr)
		}
		entries = append(entries, capture_receipt.EntryV1{
			Path:   refFileName,
			Root:   source,
			Type:   capture_receipt.TypeFile,
			Mode:   objectEntryMode,
			Size:   refSize,
			BlobId: refID.String(),
		})

		visit := func(oid, typ string, _ int64, payload io.Reader) error {
			id, size, werr := plugin_blob_io.WriteReaderBlob(ctx, store, payload)
			if werr != nil {
				failures = append(failures, artifactFailure{
					path: objectPath(typ, oid),
					err:  errors.Wrap(werr),
				})
				return nil
			}
			entries = append(entries, capture_receipt.EntryV1{
				Path:   objectPath(typ, oid),
				Root:   source,
				Type:   capture_receipt.TypeFile,
				Mode:   objectEntryMode,
				Size:   size,
				BlobId: id.String(),
			})
			return nil
		}

		return streamAllObjects(ctx, cloneDir, visit)
	})
	if err != nil {
		return nil, nil, err
	}
	return entries, failures, nil
}

// objectPath is the EntryV1.Path for a git object: its type as a
// directory segment plus its oid. Encoding the type in the path keeps
// the receipt self-describing (a consumer reconstituting the repo knows
// each blob's git type without a side table) and groups objects by kind
// — e.g. `blob/<oid>`, `tree/<oid>`, `commit/<oid>`, `tag/<oid>`.
func objectPath(typ, oid string) string {
	return typ + "/" + oid
}
