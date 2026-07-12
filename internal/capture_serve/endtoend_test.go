package capture_serve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/capture_serve"
	testpeer "code.linenisgreat.com/cutting-garden/internal/capture_serve_testpeer"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// The conformance bar (RFC 0008 §Conformance): the SAME fixed inputs,
// pushed once through the in-process capture_plugin.Writer path and once
// through Serve/RunBatch over a live SEQPACKET session, MUST yield the
// same root receipt digest and the same stored-blob set — the transport
// changes, the bytes do not. The fixture (fixed receipt, MemStore,
// ServeConfig) is shared with the packaged test-peer binary via
// internal/capture_serve_testpeer.

// connPair adapts testpeer.ConnPair to test-harness ergonomics.
func connPair(t *testing.T) (accept, dial *net.UnixConn) {
	t.Helper()
	if _, err := net.ResolveUnixAddr("unixpacket", "probe"); err != nil {
		t.Skipf("unixpacket unsupported on this platform: %v", err)
	}
	a, d, cleanup, err := testpeer.ConnPair()
	if err != nil {
		t.Fatalf("ConnPair: %v", err)
	}
	t.Cleanup(cleanup)
	return a, d
}

// TestEndToEnd_TransportMatchesInProcess is the byte-identity gate: the
// reference tree written straight into a store vs. the same tree pushed
// through initialize/capture.batch/blob.begin/blob.finish over a live
// unixpacket session.
func TestEndToEnd_TransportMatchesInProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Reference run: in-process, no transport.
	refStore := testpeer.NewMemStore()
	wantDigest, wantSize, err := testpeer.EmitFixedReceipt(ctx, refStore)
	if err != nil {
		t.Fatalf("reference emit: %v", err)
	}

	// Transport run: Serve on one end, RunBatch on the other.
	orchConn, pluginConn := connPair(t)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- capture_serve.Serve(ctx, pluginConn, testpeer.Config())
	}()

	gotStore := testpeer.NewMemStore()
	result, err := capture_serve.RunBatch(ctx, orchConn, gotStore,
		capture_serve.BatchParams{
			Target: "test://fixture",
			Captures: []capture_serve.CaptureSpec{
				{Name: "cg", Format: "fixed"},
			},
		})
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	if result.Schema != capture_serve.SchemaV2 {
		t.Errorf("result schema = %q, want %q",
			result.Schema, capture_serve.SchemaV2)
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
	want := refStore.Snapshot()
	gotBlobs := gotStore.Snapshot()
	if len(gotBlobs) != len(want) {
		t.Errorf("stored %d blobs, want %d", len(gotBlobs), len(want))
	}
	for id, wantBytes := range want {
		gotBytes, ok := gotBlobs[id]
		if !ok {
			t.Errorf("blob %s missing from transport store", id)
			continue
		}
		if !bytes.Equal(gotBytes, wantBytes) {
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
	refusing := capture_serve.NewPeer(ctx, pluginConn,
		capture_serve.HandlerFunc(func(
			context.Context, string, json.RawMessage,
		) (any, *os.File, error) {
			return nil, nil, &jsonrpc.Error{
				Code:    capture_serve.CodeUnsupportedVersion,
				Message: "unsupported-version",
			}
		}))
	defer func() { _ = refusing.Close() }()

	_, err := capture_serve.RunBatch(ctx, orchConn, testpeer.NewMemStore(),
		capture_serve.BatchParams{
			Target: "test://fixture",
			Captures: []capture_serve.CaptureSpec{
				{Name: "cg", Format: "fixed"},
			},
		})
	if err == nil {
		t.Fatal("expected unsupported-version error, got nil")
	}
	var jerr *jsonrpc.Error
	if !asJSONRPCErrorExt(err, &jerr) {
		t.Fatalf("error is %T, want *jsonrpc.Error in chain: %v", err, err)
	}
	if jerr.Code != capture_serve.CodeUnsupportedVersion {
		t.Errorf("code = %d, want %d",
			jerr.Code, capture_serve.CodeUnsupportedVersion)
	}
	if !capture_serve.IsFallbackSignal(err) {
		t.Error("unsupported-version must satisfy IsFallbackSignal")
	}
}

// TestRunBatch_BatchErrorIsNotFallback pins the other half of the
// migration policy: a failure AFTER a successful initialize is a real
// capture failure and must not read as "retry on v1".
func TestRunBatch_BatchErrorIsNotFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	orchConn, pluginConn := connPair(t)
	cfg := testpeer.Config()
	cfg.Batch = func(
		context.Context, capture_serve.BatchParams, capture_plugin.Writer,
	) (capture_serve.BatchResult, error) {
		return capture_serve.BatchResult{}, fmt.Errorf("browser crashed")
	}
	go func() { _ = capture_serve.Serve(ctx, pluginConn, cfg) }()

	_, err := capture_serve.RunBatch(ctx, orchConn, testpeer.NewMemStore(),
		capture_serve.BatchParams{
			Target: "test://fixture",
			Captures: []capture_serve.CaptureSpec{
				{Name: "cg", Format: "fixed"},
			},
		})
	if err == nil {
		t.Fatal("expected batch error, got nil")
	}
	if capture_serve.IsFallbackSignal(err) {
		t.Error("a post-handshake batch error must NOT satisfy IsFallbackSignal")
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
		serveErr <- capture_serve.Serve(ctx, pluginConn, testpeer.Config())
	}()

	_ = orchConn.Close()

	select {
	case err := <-serveErr:
		if err == nil {
			t.Error("Serve returned nil on socket close without shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Error("Serve did not return after socket close")
	}
}

// asJSONRPCErrorExt mirrors the in-package asJSONRPCError helper for the
// external test package (in-package identifiers are not visible here).
func asJSONRPCErrorExt(err error, target **jsonrpc.Error) bool {
	for err != nil {
		if je, ok := err.(*jsonrpc.Error); ok {
			*target = je
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
