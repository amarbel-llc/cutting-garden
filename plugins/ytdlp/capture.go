package cutting_garden_plugin_ytdlp

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_failures"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io"
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
// file entry, and emits Stream events per artifact. Non-zero yt-dlp
// exit collapses into a single stream Failure on rawArg.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	source, err := sourceURLFromArg(req.Source)
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootLevelFailure(req.RawArg, err)
	}

	tempDir, err := os.MkdirTemp("", "cg-ytdlp-capture-*")
	if err != nil {
		r.Failure(req.RawArg, errors.Wrap(err))
		return rootLevelFailure(req.RawArg, err)
	}
	defer func() {
		// Tempdir cleanup is best-effort: the capture has already
		// streamed every artifact into the blob store, so a leftover
		// directory only costs the user disk space. Surface it as a
		// Log line so it's visible without inflating FailCount.
		// Formerly sink.Notice; on the Stage B legacy bridge Log is a
		// no-op, so this message no longer reaches legacy piped output —
		// accepted, because it only fires when post-capture cleanup
		// fails (rare, failure-only) and forwarding Log→Notice would
		// break byte-identity on every run (see capture_render_legacy).
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			r.Log("ytdlp plugin: tempdir cleanup failed: %v", rmErr)
		}
	}()

	r.PhaseStart("download " + source)
	r.Log("running yt-dlp for %s", source)
	// The download total is unknown up front (yt-dlp streams), so no Plan
	// is emitted for this phase — bytes-based Progress yields an
	// indeterminate display, which is the correct UX for a stream.
	onProgress := func(s progressSample) {
		r.Progress(cutting_garden_plugins.ReportProgress{
			Item:       s.ID,
			Bytes:      s.Downloaded,
			BytesTotal: s.Total,
		})
	}
	onLog := func(line string) { r.Log("%s", line) }

	if err := runYtdlp(req.Context, tempDir, captureDefaultArgs(tempDir, source), onProgress, onLog); err != nil {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		r.Failure(req.RawArg, err)
		return rootLevelFailure(source, err)
	}
	r.PhaseEnd(capture_events.Verdict{OK: true})

	entries, failures := walkArtifacts(req.Context, req.BlobStore, tempDir, source, r)
	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: len(failures),
		Failures:  failures,
	}
}

// rootLevelFailure shapes a whole-arg plugin failure (source
// resolution, tempdir setup, yt-dlp non-zero exit) as a one-element
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

// artifactFile is one regular file collected by walkArtifacts' first
// pass: its absolute path, slash-separated rel-path under outDir, and
// the stat size/perm used for byte-progress accounting and the entry.
type artifactFile struct {
	path string
	rel  string
	size int64
	mode fs.FileMode
}

