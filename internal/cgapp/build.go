// Package cgapp builds the cutting-garden Utility shared by every
// binary that runs the same command set: `cutting-garden`, its short
// alias `cg`, and the manpage/completion generator
// `cutting-garden-gen`. The single Build() function keeps subcommand
// registrations from drifting across three main.go files.
package cgapp

import (
	"github.com/amarbel-llc/cutting-garden/internal/blob_writer"
	"github.com/amarbel-llc/cutting-garden/internal/capture"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/diff"
	"github.com/amarbel-llc/cutting-garden/internal/restore"
	// Blank-imports register plugin schemes and markl-id purposes at
	// init time. The file plugin must register before any subcommand
	// dispatch routes through ResolveRestore / ResolveDiff; markl_
	// registrations covers the blech32 purpose lookups that fire
	// when blob_store_env discovers an encrypted store config.
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_file"
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_git"
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_web"
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_ytdlp"
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
)

// Build returns a fully-configured cutting-garden Utility with the
// canonical name "cutting-garden", the "cg" alias, the hidden
// `complete` subcommand registered, and the three user-facing
// subcommands (capture, restore, diff) attached.
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
	// Hidden plumbing: the RFC 0002 writer-protocol sink that external
	// capturer subprocesses (the web binding's chrest) pipe node blobs
	// into. See internal/blob_writer and internal/cutting_garden_plugin_web.
	utility.AddCmd("__write-blob", blob_writer.New())
	return utility
}
