package traversal_serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/jsonrpc"
)

// newPeerPair wires two Peers over a net.Pipe (the peer takes any
// io.ReadWriteCloser — RFC 0013 §Framing needs only a byte stream):
// the returned client is handler-less, the server serves handler
// (which may be nil to exercise the client-only default).
func newPeerPair(t *testing.T, handler Handler) (client, server *Peer) {
	t.Helper()

	clientConn, serverConn := net.Pipe()

	client = NewPeer(clientConn)
	server = NewPeer(serverConn, WithHandler(handler))

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	return client, server
}

// echoHandler answers every request with its own method and params.
type echoHandler struct{}

func (echoHandler) Handle(
	_ context.Context, method string, params json.RawMessage,
) (any, error) {
	return map[string]any{"method": method, "params": params}, nil
}

// recordingHandler reports every dispatched method on calls.
type recordingHandler struct {
	calls chan string
}

func (h *recordingHandler) Handle(
	_ context.Context, method string, _ json.RawMessage,
) (any, error) {
	h.calls <- method
	return map[string]bool{"ok": true}, nil
}

// failingHandler fails every request with a fixed error.
type failingHandler struct {
	err error
}

func (h failingHandler) Handle(
	_ context.Context, _ string, _ json.RawMessage,
) (any, error) {
	return nil, h.err
}

// blockingHandler signals entry on entered, then blocks until the
// peer's handler context cancels (Close).
type blockingHandler struct {
	entered chan struct{}
}

func (h *blockingHandler) Handle(
	ctx context.Context, _ string, _ json.RawMessage,
) (any, error) {
	select {
	case h.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPeerCallRoundTrip(t *testing.T) {
	client, _ := newPeerPair(t, echoHandler{})

	var result struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}

	err := client.Call(
		context.Background(),
		MethodNodesList,
		NodesListParams{URI: "fj://forge.example/"},
		&result,
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Method != MethodNodesList {
		t.Errorf("method = %q, want %q", result.Method, MethodNodesList)
	}

	if !strings.Contains(string(result.Params), "fj://forge.example/") {
		t.Errorf("params did not round trip: %s", result.Params)
	}
}

// TestPeerPipelinedCallsCorrelateOutOfOrder pins the id-correlation
// requirement (RFC 0013 §Framing: the host MUST correlate responses by
// id, never by arrival order): a raw NDJSON counterpart collects two
// pipelined requests and answers them in reverse arrival order.
func TestPeerPipelinedCallsCorrelateOutOfOrder(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	client := NewPeer(clientConn)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverConn.Close()
	})

	go func() {
		scanner := bufio.NewScanner(serverConn)

		var msgs []*jsonrpc.Message
		for len(msgs) < 2 && scanner.Scan() {
			msg := &jsonrpc.Message{}
			if err := json.Unmarshal(scanner.Bytes(), msg); err != nil {
				return
			}
			msgs = append(msgs, msg)
		}

		for i := len(msgs) - 1; i >= 0; i-- {
			resp, err := jsonrpc.NewResponse(
				*msgs[i].ID, map[string]string{"echo": msgs[i].Method},
			)
			if err != nil {
				return
			}

			raw, err := json.Marshal(resp)
			if err != nil {
				return
			}

			if _, err := serverConn.Write(append(raw, '\n')); err != nil {
				return
			}
		}
	}()

	methods := []string{MethodRootsList, MethodFacetVersion}
	callErrs := make([]error, len(methods))
	echoes := make([]struct {
		Echo string `json:"echo"`
	}, len(methods))

	var wg sync.WaitGroup
	for i := range methods {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callErrs[i] = client.Call(
				context.Background(), methods[i], struct{}{}, &echoes[i],
			)
		}()
	}
	wg.Wait()

	for i, method := range methods {
		if callErrs[i] != nil {
			t.Fatalf("call %s: %v", method, callErrs[i])
		}

		if echoes[i].Echo != method {
			t.Errorf("call %s correlated to %q", method, echoes[i].Echo)
		}
	}
}