// walkArtifacts streams every regular file under outDir into store,
// returns one EntryV1 per file plus one FailureV1 per failed entry
// (the caller derives FailCount from len), and emits Entry/Failure on
// the reporter Stream. Subdirectories below outDir would be unexpected
// from yt-dlp's default template; if they appear they are walked
// recursively so nothing is silently dropped.
//
// Two passes: pass 1 collects the files and pre-sums their sizes (the
// files are local, so the total is free), pass 2 streams each into the
// store reporting cumulative bytes against that total — a multi-minute
// upload of one large file moves the byte bar continuously instead of
// jumping once per artifact. yt-dlp has already exited by the time we
// run, so the tempdir is quiescent and pass-1 sizes stay valid for
// pass 2.
func walkArtifacts(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	outDir string,
	source string,
	reporter cutting_garden_plugins.Reporter,
) ([]capture_receipt.EntryV1, []capture_failures.FailureV1) {
	// Nil-safe even when called directly (e.g. in tests): a nil reporter
	// becomes a no-op, so Progress emission never gates on a live consumer.
	reporter = cutting_garden_plugins.ReporterOrNop(reporter)

	var (
		entries   []capture_receipt.EntryV1
		failures  []capture_failures.FailureV1
		files     []artifactFile
		phaseDone int64
	)

	// recordFailure pairs every reporter.Failure with a durable
	// FailureV1 for the orchestrator's failure receipt; the caller
	// derives FailCount from len(failures), so the two stay 1:1.
	recordFailure := func(path, op string, err error) {
		failures = append(failures, capture_failures.FailureV1{
			Root:  source,
			Path:  path,
			Op:    op,
			Error: err.Error(),
		})
	}

	// Pass 1: collect regular files and pre-sum sizes.
	var phaseTotal int64
	walkErr := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			reporter.Failure(p, errors.Wrap(walkErr))
			recordFailure(p, capture_failures.OpWalk, walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			reporter.Failure(p, errors.Wrap(err))
			recordFailure(p, capture_failures.OpStat, err)
			return nil
		}
		if !info.Mode().IsRegular() {
			// yt-dlp never produces symlinks under -o, but be explicit:
			// only regular files become blob entries.
			return nil
		}

		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			reporter.Failure(p, errors.Wrap(err))
			recordFailure(p, capture_failures.OpStat, err)
			return nil
		}
		rel = filepath.ToSlash(rel)

		files = append(files, artifactFile{
			path: p,
			rel:  rel,
			size: info.Size(),
			mode: info.Mode().Perm(),
		})
		phaseTotal += info.Size()
		return nil
	})

	if walkErr != nil {
		reporter.Failure(outDir, errors.Wrap(walkErr))
		recordFailure(outDir, capture_failures.OpWalk, walkErr)
	}

	reporter.PhaseStart(fmt.Sprintf("write %d artifacts (%.1f MiB)",
		len(files), float64(phaseTotal)/(1<<20)))
	// The write-phase verdict counts only failures from the loop below;
	// pass-1 walk/stat failures predate the phase (they're already in
	// failures and the event stream).
	failCountAtPhaseStart := len(failures)

	// Pass 2: stream each file into the store. The byte-progress
	// callback reports phaseDone (bytes of fully-written prior
	// artifacts) plus the current file's cumulative copied bytes,
	// against phaseTotal — no Plan is emitted (the Plan contract is
	// "<=1x, before any Progress" and the download phase may already
	// have ticked); BytesTotal rides per-tick and the viewport Model
	// re-arms from it. Stream events are observability only — they never
	// influence entries, blob bytes, or receipts.
	for i, f := range files {
		onBytes := func(fileBytes int64) {
			reporter.Progress(cutting_garden_plugins.ReportProgress{
				Item:       f.rel,
				Items:      int64(i + 1),
				Bytes:      phaseDone + fileBytes,
				BytesTotal: phaseTotal,
			})
		}

		id, size, err := plugin_blob_io.WriteFileBlobProgress(ctx, store, f.path, onBytes)
		if err != nil {
			reporter.Failure(f.path, errors.Wrap(err))
			recordFailure(f.path, capture_failures.OpBlobWrite, err)
			// A failed file still advances phaseDone by its full size:
			// its bytes are in phaseTotal, so skipping them would leave
			// the bar permanently short of 100%. The failure itself is
			// surfaced via Failure/failCount, not the bar.
			phaseDone += f.size
			continue
		}

		entry := capture_receipt.EntryV1{
			Path:   f.rel,
			Root:   source,
			Type:   capture_receipt.TypeFile,
			Mode:   f.mode,
			Size:   size,
			BlobId: id.String(),
		}
		entries = append(entries, entry)
		reporter.Entry(entry)
		phaseDone += f.size
	}

	if phaseFailed := len(failures) - failCountAtPhaseStart; phaseFailed == 0 {
		reporter.PhaseEnd(capture_events.Verdict{OK: true})
	} else {
		reporter.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"entries": len(files), "failed": phaseFailed},
		})
	}

	return entries, failures
}
