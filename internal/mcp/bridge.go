package mcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
)

// runBridge relays the MCP stdio stream to the serve-mcp daemon listening on
// socket and reports whether it did. A false return means the socket could not
// be dialed (daemon down, stale path) and the caller should serve in-process
// instead; nothing has been read from in at that point. A true return means the
// session ran to its end through the daemon (client EOF, daemon hang-up, or
// ctx), including a mid-session daemon disconnect, which cannot be resumed.
//
// Diagnostics go to diag, never out: stdout carries only protocol frames.
func runBridge(
	ctx context.Context, socket string, in io.Reader, out io.Writer, diag io.Writer,
) bool {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		fmt.Fprintf(diag, "cutting-garden mcp: no daemon at %s (%v); serving in-process\n", socket, err)
		return false
	}
	defer conn.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(out, conn)
	}()

	// Client EOF: half-close so the daemon drains its in-flight handlers and
	// hangs up, which ends the copy above. ctx cancellation closes the conn.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	_, _ = io.Copy(conn, in)
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	wg.Wait()
	return true
}
