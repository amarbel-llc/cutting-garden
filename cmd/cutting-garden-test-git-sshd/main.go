// Command cutting-garden-test-git-sshd is a test-only git-over-ssh server
// for the bats ssh lane. It is the standalone, coproc-spawned form of
// internal/gittestssh: a localhost ssh server that runs git's pack helpers,
// so bats can drive a real ssh capture/diff/restore without an external
// `sshd` (which the nix bats sandbox can't run unprivileged). It is NOT
// shipped in the cutting-garden release.
//
// Protocol (mirrors madder-test-sftp-server's coproc contract): on startup
// it prints one handshake line to stdout —
//
//	<host:port> <known_hosts_path>
//
// — then serves until its stdin is closed (the shutdown signal the bats
// helper sends). The known_hosts file trusts the server's ephemeral host
// key; the caller points SSH_KNOWN_HOSTS at it.
package main

import (
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/plugins/git/gittestssh"
)

func main() {
	srv, err := gittestssh.Start()
	if err != nil {
		fail(err)
	}

	khPath, err := writeKnownHosts(srv)
	if err != nil {
		fail(err)
	}

	// Handshake line.
	fmt.Printf("%s %s\n", srv.Addr(), khPath)

	// Block until our stdin closes — the coproc shutdown signal.
	_, _ = io.Copy(io.Discard, os.Stdin)

	_ = srv.Close()
	_ = os.Remove(khPath)
}

func writeKnownHosts(srv *gittestssh.Server) (string, error) {
	f, err := os.CreateTemp("", "cg-git-sshd-known_hosts")
	if err != nil {
		return "", err
	}
	if _, werr := f.WriteString(srv.KnownHostsLine() + "\n"); werr != nil {
		_ = f.Close()
		return "", werr
	}
	if cerr := f.Close(); cerr != nil {
		return "", cerr
	}
	return f.Name(), nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "cutting-garden-test-git-sshd: %v\n", err)
	os.Exit(1)
}
