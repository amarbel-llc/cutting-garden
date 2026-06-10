// Package cutting_garden_plugin_optical is the optical-media capture
// backend for cutting-garden. Registered for the `optical` URI scheme
// (`optical:/dev/sr0`, optionally `?mode=image|audio`).
//
// Two ripping modes share one plugin:
//
//   - image (default) — GNU ddrescue images the whole disc into a
//     `disc.iso` plus its `disc.iso.map` rescue map, recovering from
//     read errors on scratched media. Works for any data disc
//     (CD-ROM, DVD, Blu-ray).
//   - audio — cdparanoia rips each audio-CD track to a separate
//     `trackNN.cdda.wav`, using its jitter/error correction. The rip
//     is preceded by a metadata phase (audio_meta.go) that captures
//     the disc TOC, its CDDB disc id, a best-effort CDDB lookup, and
//     per-track ID3v2.4 tag blobs as sidecar entries: `disc.toc.json`,
//     `disc.cddb` (raw server response, when matched), `trackNN.id3`.
//
// Restore is intentionally not implemented; the produced artifacts are
// regular files the filesystem plugin materializes. See
// docs/features/0016-optical-plugin.md for the rationale and the
// TypeTag-reuse decision.
package cutting_garden_plugin_optical

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_failures"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Plugin is the optical-media capture backend.
type Plugin struct{}

var _ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)

// Schemes returns the single URI scheme this plugin claims.
func (Plugin) Schemes() []string { return []string{opticalScheme} }

// TypeTag reuses capture_receipt.TypeTagV1 because optical artifacts
// are captured as regular file entries — byte-identical EntryV1 shape
// to fs captures. A receipt mixing fs and optical roots carries one
// type-tag and restores cleanly through the file plugin. See the
// ytdlp plugin for the same decision.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource parses the optical URL (device path + mode) without
// touching the drive: a missing or unreadable device surfaces later as
// the ripping tool's own error, with its stderr tail attached. raw is
// preserved for diagnostics.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := parseSource(u)
	return err
}

// CaptureRoot parses the source, runs the mode's ripping tool
// (ddrescue or cdparanoia) into a tempdir, streams every produced
// artifact into req.BlobStore as a separate file entry, and emits
// Stream events. A non-zero tool exit collapses into a single root
// Failure on rawArg.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	src, err := parseSource(req.Source)
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootLevelFailure(req.RawArg, err)
	}

	tempDir, err := os.MkdirTemp("", "cg-optical-capture-*")
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootLevelFailure(req.RawArg, err)
	}
	defer func() {
		// Tempdir cleanup is best-effort: every artifact has already been
		// streamed into the blob store, so a leftover directory only
		// costs disk space. Surface it as a Log line (failure-only, rare)
		// rather than inflating FailCount. Mirrors the ytdlp plugin.
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			r.Log("optical plugin: tempdir cleanup failed: %v", rmErr)
		}
	}()

	// Audio mode prepends a metadata phase: read the TOC, compute the
	// CDDB disc id, best-effort CDDB lookup, and write the
	// disc.toc.json / disc.cddb / trackNN.id3 sidecars into the tempdir
	// so the post-rip walk streams them as ordinary entries.
	if src.mode == modeAudio {
		if err := writeAudioMetadata(req.Context, tempDir, src.device, r); err != nil {
			r.Failure(req.RawArg, err)
			return rootLevelFailure(src.device, err)
		}
	}

	bin, args := toolInvocation(src)

	r.PhaseStart(fmt.Sprintf("rip %s (%s) with %s", src.device, src.mode, bin))
	r.Log("running %s for %s", bin, src.device)
	// Disc size is unknown up front (the rip streams sector-by-sector),
	// so no Plan is emitted for this phase — an indeterminate display is
	// the correct UX for a stream. Per-byte progress arrives in the
	// write phase below, where artifact sizes are known.
	onLog := func(line string) { r.Log("%s", line) }

	if err := runExternal(req.Context, tempDir, bin, args, onLog); err != nil {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		r.Failure(req.RawArg, err)
		return rootLevelFailure(src.device, err)
	}
	r.PhaseEnd(capture_events.Verdict{OK: true})

	entries, failures := walkArtifacts(req.Context, req.BlobStore, tempDir, src.device, r)
	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: len(failures),
		Failures:  failures,
	}
}

// rootLevelFailure shapes a whole-arg plugin failure (source parsing,
// tempdir setup, ripping-tool non-zero exit) as a one-element
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
// returns one EntryV1 per file plus one FailureV1 per failed entry (the
// caller derives FailCount from len), and emits Entry/Failure on the
// reporter Stream. Mirrors the ytdlp plugin's two-pass walk: pass 1
// collects files and pre-sums their sizes (local files, so the total is
// free), pass 2 streams each into the store reporting cumulative bytes
// against that total — a multi-GiB .iso moves the byte bar continuously
// instead of jumping once at completion. The ripping tool has already
// exited, so the tempdir is quiescent and pass-1 sizes stay valid.
func walkArtifacts(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	outDir string,
	root string,
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
			Root:  root,
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
			// The ripping tools only write regular files; be explicit
			// that only regular files become blob entries.
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
	// pass-1 walk/stat failures predate the phase.
	failCountAtPhaseStart := len(failures)

	// Pass 2: stream each file into the store, reporting cumulative bytes
	// against phaseTotal.
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
			// A failed file still advances phaseDone by its full size so
			// the bar reaches 100%; the failure surfaces via Failure/
			// failCount, not the bar.
			phaseDone += f.size
			continue
		}

		entry := capture_receipt.EntryV1{
			Path:   f.rel,
			Root:   root,
			Type:   capture_receipt.TypeFile,
			Mode:   f.mode,
			Size:   size,
			BlobId: id.String(),
		}
		entries = append(entries, entry)
		reporter.Entry(entry)
		// Advance by the streamed byte count, not the pass-1 stat size:
		// if the file changed size between passes, the bar must agree
		// with the Bytes values reported during the stream.
		phaseDone += size
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
