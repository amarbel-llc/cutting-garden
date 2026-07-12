package capture_serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// The conformance bar (RFC 0008 §Conformance): the SAME fixed inputs,
// pushed once through the in-process capture_plugin.Writer path and once
// through Serve/RunBatch over a live SEQPACKET session, MUST yield the
// same root receipt digest and the same stored-blob set — the transport
// changes, the bytes do not.

const fixedPayload = "fixed payload bytes for the capture-serve tracer\n"

// fakeStore is a capture_plugin.Writer over an in-memory map, digesting
// with sha256 so ids are deterministic without a madder store. Both the
// reference and transport runs use it, so digests are comparable.
type fakeStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newFakeStore() *fakeStore {
	return &fakeStore{blobs: map[string][]byte{}}
}

func (s *fakeStore) WriteBlob(
	_ context.Context, r io.Reader,
) (string, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	id := "sha256-" + hex.EncodeToString(sum[:])
	s.mu.Lock()
	s.blobs[id] = data
	s.mu.Unlock()
	return id, int64(len(data)), nil
}

// recordingWriter remembers the last written blob's size: WriteReceipt
// writes the receipt node last and returns only its digest, but the
// batch result's ReceiptRef wants {id, size}.
type recordingWriter struct {
	inner    capture_plugin.Writer
	lastSize int64
}

func (w *recordingWriter) WriteBlob(
	ctx context.Context, r io.Reader,
) (string, int64, error) {
	digest, size, err := w.inner.WriteBlob(ctx, r)
	w.lastSize = size
	return digest, size, err
}

// emitFixedReceipt writes the tracer's single deterministic receipt tree
// through w — payload first, then capture_plugin.WriteReceipt UNCHANGED,
// exactly as a real plugin binding does. Every identity input is pinned
// so the tree's digests are a pure function of the Writer.
func emitFixedReceipt(
	ctx context.Context, w capture_plugin.Writer,
) (digest string, size int64, err error) {
	payloadDigest, _, err := w.WriteBlob(ctx, strings.NewReader(fixedPayload))
	if err != nil {
		return "", 0, err
	}

	rec := &recordingWriter{inner: w}
	digest, err = capture_plugin.WriteReceipt(ctx, rec, capture_plugin.ReceiptParams{
		Kind: "test",
		Invocation: capture_plugin.Invocation{
			Target:    "test://fixture",
			Format:    "fixed",
			Normalize: true,
		},
		Host: capture_plugin.HostInfo{
			OS: "linux", Kernel: "0.0", Arch: "amd64", Libc: "unknown",
		},
		Binary: capture_plugin.BinaryInfo{
			Name: "cg-test-capture-serve", Version: "0.0.0",
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: "jcs-test-capture-environment-v1",
			Body:       map[string]any{"fixture": "v1"},
		},
		PayloadRefs: []capture_plugin.Ref{{
			Alias:      "payload",
			Digest:     payloadDigest,
			TypeString: "test-payload-v1",
		}},
		Now: func() time.Time {
			return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		},
	})
	if err != nil {
		return "", 0, err
	}
	return digest, rec.lastSize, nil
}

func testServeConfig() ServeConfig {
	return ServeConfig{
		Plugin:  PluginInfo{Name: "cg-test-capture-serve", Version: "0.0.0"},
		Formats: []string{"fixed"},
		Batch: func(
			ctx context.Context, params BatchParams, w capture_plugin.Writer,
		) (BatchResult, error) {
			if len(params.Captures) != 1 {
				return BatchResult{}, fmt.Errorf(
					"want exactly one capture, got %d", len(params.Captures),
				)
			}
			digest, size, err := emitFixedReceipt(ctx, w)
			if err != nil {
				return BatchResult{}, err
			}
			return BatchResult{
				Schema: SchemaV2,
				Plugin: PluginInfo{Name: "cg-test-capture-serve", Version: "0.0.0"},
				Errors: []ProtocolError{},
				Captures: []CaptureResult{{
					Name:    params.Captures[0].Name,
					Receipt: &ReceiptRef{ID: digest, Size: size},
				}},
			}, nil
		},
	}
}

