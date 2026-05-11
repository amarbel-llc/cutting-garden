// Package cutting_garden_plugins is the registry for cutting-garden's
// URI-scheme-keyed capture and restore backends. The filesystem
// backend is a peer leaf (`cutting_garden_plugin_file`) registered
// for both `""` (schemeless) and `"file"` schemes. Future plugins
// follow the same shape: one peer leaf per scheme, each with a
// blank-import in the CLI command so init() registration fires at
// binary startup.
//
// Per-plugin wire-format type-tags follow the convention
// `cutting_garden-capture_receipt-<segment>-v1`; the segment is
// owned by the plugin (e.g. `fs` for the filesystem backend).
package cutting_garden_plugins

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// Plugin is the cross-cutting identity every capture or restore
// backend must declare. Schemes returns the URI schemes a plugin
// handles; the empty string registers the plugin as the schemeless
// default and SHOULD only be claimed by one plugin per binary.
type Plugin interface {
	Schemes() []string

	// TypeTag returns the wire-format type-tag the plugin emits when
	// capturing and accepts when restoring. Conventionally
	// `cutting_garden-capture_receipt-<segment>-v1`.
	TypeTag() string
}

// CaptureRootRequest is what a CapturePlugin needs to walk one
// capture root: the source URL (already parsed; may be schemeless),
// the destination blob store, and a live event sink.
type CaptureRootRequest struct {
	Source    *url.URL
	RawArg    string
	BlobStore blob_stores.BlobStoreInitialized
	Sink      capture_sink.Sink
}

// CaptureRootResult is what a CapturePlugin produces from one root:
// the entries to be folded into the receipt and a count of per-entry
// failures the sink already reported.
type CaptureRootResult struct {
	Entries   []capture_receipt.EntryV1
	FailCount int
}

// CapturePlugin walks one capture root into the destination store,
// emitting entries and live sink events. Plugins MAY support only
// capture or only restore.
type CapturePlugin interface {
	Plugin

	// ValidateSource is called by the planner during arg
	// classification, before any walking starts. Returns nil if u is
	// acceptable as a capture root, or an error suitable for the
	// classify-failures channel. raw is the original CLI argument,
	// for diagnostics.
	ValidateSource(u *url.URL, raw string) error

	CaptureRoot(req CaptureRootRequest) CaptureRootResult
}

// RestoreRequest is what a RestorePlugin needs to materialize a
// previously-captured tree: the receipt's parsed entries, the source
// blob store, and the destination URL (already parsed; may be
// schemeless).
type RestoreRequest struct {
	Entries   []capture_receipt.EntryV1
	BlobStore blob_stores.BlobStoreInitialized
	Dest      *url.URL
	RawDest   string
}

// RestorePlugin materializes a receipt's entries to the destination.
type RestorePlugin interface {
	Plugin

	// ValidateDest is called before any disk writes. Returns nil if
	// dest is acceptable, or an error to surface to the caller. raw
	// is the original CLI argument.
	ValidateDest(dest *url.URL, raw string) error

	Restore(req RestoreRequest) error
}

// DiffScanRequest is what a DiffPlugin needs to enumerate the
// current state at Dir and return entries that can be compared
// against ReceiptEntries. The plugin computes blob-ids using
// BlobStore (typically a discard-store wrapping the receipt's
// source store, so only the hash family matters). ReceiptEntries
// is provided so the plugin can run its own pre-walk validation
// (path sanitization etc.) before any I/O — diff is read-only and
// atomic.
type DiffScanRequest struct {
	Dir            *url.URL
	RawDir         string
	BlobStore      blob_stores.BlobStoreInitialized
	ReceiptEntries []capture_receipt.EntryV1
}

// DiffPlugin enumerates the current state at a location and returns
// entries in the same shape a CapturePlugin would produce, suitable
// for comparison against a receipt. Plugins MAY support diff without
// supporting capture or restore (e.g. a read-only HTTP listing
// plugin) or vice versa.
type DiffPlugin interface {
	Plugin

	// ValidateDiffDir is called before any I/O. Returns nil if dir
	// is acceptable, or an error to surface to the caller. raw is
	// the original CLI argument.
	ValidateDiffDir(dir *url.URL, raw string) error

	// ScanForDiff walks/lists the location and returns entries with
	// computed blob-ids. Per-entry failures aggregate into the
	// returned error (diff is read-only and atomic — no streaming
	// sink).
	ScanForDiff(req DiffScanRequest) ([]capture_receipt.EntryV1, error)
}
