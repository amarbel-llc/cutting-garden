// Command cutting-garden is the canonical cutting-garden CLI entry
// point. See `cg` for the short-name alias (same Utility, same
// behavior). Both binaries call cgapp.Build to build the Utility.
package main

import (
	"os"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
	"code.linenisgreat.com/cutting-garden/internal/cgapp"

	// Register the standard in-repo plugin set. cgapp.Build() is
	// plugin-bare (RFC 0009 §5 step 3); the in-repo binaries opt in here.
	_ "code.linenisgreat.com/cutting-garden/plugins/all"
)

// Populated at link time via `-X main.version` / `-X main.commit`, which
// the amarbel-llc/nixpkgs fork auto-injects from the derivation's
// version/commit attrs (sourced from version.env + the flake rev). Bare
// `go build` leaves the dev defaults.
var (
	version = "dev"
	commit  = "unknown"
)

func init() {
	buildinfo.Set(version, commit)
}

func main() {
	utility := cgapp.Build()
	os.Exit(utility.Run(os.Args))
}
