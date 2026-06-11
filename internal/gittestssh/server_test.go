package gittestssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestClose_WaitsForRunningGitService pins the drain contract behind
// issue #57: Close must not return while a git pack helper spawned for
// an exec session is still running, so callers can remove the repo
// directories the helper writes into as soon as Close returns. The
// session is held open by a receive-pack waiting for the client's
// command list; Close must block across that window and return once
// the session and its connection end.
func TestClose_WaitsForRunningGitService(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH (the server runs git's pack helpers)")
	}

	bare := filepath.Join(t.TempDir(), "dest.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	srv, err := Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	client := dialTestClient(t, srv)
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := sess.Start(fmt.Sprintf("git-receive-pack '%s'", bare)); err != nil {
		t.Fatalf("exec git-receive-pack: %v", err)
	}

	// Reading the start of the ref advertisement proves receive-pack is
	// running before Close is called.
	if _, err := io.ReadFull(stdout, make([]byte, 4)); err != nil {
		t.Fatalf("read advertisement: %v", err)
	}

	closed := make(chan struct{})
	go func() {
		_ = srv.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("Close returned while a receive-pack session was still in flight")
	case <-time.After(200 * time.Millisecond):
	}

	// A flush-pkt with no update commands ends the push cleanly.
	if _, err := io.WriteString(stdin, "0000"); err != nil {
		t.Fatalf("write flush-pkt: %v", err)
	}
	_ = stdin.Close()
	_ = sess.Wait()
	_ = client.Close()

	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return after the session and connection ended")
	}
}

// dialTestClient connects an ssh client to srv. The server accepts any
// public key; the host key is ignored because the dialer already trusts
// the in-process server it just started.
func dialTestClient(t *testing.T, srv *Server) *ssh.Client {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("client signer: %v", err)
	}
	client, err := ssh.Dial("tcp", srv.Addr(), &ssh.ClientConfig{
		User:            "git",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
