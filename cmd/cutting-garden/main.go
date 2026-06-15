// Command cutting-garden is the canonical cutting-garden CLI entry
// point. See `cg` for the short-name alias (same Utility, same
// behavior). Both binaries call cgapp.Build to build the Utility.
package main

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/cgapp"

	// Register the standard in-repo plugin set. cgapp.Build() is
	// plugin-bare (RFC 0009 §5 step 3); the in-repo binaries opt in here.
	_ "github.com/amarbel-llc/cutting-garden/plugins/all"
)

func main() {
	utility := cgapp.Build()
	os.Exit(utility.Run(os.Args))
}
