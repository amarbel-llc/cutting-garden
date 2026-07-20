// Package cutting_garden_plugin_file is the filesystem capture/restore
// backend for cutting-garden. Registered for both the schemeless ("")
// and "file" URI schemes; emits the wire-format type-tag
// `cutting_garden-capture_receipt-fs-v1`.
package cutting_garden_plugin_file

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_failures"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Plugin is the filesystem capture/restore backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.RestorePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin    = (*Plugin)(nil)
)

// Schemes returns "" (schemeless default) and "file" (explicit
// file:// URIs).
func (Plugin) Schemes() []string { return []string{"", "file"} }

// TypeTag returns the legacy wire-format tag emitted by capture and
// accepted by restore. Locked per madder#16.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource enforces RFC 0001 §Producer Rules §Root Scoping:
// capture roots MUST resolve under PWD. raw is preserved for the
// diagnostic; u is what the planner parsed.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	path, err := pathFromURL(u)
	if err != nil {
		return err
	}
	return checkRootScope(path, raw)
}

// CaptureRoot walks the source filesystem path with filepath.WalkDir,
// writes every regular file as a blob into req.BlobStore, appends a
// capture_receipt.EntryV1 per visited entry, and emits Entry/Failure
// events on the unified Stream, all wrapped in one "walk <arg>" phase
// so the entries attribute to a test point on the unified renderers
// (the legacy bridge drops phases; the wire there is unchanged).
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	stream := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	path, err := pathFromURL(req.Source)
	if err != nil {
		// Pre-phase by design: the planner's ValidateSource already ran
		// pathFromURL, so this branch is unreachable through the
		// orchestrator — not worth a phase of its own. The failure is
		// root-level (no walk happened), so Path mirrors Root.
		stream.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{
			FailCount: 1,
			Failures: []capture_failures.FailureV1{{
				Root:  req.RawArg,
				Path:  req.RawArg,
				Op:    capture_failures.OpPlugin,
				Error: err.Error(),
			}},
		}
	}

	// Phase label prefers the original CLI arg (what the user typed);
	// direct callers that pass no RawArg get the resolved path.
	label := req.RawArg
	if label == "" {
		label = path
	}
	stream.PhaseStart("walk " + label)

	// Root field in EntryV1 is the resolved filesystem path so that
	// schemeless `./foo` and `file:./foo` produce byte-identical
	// receipts. The original CLI arg lives in req.RawArg (used for
	// event labels via the caller) but is not embedded in the wire
	// format.
	var entries []capture_receipt.EntryV1
	failures := walkRoot(req.Context, req.BlobStore, path, &entries, stream)
	fails := len(failures)

	// Verdict mirrors the ytdlp write-phase semantics: not-OK carries
	// {entries, failed}. The OK verdict also carries {entries} — it is
	// free (len of the accumulated slice) and gives TAP consumers the
	// count without parsing subtests (the ndjson renderer merges exact
	// counts into every phase diagnostic regardless). Here "entries"
	// counts successfully captured entries, not attempts.
	if fails == 0 {
		stream.PhaseEnd(capture_events.Verdict{
			OK:         true,
			Diagnostic: map[string]any{"entries": len(entries)},
		})
	} else {
		stream.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"entries": len(entries), "failed": fails},
		})
	}

	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: fails,
		Failures:  failures,
	}
}

// ValidateDest enforces FDR 0001 §Destination Preconditions: <dest>
// MUST NOT exist at invocation time.
func (Plugin) ValidateDest(dest *url.URL, raw string) error {
	path, err := pathFromURL(dest)
	if err != nil {
		return err
	}
	return assertDestinationDoesNotExist(path)
}

// Restore validates path sanitization across every entry, then
// materializes per-type. Mirrors the previous in-command behavior
// exactly. See FDR 0001 (`docs/features/0001-restore.md`) and
// RFC 0001 §Consumer Rules.
func (Plugin) Restore(
	req cutting_garden_plugins.RestoreRequest,
) error {
	path, err := pathFromURL(req.Dest)
	if err != nil {
		return err
	}

	if err := ValidateEntries(req.Entries, path); err != nil {
		return err
	}

	return materializeEntries(req.Context, req.BlobStore, req.Entries, path)
}