func TestPeerNotifyReachesHandlerWithoutResponse(t *testing.T) {
	handler := &recordingHandler{calls: make(chan string, 2)}
	client, _ := newPeerPair(t, handler)

	if err := client.Notify(MethodShutdown, struct{}{}); err != nil {
		t.Fatal(err)
	}

	select {
	case method := <-handler.calls:
		if method != MethodShutdown {
			t.Errorf("handler got %q, want %q", method, MethodShutdown)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification never reached the handler")
	}

	// The serve loop is sequential, so a subsequent call round-tripping
	// cleanly proves the notification produced no response line (a stray
	// id-less reply would have poisoned the client's read loop).
	var result map[string]bool
	err := client.Call(
		context.Background(), MethodRootsList, struct{}{}, &result,
	)
	if err != nil {
		t.Fatal(err)
	}

	if client.Err() != nil {
		t.Errorf("client peer died: %v", client.Err())
	}
}

func TestPeerHandlerRPCErrorSurfacesCode(t *testing.T) {
	client, _ := newPeerPair(t, failingHandler{
		err: &RPCError{Code: CodeInvalidParams, Message: "scheme mismatch"},
	})

	err := client.Call(
		context.Background(),
		MethodNodesList,
		NodesListParams{URI: "nope://x"},
		nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	code, ok := CodeOf(err)
	if !ok || code != CodeInvalidParams {
		t.Errorf("CodeOf = %d, %t, want %d, true", code, ok, CodeInvalidParams)
	}

	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error is not errors.As-compatible: %v", err)
	}

	if rpcErr.Message != "scheme mismatch" {
		t.Errorf("message = %q, want %q", rpcErr.Message, "scheme mismatch")
	}
}

// TestPeerHandlerPlainErrorMapsToInternal pins the RFC 0013 §Errors
// default: a handler error that is not *RPCError crosses the wire as
// -32603 internal.
func TestPeerHandlerPlainErrorMapsToInternal(t *testing.T) {
	client, _ := newPeerPair(t, failingHandler{
		err: errors.ErrorWithStackf("boom"),
	})

	err := client.Call(
		context.Background(), MethodRootsList, struct{}{}, nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	if code, ok := CodeOf(err); !ok || code != -32603 {
		t.Errorf("CodeOf = %d, %t, want -32603, true", code, ok)
	}
}

func TestPeerRemoteCloseFailsPendingCall(t *testing.T) {
	handler := &blockingHandler{entered: make(chan struct{}, 1)}
	client, server := newPeerPair(t, handler)

	callErr := make(chan error, 1)
	go func() {
		callErr <- client.Call(
			context.Background(), MethodRootsList, struct{}{}, nil,
		)
	}()

	<-handler.entered
	_ = server.Close()

	select {
	case err := <-callErr:
		if err == nil {
			t.Error("pending call returned nil after remote close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call never unblocked after remote close")
	}

	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done() never closed after remote close")
	}

	if client.Err() == nil {
		t.Error("Err() = nil after remote close")
	}
}

func TestPeerCallContextCancelUnblocks(t *testing.T) {
	handler := &blockingHandler{entered: make(chan struct{}, 1)}
	client, _ := newPeerPair(t, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callErr := make(chan error, 1)
	go func() {
		callErr <- client.Call(ctx, MethodRootsList, struct{}{}, nil)
	}()

	<-handler.entered
	cancel()

	select {
	case err := <-callErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled in chain", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call never unblocked on ctx cancel")
	}

	// A cancelled call abandons its response; the session stays alive
	// (RFC 0013 §Session lifecycle).
	if client.Err() != nil {
		t.Errorf("peer died on ctx cancel: %v", client.Err())
	}
}

func TestPeerOversizedLineKillsPeer(t *testing.T) {
	conn, raw := net.Pipe()

	peer := NewPeer(conn)
	t.Cleanup(func() {
		_ = peer.Close()
		_ = raw.Close()
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// One line beyond the bound, never terminated: the scanner must
		// refuse it rather than buffer without limit.
		_, _ = raw.Write(make([]byte, maxLineBytes+1))
	}()

	select {
	case <-peer.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("peer did not die on an oversized line")
	}

	err := peer.Err()
	if err == nil {
		t.Fatal("Err() = nil after oversized line")
	}

	if !strings.Contains(err.Error(), fmt.Sprintf("%d", maxLineBytes)) {
		t.Errorf("error does not mention the %d-byte bound: %v",
			maxLineBytes, err)
	}

	// Unblock the writer's remaining bytes before waiting it out.
	_ = peer.Close()
	_ = raw.Close()
	wg.Wait()
}

func TestPeerNilHandlerAnswersMethodNotFound(t *testing.T) {
	client, _ := newPeerPair(t, nil)

	err := client.Call(
		context.Background(),
		MethodNodesList,
		NodesListParams{URI: "fj://forge.example/"},
		nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	if code, ok := CodeOf(err); !ok || code != CodeMethodNotFound {
		t.Errorf("CodeOf = %d, %t, want %d, true",
			code, ok, CodeMethodNotFound)
	}
}
