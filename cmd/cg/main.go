// Command cg is the short-name alias of cutting-garden. Behavior is
// identical — both binaries call cgapp.Build to build the same
// Utility. The alias is declared via utility.AddAlias("cg") inside
// cgapp.Build, which surfaces it in PrintUsage's banner, the
// toplevel manpage's NAME line, and produces a `cg.1` manpage
// symlink + cg-named shell completion stubs.
package main

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/buildinfo"
	"github.com/amarbel-llc/cutting-garden/internal/cgapp"

	// Register the standard in-repo plugin set. cgapp.Build() is
	// plugin-bare (RFC 0009 §5 step 3); the in-repo binaries opt in here.
	_ "github.com/amarbel-llc/cutting-garden/plugins/all"
)

// Populated at link time via `-X main.version` / `-X main.commit` (see
// cmd/cutting-garden/main.go). The alias burns in the same identity.
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
