package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// captureLogEntry is one NDJSON line in
// $XDG_STATE_HOME/cutting-garden/captures.log. Shares the schema written
// by the capture command (internal/capture/capture_log.go) so a single
// log records receipts from both `capture` and `serve`.
type captureLogEntry struct {
	Ts        string   `json:"ts"`
	ReceiptID string   `json:"receipt_id"`
	StoreID   string   `json:"store_id"`
	Roots     []string `json:"roots"`
}

// appendCaptureLog appends one entry as an NDJSON line to path.
// Best-effort: any error is reported via log and swallowed — the receipt
// blob is the source of truth, the log is observability. Mirrors
// capture.appendCaptureLog's swallow-on-error policy.
func appendCaptureLog(
	path string,
	log func(format string, args ...any),
	entry captureLogEntry,
) {
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log("notice: cannot create captures.log directory %q: %v",
			filepath.Dir(path), err)
		return
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		log("notice: cannot open captures.log %q: %v", path, err)
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			log("notice: captures.log close error at %q: %v", path, cerr)
		}
	}()

	if err := json.NewEncoder(file).Encode(entry); err != nil {
		log("notice: captures.log write error at %q: %v", path, err)
	}
}

// captureLogTimestamp returns the current RFC3339 UTC timestamp.
// Indirected through a var so tests can stub it.
var captureLogTimestamp = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}
