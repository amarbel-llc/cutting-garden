package capture_serve

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// BatchFunc is the plugin's capture implementation: assemble each
// capture's RFC 0002/0003 merkle tree by writing every node blob through
// w — which realizes capture_plugin.Writer over the RFC 0008 blob
// protocol, so capture_plugin.WriteReceipt is reused UNCHANGED — and
// return the batch result. A returned error is batch-fatal (a JSON-RPC
// error response); per-capture failures belong in BatchResult.Captures[].Error.
type BatchFunc func(
	ctx context.Context, params BatchParams, w capture_plugin.Writer,
) (BatchResult, error)

// ServeConfig configures the plugin side of one capture-serve session.
type ServeConfig struct {
	// Plugin identifies the serving binary in initialize and batch
	// results.
	Plugin PluginInfo
	// Formats is the advisory format list initialize advertises.
	Formats []string
	// Batch handles capture.batch.
	Batch BatchFunc
}

// Serve runs the plugin side of one RFC 0008 session over an established
// control connection: it answers initialize and capture.batch, and
// returns nil on a graceful shutdown (the shutdown notification, or a
// socket close arriving after one). A socket close with NO shutdown is
// cancellation per RFC 0008 §Cancellation — Serve returns the transport
// error and the caller abandons work and exits non-zero.
//
// The out-of-tree entry point: a plugin's capture-serve subcommand does
// CookieFromEnv → ListenRendezvous → AnnounceLine on stdout → Accept →
// Serve, then cleanup.
func Serve(
	ctx context.Context, conn *net.UnixConn, cfg ServeConfig,
) error {
	h := &serverHandler{cfg: cfg, shutdown: make(chan struct{})}
	peer := NewPeer(ctx, conn, h)
	h.peer.Store(peer)
	defer peer.Close()

	select {
	case <-h.shutdown:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err())
	case <-peer.Done():
		// The socket may EOF right behind a shutdown notification still
		// queued for the serve loop; only after the loop drains can the
		// two be told apart.
		<-peer.serveDone
		select {
		case <-h.shutdown:
			return nil
		default:
		}
		return errors.Wrapf(
			peer.Err(), "control socket closed without shutdown",
		)
	}
}

// serverHandler answers the orchestrator-called methods. The peer
// back-reference (needed to drive blob.begin/finish from within the
// batch handler) is stored atomically because NewPeer starts dispatching
// before the constructor returns.
type serverHandler struct {
	cfg      ServeConfig
	peer     atomic.Pointer[Peer]
	shutdown chan struct{}
	once     sync.Once
}

func (h *serverHandler) Handle(
	ctx context.Context, method string, params json.RawMessage,
) (any, *os.File, error) {
	switch method {
	case MethodInitialize:
		return h.initialize(params)
	case MethodCaptureBatch:
		return h.batch(ctx, params)
	case MethodShutdown:
		h.once.Do(func() { close(h.shutdown) })
		return nil, nil, nil
	default:
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.MethodNotFound,
			Message: "unknown method " + method,
		}
	}
}

func (h *serverHandler) initialize(
	params json.RawMessage,
) (any, *os.File, error) {
	var p InitializeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.InvalidParams,
			Message: err.Error(),
		}
	}
	if !slices.Contains(p.ProtocolVersions, SchemaV2) {
		return nil, nil, &jsonrpc.Error{
			Code:    CodeUnsupportedVersion,
			Message: "unsupported-version",
		}
	}
	return InitializeResult{
		Schema:   SchemaV2,
		Plugin:   h.cfg.Plugin,
		Features: Features{BlobConcurrency: 1},
		Formats:  h.cfg.Formats,
	}, nil, nil
}

func (h *serverHandler) batch(
	ctx context.Context, params json.RawMessage,
) (any, *os.File, error) {
	var p BatchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.InvalidParams,
			Message: err.Error(),
		}
	}
	if p.Schema != SchemaV2 {
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.InvalidParams,
			Message: "batch schema " + p.Schema + ", want " + SchemaV2,
		}
	}
	result, err := h.cfg.Batch(
		ctx, p, &blobProtocolWriter{peer: h.peer.Load()},
	)
	if err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

// blobProtocolWriter realizes capture_plugin.Writer over the RFC 0008
// blob protocol: blob.begin → copy the node bytes to the passed pipe
// write-end → close it (the byte-stream terminator) → blob.finish for
// the committed {id, size}. Sequential by construction — one blob is
// fully written and finished before the next begins, the
// blob_concurrency=1 floor.
type blobProtocolWriter struct {
	peer *Peer
}

func (w *blobProtocolWriter) WriteBlob(
	ctx context.Context, r io.Reader,
) (digest string, size int64, err error) {
	var begin BlobBeginResult
	pipe, err := w.peer.CallFD(ctx, MethodBlobBegin, BlobBeginParams{}, &begin)
	if err != nil {
		return "", 0, errors.Wrapf(err, "blob.begin")
	}

	_, copyErr := io.Copy(pipe, r)
	pipe.Close()
	if copyErr != nil {
		// Truncated stream: do NOT finish. The orchestrator commits the
		// partial bytes under their own digest (a harmless orphan in a
		// content-addressed store) and the handle dies with the session;
		// the write error surfaces as the capture's error (RFC 0008
		// §Cancellation and shutdown).
		return "", 0, errors.Wrapf(copyErr, "write blob bytes to passed fd")
	}

	var fin BlobFinishResult
	if err := w.peer.Call(
		ctx, MethodBlobFinish, BlobFinishParams{Blob: begin.Blob}, &fin,
	); err != nil {
		return "", 0, errors.Wrapf(err, "blob.finish")
	}
	return fin.ID, fin.Size, nil
}
