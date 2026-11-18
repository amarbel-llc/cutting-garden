package mcp

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
	"code.linenisgreat.com/purse-first/libs/go-mcp/transport"
)

// ServeMCP is the value registered for the `serve-mcp` subcommand: the same
// MCP server as `mcp`, but listening on a unix socket and serving every
// connection from one process, so the host runs ONE cutting-garden (one set of
// listing/facet caches, one refresh loop) instead of one per session. Sessions
// reach it through `mcp -connect <socket>`.
//
// The file plugin is always excluded here: its intrinsic root is the process
// working directory, which for a shared daemon belongs to no caller. File
// traversal stays with the per-session in-process server.
type ServeMCP struct {
	// Socket is the unix socket path; empty means DefaultSocket().
	Socket string
	// ExcludeSchemes and ConfigPath behave as for `mcp`.
	ExcludeSchemes []string
	ConfigPath     string
}

var (
	_ command.Cmd                       = (*ServeMCP)(nil)
	_ interfaces.CommandComponentWriter = (*ServeMCP)(nil)
)

// NewServe constructs a serve-mcp command.
func NewServe() *ServeMCP { return &ServeMCP{} }

// DefaultSocket is $XDG_RUNTIME_DIR/cutting-garden/mcp.sock, falling back to
// the temp dir only when XDG_RUNTIME_DIR is unset.
func DefaultSocket() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "cutting-garden", "mcp.sock")
}

func (cmd *ServeMCP) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(
		&cmd.Socket,
		"socket",
		"",
		"unix socket to listen on (default $XDG_RUNTIME_DIR/cutting-garden/mcp.sock)",
	)
	flagSet.Var(
		excludeSchemesFlag{values: &cmd.ExcludeSchemes},
		"exclude-scheme",
		"URI scheme to exclude (repeatable; file is always excluded)",
	)
	flagSet.StringVar(
		&cmd.ConfigPath,
		"config",
		"",
		"config file to load instead of $XDG_CONFIG_HOME/cutting-garden/config.toml "+
			"(it must exist)",
	)
}

func (*ServeMCP) GetDescription() command.Description {
	return command.Description{
		Short: "serve the MCP server to many sessions over one unix socket",
		Long: "Runs the `mcp` server once per host: it listens on a unix " +
			"socket (mode 0600) and serves each connection as its own MCP " +
			"session over shared listing and facet caches and a single " +
			"background refresh loop, so N sessions cost one process and one " +
			"upstream refresh rather than N. Sessions connect with `mcp " +
			"-connect <socket>`. The file plugin is excluded: its root is " +
			"the process working directory, which belongs to no caller of a " +
			"shared server. Runs until interrupted.",
	}
}

func (cmd *ServeMCP) Run(req command.Request) {
	ctx := req.Context.(errors.Context)
	args := req.PopArgs()

	socket := cmd.Socket
	if socket == "" {
		socket = DefaultSocket()
	}

	exclude := append([]string{"file"}, cmd.ExcludeSchemes...)
	serve, err := prepareServer(ctx, args, exclude, cmd.ConfigPath)
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	ln, err := listenOwnerOnly(socket)
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}
	if err := serveListener(ctx, ln, serve); err != nil && !errors.Is(err, context.Canceled) {
		errors.ContextCancelWithError(ctx, err)
	}
}

// listenOwnerOnly binds socket (creating its 0700 directory), replacing a
// stale socket file but refusing to displace a live listener.
func listenOwnerOnly(socket string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, errors.Wrap(err)
	}
	if c, err := net.Dial("unix", socket); err == nil {
		c.Close()
		return nil, errors.ErrorWithStackf("%s: another serve-mcp is already listening", socket)
	}
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return nil, errors.Wrap(err)
	}
	return ln, nil
}

// serveListener accepts connections until ctx ends, serving each on its own
// goroutine, and waits for them to drain before returning.
func serveListener(ctx context.Context, ln net.Listener, serve serveFunc) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.Wrap(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			// A session's error ends only that session.
			_ = serve(ctx, transport.NewStdio(conn, conn))
		}()
	}
}
