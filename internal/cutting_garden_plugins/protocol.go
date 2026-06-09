package cutting_garden_plugins

import (
	"context"
	"net/url"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// ProtocolCaptureRequest is what a ProtocolCapturePlugin needs to
// materialize one capture as a Capture Plugin Protocol (RFC 0002) merkle
// tree: the parsed source, the destination blob store (the protocol
// "writer" sink), and the command's cancelable context. RawArg is the
// original CLI argument, for diagnostics.
type ProtocolCaptureRequest struct {
	Context   context.Context
	Source    *url.URL
	RawArg    string
	BlobStore blob_stores.BlobStoreInitialized

	// Reporter receives non-identity plan/progress/log events. Optional.
	Reporter Reporter

	// StoreName is the destination store's name (empty = default).
	// Subprocess-form plugins (the web binding) need it to build the
	// `writer.cmd` argv that re-resolves this same store from a child
	// process; in-process plugins (git) hold BlobStore directly and
	// ignore it.
	StoreName string

	// PriorReceiptDigest, when non-empty, is the markl id of the most
	// recent receipt the orchestrator found for this same source. A
	// protocol capture plugin MAY use it to fetch only the objects that
	// changed since that capture (incremental capture); an empty value,
	// an unreadable receipt, or a non-fast-forward change falls back to a
	// full capture.
	PriorReceiptDigest string
}

// ProtocolCaptureResult is the orchestrator-visible output of one
// protocol capture: the root receipt's markl id and the number of
// payload objects stored (for the sink's per-receipt summary).
type ProtocolCaptureResult struct {
	ReceiptDigest string
	ObjectCount   int
}

// ProtocolCapturePlugin is the opt-in interface a capture plugin
// implements to emit an RFC 0002 receipt merkle tree instead of (or in
// addition to) the EntryV1 stream of CapturePlugin.CaptureRoot. When the
// orchestrator resolves a capture plugin that also satisfies this
// interface, it routes the root through CaptureProtocol and records the
// returned receipt id directly — the per-root capture produces its own
// self-contained receipt rather than folding EntryV1 records into a
// shared store-group receipt.
type ProtocolCapturePlugin interface {
	Plugin

	CaptureProtocol(req ProtocolCaptureRequest) (ProtocolCaptureResult, error)
}

// ProtocolRestoreRequest is what a ProtocolRestorePlugin needs to
// rebuild a capture from its RFC 0002 receipt: the store holding the
// receipt (and its referenced blobs), the receipt's markl id, and the
// parsed destination. Routing is by receipt *kind*, not dest scheme —
// the receipt knows how it was captured.
type ProtocolRestoreRequest struct {
	Context       context.Context
	BlobStore     blob_stores.BlobStoreInitialized
	ReceiptDigest string
	Dest          *url.URL
	RawDest       string
}

// ProtocolRestorePlugin reconstructs a capture from its receipt merkle
// tree. ProtocolKind returns the receipt kind it handles (e.g. "git").
type ProtocolRestorePlugin interface {
	ProtocolKind() string
	RestoreProtocol(req ProtocolRestoreRequest) error
}

// ProtocolDiffRequest is what a ProtocolDiffPlugin needs to compare a
// captured receipt against a live source: the store holding the
// receipt, the receipt id, and the parsed comparison source.
type ProtocolDiffRequest struct {
	Context       context.Context
	BlobStore     blob_stores.BlobStoreInitialized
	ReceiptDigest string
	Source        *url.URL
	RawSource     string

	// StoreName is the receipt's store name (empty = default). A
	// subprocess-form diff (the web binding) re-captures the live source
	// and needs it to build the `writer.cmd` argv; in-process diffs (git)
	// hold BlobStore directly and ignore it.
	StoreName string
}

// ProtocolDiffResult carries the human-readable difference lines (empty
// when the receipt and source agree).
type ProtocolDiffResult struct {
	Differences []string
}

// ProtocolDiffPlugin compares an RFC 0002 receipt against a live source.
type ProtocolDiffPlugin interface {
	ProtocolKind() string
	DiffProtocol(req ProtocolDiffRequest) (ProtocolDiffResult, error)
}
