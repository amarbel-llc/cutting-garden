package cutting_garden_plugin_ytdlp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// infoJSONSuffix is the trailing extension yt-dlp gives the metadata
// sidecar (`<id>.info.json`).
const infoJSONSuffix = ".info.json"

// probeStem is the fixed `-o` stem used for the freshness probe.
// Decoupling from `%(id)s` lets us locate the info.json by exact path
// (`<probeDir>/probe.info.json`) instead of walking the tempdir and
// matching suffixes — robust to yt-dlp adding future `*.info.json`
// sidecars (e.g. live-chat) under the id-based template.
const probeStem = "probe"

// ScanForDiff implements the lightweight freshness probe documented in
// FDR 0003: fetch only the info.json (`--skip-download
// --write-info-json`), compare its content-addressed blob-id against
// the receipt's, and re-emit the receipt's entries verbatim on match.
// On mismatch, fall back to a full yt-dlp re-download so the
// comparator sees fresh blob-ids for every artifact.
//
// Returns the entries to feed into compareEntries — match → identical
// to the receipt's; miss → fresh.
func (Plugin) ScanForDiff(
	req cutting_garden_plugins.DiffScanRequest,
) (entries []capture_receipt.EntryV1, err error) {
	source, err := sourceURLFromArg(req.Dir)
	if err != nil {
		return nil, err
	}

	groupEntries := entriesForRoot(req.ReceiptEntries, source)
	if len(groupEntries) == 0 {
		return nil, errors.ErrorWithStackf(
			"ytdlp plugin: receipt has no entries for source %q\n"+
				"hint: confirm the receipt was captured from this URL",
			source,
		)
	}

	probeDir, err := os.MkdirTemp("", "cg-ytdlp-probe-*")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer errors.Deferred(&err, func() error { return os.RemoveAll(probeDir) })

	if err = runYtdlp(req.Context, probeDir, probeArgs(probeDir, source), nil, nil); err != nil {
		return nil, err
	}

	freshInfoPath := filepath.Join(probeDir, probeStem+infoJSONSuffix)
	if _, statErr := os.Stat(freshInfoPath); statErr != nil {
		return nil, errors.ErrorWithStackf(
			"ytdlp plugin: probe info.json missing at %q (%v)\n"+
				"hint: yt-dlp may have refused the URL silently",
			freshInfoPath, statErr,
		)
	}

	freshID, _, err := plugin_blob_io.WriteFileBlob(req.Context, req.BlobStore, freshInfoPath)
	if err != nil {
		return nil, err
	}

	receiptInfoBlobID, ok := infoJSONBlobID(groupEntries)
	if ok && receiptInfoBlobID == freshID.String() {
		// Freshness probe says nothing changed; re-emit the receipt's
		// entries unchanged so compareEntries reports zero diffs
		// without paying for a full re-download.
		return groupEntries, nil
	}

	// Stale — either info.json content changed or the receipt didn't
	// carry an info.json sidecar. Re-download in full so every
	// artifact gets a fresh blob-id and compareEntries can localize
	// the difference. Rescan uses its own tempdir so a partial
	// failure can't mix probe leftovers into the returned entries.
	return rescan(req, source)
}

// probeArgs builds the `yt-dlp --skip-download --write-info-json`
// invocation used by the diff freshness probe. The fixed `probeStem`
// (not `%(id)s`) makes the resulting filename deterministic without
// knowing the video id upfront.
func probeArgs(outDir, source string) []string {
	return []string{
		"--no-progress",
		"--no-warnings",
		"--skip-download",
		"--write-info-json",
		"-o", filepath.Join(outDir, probeStem+".%(ext)s"),
		"--",
		source,
	}
}

// entriesForRoot picks the receipt entries that belong to source.
// Two cases:
//
//   - Multi-root receipt: at least one entry has Root == source.
//     Return all such entries.
//   - Single-root receipt: every entry has Root == "." (the collapse
//     applied in internal/capture/capture.go). Return all entries.
//
// Mixed receipts (some entries Root=".", others Root=other-url) fall
// to the multi-root branch and silently ignore the dotted entries; a
// later receipt-shape refinement can tighten this.
func entriesForRoot(entries []capture_receipt.EntryV1, source string) []capture_receipt.EntryV1 {
	var matched []capture_receipt.EntryV1
	for _, e := range entries {
		if e.Root == source {
			matched = append(matched, e)
		}
	}
	if len(matched) > 0 {
		return matched
	}

	allDotted := true
	for _, e := range entries {
		if e.Root != "." {
			allDotted = false
			break
		}
	}
	if allDotted {
		return entries
	}
	return nil
}

// infoJSONBlobID returns the blob-id of the `*.info.json` entry in
// group, if any. ok is false when no info.json sidecar is present
// (e.g. a future capture mode that omits sidecars) — callers treat
// that as "always stale" so a full re-download covers them.
func infoJSONBlobID(group []capture_receipt.EntryV1) (id string, ok bool) {
	for _, e := range group {
		if e.Type == capture_receipt.TypeFile && strings.HasSuffix(e.Path, infoJSONSuffix) {
			return e.BlobId, true
		}
	}
	return "", false
}

// rescan runs a full media+sidecar yt-dlp invocation into a fresh
// tempdir and returns freshly-hashed EntryV1s. Used when the
// info.json freshness probe reports a miss. The dedicated tempdir
// guarantees that probe leftovers (or a partial probe failure) can't
// surface as ghost entries in the returned set.
func rescan(
	req cutting_garden_plugins.DiffScanRequest,
	source string,
) (entries []capture_receipt.EntryV1, err error) {
	rescanDir, err := os.MkdirTemp("", "cg-ytdlp-rescan-*")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer errors.Deferred(&err, func() error { return os.RemoveAll(rescanDir) })

	if err = runYtdlp(req.Context, rescanDir, captureDefaultArgs(rescanDir, source), nil, nil); err != nil {
		return nil, err
	}

	var perEntryFailures []string

	walkErr := filepath.WalkDir(rescanDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			perEntryFailures = append(perEntryFailures, walkErr.Error())
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, walkInfoErr := d.Info()
		if walkInfoErr != nil {
			perEntryFailures = append(perEntryFailures, walkInfoErr.Error())
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(rescanDir, p)
		if relErr != nil {
			perEntryFailures = append(perEntryFailures, relErr.Error())
			return nil
		}
		rel = filepath.ToSlash(rel)
		id, size, blobErr := plugin_blob_io.WriteFileBlob(req.Context, req.BlobStore, p)
		if blobErr != nil {
			perEntryFailures = append(perEntryFailures, blobErr.Error())
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
		return nil, errors.Wrap(walkErr)
	}
	if len(perEntryFailures) > 0 {
		return nil, errors.ErrorWithStackf(
			"ytdlp plugin: %d entry failures during diff rescan:\n  %s",
			len(perEntryFailures),
			strings.Join(perEntryFailures, "\n  "),
		)
	}

	return entries, nil
}
