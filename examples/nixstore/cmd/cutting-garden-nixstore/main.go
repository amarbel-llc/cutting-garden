// Command cutting-garden-nixstore is a compile-checked reference binary
// for the out-of-tree nix_store plugin (RFC 0009 §6). It blank-imports
// the plugin package — whose init() registers the nix-store scheme — and
// runs the SDK's binary builder, which yields a Utility carrying the
// standard subcommands (list/mcp/serve/…). It imports ONLY pkgs/, the
// shape a real out-of-repo binary takes.
//
// It is not shipped: the flake's buildGoApplication builds an explicit
// subPackages list (cmd/cutting-garden, cmd/cg, cmd/cutting-garden-gen),
// so this example is exercised only by `go build ./...` / `go test ./...`.
//
// Until the RFC 0009 §5 step-3 relocation lands, cgapp.Build() also links
// the in-tree plugins, so this binary bundles them; the real out-of-repo
// binary will link only its own plugin once Build() is bare.
package main

import (
	"os"

	cgapp "github.com/amarbel-llc/cutting-garden/pkgs/cgapp"

	_ "github.com/amarbel-llc/cutting-garden/examples/nixstore"
)

func main() {
	os.Exit(cgapp.Build().Run(os.Args))
}
