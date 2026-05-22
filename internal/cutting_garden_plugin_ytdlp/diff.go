package cutting_garden_plugin_ytdlp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// infoJSONSuffix is the trailing extension yt-dlp gives the metadata
// sidecar (`<id>.info.json`).
const infoJSONSuffix = ".info.json"

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
) ([]capture_receipt.EntryV1, error) {
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

	tempDir, err := os.MkdirTemp("", "cg-ytdlp-diff-*")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer os.RemoveAll(tempDir)

	if err := runYtdlp(req.Context, tempDir, probeArgs(tempDir, source)); err != nil {
		return nil, err
	}

	freshInfoPath, err := findInfoJSON(tempDir)
	if err != nil {
		return nil, err
	}

	freshID, _, err := writeFileBlob(req.Context, req.BlobStore, freshInfoPath)
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
	// the difference.
	return rescan(req, tempDir, source)
}

// probeArgs builds the `yt-dlp --skip-download --write-info-json`
// invocation used by the diff freshness probe.
func probeArgs(outDir, source string) []string {
	return []string{
		"--no-progress",
		"--no-warnings",
		"--skip-download",
		"--write-info-json",
		"-o", filepath.Join(outDir, outputTemplate),
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

// findInfoJSON returns the path to the single *.info.json under
// dir. yt-dlp writes exactly one with the default template; an
// unexpected count is reported as an error so silent surprises don't
// pollute diff output.
func findInfoJSON(dir string) (string, error) {
	var matches []string
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, infoJSONSuffix) {
			matches = append(matches, p)
		}
		return nil
	})
	if walkErr != nil {
		return "", errors.Wrap(walkErr)
	}
	if len(matches) != 1 {
		return "", errors.ErrorWithStackf(
			"ytdlp plugin: expected exactly one .info.json under %q, found %d",
			dir, len(matches),
		)
	}
	return matches[0], nil
}

// rescan runs a full media+sidecar yt-dlp invocation and returns
// freshly-hashed EntryV1s, used when the info.json freshness probe
// reports a miss.
func rescan(
	req cutting_garden_plugins.DiffScanRequest,
	tempDir string,
	source string,
) ([]capture_receipt.EntryV1, error) {
	// Reuse tempDir; the probe's info.json already lives there but
	// yt-dlp will overwrite it identically (same -o template, same
	// blob-id) and the new artifacts are additive.
	if err := runYtdlp(req.Context, tempDir, captureDefaultArgs(tempDir, source)); err != nil {
		return nil, err
	}

	var entries []capture_receipt.EntryV1
	var perEntryFailures []string

	walkErr := filepath.WalkDir(tempDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			perEntryFailures = append(perEntryFailures, walkErr.Error())
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			perEntryFailures = append(perEntryFailures, err.Error())
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(tempDir, p)
		if err != nil {
			perEntryFailures = append(perEntryFailures, err.Error())
			return nil
		}
		rel = filepath.ToSlash(rel)
		id, size, err := writeFileBlob(req.Context, req.BlobStore, p)
		if err != nil {
			perEntryFailures = append(perEntryFailures, err.Error())
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
