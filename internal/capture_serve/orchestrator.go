package capture_serve

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// RunBatch drives one capture batch over an established RFC 0008 control
// connection: initialize (offering SchemaV2), capture.batch, then a
// best-effort shutdown notification. Every node blob the plugin writes
// back through the blob protocol lands in dest — NewBlobStoreWriter in
// production, so the stored bytes and markl ids are identical to the
// in-process RFC 0002 form.
//
// This is the transport core; the launch wrapper (spawn the plugin's
// capture-serve subcommand, ReadAnnounce, DialAnnounced) composes around
// it. An initialize failure with CodeUnsupportedVersion is the v2→v1
// fallback signal and surfaces as a *jsonrpc.Error for the caller to
// dispatch on.
func RunBatch(
	ctx context.Context,
	conn *net.UnixConn,
	dest capture_plugin.Writer,
	batch BatchParams,
) (BatchResult, error) {
	peer := NewPeer(ctx, conn, newOrchBlobHandler(dest))
	defer peer.Close()

	var init InitializeResult
	if err := peer.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersions: []string{SchemaV2},
		Features:         Features{BlobConcurrency: 1},
	}, &init); err != nil {
		return BatchResult{}, errors.Wrapf(err, "initialize")
	}
	if init.Schema != SchemaV2 {
		return BatchResult{}, errors.ErrorWithStackf(
			"plugin selected schema %q, want %q", init.Schema, SchemaV2,
		)
	}

	batch.Schema = SchemaV2
	var result BatchResult
	if err := peer.Call(ctx, MethodCaptureBatch, batch, &result); err != nil {
		return BatchResult{}, errors.Wrapf(err, "capture.batch")
	}

	// Graceful exit request; the datagram is queued before the deferred
	// Close, and a failure to send it only costs the plugin its clean
	// exit path (it treats the subsequent EOF as cancellation).
	_ = peer.Notify(MethodShutdown, nil)
	return result, nil
}

// orchBlobHandler serves the plugin-called half of the session: each
// blob.begin opens a pipe whose write-end rides the response as
// SCM_RIGHTS while the read-end streams into dest; blob.finish joins
// that stream's commit and returns the {id, size} the plugin embeds in
// its next node.
type orchBlobHandler struct {
	dest capture_plugin.Writer

	mu   sync.Mutex
	open map[int64]*openBlob
	next int64
}

// openBlob is one begun-but-unfinished blob: done closes when the
// read-side commit completes and id/size/err are set.
type openBlob struct {
	done chan struct{}
	id   string
	size int64
	err  error
}

func newOrchBlobHandler(dest capture_plugin.Writer) *orchBlobHandler {
	return &orchBlobHandler{
		dest: dest,
		open: make(map[int64]*openBlob),
	}
}

func (h *orchBlobHandler) Handle(
	ctx context.Context, method string, params json.RawMessage,
) (any, *os.File, error) {
	switch method {
	case MethodBlobBegin:
		return h.begin(ctx)
	case MethodBlobFinish:
		return h.finish(ctx, params)
	default:
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.MethodNotFound,
			Message: "unknown method " + method,
		}
	}
}

func (h *orchBlobHandler) begin(ctx context.Context) (any, *os.File, error) {
	h.mu.Lock()
	if len(h.open) != 0 {
		h.mu.Unlock()
		return nil, nil, errors.ErrorWithStackf(
			"blob.begin while a blob is open: blob_concurrency=1 violated",
		)
	}
	h.next++
	handle := h.next
	ob := &openBlob{done: make(chan struct{})}
	h.open[handle] = ob
	h.mu.Unlock()

	r, w, err := os.Pipe()
	if err != nil {
		h.mu.Lock()
		delete(h.open, handle)
		h.mu.Unlock()
		return nil, nil, errors.Wrap(err)
	}

	go func() {
		defer close(ob.done)
		ob.id, ob.size, ob.err = h.dest.WriteBlob(ctx, r)
		r.Close()
	}()

	// w rides the response datagram as SCM_RIGHTS; the peer closes this
	// copy after send so the plugin's close is the one EOF source.
	return BlobBeginResult{Blob: handle}, w, nil
}

func (h *orchBlobHandler) finish(
	ctx context.Context, params json.RawMessage,
) (any, *os.File, error) {
	var p BlobFinishParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.InvalidParams,
			Message: err.Error(),
		}
	}

	h.mu.Lock()
	ob := h.open[p.Blob]
	delete(h.open, p.Blob)
	h.mu.Unlock()
	if ob == nil {
		return nil, nil, errors.ErrorWithStackf(
			"blob.finish for unknown handle %d", p.Blob,
		)
	}

	// The plugin closed its write-end before finishing, so the commit's
	// EOF — and this join — is already in flight.
	select {
	case <-ob.done:
	case <-ctx.Done():
		return nil, nil, errors.Wrap(ctx.Err())
	}
	if ob.err != nil {
		return nil, nil, errors.Wrapf(ob.err, "commit blob %d", p.Blob)
	}
	return BlobFinishResult{ID: ob.id, Size: ob.size}, nil, nil
}
