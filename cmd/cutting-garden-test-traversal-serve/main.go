// Command cutting-garden-test-traversal-serve is the cg-owned RFC 0013
// test plugin as a standalone binary: a deterministic traversal peer
// serving the fixed cgtest tree
// (internal/traversal_serve_testpeer) that the bats conformance lane
// launches to exercise the transport with no real backend. Built as its
// own derivation (flake.nix, Task 8; injected into the bats lane as
// CG_TEST_TRAVERSAL_SERVE) and NOT shipped.
package main

import (
	"os"

	testpeer "code.linenisgreat.com/cutting-garden/internal/traversal_serve_testpeer"
)

func main() { os.Exit(testpeer.Main()) }
