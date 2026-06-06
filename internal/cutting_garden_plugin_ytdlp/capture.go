package cutting_garden_plugin_ytdlp

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// outputTemplate is the yt-dlp -o template used for every invocation.
// `%(id)s.%(ext)s` keeps filenames short and predictable; titles vary
// across re-uploads and can contain shell metacharacters, while the
// video id is stable. Sidecars (info.json, thumbnail, subs) inherit
// the same stem.
const outputTemplate = "%(id)s.%(ext)s"

// captureDefaultArgs returns the yt-dlp args for a full media+sidecar
// capture into outDir for source. Kept as a function so the diff path
// (skip-download mode) can share the -o handling.
func captureDefaultArgs(outDir, source string) []string {
	return []string{
		// Structured progress: --newline keeps each progress update on
		// its own line (no carriage-return overwrites), --progress-delta
		// throttles updates to every 0.5s, and --progress-template emits
		// our tab-delimited sentinel line (see exec.go) that runYtdlp
		// parses into progressSamples.
		"--newline",
		"--progress-delta", "0.5",
		"--progress-template", progressTemplate,
		"--no-warnings",
		"--write-info-json",
		"--write-thumbnail",
		"--write-subs",
		"-o", filepath.Join(outDir, outputTemplate),
		"--",
		source,
	}
}

// CaptureRoot resolves the source URL, runs yt-dlp into a tempdir,
// streams every produced artifact into req.BlobStore as a separate
// file entry, and emits sink events per artifact. Non-zero yt-dlp
// exit collapses into a single sink.Failure on rawArg.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	source, err := sourceURLFromArg(req.Source)
	if err != nil {
		req.Sink.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}

	tempDir, err := os.MkdirTemp("", "cg-ytdlp-capture-*")
	if err != nil {
		req.Sink.Failure(req.RawArg, errors.Wrap(err))
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}
	defer func() {
		// Tempdir cleanup is best-effort: the capture has already
		// streamed every artifact into the blob store, so a leftover
		// directory only costs the user disk space. Surface it as a
		// notice so it's visible without inflating FailCount.
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			req.Sink.Notice("ytdlp plugin: tempdir cleanup failed: %v", rmErr)
		}
	}()

	r.Log("running yt-dlp for %s", source)
	// The download total is unknown up front (yt-dlp streams), so no Plan
	// is emitted for this phase — bytes-based Progress yields an
	// indeterminate display, which is the correct UX for a stream.
	onProgress := func(s progressSample) {
		r.Progress(cutting_garden_plugins.ReportProgress{
			Item:  s.ID,
			Bytes: s.Downloaded,
		})
	}
	onLog := func(line string) { r.Log("%s", line) }

	if err := runYtdlp(req.Context, tempDir, captureDefaultArgs(tempDir, source), onProgress, onLog); err != nil {
		req.Sink.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}

	r.Log("downloaded, writing artifacts")
	entries, failCount := walkArtifacts(req.Context, req.BlobStore, tempDir, source, req.Sink, r)
	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: failCount,
	}
}

// walkArtifacts streams every regular file under outDir into store
// and returns one EntryV1 per file. Subdirectories below outDir would
// be unexpected from yt-dlp's default template; if they appear they
// are walked recursively so nothing is silently dropped.
func walkArtifacts(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	outDir string,
	source string,
	sink capture_sink.Sink,
	reporter cutting_garden_plugins.Reporter,
) ([]capture_receipt.EntryV1, int) {
	// Nil-safe even when called directly (e.g. in tests): a nil reporter
	// becomes a no-op, so Progress emission never gates on a live consumer.
	reporter = cutting_garden_plugins.ReporterOrNop(reporter)

	var (
		entries   []capture_receipt.EntryV1
		failCount int
	)

	walkErr := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			sink.Failure(p, errors.Wrap(walkErr))
			failCount++
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			sink.Failure(p, errors.Wrap(err))
			failCount++
			return nil
		}
		if !info.Mode().IsRegular() {
			// yt-dlp never produces symlinks under -o, but be explicit:
			// only regular files become blob entries.
			return nil
		}

		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			sink.Failure(p, errors.Wrap(err))
			failCount++
			return nil
		}
		rel = filepath.ToSlash(rel)

		id, size, err := plugin_blob_io.WriteFileBlob(ctx, store, p)
		if err != nil {
			sink.Failure(p, errors.Wrap(err))
			failCount++
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
		sink.Entry(entry)
		// Items is the count of artifacts written so far (monotonic
		// non-decreasing); Item is the artifact's rel-path. The Reporter
		// is observability only — it never influences entries, blob bytes,
		// or the sink stream above.
		reporter.Progress(cutting_garden_plugins.ReportProgress{
			Item:  rel,
			Items: int64(len(entries)),
		})
		return nil
	})

	if walkErr != nil {
		sink.Failure(outDir, errors.Wrap(walkErr))
		failCount++
	}

	return entries, failCount
}
