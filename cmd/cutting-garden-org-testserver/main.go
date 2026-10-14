// Command cutting-garden-org-testserver is a test-only in-memory WebDAV
// server for the bats org lane: the standalone, coproc-spawned form of
// plugins/org/orgtestserver, mirroring cutting-garden-fastmail-testserver's
// contract. It is NOT shipped in the cutting-garden release.
//
// Usage: cutting-garden-org-testserver FIXTURE_DIR
//
// It seeds every file under FIXTURE_DIR (lexical order, so ETags are
// deterministic), prints one handshake line to stdout —
//
//	<root-url>
//
// — and serves until its stdin is closed. Credentials are
// orgtestserver.Username / orgtestserver.Password.
package main

import (
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/plugins/org/orgtestserver"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cutting-garden-org-testserver FIXTURE_DIR")
		os.Exit(64)
	}
	// CG_TEST_ORG_PORT pins the listen port (else an ephemeral one).
	var addr string
	if port := os.Getenv("CG_TEST_ORG_PORT"); port != "" {
		addr = "127.0.0.1:" + port
	}
	srv := orgtestserver.StartAt(addr)
	if err := srv.SeedDir(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "cutting-garden-org-testserver:", err)
		srv.Close()
		os.Exit(1)
	}

	fmt.Println(srv.URL())

	// Block until stdin closes — the coproc shutdown signal.
	_, _ = io.Copy(io.Discard, os.Stdin)

	srv.Close()
}
