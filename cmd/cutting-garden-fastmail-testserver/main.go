// Command cutting-garden-fastmail-testserver is a test-only in-memory JMAP
// server for the bats fastmail organize lane. It is the standalone,
// coproc-spawned form of plugins/fastmail/fastmailtestserver, seeded with
// that package's deterministic inbox-triage fixture (SeedTriageFixture), and
// mirrors cutting-garden-caldav-testserver's contract. It is NOT shipped in
// the cutting-garden release.
//
// Protocol (the caldav-testserver coproc contract): on startup it seeds the
// fixture, prints one handshake line to stdout —
//
//	<session-url> <account-id>
//
// — where <session-url> is the JMAP Session endpoint a config account's
// `session_url` points at and <account-id> the mail account id the session
// advertises, then serves until its stdin is closed (the shutdown signal the
// bats helper sends). It accepts any (or no) bearer token.
package main

import (
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/plugins/fastmail/fastmailtestserver"
)

func main() {
	// CG_TEST_FASTMAIL_PORT pins the listen port (else an ephemeral one). The
	// organize lane sets it so the session URL it writes into config.toml —
	// and thus every organize document's `_base` digest — is stable enough
	// for whole-document vectors.
	var addr string
	if port := os.Getenv("CG_TEST_FASTMAIL_PORT"); port != "" {
		addr = "127.0.0.1:" + port
	}
	srv := fastmailtestserver.StartAt("acct-test", addr)
	fastmailtestserver.SeedTriageFixture(srv)

	fmt.Printf("%s %s\n", srv.SessionURL(), srv.AccountID())

	// Block until stdin closes — the coproc shutdown signal.
	_, _ = io.Copy(io.Discard, os.Stdin)

	srv.Close()
}