// ValidateDiffDir enforces that the diff dir resolves to an existing
// directory on disk.
func (Plugin) ValidateDiffDir(dir *url.URL, raw string) error {
	path, err := pathFromURL(dir)
	if err != nil {
		return err
	}
	return assertDirectoryExists(path)
}

// ScanForDiff validates the receipt's entries against the diff dir
// boundary, then walks the dir with filepath.WalkDir and returns
// EntryV1 records with computed blob-ids. Per-entry failures
// aggregate into the returned error — diff is read-only and atomic.
func (Plugin) ScanForDiff(
	req cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	path, err := pathFromURL(req.Dir)
	if err != nil {
		return nil, err
	}
	if err := ValidateEntries(req.ReceiptEntries, path); err != nil {
		return nil, err
	}
	return walkForDiff(req.Context, req.BlobStore, path)
}

// walkRoot, writeFileBlob: previously inline in
// madder's `india/commands_cutting_garden/capture.go`. The walk is
// unchanged; only the home moved — except the per-entry Progress
// emission (cg#68), which diverges from the madder original.
//
// Progress is indeterminate by design: a single-pass WalkDir has no
// up-front total, and the Reporter contract says a plugin that cannot
// estimate never calls Plan. Each captured entry emits one sample
// whose Item names the directory being walked (the viewport tail
// dedupes consecutive identical lines, so this rolls through
// directories rather than flooding with files) and whose Items is the
// monotonic captured-entry count, ready as a bar numerator if a
// pre-walk estimate (cg#10) lands later.
func walkRoot(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	walkPath string,
	accum *[]capture_receipt.EntryV1,
	stream capture_events.Stream,
) (failures []capture_failures.FailureV1) {
	rootArg := walkPath

	// recordFailure pairs every stream.Failure with a durable
	// FailureV1 for the orchestrator's failure receipt; the caller
	// derives FailCount from len(failures), so the two stay 1:1.
	recordFailure := func(path, op string, err error) {
		failures = append(failures, capture_failures.FailureV1{
			Root:  rootArg,
			Path:  path,
			Op:    op,
			Error: err.Error(),
		})
	}

	walkErr := filepath.WalkDir(walkPath, func(p string, d fs.DirEntry, walkErr error) error {
		// Cancellation (SIGINT/SIGTERM cancel the errors.Context this
		// derives from) aborts the walk itself: returning the error
		// stops WalkDir, and the trailing handler records it as the
		// single failure. Without this check only the per-file blob
		// copy aborts (via CtxReader) and the walk would enumerate the
		// whole remaining tree into one Failure per file.
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			stream.Failure(p, walkErr)
			recordFailure(p, capture_failures.OpWalk, walkErr)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			stream.Failure(p, errors.Wrap(err))
			recordFailure(p, capture_failures.OpStat, err)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(walkPath, p)
		if err != nil {
			stream.Failure(p, errors.Wrap(err))
			recordFailure(p, capture_failures.OpStat, err)
			return nil
		}
		rel = filepath.ToSlash(rel)

		mode := info.Mode()
		entry := capture_receipt.EntryV1{
			Path: rel,
			Root: rootArg,
			Mode: mode,
		}

		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				stream.Failure(p, errors.Wrap(err))
				recordFailure(p, capture_failures.OpReadlink, err)
				return nil
			}
			entry.Type = capture_receipt.TypeSymlink
			entry.Target = target

		case mode.IsDir():
			entry.Type = capture_receipt.TypeDir

		case mode.IsRegular():
			id, size, err := plugin_blob_io.WriteFileBlob(ctx, store, p)
			if err != nil {
				stream.Failure(p, errors.Wrap(err))
				recordFailure(p, capture_failures.OpBlobWrite, err)
				return nil
			}
			entry.Type = capture_receipt.TypeFile
			entry.Size = size
			entry.BlobId = id.String()

		default:
			entry.Type = capture_receipt.TypeOther
		}

		*accum = append(*accum, entry)
		stream.Entry(entry)

		walkingDir := p
		if !mode.IsDir() {
			walkingDir = filepath.Dir(p)
		}
		stream.Progress(capture_events.ReportProgress{
			Item:  walkingDir,
			Items: int64(len(*accum)),
		})
		return nil
	})

	if walkErr != nil {
		stream.Failure(rootArg, errors.Wrap(walkErr))
		recordFailure(rootArg, capture_failures.OpWalk, walkErr)
	}

	return failures
}

