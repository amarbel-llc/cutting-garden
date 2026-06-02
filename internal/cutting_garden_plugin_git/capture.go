package cutting_garden_plugin_git

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// CaptureRoot resolves the remote+branch, clones the single branch into
// a scratch dir, bundles it plus a freshness sidecar into a staging dir,
// streams both artifacts into req.BlobStore as separate file entries,
// and emits sink events per artifact. Any failure before the walk
// collapses into a single sink.Failure on rawArg.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	remote, branch, err := remoteAndBranchFromArg(req.Source)
	if err != nil {
		req.Sink.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}
	source := canonicalSource(remote, branch)

	outDir, err := os.MkdirTemp("", "cg-git-capture-*")
	if err != nil {
		req.Sink.Failure(req.RawArg, errors.Wrap(err))
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}
	defer func() {
		// Staging cleanup is best-effort: every artifact is already in
		// the blob store, so a leftover dir only costs disk. Surface it
		// as a notice without inflating FailCount.
		if rmErr := os.RemoveAll(outDir); rmErr != nil {
			req.Sink.Notice("git plugin: staging cleanup failed: %v", rmErr)
		}
	}()

	if err := materializeBranch(req.Context, remote, branch, outDir); err != nil {
		req.Sink.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}

	entries, failures := walkArtifacts(req.Context, req.BlobStore, outDir, source)
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

// materializeBranch resolves the branch tip, clones the single branch
// bare into a scratch dir, writes the freshness sidecar (refFileName)
// and the git bundle (bundleFileName) into outDir, and removes the
// scratch clone. Shared by capture and the diff rescan path.
func materializeBranch(ctx context.Context, remote, branch, outDir string) (err error) {
	resolvedBranch, commit, err := resolveTip(ctx, remote, branch)
	if err != nil {
		return err
	}

	if err = os.WriteFile(
		filepath.Join(outDir, refFileName),
		[]byte(commit+"\n"),
		0o644,
	); err != nil {
		return errors.Wrap(err)
	}

	cloneDir, err := os.MkdirTemp("", "cg-git-clone-*")
	if err != nil {
		return errors.Wrap(err)
	}
	defer errors.Deferred(&err, func() error { return os.RemoveAll(cloneDir) })

	if err = runGit(ctx, "",
		"clone", "--bare", "--single-branch", "--branch", resolvedBranch,
		"--", remote, cloneDir,
	); err != nil {
		return err
	}

	if err = runGit(ctx, cloneDir,
		"bundle", "create",
		filepath.Join(outDir, bundleFileName),
		"refs/heads/"+resolvedBranch,
	); err != nil {
		return err
	}

	return nil
}

// artifactFailure pairs a path with the error that kept it out of the
// blob store. Capture routes these to the sink; diff aggregates them
// into a single error.
type artifactFailure struct {
	path string
	err  error
}

// walkArtifacts streams every regular file under outDir into store and
// returns one EntryV1 per file plus any per-file failures. outDir holds
// the flat ref.txt + repo.bundle pair, but the walk is recursive so a
// future layout change can't silently drop files.
func walkArtifacts(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	outDir string,
	source string,
) (entries []capture_receipt.EntryV1, failures []artifactFailure) {
	walkErr := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			failures = append(failures, artifactFailure{p, errors.Wrap(walkErr)})
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			failures = append(failures, artifactFailure{p, errors.Wrap(err)})
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			failures = append(failures, artifactFailure{p, errors.Wrap(err)})
			return nil
		}
		rel = filepath.ToSlash(rel)

		id, size, err := plugin_blob_io.WriteFileBlob(ctx, store, p)
		if err != nil {
			failures = append(failures, artifactFailure{p, errors.Wrap(err)})
			return nil
		}

		entries = append(entries, capture_receipt.EntryV1{
			Path:   rel,
			Root:   source,
			Type:   capture_receipt.TypeFile,
			Mode:   info.Mode().Perm(),
			Size:   size,
			BlobId: id.String(),
		})
		return nil
	})

	if walkErr != nil {
		failures = append(failures, artifactFailure{outDir, errors.Wrap(walkErr)})
	}

	return entries, failures
}
