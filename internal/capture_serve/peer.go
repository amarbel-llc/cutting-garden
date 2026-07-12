package capture_serve

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/jsonrpc"
)

// maxDatagram bounds one received JSON-RPC message. Control messages MUST
// fit one SEQPACKET datagram (RFC 0008 §Message size); capture.batch
// params scale with the capture list but stay far below this in practice.
const maxDatagram = 256 << 10

// requestQueueDepth buffers incoming requests between the read loop and
// the sequential serve loop. Under the protocol's blob_concurrency=1
// discipline at most one request is ever in flight per direction, so this
// never fills; the buffer only keeps the read loop free to correlate
// responses while a handler runs.
const requestQueueDepth = 16

// Handler serves a peer's incoming JSON-RPC requests and notifications,
// one at a time in arrival order. result is marshaled as the JSON-RPC
// response; oob, when non-nil, is attached to that response datagram as an
// SCM_RIGHTS fd (blob.begin's pipe write-end) and closed by the peer after
// the send — the kernel duplicates it into the receiver at sendmsg time. A
// returned *jsonrpc.Error is sent verbatim; any other error becomes an
// InternalError response. For a notification the return values are
// discarded (there is no response to carry them).
type Handler interface {
	Handle(
		ctx context.Context, method string, params json.RawMessage,
	) (result any, oob *os.File, err error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(
	ctx context.Context, method string, params json.RawMessage,
) (any, *os.File, error)

func (f HandlerFunc) Handle(
	ctx context.Context, method string, params json.RawMessage,
) (any, *os.File, error) {
	return f(ctx, method, params)
}

// incoming pairs one received datagram's parsed message with any
// SCM_RIGHTS files that rode its ancillary data.
type incoming struct {
	msg   *jsonrpc.Message
	files []*os.File
}

// Peer is one side of an RFC 0008 session: a JSON-RPC peer over a
// connected SOCK_SEQPACKET AF_UNIX socket, framing exactly one message
// per datagram. Both sides are simultaneously client and server — the
// orchestrator calls initialize/capture.batch while serving
// blob.begin/blob.finish, and vice versa — so a Peer runs a read loop
// (correlating responses to pending Calls) and a serve loop (dispatching
// requests to its Handler sequentially, preserving arrival order — the
// wire-level blob_concurrency=1 guarantee).
type Peer struct {
	conn    *net.UnixConn
	handler Handler

	// ctx is the base context handler dispatch runs under; canceled on
	// Close so a blocked handler unwinds with the peer.
	ctx    context.Context
	cancel context.CancelFunc

	// writeMu serializes datagram writes: handler responses and outgoing
	// calls interleave on the one socket.
	writeMu sync.Mutex

	nextID    atomic.Int64
	pendingMu sync.Mutex
	pending   map[string]chan incoming

	requests chan incoming

	// done closes when the read loop exits; err (set before the close)
	// is the terminal reason every pending and future Call reports.
	done chan struct{}
	err  error

	// serveDone closes when the serve loop has drained every request
	// queued before the read loop exited — the "all notifications
	// delivered" point Serve consults to tell a graceful shutdown from a
	// dropped socket.
	serveDone chan struct{}

	closeOnce sync.Once
}

// Done closes once the peer's read loop has exited (the socket closed or
// errored); Err then reports why. Before that point Err returns nil.
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

// NewPeer wraps conn as an RFC 0008 peer and starts its read and serve
// loops. conn MUST be a connected "unixpacket" (SOCK_SEQPACKET) socket;
// handler serves the counterpart's requests. Close the peer (or cancel
// nothing — closing the conn suffices) to stop both loops.
func NewPeer(
	ctx context.Context, conn *net.UnixConn, handler Handler,
) *Peer {
	ctx, cancel := context.WithCancel(ctx)
	p := &Peer{
		conn:      conn,
		handler:   handler,
		ctx:       ctx,
		cancel:    cancel,
		pending:   make(map[string]chan incoming),
		requests:  make(chan incoming, requestQueueDepth),
		done:      make(chan struct{}),
		serveDone: make(chan struct{}),
	}
	go p.readLoop()
	go p.serveLoop()
	return p
}

// Close tears the session down: the socket closes (unblocking the read
// loop), the handler context cancels, and every pending Call fails.
func (p *Peer) Close() error {
	p.closeOnce.Do(func() {
		p.cancel()
		p.conn.Close()
	})
	return nil
}

// Call sends method(params) as a request and decodes the response's
// result into result (which may be nil to discard it). A JSON-RPC error
// response is returned as *jsonrpc.Error. Any SCM_RIGHTS files on the
// response are closed — use CallFD for blob.begin, whose response carries
// the pipe write-end.
func (p *Peer) Call(
	ctx context.Context, method string, params, result any,
) error {
	files, err := p.call(ctx, method, params, result)
	closeFiles(files)
	return err
}

// CallFD is Call for a request whose response datagram carries exactly
// one SCM_RIGHTS fd (blob.begin): the passed file is returned alongside
// the decoded result. A response with no fd is an error.
func (p *Peer) CallFD(
	ctx context.Context, method string, params, result any,
) (*os.File, error) {
	files, err := p.call(ctx, method, params, result)
	if err != nil {
		closeFiles(files)
		return nil, err
	}
	if len(files) != 1 {
		closeFiles(files)
		return nil, errors.ErrorWithStackf(
			"%s response carried %d passed fds, want exactly 1",
			method, len(files),
		)
	}
	return files[0], nil
}

// Notify sends method(params) as a notification (no id, no response).
func (p *Peer) Notify(method string, params any) error {
	msg, err := jsonrpc.NewNotification(method, params)
	if err != nil {
		return errors.Wrap(err)
	}
	return p.writeMsg(msg, nil)
}

func (p *Peer) call(
	ctx context.Context, method string, params, result any,
) ([]*os.File, error) {
	id := jsonrpc.NewNumberID(p.nextID.Add(1))
	msg, err := jsonrpc.NewRequest(id, method, params)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	ch := make(chan incoming, 1)
	key := id.String()
	p.pendingMu.Lock()
	p.pending[key] = ch
	p.pendingMu.Unlock()
	defer func() {
		p.pendingMu.Lock()
		delete(p.pending, key)
		p.pendingMu.Unlock()
	}()

	if err := p.writeMsg(msg, nil); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, errors.Wrapf(ctx.Err(), "call %s", method)
	case <-p.done:
		// %s, not a wrap: a clean remote close terminates the read loop
		// with bare io.EOF, which dewey's errors refuses to wrap.
		return nil, errors.ErrorWithStackf(
			"call %s: peer closed: %s", method, p.err,
		)
	case inc := <-ch:
		if inc.msg.Error != nil {
			return inc.files, inc.msg.Error
		}
		if result != nil {
			if err := json.Unmarshal(inc.msg.Result, result); err != nil {
				return inc.files, errors.Wrapf(
					err, "decode %s result", method,
				)
			}
		}
		return inc.files, nil
	}
}

// writeMsg marshals msg and sends it as one datagram, with oobFile (when
// non-nil) attached as SCM_RIGHTS. The caller retains ownership of
// oobFile — the kernel duplicates the descriptor at sendmsg time, so the
// caller closes its copy after this returns.
func (p *Peer) writeMsg(msg *jsonrpc.Message, oobFile *os.File) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return errors.Wrap(err)
	}
	var oob []byte
	if oobFile != nil {
		oob = syscall.UnixRights(int(oobFile.Fd()))
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, _, err := p.conn.WriteMsgUnix(data, oob, nil); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// readLoop receives datagrams until the socket errors or closes: it
// routes responses to their pending Call and queues requests and
// notifications for the serve loop. Its terminal error becomes p.err.
func (p *Peer) readLoop() {
	defer close(p.requests)

	buf := make([]byte, maxDatagram)
	// One fd per datagram is the protocol max (blob.begin's write-end).
	oob := make([]byte, syscall.CmsgSpace(4))

	for {
		n, oobn, flags, _, err := p.conn.ReadMsgUnix(buf, oob)
		if err != nil {
			p.terminate(err)
			return
		}
		if flags&syscall.MSG_TRUNC != 0 {
			p.terminate(errors.ErrorWithStackf(
				"datagram exceeds %d-byte bound (RFC 0008 §Message size)",
				maxDatagram,
			))
			return
		}
		files, ferr := parseFiles(oob[:oobn], flags)
		if ferr != nil {
			p.terminate(ferr)
			return
		}

		msg := &jsonrpc.Message{}
		if err := json.Unmarshal(buf[:n], msg); err != nil {
			closeFiles(files)
			p.terminate(errors.Wrapf(err, "malformed datagram"))
			return
		}

		switch {
		case msg.IsResponse():
			p.deliver(incoming{msg: msg, files: files})
		case msg.IsRequest() || msg.IsNotification():
			p.requests <- incoming{msg: msg, files: files}
		default:
			closeFiles(files)
			p.terminate(errors.ErrorWithStackf(
				"datagram is neither request, notification, nor response",
			))
			return
		}
	}
}

// deliver hands a response to its pending Call; an unmatched response's
// files are closed and the message dropped (the caller may have timed out
// and unregistered).
func (p *Peer) deliver(inc incoming) {
	key := inc.msg.ID.String()
	p.pendingMu.Lock()
	ch := p.pending[key]
	delete(p.pending, key)
	p.pendingMu.Unlock()
	if ch == nil {
		closeFiles(inc.files)
		return
	}
	ch <- inc
}

// terminate records the read loop's terminal error and releases every
// waiter. Socket-close during shutdown is the ordinary exit path.
func (p *Peer) terminate(err error) {
	p.err = err
	close(p.done)
	p.cancel()
}

// serveLoop dispatches queued requests to the handler strictly one at a
// time in arrival order — the peer-level realization of the
// blob_concurrency=1 sequential invariant.
func (p *Peer) serveLoop() {
	defer close(p.serveDone)
	for inc := range p.requests {
		p.serveOne(inc)
	}
}

func (p *Peer) serveOne(inc incoming) {
	// Only responses carry fds under RFC 0008; drop any that arrived on a
	// request rather than leaking them.
	closeFiles(inc.files)

	result, oobFile, err := p.handler.Handle(
		p.ctx, inc.msg.Method, inc.msg.Params,
	)
	if inc.msg.IsNotification() {
		if oobFile != nil {
			oobFile.Close()
		}
		return
	}

	var resp *jsonrpc.Message
	var berr error
	if err != nil {
		code, text := jsonrpc.InternalError, err.Error()
		var jerr *jsonrpc.Error
		if errors.As(err, &jerr) {
			code, text = jerr.Code, jerr.Message
		}
		resp, berr = jsonrpc.NewErrorResponse(*inc.msg.ID, code, text, nil)
	} else {
		resp, berr = jsonrpc.NewResponse(*inc.msg.ID, result)
	}
	if berr != nil {
		if oobFile != nil {
			oobFile.Close()
		}
		return
	}

	// Send, then close our copy of the attached fd: the kernel dup'd it
	// into the peer at sendmsg time, and for blob.begin's pipe write-end
	// the reader's EOF depends on this copy closing.
	p.writeMsg(resp, oobFile)
	if oobFile != nil {
		oobFile.Close()
	}
}

// parseFiles extracts SCM_RIGHTS-passed descriptors from one datagram's
// ancillary bytes, wrapping each as *os.File. MSG_CTRUNC means the kernel
// dropped part of the ancillary payload — descriptors may already be
// leaked into this process, so it is terminal.
func parseFiles(oob []byte, flags int) ([]*os.File, error) {
	if flags&syscall.MSG_CTRUNC != 0 {
		return nil, errors.ErrorWithStackf(
			"ancillary data truncated (MSG_CTRUNC): passed fd lost",
		)
	}
	if len(oob) == 0 {
		return nil, nil
	}
	scms, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, errors.Wrapf(err, "parse control message")
	}
	var files []*os.File
	for i := range scms {
		fds, err := syscall.ParseUnixRights(&scms[i])
		if err != nil {
			closeFiles(files)
			return nil, errors.Wrapf(err, "parse SCM_RIGHTS")
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "capture-serve-passed-fd"))
		}
	}
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}
