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
// advertises resource and tool capabilities:
//
//   - resources/list — the immediate children of every root (one
//     ListRoots call per root).
//   - resources/read — the immediate children of the read URI, letting a
//     client descend a container lazily, one level per read; a childless
//     leaf instead reads as the object's parsed fields (#85).
//   - tools/call — the same tree as tools, for clients that render only
//     tools, not resources (the claude.ai web UI; circus#29). Read-only:
//     list_nodes (browse children; omit uri for the roots, optional
//     limit/offset to page a large listing, cutting-garden#86), read_node
//     (read one node, = resources/read), read_facets (a container's
//     hoisted facet summary — RFC 0012 §7's progressive-disclosure block,
//     otherwise reachable only via resources/read — with an optional
//     filter to narrow it, cutting-garden#151), describe_node_types
//     (schema discovery). Write (FDR 0020, plugins implementing NodeMutator):
//     create_node / put_node / patch_node / delete_node, advertised
//     only when a configured root supports mutation and annotated
//     destructive so a client gates them (and the clown PreToolUse
//     hook classifies them `ask`, #102).
//
// Discovery (resources) is read-only and captures nothing; the write tools
// mutate live nodes directly, with no blob store or receipt. (One
// content-addressed write remains on the read side: a leaf read of a
// LeafReader object stores the verbatim bytes in the host's default madder
// blob store, when configured, and links them by digest beside the parsed
// fields, #85.) The server runs until the client closes the connection or
// it is interrupted (SIGINT/SIGTERM/SIGHUP). Exit 0 on a clean shutdown,
// 64 on a malformed config or unresolvable endpoint argument, 2 on a
// transport error.
package mcp

import (
	"context"
	"net/url"
	"os"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
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
	"array, so you descend the tree one level per read. Reading a leaf " +
	"object returns its parsed fields as JSON, plus (when available) a " +
	"madder://blobs/<digest> link to its verbatim bytes. The same surface " +
	"is also exposed as tools (for clients that render only tools): " +
	"list_nodes browses children (omit the uri for the entry points; " +
	"optional limit/offset page a large listing), read_node reads one " +
	"node, and describe_node_types reports each scheme's node types and " +
	"what body create_node accepts, including any declared facet " +
	"dimensions. Call read_facets on a container FIRST, before " +
	"enumerating it: it summarizes children by their facet dimensions " +
	"(counts per value, e.g. status or read/unread) without listing them, " +
	"and an optional filter narrows the summary directly — the cheap way " +
	"to orient on a large tree's size and shape before deciding whether " +
	"or how to browse further. The create_node / put_node / patch_node / " +
	"delete_node tools mutate a node at its URI (e.g. create a calendar " +
	"event); they are destructive and require user approval."

// MCP is the value registered for the `mcp` subcommand. Endpoints come from
// the config, or from optional positional args that override it;
// ExcludeSchemes (the -exclude-scheme flag) additionally suppresses a
// scheme's contribution to both paths (cutting-garden#148).
type MCP struct {
	// ExcludeSchemes accumulates one entry per -exclude-scheme occurrence
	// (a repeatable flag; see excludeSchemesFlag). A deployment that wants
	// to run the file plugin's traversal but not expose it to an MCP
	// client (krone invokes `cutting-garden mcp` directly with no
	// interactive user to consent through the write-tool gate) passes
	// -exclude-scheme=file. Matched against Node/root URI Scheme values
	// exactly as each plugin sets them (e.g. "file", never the schemeless
	// "" a bare relative path would parse to).
	ExcludeSchemes []string
}

var (
	_ command.Cmd                       = (*MCP)(nil)
	_ interfaces.CommandComponentWriter = (*MCP)(nil)
)

// New constructs an MCP command.
func New() *MCP { return &MCP{} }

// excludeSchemesFlag is a repeatable string flag: flagSet.Var calls Set
// once per -exclude-scheme occurrence on argv (the dewey/stdlib flag.Value
// contract), so -exclude-scheme=file -exclude-scheme=web accumulates both
// rather than the last one winning (StringVar's overwrite semantics).
type excludeSchemesFlag struct{ values *[]string }

func (f excludeSchemesFlag) String() string {
	if f.values == nil {
		return ""
	}
	return strings.Join(*f.values, ",")
}

func (f excludeSchemesFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.ErrorWithStackf("-exclude-scheme: value must not be empty")
	}
	*f.values = append(*f.values, value)
	return nil
}

