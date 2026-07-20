package cutting_garden_plugin_googlephotos

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_failures"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// captureDefaultArgs returns the gallery-dl args for a full media +
// metadata-sidecar download into outDir for source. `--directory` pins
// the exact (flat) output location so the artifact walk does not depend
// on gallery-dl's per-extractor directory templates; `--write-metadata`
// emits a `<file>.json` sidecar beside each downloaded item. `--`
// terminates option parsing before the URL. Kept as a function so the
// diff path can reuse the identical invocation.
func captureDefaultArgs(outDir, source string) []string {
	return []string{
		"--quiet",
		"--write-metadata",
		"--directory", outDir,
		"--",
		source,
	}
}

// CaptureRoot resolves the source URL, runs gallery-dl into a tempdir,
// streams every produced artifact into req.BlobStore as a separate file
// entry, and emits stream events per artifact. Non-zero gallery-dl exit
// collapses into a single root-level failure on rawArg.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	source, err := sourceURLFromArg(req.Source)
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootLevelFailure(req.RawArg, err)
	}

	tempDir, err := os.MkdirTemp("", "cg-gphotos-capture-*")
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootLevelFailure(req.RawArg, err)
	}
	defer func() {
		// Tempdir cleanup is best-effort: the capture has already
		// streamed every artifact into the blob store, so a leftover
		// directory only costs the user disk space. Surface it as a
		// Log line so it's visible without inflating FailCount.
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			r.Log("google-photos plugin: tempdir cleanup failed: %v", rmErr)
		}
	}()

	if err := runGalleryDL(req.Context, tempDir, captureDefaultArgs(tempDir, source)); err != nil {
		r.Failure(req.RawArg, err)
		return rootLevelFailure(req.RawArg, err)
	}

	entries, failures := walkArtifacts(req.Context, req.BlobStore, tempDir, source, r)
	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: len(failures),
		Failures:  failures,
	}
}

// rootLevelFailure shapes a whole-arg plugin failure (source parsing,
// tempdir setup, gallery-dl non-zero exit) as a one-element
// CaptureRootResult. The failure has no per-entry identity below the
// root, so Path mirrors Root per the CaptureRootResult contract.
func rootLevelFailure(root string, err error) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{
		FailCount: 1,
		Failures: []capture_failures.FailureV1{{
			Root:  root,
			Path:  root,
			Op:    capture_failures.OpPlugin,
			Error: err.Error(),
		}},
	}
}

// walkArtifacts streams every regular file under outDir into store,
// returns one EntryV1 per file plus one FailureV1 per failed entry (the
// caller derives FailCount from len), and emits Entry/Failure on the
// reporter Stream. gallery-dl may nest artifacts under album
// subdirectories, so the walk recurses and records each file's
// slash-relative path so nothing is silently dropped.
func walkArtifacts(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	outDir string,
	source string,
	reporter cutting_garden_plugins.Reporter,
) ([]capture_receipt.EntryV1, []capture_failures.FailureV1) {
	// Nil-safe even when called directly (e.g. in tests): a nil reporter
	// becomes a no-op.
	reporter = cutting_garden_plugins.ReporterOrNop(reporter)

	var (
		entries  []capture_receipt.EntryV1
		failures []capture_failures.FailureV1
	)

	// recordFailure pairs every reporter.Failure with a durable
	// FailureV1 for the orchestrator's failure receipt.
	recordFailure := func(path, op string, err error) {
		reporter.Failure(path, errors.Wrap(err))
		failures = append(failures, capture_failures.FailureV1{
			Root:  source,
			Path:  path,
			Op:    op,
			Error: err.Error(),
		})
	}

	walkErr := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			recordFailure(p, capture_failures.OpWalk, walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			recordFailure(p, capture_failures.OpStat, err)
			return nil
		}
		if !info.Mode().IsRegular() {
			// Only regular files become blob entries; gallery-dl does
			// not emit symlinks, but be explicit.
			return nil
		}

		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			recordFailure(p, capture_failures.OpWalk, err)
			return nil
		}
		rel = filepath.ToSlash(rel)

		id, size, err := plugin_blob_io.WriteFileBlob(ctx, store, p)
		if err != nil {
			recordFailure(p, capture_failures.OpBlobWrite, err)
			return nil
		}

		entry := capture_receipt.EntryV1{
			Path:   rel,
			Root:   source,
			Type:   capture_receipt.TypeFile,
			Mode:   info.Mode().Perm(),
			Size:   size,
			BlobId: id.String(),
		}
		entries = append(entries, entry)
		reporter.Entry(entry)
		return nil
	})

	if walkErr != nil {
		recordFailure(outDir, capture_failures.OpWalk, walkErr)
	}

	return entries, failures
}