// TestEndToEnd_TransportMatchesInProcess is the byte-identity gate: the
// reference tree written straight into a store vs. the same tree pushed
// through initialize/capture.batch/blob.begin/blob.finish over a live
// unixpacket session.
func TestEndToEnd_TransportMatchesInProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Reference run: in-process, no transport.
	refStore := newFakeStore()
	wantDigest, wantSize, err := emitFixedReceipt(ctx, refStore)
	if err != nil {
		t.Fatalf("reference emit: %v", err)
	}

	// Transport run: Serve on one end, RunBatch on the other.
	orchConn, pluginConn := connPair(t)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, pluginConn, testServeConfig())
	}()

	gotStore := newFakeStore()
	result, err := RunBatch(ctx, orchConn, gotStore, BatchParams{
		Target:   "test://fixture",
		Captures: []CaptureSpec{{Name: "cg", Format: "fixed"}},
	})
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	if result.Schema != SchemaV2 {
		t.Errorf("result schema = %q, want %q", result.Schema, SchemaV2)
	}
	if len(result.Captures) != 1 {
		t.Fatalf("captures = %d, want 1", len(result.Captures))
	}
	got := result.Captures[0]
	if got.Error != nil {
		t.Fatalf("capture error: %+v", got.Error)
	}
	if got.Receipt == nil {
		t.Fatal("capture has no receipt")
	}
	if got.Receipt.ID != wantDigest {
		t.Errorf("receipt id = %s, want %s (transport changed the bytes)",
			got.Receipt.ID, wantDigest)
	}
	if got.Receipt.Size != wantSize {
		t.Errorf("receipt size = %d, want %d", got.Receipt.Size, wantSize)
	}

	// The stored sets must match blob-for-blob, not just at the root.
	if len(gotStore.blobs) != len(refStore.blobs) {
		t.Errorf("stored %d blobs, want %d",
			len(gotStore.blobs), len(refStore.blobs))
	}
	for id, want := range refStore.blobs {
		gotBytes, ok := gotStore.blobs[id]
		if !ok {
			t.Errorf("blob %s missing from transport store", id)
			continue
		}
		if !bytes.Equal(gotBytes, want) {
			t.Errorf("blob %s bytes differ across transports", id)
		}
	}

	// RunBatch's shutdown notification ends Serve cleanly.
	select {
	case err := <-serveErr:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Serve did not return after shutdown")
	}
}

// TestRunBatch_UnsupportedVersionSurfacesCode pins the v2→v1 fallback
// hinge on the orchestrator side: a plugin rejecting every offered
// version yields a *jsonrpc.Error carrying CodeUnsupportedVersion.
func TestRunBatch_UnsupportedVersionSurfacesCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	orchConn, pluginConn := connPair(t)
	refusing := NewPeer(ctx, pluginConn, HandlerFunc(func(
		context.Context, string, json.RawMessage,
	) (any, *os.File, error) {
		return nil, nil, &jsonrpc.Error{
			Code:    CodeUnsupportedVersion,
			Message: "unsupported-version",
		}
	}))
	defer func() { _ = refusing.Close() }()

	_, err := RunBatch(ctx, orchConn, newFakeStore(), BatchParams{
		Target:   "test://fixture",
		Captures: []CaptureSpec{{Name: "cg", Format: "fixed"}},
	})
	if err == nil {
		t.Fatal("expected unsupported-version error, got nil")
	}
	var jerr *jsonrpc.Error
	if !asJSONRPCError(err, &jerr) {
		t.Fatalf("error is %T, want *jsonrpc.Error in chain: %v", err, err)
	}
	if jerr.Code != CodeUnsupportedVersion {
		t.Errorf("code = %d, want %d", jerr.Code, CodeUnsupportedVersion)
	}
}

// TestServe_SocketCloseWithoutShutdownErrors pins RFC 0008 §Cancellation:
// a dropped control socket with no shutdown notification is cancellation,
// not a clean exit.
func TestServe_SocketCloseWithoutShutdownErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	orchConn, pluginConn := connPair(t)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- Serve(ctx, pluginConn, testServeConfig())
	}()

	orchConn.Close()

	select {
	case err := <-serveErr:
		if err == nil {
			t.Error("Serve returned nil on socket close without shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Error("Serve did not return after socket close")
	}
}
