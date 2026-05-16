// Command cutting-garden is the canonical cutting-garden CLI entry
// point. See `cg` for the short-name alias (same Utility, same
// behavior). Both binaries call cgapp.Build to build the Utility.
package main

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/cgapp"
)

func main() {
	utility := cgapp.Build()
	os.Exit(utility.Run(os.Args))
}
