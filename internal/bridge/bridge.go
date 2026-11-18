// Package bridge relays an MCP stdio stream to the host's shared
// `cutting-garden serve-mcp` daemon over its unix socket. It imports only the
// standard library, so a binary that links it (cmd/cutting-garden-bridge, the
// per-session process) carries none of the plugin registry the full
// cutting-garden binary initialises.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// Outcome is how a relay ended.
type Outcome int

const (
	// NoDaemon: the socket could not be dialed; nothing was read from in, so
	// the caller can serve in-process instead.
	NoDaemon Outcome = iota
	// Done: the client closed its input and the daemon drained and hung up, or
	// ctx ended.
	Done
	// DaemonLost: the daemon hung up while the client was still connected. The
	// session cannot be resumed (its protocol state lived in the daemon), so
	// the caller should exit non-zero and let its host restart it.
	DaemonLost
)

// Run relays in/out to the daemon listening on socket, sending hello first
// when it is non-empty. Diagnostics go to diag, never out: stdout carries
// only protocol frames.
func Run(
	ctx context.Context, socket, hello string, in io.Reader, out io.Writer, diag io.Writer,
) Outcome {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		fmt.Fprintf(diag, "cutting-garden mcp: no daemon at %s (%v); serving in-process\n", socket, err)
		return NoDaemon
	}
	// A close error on a relayed socket has no one to report to.
	defer func() { _ = conn.Close() }()

	if hello != "" {
		if _, err := io.WriteString(conn, hello); err != nil {
			fmt.Fprintf(diag, "cutting-garden mcp: daemon at %s hung up before the session started\n", socket)
			return DaemonLost
		}
	}

	downstream := make(chan struct{}) // closed when the daemon's side ends
	go func() {
		defer close(downstream)
		_, _ = io.Copy(out, conn)
	}()
	upstream := make(chan struct{}) // closed when the client's side ends
	go func() {
		defer close(upstream)
		_, _ = io.Copy(conn, in)
	}()

	select {
	case <-upstream:
		// Client EOF: half-close so the daemon drains its in-flight handlers
		// and hangs up, which ends the downstream copy.
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		select {
		case <-downstream:
		case <-ctx.Done():
		}
		return Done
	case <-downstream:
		fmt.Fprintf(diag, "cutting-garden mcp: daemon at %s hung up mid-session\n", socket)
		return DaemonLost
	case <-ctx.Done():
		return Done
	}
}
