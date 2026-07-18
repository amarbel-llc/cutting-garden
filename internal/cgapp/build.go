// Package cgapp builds the cutting-garden Utility shared by every
// binary that runs the same command set: `cutting-garden`, its short
// alias `cg`, and the manpage/completion generator
// `cutting-garden-gen`. The single Build() function keeps subcommand
// registrations from drifting across three main.go files.
package cgapp

import (
	"code.linenisgreat.com/cutting-garden/internal/blob_writer"
	"code.linenisgreat.com/cutting-garden/internal/capture"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/diff"
	"code.linenisgreat.com/cutting-garden/internal/failures"
	"code.linenisgreat.com/cutting-garden/internal/health"
	"code.linenisgreat.com/cutting-garden/internal/hook"
	"code.linenisgreat.com/cutting-garden/internal/list"
	"code.linenisgreat.com/cutting-garden/internal/mcp"
	"code.linenisgreat.com/cutting-garden/internal/restore"
	"code.linenisgreat.com/cutting-garden/internal/serve"
	"code.linenisgreat.com/cutting-garden/internal/version"

	// markl-id purpose registrations (the blech32 purpose lookups that
	// fire when blob_store_env discovers an encrypted store config). Not a
	// plugin, and every binary that calls Build needs it, so it stays
	// wired here.
	//
	// The capture/restore/diff/traversal PLUGINS are deliberately NOT
	// imported here (RFC 0009 §5 step 3): Build() must stay plugin-bare so
	// an external binary built on the SDK (pkgs/cgapp.Build) inherits only
	// the plugins it explicitly links. Each in-repo binary blank-imports
	// `plugins/all` from its own main to register the standard set; that
	// aggregator is the single list, so the three in-repo mains cannot
	// drift apart.
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
)

// Build returns a fully-configured cutting-garden Utility with the
// canonical name "cutting-garden", the "cg" alias, the hidden
// `complete` subcommand registered, and the nine user-facing
// subcommands (capture, restore, diff, serve, failures, health, list,
// mcp, version) attached.
//
// Every cutting-garden binary main.go calls this and dispatches
// utility.Run(os.Args).
func Build() command.Utility {
	utility := command.MakeUtility("cutting-garden", nil)
	utility.AddAlias("cg")
	command.RegisterComplete(&utility)
	utility.AddCmd("capture", capture.New())
	utility.AddCmd("restore", restore.New())
	utility.AddCmd("diff", diff.New())
	utility.AddCmd("serve", serve.New())
	utility.AddCmd("failures", failures.New())
	utility.AddCmd("health", health.New())
	utility.AddCmd("list", list.New())
	utility.AddCmd("mcp", mcp.New())
	utility.AddCmd("version", version.New())
	// Hidden plumbing: the RFC 0002 writer-protocol sink a config-
	// declared capture plugin's v1 capture-batch fallback pipes node
	// blobs into when its binary (e.g. chrest) has no working RFC 0008
	// capture-serve session. See internal/blob_writer and
	// internal/capture_wire.
	utility.AddCmd("__write-blob", blob_writer.New())
	// Hidden plumbing: the clown-plugin PreToolUse hook sink. The plugin's
	// hooks/handler execs `cutting-garden hook` per hook event; it routes
	// stdin/stdout through internal/claude_hooks, which gates the CUD write
	// tools (create/update/delete_node) as `ask` (#102, FDR 0020).
	utility.AddCmd("hook", hook.New())
	return utility
}
