// Command cutting-garden-test-capture-serve is the cg-owned RFC 0008
// test plugin as a standalone binary: a deterministic capture-serve peer
// (internal/capture_serve_testpeer) that the bats sandbox lane launches
// to exercise the transport with no chrest dependency. Built as its own
// derivation (flake.nix: cuttingGardenTestCaptureServe, injected into
// bats-capture as CG_TEST_CAPTURE_SERVE) and NOT shipped.
package main

import (
	"os"

	testpeer "code.linenisgreat.com/cutting-garden/internal/capture_serve_testpeer"
)

func main() { os.Exit(testpeer.Main()) }
