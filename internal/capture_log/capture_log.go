// Package capture_log owns the captures.log NDJSON schema and append
// machinery shared by the capture and serve commands, so the two
// producers cannot drift apart on the wire format.
package capture_log

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// FileName is the leaf filename under
// $XDG_STATE_HOME/cutting-garden/ — cg's audit trail of past captures.
const FileName = "captures.log"

// Entry is one NDJSON line in captures.log. One entry per receipt
// produced — a single capture invocation that touches N store-groups
// produces N entries; serve produces one per finalized session.
type Entry struct {
	// Ts is the RFC3339 UTC timestamp at which the receipt was written.
	Ts string `json:"ts"`
	// ReceiptID is the markl-id of the receipt blob, as produced by
	// capture_receipt.WriteV1ToStore.
	ReceiptID string `json:"receipt_id"`
	// StoreID is the blob-store-id string the receipt landed in. Empty
	// string for the default store, matching blob_store_id.Id.IsEmpty()
	// conventions in the planner and the user-facing NDJSON sink.
	//
	// Deliberately diverges from the receipt store-hint metadata, which
	// records the *resolved* default-store id. The log keeps the user's
	// CLI-level intent (no store-id arg → empty); the receipt records
	// the resolved on-disk store.
	StoreID string `json:"store_id"`
	// Roots is the directory args for this store-group's receipt, in the
	// order they were captured. serve records a pseudo-root naming the
	// sending device ("localsend:<alias>").
	Roots []string `json:"roots"`
}

// Append appends entries as NDJSON lines to the captures.log at path.
// Best-effort: errors surface through notice (the caller's
// informational channel), never fatal. The receipt blob is the source
// of truth; the log is observability.
//
// Mirrors madder's inventory_log swallow-on-error policy — if a user's
// $XDG_STATE_HOME is unwritable, the capture itself still succeeds.
//
// No daily rotation, no hyphence wrapping, no codec registry. A
// focused multi-scope tracer; richer infrastructure would be a
// generalization if a real consumer ever wants it.
func Append(
	path string,
	notice func(format string, args ...any),
	entries []Entry,
) {
	if len(entries) == 0 {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		notice(
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
		notice(
			"notice: cannot open captures.log %q: %v", path, err,
		)
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			notice(
				"notice: captures.log close error at %q: %v", path, cerr,
			)
		}
	}()

	encoder := json.NewEncoder(file)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			notice(
				"notice: captures.log write error at %q: %v", path, err,
			)
			return
		}
	}
}

// Timestamp returns the current RFC3339 UTC timestamp. Indirected
// through a var so tests (or a future --fixed-clock debug knob) can
// stub it; today it is just time.Now().
var Timestamp = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}
