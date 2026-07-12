package capture_serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// peerPair connects two Peers over an in-process unixpacket socket and
// registers cleanup. The socket binds under /tmp with a short name —
// sun_path is ~108 bytes and the devshell's deep $TMPDIR overflows it
// (the Phase 0 spike's design finding).
func peerPair(
	t *testing.T, orchHandler, pluginHandler Handler,
) (orch, plugin *Peer) {
	t.Helper()

	if _, err := net.ResolveUnixAddr("unixpacket", "probe"); err != nil {
		t.Skipf("unixpacket unsupported on this platform: %v", err)
	}

	tmpDir, err := os.MkdirTemp("/tmp", "cgpeer-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	sockPath := filepath.Join(tmpDir, "s.sock")

	ln, err := net.Listen("unixpacket", sockPath)
	if err != nil {
		t.Fatalf("listen unixpacket: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	accepted := make(chan *net.UnixConn, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			close(accepted)
			return
		}
		accepted <- conn.(*net.UnixConn)
	}()

	dialed, err := net.Dial("unixpacket", sockPath)
	if err != nil {
		t.Fatalf("dial unixpacket: %v", err)
	}
	orchConn, ok := <-accepted
	if !ok {
		dialed.Close()
		t.Fatal("accept failed")
	}

	orch = NewPeer(context.Background(), orchConn, orchHandler)
	plugin = NewPeer(
		context.Background(), dialed.(*net.UnixConn), pluginHandler,
	)
	t.Cleanup(func() {
		orch.Close()
		plugin.Close()
	})
	return orch, plugin
}

// nopHandler rejects every request; the side that only ever calls uses it.
var nopHandler = HandlerFunc(func(
	_ context.Context, method string, _ json.RawMessage,
) (any, *os.File, error) {
	return nil, nil, &jsonrpc.Error{
		Code:    jsonrpc.MethodNotFound,
		Message: "unexpected method " + method,
	}
})

// TestPeer_CallRoundTrip pins the request/response path: params reach the
// handler decoded, the result decodes on the caller.
func TestPeer_CallRoundTrip(t *testing.T) {
	pluginSide := HandlerFunc(func(
		_ context.Context, method string, params json.RawMessage,
	) (any, *os.File, error) {
		if method != MethodInitialize {
			t.Errorf("method = %q, want %q", method, MethodInitialize)
		}
		var p InitializeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, err
		}
		if len(p.ProtocolVersions) != 1 || p.ProtocolVersions[0] != SchemaV2 {
			t.Errorf("protocol_versions = %v, want [%s]",
				p.ProtocolVersions, SchemaV2)
		}
		return InitializeResult{
			Schema:   SchemaV2,
			Plugin:   PluginInfo{Name: "test-peer", Version: "0.0.0"},
			Features: Features{BlobConcurrency: 1},
		}, nil, nil
	})

	orch, _ := peerPair(t, nopHandler, pluginSide)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result InitializeResult
	err := orch.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersions: []string{SchemaV2},
		Features:         Features{BlobConcurrency: 1},
	}, &result)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Schema != SchemaV2 {
		t.Errorf("schema = %q, want %q", result.Schema, SchemaV2)
	}
	if result.Plugin.Name != "test-peer" {
		t.Errorf("plugin.name = %q, want test-peer", result.Plugin.Name)
	}
}

// TestPeer_BlobRoundTrip_FDDigestIdentity exercises the whole blob
// protocol over two Peers: the plugin side CallFDs blob.begin, receives
// the pipe write-end as a passed fd, writes the bytes, closes, and
// blob.finishes; the orchestrator side's handler digests the read end.
// Digest identity across a sequential multi-blob run proves the
// datagram<->fd association AND that the responding peer closed its copy
// of the write-end after send — without that close, the reader never sees
// EOF and this test times out (the fd-leak check).
func TestPeer_BlobRoundTrip_FDDigestIdentity(t *testing.T) {
	blobs := [][]byte{
		[]byte("first node bytes\n"),
		[]byte("second\n"),
		[]byte("third blob, a little longer than the others\n"),
	}

	// Orchestrator-side blob state: one open blob max (concurrency 1).
	type openBlob struct {
		digest chan string
		size   chan int64
	}
	var mu sync.Mutex
	open := map[int64]*openBlob{}
	var nextHandle int64

	orchSide := HandlerFunc(func(
		_ context.Context, method string, params json.RawMessage,
	) (any, *os.File, error) {
		switch method {
		case MethodBlobBegin:
			r, w, err := os.Pipe()
			if err != nil {
				return nil, nil, err
			}
			mu.Lock()
			nextHandle++
			handle := nextHandle
			if len(open) != 0 {
				mu.Unlock()
				w.Close()
				r.Close()
				return nil, nil, fmt.Errorf(
					"blob.begin with %d blob(s) already open; concurrency=1",
					len(open),
				)
			}
			ob := &openBlob{
				digest: make(chan string, 1),
				size:   make(chan int64, 1),
			}
			open[handle] = ob
			mu.Unlock()

			go func() {
				h := sha256.New()
				n, _ := io.Copy(h, r)
				r.Close()
				ob.digest <- hex.EncodeToString(h.Sum(nil))
				ob.size <- n
			}()
			// w rides the response as SCM_RIGHTS; the peer closes our copy
			// after send.
			return BlobBeginResult{Blob: handle}, w, nil
		case MethodBlobFinish:
			var p BlobFinishParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, nil, err
			}
			mu.Lock()
			ob := open[p.Blob]
			delete(open, p.Blob)
			mu.Unlock()
			if ob == nil {
				return nil, nil, fmt.Errorf("unknown blob handle %d", p.Blob)
			}
			return BlobFinishResult{ID: <-ob.digest, Size: <-ob.size}, nil, nil
		default:
			return nil, nil, fmt.Errorf("unexpected method %s", method)
		}
	})

	orch, plugin := peerPair(t, orchSide, nopHandler)
	_ = orch

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i, b := range blobs {
		var begin BlobBeginResult
		w, err := plugin.CallFD(ctx, MethodBlobBegin, BlobBeginParams{}, &begin)
		if err != nil {
			t.Fatalf("blob %d begin: %v", i, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("blob %d write to passed fd: %v", i, err)
		}
		w.Close()

		var fin BlobFinishResult
		err = plugin.Call(
			ctx, MethodBlobFinish, BlobFinishParams{Blob: begin.Blob}, &fin,
		)
		if err != nil {
			t.Fatalf("blob %d finish: %v", i, err)
		}
		wantSum := sha256.Sum256(b)
		if want := hex.EncodeToString(wantSum[:]); fin.ID != want {
			t.Errorf("blob %d digest = %s, want %s", i, fin.ID, want)
		}
		if fin.Size != int64(len(b)) {
			t.Errorf("blob %d size = %d, want %d", i, fin.Size, len(b))
		}
	}
}

