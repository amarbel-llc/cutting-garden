package traversal_serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// Scanner sizing for one received NDJSON line (RFC 0013 §Framing). The
// stream imposes no datagram bound, but the read buffer needs a
// ceiling: maxLineBytes comfortably covers the only large payloads —
// inline base64 bodies on leaf.read and the mutation methods — while
// refusing an unbounded line rather than buffering it forever.
const (
	scanBufferInitial = 64 << 10
	maxLineBytes      = 16 << 20
)

// requestQueueDepth buffers incoming requests between the read loop and
// the sequential serve loop. The host is the only request initiator in
// v1 and pipelines at most a handful of requests; the buffer just keeps
// the read loop free to correlate responses while a handler runs.
const requestQueueDepth = 16

// RPCError is a JSON-RPC error crossing the peer in either direction: a
// Handler returns *RPCError to pin its error response's wire code and
// message, and Call surfaces a received error response as *RPCError so
// callers can branch on the RFC 0013 §Errors codes
// (CodeUnsupportedVersion, CodeInvalidParams, …) via errors.As or
// CodeOf.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// CodeOf extracts the JSON-RPC error code from err's chain; ok is false
// when no *RPCError is present (a transport-level failure rather than a
// wire error response).
func CodeOf(err error) (code int, ok bool) {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code, true
	}

	return 0, false
}

// Handler serves a peer's incoming JSON-RPC requests and notifications,
// one at a time in arrival order (a plugin MAY process pipelined
// requests sequentially — RFC 0013 §Framing). result is marshaled as
// the JSON-RPC response; a returned *RPCError is sent with its code and
// message verbatim, any other error becomes a -32603 internal error
// (RFC 0013 §Errors). For a notification (shutdown) the return values
// are discarded — there is no response to carry them.
type Handler interface {
	Handle(
		ctx context.Context, method string, params json.RawMessage,
	) (result any, err error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(
	ctx context.Context, method string, params json.RawMessage,
) (any, error)

func (f HandlerFunc) Handle(
	ctx context.Context, method string, params json.RawMessage,
) (any, error) {
	return f(ctx, method, params)
}

// PeerOption configures NewPeer.
type PeerOption func(*Peer)

// WithHandler makes the peer serve incoming requests — the plugin role.
// Without it the peer is client-only (the host role: the sole request
// initiator in v1) and answers any incoming request with
// CodeMethodNotFound.
func WithHandler(handler Handler) PeerOption {
	return func(p *Peer) { p.handler = handler }
}

// Peer is one side of an RFC 0013 session: a JSON-RPC peer over a byte
// stream, framing exactly one message per newline-terminated line
// (§Framing). Unlike capture_serve's datagram peer there is no fd
// passing and no message-size datagram truncation — the transport is
// any io.ReadWriteCloser (an AF_UNIX SOCK_STREAM conn in production,
// net.Pipe in tests). The read loop correlates responses to pending
// Calls by id — never by arrival order — while the serve loop
// dispatches requests to the Handler sequentially.
type Peer struct {
	rw      io.ReadWriteCloser
	handler Handler

	// ctx is the base context handler dispatch runs under; canceled on
	// Close so a blocked handler unwinds with the peer.
	ctx    context.Context
	cancel context.CancelFunc

	// writeMu serializes line writes: handler responses and outgoing
	// calls interleave on the one stream, and a torn line is a framing
	// violation.
	writeMu sync.Mutex

	nextID    atomic.Int64
	pendingMu sync.Mutex
	pending   map[string]chan *jsonrpc.Message

	requests chan *jsonrpc.Message

	// done closes when the read loop exits; err (set before the close)
	// is the terminal reason every pending and future Call reports.
	done chan struct{}
	err  error

	// serveDone closes when the serve loop has drained every request
	// queued before the read loop exited — the "shutdown notification
	// delivered" point a plugin-side Serve consults.
	serveDone chan struct{}

	closeOnce sync.Once
}

// NewPeer wraps rw as an RFC 0013 peer and starts its read and serve
// loops. Close the peer (or just close rw) to stop both.
func NewPeer(rw io.ReadWriteCloser, options ...PeerOption) *Peer {
	ctx, cancel := context.WithCancel(context.Background())

	p := &Peer{
		rw:        rw,
		ctx:       ctx,
		cancel:    cancel,
		pending:   make(map[string]chan *jsonrpc.Message),
		requests:  make(chan *jsonrpc.Message, requestQueueDepth),
		done:      make(chan struct{}),
		serveDone: make(chan struct{}),
	}

	for _, option := range options {
		option(p)
	}

	go p.readLoop()
	go p.serveLoop()

	return p
}

// Close tears the session down: the stream closes (unblocking the read
// loop), the handler context cancels, and every pending Call fails.
func (p *Peer) Close() error {
	p.closeOnce.Do(func() {
		p.cancel()
		p.rw.Close()
	})

	return nil
}

// Done closes once the peer's read loop has exited (the stream closed
// or errored); Err then reports why. Before that point Err returns nil.
func (p *Peer) Done() <-chan struct{} { return p.done }

// Err reports the read loop's terminal error, nil while the peer is
// still live.
func (p *Peer) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

// Call sends method(params) as a request and decodes the response's
// result into result (which may be nil to discard it). A JSON-RPC error
// response is returned as *RPCError. A canceled ctx abandons the
// response without killing the session (RFC 0013 §Session lifecycle:
// v1 has no per-request cancel).
func (p *Peer) Call(
	ctx context.Context, method string, params, result any,
) error {
	id := jsonrpc.NewNumberID(p.nextID.Add(1))

	msg, err := jsonrpc.NewRequest(id, method, params)
	if err != nil {
		return errors.Wrap(err)
	}

	ch := make(chan *jsonrpc.Message, 1)
	key := id.String()
	p.pendingMu.Lock()
	p.pending[key] = ch
	p.pendingMu.Unlock()
	defer func() {
		p.pendingMu.Lock()
		delete(p.pending, key)
		p.pendingMu.Unlock()
	}()

	if err := p.writeLine(msg); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return errors.Wrapf(ctx.Err(), "call %s", method)
	case <-p.done:
		// %s, not a wrap: a clean remote close terminates the read loop
		// with bare io.EOF, which dewey's errors refuses to wrap.
		return errors.ErrorWithStackf(
			"call %s: peer closed: %s", method, p.err,
		)
	case resp := <-ch:
		if resp.Error != nil {
			return &RPCError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
			}
		}

		if result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return errors.Wrapf(err, "decode %s result", method)
			}
		}

		return nil
	}
}

