// Command cutting-garden-ynab-testserver is a test-only in-memory YNAB REST
// server for the bats ynab lane: the standalone, coproc-spawned form of
// plugins/ynab/ynabtestserver, seeded with its deterministic fixture and
// mirroring cutting-garden-org-testserver's contract. It is NOT shipped in the
// cutting-garden release.
//
// Usage: cutting-garden-ynab-testserver
//
// It prints one handshake line to stdout —
//
//	<api-base-url>
//
// — and serves until its stdin is closed. The API base is what an account's
// `session_url` carries; every request must present the bearer token
// ynabtestserver.Token.
package main

import (
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/plugins/ynab/ynabtestserver"
)

func main() {
	// CG_TEST_YNAB_PORT pins the listen port (else an ephemeral one).
	var addr string
	if port := os.Getenv("CG_TEST_YNAB_PORT"); port != "" {
		addr = "127.0.0.1:" + port
	}
	srv := ynabtestserver.StartAt(ynabtestserver.Token, addr)
	ynabtestserver.SeedFixture(srv)

	fmt.Println(srv.BaseURL())

	// Block until stdin closes — the coproc shutdown signal.
	_, _ = io.Copy(io.Discard, os.Stdin)

	srv.Close()
}
