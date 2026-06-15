// Command cutting-garden-nixstore is a compile-checked reference binary
// for the out-of-tree nix_store plugin (RFC 0009 §6). It blank-imports
// the plugin package — whose init() registers the nix-store scheme — and
// runs the SDK's binary builder, which yields a Utility carrying the
// standard subcommands (list/mcp/serve/…). It imports ONLY pkgs/, the
// shape a real out-of-repo binary takes.
//
// Because cgapp.Build() is plugin-bare (RFC 0009 §5 step 3), this binary
// links ONLY the nix-store plugin — none of the in-tree plugins, which the
// cutting-garden binaries opt into via plugins/all. That is the decoupling
// the SDK exists to provide.
//
// It is not shipped: the flake's buildGoApplication builds an explicit
// subPackages list (cmd/cutting-garden, cmd/cg, cmd/cutting-garden-gen),
// so this example is exercised only by `go build ./...` / `go test ./...`.
package main

import (
	"os"

	cgapp "github.com/amarbel-llc/cutting-garden/pkgs/cgapp"

	_ "github.com/amarbel-llc/cutting-garden/examples/nixstore"
)

func main() {
	os.Exit(cgapp.Build().Run(os.Args))
}
