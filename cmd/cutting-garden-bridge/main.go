// Command cutting-garden-bridge is the per-session end of the shared
// cutting-garden MCP server: it relays stdio to the host's `serve-mcp`
// daemon and, when no daemon answers, replaces itself with the in-process
// `cutting-garden mcp` so a session never loses its tools to a stopped
// daemon. It imports no plugin, so it stays a few MB lighter than
// `cutting-garden mcp -connect`.
//
//	cutting-garden-bridge [-socket PATH] [-- mcp args...]
//
// Arguments after `--` are passed to the fallback `cutting-garden mcp`.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"code.linenisgreat.com/cutting-garden/internal/bridge"
)

func main() {
	socket := flag.String("socket", "", "daemon unix socket (default $XDG_RUNTIME_DIR/cutting-garden/mcp.sock)")
	flag.Parse()
	if *socket == "" {
		*socket = bridge.DefaultSocket()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if bridge.Run(ctx, *socket, os.Stdin, os.Stdout, os.Stderr) {
		return
	}
	stop()
	if err := execInProcess(flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "cutting-garden-bridge: %v\n", err)
		os.Exit(2)
	}
}

// execInProcess replaces this process with `cutting-garden mcp <args>`,
// preferring the sibling binary (the wrapped one in the same bin dir) over PATH.
func execInProcess(args []string) error {
	target := ""
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), "cutting-garden")
		if _, err := os.Stat(sibling); err == nil {
			target = sibling
		}
	}
	if target == "" {
		p, err := exec.LookPath("cutting-garden")
		if err != nil {
			return fmt.Errorf("no daemon, and no cutting-garden to fall back to: %w", err)
		}
		target = p
	}
	argv := append([]string{target, "mcp"}, args...)
	return syscall.Exec(target, argv, os.Environ())
}