// TestPeer_ErrorResponsePropagates pins that a handler's *jsonrpc.Error
// comes back to the caller with its code intact — the v2→v1 fallback
// dispatches on CodeUnsupportedVersion.
func TestPeer_ErrorResponsePropagates(t *testing.T) {
	pluginSide := HandlerFunc(func(
		_ context.Context, _ string, _ json.RawMessage,
	) (any, *os.File, error) {
		return nil, nil, &jsonrpc.Error{
			Code:    CodeUnsupportedVersion,
			Message: "unsupported-version",
		}
	})

	orch, _ := peerPair(t, nopHandler, pluginSide)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersions: []string{SchemaV2},
	}, nil)
	if err == nil {
		t.Fatal("expected error response, got nil")
	}
	var jerr *jsonrpc.Error
	if !asJSONRPCError(err, &jerr) {
		t.Fatalf("error is %T, want *jsonrpc.Error: %v", err, err)
	}
	if jerr.Code != CodeUnsupportedVersion {
		t.Errorf("code = %d, want %d", jerr.Code, CodeUnsupportedVersion)
	}
}

// TestPeer_NotificationDelivered pins the id-less path: shutdown reaches
// the handler and produces no response traffic.
func TestPeer_NotificationDelivered(t *testing.T) {
	got := make(chan string, 1)
	pluginSide := HandlerFunc(func(
		_ context.Context, method string, _ json.RawMessage,
	) (any, *os.File, error) {
		got <- method
		return nil, nil, nil
	})

	orch, _ := peerPair(t, nopHandler, pluginSide)

	if err := orch.Notify(MethodShutdown, nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case method := <-got:
		if method != MethodShutdown {
			t.Errorf("method = %q, want %q", method, MethodShutdown)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification never reached the handler")
	}
}

// TestPeer_CallHonorsContext pins that a Call against a stalled handler
// unblocks on context cancellation rather than hanging.
func TestPeer_CallHonorsContext(t *testing.T) {
	release := make(chan struct{})
	pluginSide := HandlerFunc(func(
		ctx context.Context, _ string, _ json.RawMessage,
	) (any, *os.File, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil, nil
	})

	orch, _ := peerPair(t, nopHandler, pluginSide)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(
		context.Background(), 50*time.Millisecond,
	)
	defer cancel()

	err := orch.Call(ctx, MethodCaptureBatch, BatchParams{Schema: SchemaV2}, nil)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

// TestPeer_CloseFailsPendingCalls pins that tearing the peer down releases
// a blocked caller with the terminal error instead of leaking it.
func TestPeer_CloseFailsPendingCalls(t *testing.T) {
	stall := make(chan struct{})
	pluginSide := HandlerFunc(func(
		ctx context.Context, _ string, _ json.RawMessage,
	) (any, *os.File, error) {
		select {
		case <-stall:
		case <-ctx.Done():
		}
		return nil, nil, nil
	})

	orch, plugin := peerPair(t, nopHandler, pluginSide)
	t.Cleanup(func() { close(stall) })

	callErr := make(chan error, 1)
	go func() {
		callErr <- orch.Call(
			context.Background(), MethodCaptureBatch,
			BatchParams{Schema: SchemaV2}, nil,
		)
	}()

	// Let the request hit the wire before tearing down.
	time.Sleep(20 * time.Millisecond)
	plugin.Close()

	select {
	case err := <-callErr:
		if err == nil {
			t.Fatal("expected error after peer close, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call still blocked after peer close")
	}
}

// asJSONRPCError unwraps err looking for a *jsonrpc.Error. Local helper so
// the test does not depend on which errors package produced the chain.
func asJSONRPCError(err error, target **jsonrpc.Error) bool {
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
