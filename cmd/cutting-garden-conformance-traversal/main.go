// Command cutting-garden-conformance-traversal is the RFC 0013
// session-level conformance driver (cutting-garden#186): given a peer
// manifest (TOML), it launches the peer over the transport's own
// bring-up grammar, runs the slice-1 case list against the peer's RAW
// wire responses, and emits TAP 14 on stdout. Exposed as a flake
// package but NOT shipped in release artifacts (the caldav-testserver
// pattern), so an external peer runs
// `nix run .#conformance-traversal -- -manifest peer.toml`.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"code.linenisgreat.com/cutting-garden/internal/traversal_conformance"
)

func main() { os.Exit(run()) }

// run keeps main os.Exit-free above one call site (the utility.Run
// pattern). Exit codes mirror the repo's convention (internal/command:
// 64 = EX_USAGE for caller mistakes) without pulling in the Utility
// dispatch — this is a standalone conformance tool, not a
// cutting-garden subcommand: 0 = every non-SKIP case passed, 1 = a
// conformance failure or driver trouble, 64 = usage (missing/invalid
// manifest).
func run() int {
	manifestPath := flag.String(
		"manifest", "", "path to the peer manifest (TOML); required",
	)
	flag.Parse()

	if *manifestPath == "" {
		fmt.Fprintln(
			os.Stderr,
			"usage: cutting-garden-conformance-traversal -manifest <path>",
		)

		return 64
	}

	manifest, err := traversal_conformance.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 64
	}

	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer stop()

	passed, err := traversal_conformance.Run(ctx, manifest, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	if !passed {
		return 1
	}

	return 0
}