// checkRootScope refuses dir args that resolve outside PWD per RFC
// 0003 §Producer Rules §Root Scoping. Mirrors git's "outside
// repository" diagnostic; PWD is the implicit work tree. raw is the
// original CLI argument, used in the diagnostic.
func checkRootScope(path, raw string) error {
	pwd, err := os.Getwd()
	if err != nil {
		return errors.Wrap(err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.Wrap(err)
	}

	rel, err := filepath.Rel(pwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		label := raw
		if label == "" {
			label = path
		}
		return errors.ErrorWithStackf(
			"%s: outside working directory at %s\nhint: cd to a parent directory containing %s, then re-run",
			label, pwd, label,
		)
	}

	return nil
}

func assertDestinationDoesNotExist(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return errors.ErrorWithStackf(
			"%s: destination already exists\n"+
				"hint: choose a destination that does not exist, or remove this one",
			dest,
		)
	} else if !os.IsNotExist(err) {
		return errors.Wrapf(err, "stat %q", dest)
	}
	return nil
}

// ValidateEntries runs the RFC 0001 §Consumer Rules §Path
// Sanitization checks across every entry. First failure is the
// refusal — atomic per FDR 0001. Exported because the `diff`
// command (also filesystem-anchored) reuses the same checks.
func ValidateEntries(entries []capture_receipt.EntryV1, dest string) error {
	cleanDest := filepath.Clean(dest)

	for i := range entries {
		e := entries[i]

		if e.Root == "" {
			return errors.ErrorWithStackf(
				"entry has empty root\n  path: %s",
				e.Path,
			)
		}

		if strings.ContainsRune(e.Root, 0) || strings.ContainsRune(e.Path, 0) {
			return errors.ErrorWithStackf(
				"entry contains NUL byte\n  root: %q\n  path: %q",
				e.Root, e.Path,
			)
		}

		materialized := filepath.Clean(filepath.Join(cleanDest, e.Root, e.Path))

		if !pathConfinedTo(materialized, cleanDest) {
			return errors.ErrorWithStackf(
				"entry escapes destination\n"+
					"  root: %s\n"+
					"  path: %s\n"+
					"  materialized: %s\n"+
					"  destination: %s",
				e.Root, e.Path, materialized, cleanDest,
			)
		}
	}

	return nil
}

func pathConfinedTo(materialized, dest string) bool {
	rel, err := filepath.Rel(dest, materialized)
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

func materializeEntries(
	ctx context.Context,
	blobStore blob_stores.BlobStoreInitialized,
	entries []capture_receipt.EntryV1,
	dest string,
) error {
	cleanDest := filepath.Clean(dest)

	if err := os.MkdirAll(cleanDest, 0o755); err != nil {
		return errors.Wrapf(err, "create destination %q", cleanDest)
	}

	for i := range entries {
		// Per-entry cancel check (SIGINT/SIGTERM): CtxReader only covers
		// file blob copies; dir and symlink entries are mkdir/symlink IO
		// that would otherwise run the receipt to completion after a
		// cancel.
		if err := ctx.Err(); err != nil {
			return errors.Wrap(err)
		}

		e := entries[i]
		materialized := filepath.Clean(filepath.Join(cleanDest, e.Root, e.Path))

		switch e.Type {
		case capture_receipt.TypeFile:
			if err := materializeFile(ctx, blobStore, e, materialized); err != nil {
				return err
			}

		case capture_receipt.TypeDir:
			if err := os.MkdirAll(materialized, e.Mode.Perm()); err != nil {
				return errors.Wrapf(err, "mkdir %q", materialized)
			}
			// MkdirAll umasks the requested mode and skips it
			// entirely for dirs that already exist (e.g. cleanDest
			// itself, pre-created above). Chmod unconditionally so
			// the captured perms survive both paths.
			if err := os.Chmod(materialized, e.Mode.Perm()); err != nil {
				return errors.Wrapf(err, "chmod %q", materialized)
			}

		case capture_receipt.TypeSymlink:
			if err := os.Symlink(e.Target, materialized); err != nil {
				return errors.Wrapf(err, "symlink %q -> %q", materialized, e.Target)
			}

		case capture_receipt.TypeOther:
			fmt.Fprintf(os.Stderr,
				"notice: skipping entry of type %q: %s\n",
				e.Type, materialized)

		default:
			return errors.ErrorWithStackf(
				"%s: unknown entry type %q", materialized, e.Type,
			)
		}
	}

	return nil
}

// assertDirectoryExists is the diff-side precondition: the dir must
// already exist on disk and be a directory. Diff is read-only — it
// does not materialize anything — so a missing directory is the
// caller's mistake, not a step diff should perform.
func assertDirectoryExists(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.ErrorWithStackf(
				"%s: directory does not exist\n"+
					"hint: pass an existing directory; diff does not "+
					"materialize anything",
				dir,
			)
		}
		return errors.Wrapf(err, "stat %q", dir)
	}
	if !info.IsDir() {
		return errors.ErrorWithStackf(
			"%s: not a directory (mode %s)", dir, info.Mode(),
		)
	}
	return nil
}

