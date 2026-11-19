// Package bridge relays an MCP stdio stream to the host's shared
// `cutting-garden serve-mcp` daemon over its unix socket. It imports only the
// standard library, so a binary that links it (cmd/cutting-garden-bridge, the
// per-session process) carries none of the plugin registry the full
// cutting-garden binary initialises.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// DefaultSocket is $XDG_RUNTIME_DIR/cutting-garden/mcp.sock, falling back to
// the temp dir only when XDG_RUNTIME_DIR is unset.
func DefaultSocket() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "cutting-garden", "mcp.sock")
}

// helloMethod names the one JSON-RPC notification a bridge may send first. It
// is a notification, so a server that does not know it sends no reply, and the
// daemon strips it before the MCP server sees the stream.
const helloMethod = "cutting-garden/hello"

// maxPrincipal bounds a principal so a hostile peer cannot flood the log.
const maxPrincipal = 128

// HelloPrefix is what every hello line starts with; the daemon peeks for it.
const HelloPrefix = `{"jsonrpc":"2.0","method":"` + helloMethod + `"`

// Principal is the session identity a bridge announces: the clown session id,
// else the spinclass one, else "" (no hello is sent).
func Principal() string {
	for _, name := range []string{"CLOWN_SESSION_ID", "SPINCLASS_SESSION_ID"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// HelloLine renders the hello notification for principal, newline-terminated,
// or "" when principal is empty.
func HelloLine(principal string) string {
	if principal == "" {
		return ""
	}
	b, _ := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Principal string `json:"principal"`
		} `json:"params"`
	}{JSONRPC: "2.0", Method: helloMethod, Params: struct {
		Principal string `json:"principal"`
	}{Principal: principal}})
	return string(b) + "\n"
}

