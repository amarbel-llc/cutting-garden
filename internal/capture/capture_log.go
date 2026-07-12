package capture

import (
	"encoding/json"
	"os"

	"code.linenisgreat.com/cutting-garden/internal/capture_log"
	"github.com/amarbel-llc/madder/go/pkgs/env_dir"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/files"
)

// captureLogEntry is one NDJSON line in captures.log. The schema (and
// the append machinery) lives in internal/capture_log, shared with the
// serve command so the two producers cannot drift.
type captureLogEntry = capture_log.Entry

// appendCaptureLog appends entries to the captures.log file under
// cgEnvDir's $XDG_STATE_HOME/cutting-garden/. Best-effort: errors
// surface through notice, never fatal (see capture_log.Append).
func appendCaptureLog(
	cgEnvDir env_dir.Env,
	notice func(format string, args ...any),
	entries []captureLogEntry,
) {
	path := cgEnvDir.GetXDG().State.MakePath(capture_log.FileName).String()
	capture_log.Append(path, notice, entries)
}

// findPriorReceipt scans captures.log for the most recent entry whose
// store-id and roots match storeID and rootPath, returning its receipt
// id ("" if none). It seeds incremental protocol captures: the prior
// receipt's object set becomes the "haves" for a delta fetch.
// Best-effort — any open/parse error yields "" and a full capture.
func findPriorReceipt(cgEnvDir env_dir.Env, storeID, rootPath string) string {
	path := cgEnvDir.GetXDG().State.MakePath(capture_log.FileName).String()
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer files.CloseReadOnly(file)

	var latest string
	decoder := json.NewDecoder(file)
	for {
		var entry captureLogEntry
		if derr := decoder.Decode(&entry); derr != nil {
			break
		}
		if entry.StoreID != storeID {
			continue
		}
		for _, r := range entry.Roots {
			if r == rootPath {
				latest = entry.ReceiptID
				break
			}
		}
	}
	return latest
}

// rootPaths extracts the .path field from each captureRoot in order.
// captureRoot also carries a shadowNotice; the log records only the
// path itself.
func rootPaths(roots []captureRoot) []string {
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = r.path
	}
	return out
}

// quoteEmpty renders an empty store-name as "(default)" for
// user-facing notices, matching madder's parity convention.
func quoteEmpty(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}
