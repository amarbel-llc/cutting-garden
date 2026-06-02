package cutting_garden_plugin_git

import (
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ScanForDiff implements the lightweight freshness probe: resolve the
// remote branch tip with `git ls-remote` (no object transfer), hash the
// resulting ref.txt, and compare its blob-id against the receipt's. On
// match, re-emit the receipt's entries verbatim so the comparator
// reports zero drift without paying for a re-clone. On miss (the branch
// moved, or the receipt carried no ref.txt), fall back to a full
// re-clone that re-extracts the object graph so every object gets a
// fresh blob-id.
func (Plugin) ScanForDiff(
	req cutting_garden_plugins.DiffScanRequest,
) (entries []capture_receipt.EntryV1, err error) {
	remote, branch, err := remoteAndBranchFromArg(req.Dir)
	if err != nil {
		return nil, err
	}
	source := canonicalSource(remote, branch)

	groupEntries := entriesForRoot(req.ReceiptEntries, source)
	if len(groupEntries) == 0 {
		return nil, errors.ErrorWithStackf(
			"git plugin: receipt has no entries for source %q\n"+
				"hint: confirm the receipt was captured from this remote+branch",
			source,
		)
	}

	_, commit, err := resolveTip(req.Context, remote, branch)
	if err != nil {
		return nil, err
	}

	// Hash the same `<tip>\n` bytes capture wrote to ref.txt; an
	// unchanged tip yields an identical blob-id.
	freshID, _, err := plugin_blob_io.WriteReaderBlob(
		req.Context, req.BlobStore, strings.NewReader(commit+"\n"))
	if err != nil {
		return nil, err
	}

	receiptRefBlobID, ok := refBlobID(groupEntries)
	if ok && receiptRefBlobID == freshID.String() {
		// Tip unchanged: re-emit the receipt's entries so compareEntries
		// reports zero diffs without re-cloning.
		return groupEntries, nil
	}

	// Stale — the branch tip moved or the receipt lacked a ref.txt.
	// Re-clone and re-extract the object graph so every object gets a
	// fresh blob-id and the comparator can localize the difference.
	return rescan(req, remote, branch, source)
}

// entriesForRoot picks the receipt entries that belong to source.
// Mirrors the yt-dlp plugin's grouping:
//
//   - Multi-root receipt: at least one entry has Root == source. Return
//     all such entries.
//   - Single-root receipt: every entry has Root == "." (the collapse
//     applied in internal/capture). Return all entries.
//
// Mixed receipts (some "." , some other-source) fall to the multi-root
// branch and ignore the dotted entries.
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

// refBlobID returns the blob-id of the ref.txt entry in group, if any.
// ok is false when no ref.txt is present (e.g. a hand-written or future
// receipt that omits it) — callers treat that as "always stale" so a
// full re-clone covers them.
func refBlobID(group []capture_receipt.EntryV1) (id string, ok bool) {
	for _, e := range group {
		if e.Type == capture_receipt.TypeFile && pathBase(e.Path) == refFileName {
			return e.BlobId, true
		}
	}
	return "", false
}

// pathBase returns the final slash-separated segment of an EntryV1.Path
// (always forward-slash, even on Windows captures).
func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// rescan re-clones the branch and re-extracts its full object graph,
// returning freshly-hashed EntryV1s. Used when the ref.txt freshness
// probe reports a miss (the tip moved). Per-object failures aggregate
// into the returned error — diff is read-only and atomic.
func rescan(
	req cutting_garden_plugins.DiffScanRequest,
	remote, branch, source string,
) ([]capture_receipt.EntryV1, error) {
	entries, failures, err := extractBranch(req.Context, req.BlobStore, remote, branch, source)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		lines := make([]string, len(failures))
		for i, f := range failures {
			lines[i] = f.path + ": " + f.err.Error()
		}
		return nil, errors.ErrorWithStackf(
			"git plugin: %d object failures during diff rescan:\n  %s",
			len(failures),
			strings.Join(lines, "\n  "),
		)
	}
	return entries, nil
}
