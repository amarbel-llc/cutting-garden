// Command cg is the short-name alias of cutting-garden. Behavior is
// identical — both binaries call cgapp.Build to build the same
// Utility. The alias is declared via utility.AddAlias("cg") inside
// cgapp.Build, which surfaces it in PrintUsage's banner, the
// toplevel manpage's NAME line, and produces a `cg.1` manpage
// symlink + cg-named shell completion stubs.
package main

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/cgapp"
)

func main() {
	utility := cgapp.Build()
	os.Exit(utility.Run(os.Args))
}