func (cmd *MCP) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.Var(
		excludeSchemesFlag{values: &cmd.ExcludeSchemes},
		"exclude-scheme",
		"URI scheme to exclude from both aggregated and explicit roots "+
			"(repeatable, e.g. -exclude-scheme=file)",
	)
}

func (*MCP) GetDescription() command.Description {
	return command.Description{
		Short: "serve traversable plugin endpoints over the Model Context Protocol",
		Long: "Runs a Model Context Protocol server (newline-delimited " +
			"JSON-RPC over stdin/stdout) that exposes the capturable tree of " +
			"each endpoint URI as MCP resources. resources/list returns every " +
			"endpoint's immediate children; reading a container resource " +
			"returns that node's children, so a client descends lazily one " +
			"level per read \\(em the same RootLister traversal `list` and " +
			"capture share. Reading a leaf object returns its parsed fields " +
			"as JSON, plus a content-addressed madder blob link to its " +
			"verbatim bytes when a store is configured. It also exposes " +
			"create_node/put_node/patch_node/delete_node write tools (FDR 0020) for " +
			"plugins that support mutation (caldav); these are annotated " +
			"destructive and gated by the clown PreToolUse hook. Launched by " +
			"an MCP client, not run interactively; runs until the client " +
			"disconnects or it is interrupted.",
	}
}

func (cmd *MCP) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	roots, err := mcpRoots(ctx, req.PopArgs(), cmd.ExcludeSchemes)
	if err != nil {
		// A bad endpoint or malformed config is a usage error: the client
		// misconfigured the server. Fail fast (EX_USAGE) before the
		// transport opens.
		errors.ContextCancelWithError(ctx, err)
		return
	}

	provider := newResources(roots, mcpBlobWriter(ctx))
	tools := newTools(roots, provider)

	// Warm the facet cache with the configured roots and keep summaries
	// fresh in the background (RFC 0012 §11.2); ends with ctx.
	go provider.startFacetMaintenance(ctx)

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
			Tools:             tools,
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
//
// excludeSchemes (cutting-garden#148) applies to both branches: an
// aggregated root whose Scheme is excluded is dropped silently (it simply
// does not appear, exactly as a plugin with no roots at all would not);
// an EXPLICIT argument naming an excluded scheme is instead rejected as a
// usage error — a deliberate defense-in-depth choice, since an explicit
// endpoint argument is otherwise this server's escape hatch past the file
// plugin's PWD scoping (RFC 0001 §Producer Rules §Root Scoping), and
// silently dropping it would either produce a confusing empty listing or
// (worse) look like the flag has no effect on the one path an operator is
// most likely to test by hand.
func mcpRoots(
	ctx context.Context, args []string, excludeSchemes []string,
) ([]*url.URL, error) {
	// Config load precedes BOTH branches: explicit root args still
	// resolve through the scheme registry, and a [[traversal_plugins]]
	// wire plugin exists there only after registration (RFC 0013 §Host
	// integration; the same gap `list <uri>` had, found via #140).
	if err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
		return nil, err
	}
	excluded := excludedSchemeSet(excludeSchemes)
	if len(args) > 0 {
		roots, err := resolveRoots(args, excluded)
		if err != nil {
			return nil, errors.BadRequestf("%s", err.Error())
		}
		return roots, nil
	}
	roots, err := command_components.AggregateRoots(ctx)
	if err != nil {
		return nil, err
	}
	return filterExcludedSchemes(roots, excluded), nil
}

// resolveRoots parses each endpoint argument and verifies its scheme has
// a RootLister plugin, so a non-traversable or unknown scheme is rejected
// up front rather than producing an empty listing at runtime. An argument
// whose scheme is in excluded is rejected too (see mcpRoots).
func resolveRoots(
	args []string, excluded map[string]bool,
) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(args))
	for _, arg := range args {
		u, _, err := command_components.ResolveRootListerPlugin(arg)
		if err != nil {
			return nil, err
		}
		if excluded[u.Scheme] {
			return nil, errors.ErrorWithStackf(
				"%s: scheme %q is excluded by -exclude-scheme", arg, u.Scheme,
			)
		}
		roots = append(roots, u)
	}
	return roots, nil
}

