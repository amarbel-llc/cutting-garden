// Package buildinfo exposes the version and commit SHA that were burnt
// into each binary at build time. Values are owned by `package main` in
// each cmd/ binary (to match the amarbel-llc/nixpkgs fork's
// auto-injected -X main.version / -X main.commit ldflags), and pushed in
// here via Set() from each binary's init().
//
// The `version` subcommand is the canonical consumer; any other code that
// needs to report build identity (the capture-receipt Binary node, the
// MCP server's serverInfo.version) reads from here rather than hardcoding
// a second string or re-deriving from debug.ReadBuildInfo (which reports
// "(devel)" under a Nix source-tree build). Mirrors madder's
// go/internal/0/buildinfo.
package buildinfo

var (
	Version = "dev"
	Commit  = "unknown"
)

// Set is called from each cmd/ binary's init() with the ldflag-injected
// main.version / main.commit values. Must run before any consumer reads
// Version / Commit.
func Set(v, c string) {
	Version = v
	Commit = c
}

// String returns "Version+Commit" — the canonical one-line build
// identity, matching madder's convention.
func String() string {
	return Version + "+" + Commit
}
