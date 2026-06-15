// Package mcp wires the `mcp` subcommand: serve the capturable trees of
// the configured plugin endpoints over the Model Context Protocol, so an
// MCP client (e.g. Claude) can discover and descend what cutting-garden
// exposes without capturing it.
//
// Positional surface:
//
//	mcp [URI...]
//
// With no URI, the server surfaces every plugin's configured and
// intrinsic roots (RFC 0007): the CalDAV accounts from
// $XDG_CONFIG_HOME/cutting-garden/config.toml and the file plugin's
// working directory. Explicit URIs override the config with exactly those
// endpoints (each scheme's plugin must support traversal). The server
// speaks newline-delimited JSON-RPC over stdin/stdout — the MCP stdio
// transport — so it is launched by a client, not run interactively. It
// advertises only resource capabilities:
//
//   - resources/list — the immediate children of every root (one
//     ListRoots call per root).
//   - resources/read — the immediate children of the read URI, letting a
//     client descend a container lazily, one level per read.
//
// Read-only: no blob store is touched and nothing is captured (FDR
// 0014's body-fetch path stays with `capture`). The server runs until
// the client closes the connection or it is interrupted
// (SIGINT/SIGTERM/SIGHUP). Exit 0 on a clean shutdown, 64 on a malformed
// config or unresolvable endpoint argument, 2 on a transport error.
package mcp

import (
	"context"
	"net/url"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/buildinfo"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/transport"
)

const serverName = "cutting-garden"

// instructions is the usage hint advertised to MCP clients. It names the
// traversal model so a client knows reads descend rather than fetch
// bytes.
const instructions = "Resources are the capturable trees of cutting-garden " +
	"plugin endpoints. resources/list returns each endpoint's immediate " +
	"children; reading a container resource returns its children as a JSON " +
	"array, so you descend the tree one level per read. Discovery is " +
	"read-only — nothing is captured."

// MCP is the value registered for the `mcp` subcommand. It carries no
// flags; endpoints come from the config, or from optional positional args
// that override it.
type MCP struct{}

var _ command.Cmd = (*MCP)(nil)

// New constructs an MCP command.
func New() *MCP { return &MCP{} }

func (*MCP) GetDescription() command.Description {
	return command.Description{
		Short: "serve traversable plugin endpoints over the Model Context Protocol",
		Long: "Runs a Model Context Protocol server (newline-delimited " +
			"JSON-RPC over stdin/stdout) that exposes the capturable tree of " +
			"each endpoint URI as MCP resources. resources/list returns every " +
			"endpoint's immediate children; reading a container resource " +
			"returns that node's children, so a client descends lazily one " +
			"level per read \\(em the same RootLister traversal `list` and " +
			"capture share. Read-only; no blob store is touched and nothing " +
			"is captured. Launched by an MCP client, not run interactively; " +
			"runs until the client disconnects or it is interrupted.",
	}
}

func (cmd *MCP) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	roots, err := mcpRoots(ctx, req.PopArgs())
	if err != nil {
		// A bad endpoint or malformed config is a usage error: the client
		// misconfigured the server. Fail fast (EX_USAGE) before the
		// transport opens.
		errors.ContextCancelWithError(ctx, err)
		return
	}

	provider := newResources(roots)

	// The MCP protocol owns stdout (JSON-RPC frames), so diagnostics must
	// never leak there; the server itself writes only protocol frames.
	srv, err := server.New(
		transport.NewStdio(os.Stdin, os.Stdout),
		server.Options{
			ServerName: serverName,
			// ServerVersion is REQUIRED: the MCP initialize response's
			// serverInfo.version is a non-optional string in the client's
			// schema. Omitting it makes clients reject the handshake
			// ("expected string, received undefined"). buildinfo.Version is
			// the ldflag-burnt release version ("dev" on bare builds —
			// still a valid non-empty string).
			ServerVersion:     buildinfo.Version,
			Instructions:      instructions,
			Resources:         provider,
			PreferV1Providers: true,
		},
	)
	if err != nil {
		errors.ContextCancelWithError(ctx, errors.Wrap(err))
		return
	}

	// errors.Context satisfies context.Context, so SIGINT/SIGTERM/SIGHUP
	// cancellation threads straight into Run and unwinds the message loop.
	// A clean shutdown (context cancelled or client EOF) is exit 0.
	if runErr := srv.Run(ctx); runErr != nil &&
		!errors.Is(runErr, context.Canceled) {
		errors.ContextCancelWithError(ctx, errors.Wrap(runErr))
	}
}

// mcpRoots resolves the endpoints the server exposes. With explicit
// positional args it uses them verbatim (the override path, RFC 0007 §
// Compatibility); a bad endpoint is EX_USAGE. With no args it loads the
// config, injects it into the plugins, and aggregates every plugin's
// configured/intrinsic roots — the default "surface all plugins from
// their roots" behavior. A malformed config is EX_USAGE; an empty config
// still yields the file plugin's working-directory root.
func mcpRoots(ctx context.Context, args []string) ([]*url.URL, error) {
	if len(args) > 0 {
		roots, err := resolveRoots(args)
		if err != nil {
			return nil, errors.BadRequestf("%s", err.Error())
		}
		return roots, nil
	}
	if err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
		return nil, err
	}
	return command_components.AggregateRoots(ctx)
}

// resolveRoots parses each endpoint argument and verifies its scheme has
// a RootLister plugin, so a non-traversable or unknown scheme is rejected
// up front rather than producing an empty listing at runtime.
func resolveRoots(args []string) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(args))
	for _, arg := range args {
		u, _, err := command_components.ResolveRootListerPlugin(arg)
		if err != nil {
			return nil, err
		}
		roots = append(roots, u)
	}
	return roots, nil
}