// excludedSchemeSet builds a lookup set from the repeatable
// -exclude-scheme flag's accumulated values. Empty/nil input yields an
// empty (non-nil) map so callers can index it unconditionally.
func excludedSchemeSet(schemes []string) map[string]bool {
	set := make(map[string]bool, len(schemes))
	for _, s := range schemes {
		set[s] = true
	}
	return set
}

// filterExcludedSchemes drops every root whose URI Scheme is in excluded,
// preserving order. Returns roots unmodified (same slice) when excluded is
// empty, so the common no-flag case allocates nothing extra.
func filterExcludedSchemes(roots []*url.URL, excluded map[string]bool) []*url.URL {
	if len(excluded) == 0 {
		return roots
	}
	out := make([]*url.URL, 0, len(roots))
	for _, r := range roots {
		if excluded[r.Scheme] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// mcpBlobWriter returns the sink a leaf read persists verbatim object bytes
// to, so they can be surfaced as a `madder://blobs/<digest>` link (#85). It
// resolves the host's default madder blob store; when no store is
// configured — or the store cannot be acquired — it returns nil and the
// server serves structured-only reads (no raw-bytes link). Store resolution
// is best-effort: it never blocks the server from starting, since the
// structured read does not depend on it.
//
// Acquisition runs on a throwaway errors.Context, never the command's ctx
// (#121). MakeBlobStoreEnv eagerly creates dirs under the cache root
// (env_dir.initializeXDG mkdir's <cache>/tmp-<pid>; store init mkdir's the
// store's blob dir). On a read-only CWD that mkdir fails, and madder's
// errors.Context.Cancel panics via ContextContinueOrPanic — which, were the
// command's ctx passed here, would unwind up and crash the server before the
// transport ever opens. acquireBlobWriter confines that cancel-panic to a
// private context (and recovers it): a failure leaves the writer nil and the
// server starts.
//
// The private context is deliberately NOT Run: env_dir registers its temp-dir
// teardown (resetTempOnExit, which os.RemoveAll's <cache>/tmp-<pid>) as an
// After hook that fires on Run completion — running it would delete the temp
// dir the moment acquisition returns, leaving the long-lived writer pointing
// at a removed dir so its writes silently fail. Instead the temp dir's cleanup
// is re-registered on the command ctx, which fires it at server shutdown
// (matching the pre-#121 lifetime).
func mcpBlobWriter(ctx errors.Context) capture_plugin.Writer {
	writer, tempDir := acquireBlobWriter()
	if tempDir != "" {
		ctx.After(errors.MakeFuncContextFromFuncErr(func() error {
			return os.RemoveAll(tempDir)
		}))
	}
	return writer
}

// acquireBlobWriter resolves the default-blob-store writer on a throwaway,
// never-Run errors.Context, recovering the Cancel-panic an eager mkdir raises
// on a read-only cache (#121) so a failure yields a nil writer instead of
// crashing the server. It returns the env's temp-dir path (empty when there is
// no writer) so the caller can schedule its cleanup on a longer-lived context
// — the acquisition context must not be Run, since its completion would delete
// that very temp dir out from under the writer.
func acquireBlobWriter() (writer capture_plugin.Writer, tempDir string) {
	// Any failure acquiring the store (read-only cache mkdir cancel-panic,
	// store-init error) degrades to structured-only reads. The blob link is a
	// best-effort enrichment; it must never take the server down.
	defer func() { _ = recover() }()

	env := command_components.MakeBlobStoreEnv(errors.MakeContextDefault())
	// GetDefaultBlobStore panics when no stores are initialized, so gate on a
	// configured store first — an absent store is a normal deployment, not an
	// error.
	if len(env.GetBlobStores()) == 0 {
		return nil, ""
	}
	return capture_plugin.NewBlobStoreWriter(env.GetDefaultBlobStore()),
		env.GetTempLocal().BasePath
}
