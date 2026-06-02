package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/madder/go/pkgs/env_dir"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/files"
)

// captureLogEntry is one NDJSON line in
// $XDG_STATE_HOME/cutting-garden/captures.log. One entry per receipt
// produced — a single capture invocation that touches N store-groups
// produces N entries.
type captureLogEntry struct {
	// Ts is the RFC3339 UTC timestamp at which the receipt was written.
	Ts string `json:"ts"`
	// ReceiptID is the markl-id of the receipt blob, as produced by
	// writeReceipt in capture.go.
	ReceiptID string `json:"receipt_id"`
	// StoreID is the blob-store-id string the receipt landed in. Empty
	// string for the default store, matching blob_store_id.Id.IsEmpty()
	// conventions in the planner and the user-facing NDJSON sink.
	//
	// Deliberately diverges from the future receipt store-hint metadata
	// (step 8), which records the *resolved* default-store id. The log
	// keeps the user's CLI-level intent (no store-id arg → empty); the
	// receipt records the resolved on-disk store.
	StoreID string `json:"store_id"`
	// Roots is the directory args for this store-group's receipt, in the
	// order they were captured.
	Roots []string `json:"roots"`
}

// captureLogFileName is the leaf filename under
// <cgEnvDir.GetXDG().State>/captures.log — cg's audit trail of past
// captures.
const captureLogFileName = "captures.log"

// appendCaptureLog appends entries as NDJSON lines to the captures.log
// file under cgEnvDir's $XDG_STATE_HOME/cutting-garden/. Best-effort:
// errors surface as sink notices, never fatal. The blob is the source
// of truth; the log is observability.
//
// Mirrors madder's inventory_log swallow-on-error policy — if a user's
// $XDG_STATE_HOME is unwritable, the capture itself still succeeds.
//
// No daily rotation, no hyphence wrapping, no codec registry. A
// focused multi-scope tracer; richer infrastructure would be a
// generalization if a real consumer ever wants it.
func appendCaptureLog(
	cgEnvDir env_dir.Env,
	sink capture_sink.Sink,
	entries []captureLogEntry,
) {
	if len(entries) == 0 {
		return
	}

	path := cgEnvDir.GetXDG().State.MakePath(captureLogFileName).String()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		sink.Notice(
			"notice: cannot create captures.log directory %q: %v",
			filepath.Dir(path), err,
		)
		return
	}

	file, err := os.OpenFile(
		path,
		os.O_WRONLY|os.O_APPEND|os.O_CREATE,
		0o644,
	)
	if err != nil {
		sink.Notice(
			"notice: cannot open captures.log %q: %v", path, err,
		)
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			sink.Notice(
				"notice: captures.log close error at %q: %v", path, cerr,
			)
		}
	}()

	encoder := json.NewEncoder(file)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			sink.Notice(
				"notice: captures.log write error at %q: %v", path, err,
			)
			return
		}
	}
}

// findPriorReceipt scans captures.log for the most recent entry whose
// store-id and roots match storeID and rootPath, returning its receipt
// id ("" if none). It seeds incremental protocol captures: the prior
// receipt's object set becomes the "haves" for a delta fetch.
// Best-effort — any open/parse error yields "" and a full capture.
func findPriorReceipt(cgEnvDir env_dir.Env, storeID, rootPath string) string {
	path := cgEnvDir.GetXDG().State.MakePath(captureLogFileName).String()
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

// captureLogTimestamp returns the current RFC3339 UTC timestamp.
// Indirected through a var so tests (or a future --fixed-clock debug
// knob) can stub it; today it is just time.Now().
var captureLogTimestamp = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// quoteEmpty renders an empty store-name as "(default)" for
// user-facing notices, matching madder's parity convention.
func quoteEmpty(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}