// Notify sends method(params) as a notification (no id, no response).
func (p *Peer) Notify(method string, params any) error {
	msg, err := jsonrpc.NewNotification(method, params)
	if err != nil {
		return errors.Wrap(err)
	}

	return p.writeLine(msg)
}

// writeLine marshals msg and sends it as one newline-terminated line
// (RFC 0013 §Framing: standard JSON string escaping guarantees the
// serialized value contains no raw newline).
func (p *Peer) writeLine(msg *jsonrpc.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return errors.Wrap(err)
	}

	data = append(data, '\n')

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	if _, err := p.rw.Write(data); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

// readLoop scans lines until the stream errors or closes: it routes
// responses to their pending Call and queues requests and notifications
// for the serve loop. Its terminal reason becomes p.err.
func (p *Peer) readLoop() {
	defer close(p.requests)

	scanner := bufio.NewScanner(p.rw)
	scanner.Buffer(make([]byte, 0, scanBufferInitial), maxLineBytes)

	for scanner.Scan() {
		msg := &jsonrpc.Message{}
		if err := json.Unmarshal(scanner.Bytes(), msg); err != nil {
			p.terminate(errors.Wrapf(err, "malformed line"))
			return
		}

		switch {
		case msg.IsResponse():
			p.deliver(msg)
		case msg.IsRequest() || msg.IsNotification():
			p.requests <- msg
		default:
			p.terminate(errors.ErrorWithStackf(
				"line is neither request, notification, nor response",
			))
			return
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			p.terminate(errors.ErrorWithStackf(
				"line exceeds %d-byte bound (RFC 0013 §Framing): %s",
				maxLineBytes, err,
			))
			return
		}

		p.terminate(err)
		return
	}

	// Clean end of stream. Recorded bare — Call formats p.err with %s
	// because dewey's errors refuses to wrap io.EOF.
	p.terminate(io.EOF)
}

// deliver hands a response to its pending Call; an unmatched response
// is dropped (the caller may have canceled and unregistered — the
// abandoned-response path of RFC 0013 §Session lifecycle).
func (p *Peer) deliver(msg *jsonrpc.Message) {
	key := msg.ID.String()
	p.pendingMu.Lock()
	ch := p.pending[key]
	delete(p.pending, key)
	p.pendingMu.Unlock()

	if ch == nil {
		return
	}

	ch <- msg
}

// terminate records the read loop's terminal reason and releases every
// waiter. Stream-close during shutdown is the ordinary exit path.
func (p *Peer) terminate(err error) {
	p.err = err
	close(p.done)
	p.cancel()
}

// serveLoop dispatches queued requests to the handler strictly one at a
// time in arrival order — the sequential processing RFC 0013 §Framing
// permits a plugin.
func (p *Peer) serveLoop() {
	defer close(p.serveDone)

	for msg := range p.requests {
		p.serveOne(msg)
	}
}

func (p *Peer) serveOne(msg *jsonrpc.Message) {
	var result any
	var err error

	if p.handler != nil {
		result, err = p.handler.Handle(p.ctx, msg.Method, msg.Params)
	} else {
		// Client-only peer: with the host as sole initiator (RFC 0013
		// §Framing) any incoming request is unknown by definition.
		err = &RPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("method %q not found", msg.Method),
		}
	}

	if msg.IsNotification() {
		return
	}

	var resp *jsonrpc.Message
	var buildErr error
	if err != nil {
		code, text := jsonrpc.InternalError, err.Error()

		var rpcErr *RPCError
		if errors.As(err, &rpcErr) {
			code, text = rpcErr.Code, rpcErr.Message
		}

		resp, buildErr = jsonrpc.NewErrorResponse(*msg.ID, code, text, nil)
	} else {
		resp, buildErr = jsonrpc.NewResponse(*msg.ID, result)
	}

	if buildErr != nil {
		return
	}

	// Best-effort: a write failure means the stream is dying and the
	// read loop will surface it as the peer's terminal error.
	_ = p.writeLine(resp)
}
