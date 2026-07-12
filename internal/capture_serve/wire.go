// Package capture_serve implements the RFC 0008 capture-plugin transport:
// a persistent JSON-RPC 2.0 session over an AF_UNIX SOCK_SEQPACKET
// connection, with node-blob bytes passed out of band via SCM_RIGHTS
// FD-passed pipes. It replaces the RFC 0002 subprocess Writer Protocol's
// per-blob process spawn with one long-lived peer per capture batch; the
// stored bytes, markl ids, and receipts are unchanged.
//
// This file defines the wire shapes: the five method names, the v2 schema
// token, and the param/result structs each method carries. The JSON-RPC
// envelope itself is go-mcp's jsonrpc.Message; the SEQPACKET peer that
// frames one message per datagram lives in peer.go.
package capture_serve

// SchemaV2 is the protocol version token carried by initialize,
// capture.batch params, and batch results (RFC 0008 §Handshake).
const SchemaV2 = "capture-plugin/v2"

// Method names and their directions (RFC 0008 §JSON-RPC, peer-to-peer):
// the orchestrator calls initialize/capture.batch and notifies shutdown;
// the plugin calls blob.begin/blob.finish back over the same socket.
const (
	MethodInitialize   = "initialize"
	MethodCaptureBatch = "capture.batch"
	MethodShutdown     = "shutdown"
	MethodBlobBegin    = "blob.begin"
	MethodBlobFinish   = "blob.finish"
)

// CodeUnsupportedVersion is the JSON-RPC error code a plugin returns from
// initialize when no offered protocol version is acceptable (RFC 0008
// §Handshake). The v2→v1 fallback in the web plugin keys off it.
const CodeUnsupportedVersion = -32000

// Features is the per-peer capability advertisement exchanged during
// initialize. The effective limit of a feature is the min of both peers'
// advertised values.
type Features struct {
	// BlobConcurrency is the max number of simultaneously-open blobs
	// (begun but not finished). 1 — fully sequential — is the floor and
	// the default.
	BlobConcurrency int `json:"blob_concurrency"`
}

// InitializeParams is the orchestrator→plugin initialize request payload.
type InitializeParams struct {
	// ProtocolVersions are the schema tokens the orchestrator can speak,
	// in preference order.
	ProtocolVersions []string `json:"protocol_versions"`
	Features         Features `json:"features"`
}

// PluginInfo identifies the plugin binary; recorded verbatim in batch
// results (mirrors the RFC 0002 output's plugin block).
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the plugin's initialize response.
type InitializeResult struct {
	// Schema is the single version the plugin selected from
	// ProtocolVersions.
	Schema   string     `json:"schema"`
	Plugin   PluginInfo `json:"plugin"`
	Features Features   `json:"features"`
	// Formats is advisory; the authoritative capability surface remains
	// the capabilities blob in the receipt tree (RFC 0003).
	Formats []string `json:"formats,omitempty"`
}

// BatchDefaults mirrors the RFC 0002 Batch Input defaults block: values
// applied to every capture unless overridden per-capture.
type BatchDefaults struct {
	Normalize *bool          `json:"normalize,omitempty"`
	Plugin    map[string]any `json:"plugin,omitempty"`
}

// CaptureSpec is one requested capture within a batch. Name only
// correlates input to output; RFC 0002 forbids emitting it into any blob.
type CaptureSpec struct {
	Name    string         `json:"name"`
	Format  string         `json:"format"`
	Options map[string]any `json:"options,omitempty"`
}

// BatchParams is the capture.batch request payload: the RFC 0002 Batch
// Input minus writer.cmd — the writer is the FD-passing blob protocol,
// not a spawned process.
type BatchParams struct {
	// Schema MUST be SchemaV2.
	Schema   string         `json:"schema"`
	Target   string         `json:"target"`
	Defaults *BatchDefaults `json:"defaults,omitempty"`
	Captures []CaptureSpec  `json:"captures"`
}

// ProtocolError is a structured per-capture or batch-level failure
// (RFC 0002 error shape, unchanged).
type ProtocolError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// ReceiptRef addresses one capture's receipt-tree root blob.
type ReceiptRef struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

// CaptureResult is one capture's outcome: exactly one of Receipt or Error
// is set. Per-capture failures land here; only transport- or batch-fatal
// conditions surface as a JSON-RPC error response to capture.batch.
type CaptureResult struct {
	Name    string         `json:"name"`
	Receipt *ReceiptRef    `json:"receipt,omitempty"`
	Error   *ProtocolError `json:"error,omitempty"`
}

// BatchResult is the capture.batch response payload (the RFC 0002 Batch
// Output, schema v2).
type BatchResult struct {
	Schema   string          `json:"schema"`
	Plugin   PluginInfo      `json:"plugin"`
	Errors   []ProtocolError `json:"errors"`
	Captures []CaptureResult `json:"captures"`
}

// BlobBeginParams is the plugin→orchestrator blob.begin payload. It is
// empty on the wire; the response carries the blob handle AND the pipe
// write-fd as SCM_RIGHTS ancillary data on the same datagram.
type BlobBeginParams struct{}

// BlobBeginResult correlates the passed write-fd to its later
// blob.finish.
type BlobBeginResult struct {
	Blob int64 `json:"blob"`
}

// BlobFinishParams names the blob (by begin handle) whose bytes the
// plugin has fully written and closed.
type BlobFinishParams struct {
	Blob int64 `json:"blob"`
}

// BlobFinishResult is the committed blob's identity — the same {id, size}
// contract the RFC 0002 Writer Protocol printed on stdout.
type BlobFinishResult struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}
