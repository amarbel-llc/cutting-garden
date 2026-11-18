// Package bridge relays an MCP stdio stream to the host's shared
// `cutting-garden serve-mcp` daemon over its unix socket. It imports only the
// standard library, so a binary that links it (cmd/cutting-garden-bridge, the
// per-session process) carries none of the plugin registry the full
// cutting-garden binary initialises.
package bridge

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
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

// Run relays in/out to the daemon listening on socket and reports whether it
// did. A false return means the socket could not be dialed (daemon down, stale
// path) and the caller should serve in-process instead; nothing has been read
// from in at that point. A true return means the session ran to its end
// through the daemon (client EOF, daemon hang-up, or ctx), including a
// mid-session daemon disconnect, which cannot be resumed.
//
// Diagnostics go to diag, never out: stdout carries only protocol frames.
func Run(
	ctx context.Context, socket string, in io.Reader, out io.Writer, diag io.Writer,
) bool {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		fmt.Fprintf(diag, "cutting-garden mcp: no daemon at %s (%v); serving in-process\n", socket, err)
		return false
	}
	// A close error on a relayed socket has no one to report to.
	defer func() { _ = conn.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(out, conn)
	}()

	// ctx cancellation closes the conn, which ends both copies.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Client EOF: half-close so the daemon drains its in-flight handlers and
	// hangs up, which ends the copy above.
	_, _ = io.Copy(conn, in)
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	wg.Wait()
	return true
}