// walkForDiff is the read-only analogue of walkRoot: same WalkDir
// loop, same per-type entry shaping, but errors aggregate into a
// single error (no streaming sink) and blobs are computed via the
// caller's discard-store wrapper rather than written for real.
func walkForDiff(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	dir string,
) (entries []capture_receipt.EntryV1, err error) {
	var perEntryFailures []string

	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		// Same prompt-cancel semantics as walkRoot: abort the walk
		// instead of aggregating one failure per remaining file.
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			perEntryFailures = append(perEntryFailures,
				fmt.Sprintf("%s: %v", p, walkErr))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			perEntryFailures = append(perEntryFailures,
				fmt.Sprintf("%s: %v", p, err))
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			perEntryFailures = append(perEntryFailures,
				fmt.Sprintf("%s: %v", p, err))
			return nil
		}
		rel = filepath.ToSlash(rel)

		mode := info.Mode()
		entry := capture_receipt.EntryV1{
			Path: rel,
			Root: dir,
			Mode: mode,
		}

		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				perEntryFailures = append(perEntryFailures,
					fmt.Sprintf("%s: %v", p, err))
				return nil
			}
			entry.Type = capture_receipt.TypeSymlink
			entry.Target = target

		case mode.IsDir():
			entry.Type = capture_receipt.TypeDir

		case mode.IsRegular():
			id, size, err := plugin_blob_io.WriteFileBlob(ctx, store, p)
			if err != nil {
				perEntryFailures = append(perEntryFailures,
					fmt.Sprintf("%s: %v", p, err))
				return nil
			}
			entry.Type = capture_receipt.TypeFile
			entry.Size = size
			entry.BlobId = id.String()

		default:
			entry.Type = capture_receipt.TypeOther
		}

		entries = append(entries, entry)
		return nil
	})

	if walkErr != nil {
		return nil, errors.Wrapf(walkErr, "walk %q", dir)
	}
	if len(perEntryFailures) > 0 {
		return nil, errors.ErrorWithStackf(
			"%d entry walk failures:\n  %s",
			len(perEntryFailures),
			joinDiffFailures(perEntryFailures),
		)
	}

	return entries, nil
}

func joinDiffFailures(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}

func materializeFile(
	ctx context.Context,
	blobStore blob_stores.BlobStoreInitialized,
	e capture_receipt.EntryV1,
	materialized string,
) (err error) {
	var blobId markl.Id
	if err = blobId.Set(e.BlobId); err != nil {
		return errors.Wrapf(err, "%s: parse blob_id %q", materialized, e.BlobId)
	}

	reader, err := blobStore.MakeBlobReader(&blobId)
	if err != nil {
		return errors.Wrapf(err, "%s: open blob %s", materialized, &blobId)
	}
	defer errors.DeferredCloser(&err, reader)

	file, err := os.OpenFile(
		materialized,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o666,
	)
	if err != nil {
		return errors.Wrapf(err, "create %q", materialized)
	}
	defer errors.DeferredCloser(&err, file)

	if _, err = io.Copy(file, plugin_blob_io.NewCtxReader(ctx, reader)); err != nil {
		return errors.Wrapf(err,
			"%s: blob read failed\n  blob_id: %s",
			materialized, &blobId)
	}

	if err = os.Chmod(materialized, e.Mode.Perm()); err != nil {
		return errors.Wrapf(err, "chmod %q", materialized)
	}

	return nil
}