// ParseHello reads a hello line and returns its principal, cleaned of control
// characters and truncated. ok is false when line is not a hello.
func ParseHello(line []byte) (principal string, ok bool) {
	if !bytes.HasPrefix(line, []byte(HelloPrefix)) {
		return "", false
	}
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Principal string `json:"principal"`
		} `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &msg); err != nil || msg.Method != helloMethod {
		return "", false
	}
	p := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, msg.Params.Principal)
	if len(p) > maxPrincipal {
		p = p[:maxPrincipal]
	}
	return p, true
}

// ReconnectFor is how long Run keeps trying to reach the server again after
// losing it mid-session, before giving up with DaemonLost. A var so the
// bridge's flag and the tests can set it.
var ReconnectFor = 10 * time.Second

// Outcome is how a relay ended.
type Outcome int

const (
	// NoDaemon: the socket could not be dialed; nothing was read from in, so
	// the caller can serve in-process instead.
	NoDaemon Outcome = iota
	// Done: the client closed its input and the daemon drained and hung up, or
	// ctx ended.
	Done
	// DaemonLost: the daemon hung up while the client was still connected and
	// did not come back within ReconnectFor. The caller should exit non-zero so
	// its host sees the child die.
	DaemonLost
)

// message is the slice of a JSON-RPC message the relay needs.
type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func parseMessage(line []byte) (m message, ok bool) {
	if json.Unmarshal(line, &m) != nil {
		return message{}, false
	}
	return m, true
}

func (m message) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

// isRequest: a request from the client that expects exactly one reply.
func (m message) isRequest() bool { return m.Method != "" && m.hasID() }

// isResponse: a reply to a request.
func (m message) isResponse() bool { return m.Method == "" && m.hasID() }

// relay carries what a session needs to survive the server's restart: the
// handshake the client already made, and the requests still waiting for a
// reply.
type relay struct {
	socket, hello string
	out, diag     io.Writer

	// initLine and initializedLine are the client's handshake, replayed to a
	// new server session. initDone says initialize was already answered, so the
	// replayed reply is swallowed (the client has its reply).
	initLine, initializedLine []byte
	initID                    string
	initDone                  bool
	swallow                   string

	// pending maps a client request's id to its raw form until it is answered.
	pending map[string]json.RawMessage
}

// Run relays in/out to the server listening on socket, sending hello first
// when it is non-empty. If the server goes away mid-session (a restart, an
// upgrade, a crash) the relay reconnects for up to ReconnectFor, replays the
// client's initialize handshake so the new server session is ready, and
// answers any request that was waiting with a JSON-RPC error; the client sees
// no EOF. Diagnostics go to diag, never out: stdout carries only protocol
// frames.
func Run(
	ctx context.Context, socket, hello string, in io.Reader, out io.Writer, diag io.Writer,
) Outcome {
	conn, err := dial(ctx, socket)
	if err != nil {
		fmt.Fprintf(diag, "cutting-garden mcp: no daemon at %s (%v); serving in-process\n", socket, err)
		return NoDaemon
	}
	r := &relay{socket: socket, hello: hello, out: out, diag: diag, pending: map[string]json.RawMessage{}}
	clientLines := make(chan []byte)
	go readLines(in, clientLines)

	for first := true; ; first = false {
		outcome, lost := r.session(ctx, conn, clientLines, first)
		// A close error on a relayed socket has no one to report to.
		_ = conn.Close()
		if !lost {
			return outcome
		}
		fmt.Fprintf(diag, "cutting-garden mcp: daemon at %s lost; reconnecting for up to %s\n", socket, ReconnectFor)
		r.failPending()
		var next net.Conn
		next, outcome = r.reconnect(ctx)
		if next == nil {
			if outcome == DaemonLost {
				fmt.Fprintf(diag, "cutting-garden mcp: daemon at %s did not come back; giving up\n", socket)
			}
			return outcome
		}
		fmt.Fprintf(diag, "cutting-garden mcp: reconnected to %s\n", socket)
		conn = next
	}
}

func dial(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}

// readLines sends each newline-terminated line of r to ch, then closes ch.
func readLines(r io.Reader, ch chan<- []byte) {
	defer close(ch)
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			ch <- line
		}
		if err != nil {
			return
		}
	}
}

// session relays one connection until the client ends it (lost=false) or the
// server does (lost=true).
func (r *relay) session(
	ctx context.Context, conn net.Conn, clientLines <-chan []byte, first bool,
) (outcome Outcome, lost bool) {
	srv := make(chan []byte)
	go readLines(conn, srv)

	if r.hello != "" {
		if _, err := io.WriteString(conn, r.hello); err != nil {
			return 0, true
		}
	}
	if !first && !r.replay(conn) {
		return 0, true
	}

	for {
		select {
		case line, ok := <-clientLines:
			if !ok {
				// Client EOF: half-close so the server drains its in-flight
				// handlers and hangs up, which ends srv.
				if uc, isUnix := conn.(*net.UnixConn); isUnix {
					_ = uc.CloseWrite()
				}
				for {
					select {
					case l, ok := <-srv:
						if !ok {
							return Done, false
						}
						r.forward(l)
					case <-ctx.Done():
						return Done, false
					}
				}
			}
			r.noteClient(line)
			if _, err := conn.Write(line); err != nil {
				return 0, true
			}
		case line, ok := <-srv:
			if !ok {
				return 0, true
			}
			r.forward(line)
		case <-ctx.Done():
			return Done, false
		}
	}
}

// noteClient remembers what a later reconnect needs from a client message.
func (r *relay) noteClient(line []byte) {
	m, ok := parseMessage(line)
	if !ok {
		return
	}
	switch {
	case m.isRequest() && m.Method == "initialize":
		r.initLine, r.initID, r.initDone = append([]byte(nil), line...), string(m.ID), false
	case m.Method == "notifications/initialized":
		r.initializedLine = append([]byte(nil), line...)
	}
	if m.isRequest() {
		r.pending[string(m.ID)] = append(json.RawMessage(nil), m.ID...)
	}
}

// forward passes a server line to the client, except the reply to a replayed
// initialize, which the client already has.
func (r *relay) forward(line []byte) {
	if m, ok := parseMessage(line); ok && m.isResponse() {
		key := string(m.ID)
		if r.swallow != "" && key == r.swallow {
			r.swallow = ""
			return
		}
		delete(r.pending, key)
		if key == r.initID {
			r.initDone = true
		}
	}
	_, _ = r.out.Write(line)
}

// failPending answers every request that was waiting when the server died: no
// new server session can answer it. An initialize that was not yet answered is
// left alone; the replay answers it.
func (r *relay) failPending() {
	for key, id := range r.pending {
		if key == r.initID && !r.initDone {
			continue
		}
		fmt.Fprintf(r.out,
			`{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"cutting-garden server restarted; this request was lost, send it again"}}`+"\n",
			id)
		delete(r.pending, key)
	}
}

// replay brings a new server session to the state the client believes it is
// in: initialize, then initialized.
func (r *relay) replay(conn net.Conn) bool {
	r.swallow = ""
	if r.initLine != nil {
		if _, err := conn.Write(r.initLine); err != nil {
			return false
		}
		if r.initDone {
			r.swallow = r.initID
		}
	}
	if r.initializedLine != nil {
		if _, err := conn.Write(r.initializedLine); err != nil {
			return false
		}
	}
	return true
}

// reconnect dials until it connects or ReconnectFor has passed. A nil
// connection comes with the outcome to return.
func (r *relay) reconnect(ctx context.Context) (net.Conn, Outcome) {
	deadline := time.Now().Add(ReconnectFor)
	wait := 25 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return nil, Done
		}
		if conn, err := dial(ctx, r.socket); err == nil {
			return conn, 0
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, DaemonLost
		}
		select {
		case <-time.After(min(wait, left)):
		case <-ctx.Done():
			return nil, Done
		}
		wait = min(wait*2, 500*time.Millisecond)
	}
}
