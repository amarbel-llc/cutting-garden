// Package capture_plugin implements the orchestrator side of the
// Capture Plugin Protocol (cutting-garden RFC 0002): it serializes a
// capture as a merkle tree of typed hyphence node blobs — receipt →
// identity → {invocation, environment → {host, binary, plugin}},
// outcome, payload — and writes every node through a generic Writer,
// returning the root receipt's markl id.
//
// The protocol-defined nodes (receipt, identity, invocation,
// environment, host, binary, outcome) live here. The payload subtree is
// plugin-defined: a capture plugin writes its own payload node(s)
// through the same Writer and hands the resulting reference(s) to
// WriteReceipt, which links them under the receipt's `payload` slot.
//
// This is the in-process form of RFC 0002 §In-Process Plugin Interface.
// Type signatures (`@<sig>`) on reference lines are OPTIONAL per the
// hyphence RFC and FDR-0001; this implementation emits sig-less typed
// references (`< @<digest> !<type-string>`) — the type-string alone
// identifies the type. JCS bodies are produced by jcsMarshal (see
// jcs.go).
package capture_plugin

import (
	"bytes"
	"context"
	"io"

	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// Writer is the narrow sink every node blob flows through. WriteBlob
// streams r into the backing store and returns the content-addressed
// markl id (as a string, e.g. "sha256-…") plus the byte count. It is
// the in-process analogue of RFC 0002's writer-subprocess contract.
type Writer interface {
	WriteBlob(ctx context.Context, r io.Reader) (digest string, size int64, err error)
}

// WriteNode is a convenience wrapper for writing a fully-materialized
// node (its bytes are already in memory). Streaming payloads (large git
// blobs, media files) should call WriteBlob with a reader instead.
func WriteNode(ctx context.Context, w Writer, node []byte) (digest string, size int64, err error) {
	return w.WriteBlob(ctx, bytes.NewReader(node))
}

// blobStoreWriter adapts a madder blob store to the Writer interface,
// reusing plugin_blob_io.WriteReaderBlob's ctx-cancelable copy loop.
type blobStoreWriter struct {
	store blob_stores.BlobStoreInitialized
}

// NewBlobStoreWriter returns a Writer backed by store.
func NewBlobStoreWriter(store blob_stores.BlobStoreInitialized) Writer {
	return blobStoreWriter{store: store}
}

func (w blobStoreWriter) WriteBlob(
	ctx context.Context,
	r io.Reader,
) (digest string, size int64, err error) {
	id, size, err := plugin_blob_io.WriteReaderBlob(ctx, w.store, r)
	if err != nil {
		return "", 0, err
	}
	return id.String(), size, nil
}
