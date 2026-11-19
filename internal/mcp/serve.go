package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"code.linenisgreat.com/cutting-garden/internal/bridge"
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
	// AgentSocket, the -agent-socket flag, is the piggy agent socket the
	// daemon's `piggy pass show` calls reach (exported as PIGGY_AUTH_SOCK, which
	// piggy reads before SSH_AUTH_SOCK). A systemd user unit has no session
	// environment, so the unit names the socket here. The socket need not
	// exist: a fetch made while it is absent fails and is retried by the next
	// request, and a password already fetched is kept.
	AgentSocket string
}

// applyAgentSocket points the process's piggy calls at socket. Empty leaves
// the environment alone. The path must be absolute: a unit's working directory
// is not the operator's, and `~` is not expanded here.
func applyAgentSocket(socket string) error {
	if socket == "" {
		return nil
	}
	if !filepath.IsAbs(socket) {
		return errors.BadRequestf("-agent-socket %q: want an absolute path", socket)
	}
	return os.Setenv("PIGGY_AUTH_SOCK", socket)
}

var (
	_ command.Cmd                       = (*ServeMCP)(nil)
	_ interfaces.CommandComponentWriter = (*ServeMCP)(nil)
)

// NewServe constructs a serve-mcp command.
func NewServe() *ServeMCP { return &ServeMCP{} }

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
	flagSet.StringVar(
		&cmd.AgentSocket,
		"agent-socket",
		"",
		"absolute path of the piggy agent socket for `piggy pass show` "+
			"(exported as PIGGY_AUTH_SOCK); it may be absent now and appear later",
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
			"shared server. Each session's writes (mutate_nodes) are logged " +
			"to stderr with the session's principal and its peer's pid and " +
			"uid. The principal is announced by the bridge and SELF-ASSERTED: " +
			"it is an audit aid, not authentication. Access is the socket's " +
			"0600 mode and a same-user check. -agent-socket names the piggy " +
			"agent the daemon's `piggy pass show` calls use. Runs until " +
			"interrupted.",
	}
}

func (cmd *ServeMCP) Run(req command.Request) {
	ctx := req.Context.(errors.Context)
	args := req.PopArgs()

	socket := cmd.Socket
	if socket == "" {
		socket = bridge.DefaultSocket()
	}

	if err := applyAgentSocket(cmd.AgentSocket); err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
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
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	if err := serveListener(ctx, ln, serve, logf); err != nil && !errors.Is(err, context.Canceled) {
		errors.ContextCancelWithError(ctx, err)
	}
}

// consumeHello reads the connection's first line and, iff it is a hello
// notification, drops it and returns its principal ("" when there was none).
// Any other first line goes back in front of the stream untouched. It waits
// for a whole line rather than peeking a fixed prefix: a short first message
// (a ping) is shorter than the prefix, and waiting for more bytes would stall
// a client that is waiting for its reply.
func consumeHello(conn io.Reader) (principal string, rest io.Reader) {
	in := bufio.NewReader(conn)
	line, err := in.ReadBytes('\n')
	if p, ok := bridge.ParseHello(line); ok {
		return p, in
	}
	if err != nil && len(line) == 0 {
		return "", in
	}
	return "", io.MultiReader(bytes.NewReader(line), in)
}

// listenOwnerOnly binds socket (creating its 0700 directory), replacing a
// stale socket file but refusing to displace a live listener.
func listenOwnerOnly(socket string) (net.Listener, error) {
	// sockaddr_un.sun_path holds 108 bytes on Linux, NUL included; the kernel's
	// own error for a longer path ("invalid argument") names nothing.
	if len(socket) > 107 {
		return nil, errors.ErrorWithStackf(
			"socket path is %d bytes, over the 107-byte unix socket limit: %s", len(socket), socket,
		)
	}
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
//
// A connection may open with the bridge's hello notification; it is consumed
// here (the MCP server never sees it) and the principal it names is passed to
// logf with the session's open and close. A connection that sends none, as an
// older bridge does, is served unchanged and logged as anonymous.
func serveListener(
	ctx context.Context, ln net.Listener, serve serveFunc, logf func(format string, args ...any),
) error {
	// On shutdown, close the listener AND every live connection: a session
	// blocked reading its client (or the hello) would otherwise keep the
	// daemon from ever exiting, and a unit would have to be killed.
	var (
		mu       sync.Mutex
		live     = map[net.Conn]struct{}{}
		shutdown bool
	)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		mu.Lock()
		shutdown = true
		for c := range live {
			_ = c.Close()
		}
		mu.Unlock()
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
		mu.Lock()
		if shutdown {
			mu.Unlock()
			_ = conn.Close()
			continue
		}
		live[conn] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(live, conn)
				mu.Unlock()
				_ = conn.Close()
			}()
			caller := Caller{}
			if pid, uid, ok := peerCred(conn); ok {
				if int(uid) != os.Getuid() {
					// The socket is 0600, so this should be unreachable; a
					// connection from another user is dropped, not served.
					logf("serve-mcp: refused a connection from uid %d (pid %d)", uid, pid)
					return
				}
				caller.HasPeer, caller.PID, caller.UID = true, pid, int64(uid)
			}
			principal, in := consumeHello(conn)
			caller.Principal = principal
			logf("serve-mcp: session open%s", caller.logSuffix())
			// A session's error ends only that session.
			err := serve(withCaller(ctx, caller), transport.NewStdio(in, conn))
			logf("serve-mcp: session closed%s err=%v", caller.logSuffix(), err)
		}()
	}
}
