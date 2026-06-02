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
