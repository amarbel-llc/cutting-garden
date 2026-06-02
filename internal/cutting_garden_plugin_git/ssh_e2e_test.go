package cutting_garden_plugin_git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// TestSSHRemote_CaptureDiffRestore is the ssh-transport E2E: capture, diff,
// and restore (push) against a repo served over real ssh — exercising the
// plugin's actual ssh path including authMethod's ssh-agent auth and
// go-git's known_hosts host-key check.
//
// The "remote" is a pure-Go ssh server (golang.org/x/crypto/ssh) that, on a
// `git-upload-pack`/`git-receive-pack` exec request, runs git's pack
// helpers — the same mechanism real git-over-ssh uses, without needing an
// unprivileged `sshd` (which the nix bats sandbox can't run). git's pack
// helpers are test scaffolding here; the plugin itself stays pure-Go.
func TestSSHRemote_CaptureDiffRestore(t *testing.T) {
	if _, err := exec.LookPath("git-upload-pack"); err != nil {
		t.Skip("git-upload-pack not on PATH")
	}

	hostSigner, hostPub := genSigner(t)
	port := startSSHGitServer(t, hostSigner)
	addr := net.JoinHostPort("127.0.0.1", port)

	// Trust the server's host key via SSH_KNOWN_HOSTS (go-git reads it).
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, hostPub) + "\n"
	if err := os.WriteFile(knownHosts, []byte(line), 0o644); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)

	// Offer a key through an in-process ssh-agent, which is what authMethod
	// (NewSSHAgentAuth) consumes. The server accepts any key.
	startAgent(t)

	src, branch, tips := buildRepo(t, map[string]string{"f.txt": "v1"})
	sshURL := func(path string) string { return fmt.Sprintf("ssh://git@%s%s", addr, path) }

	// capture over ssh
	store := newMemStore(t)
	arg := "git:" + sshURL(src) + "#" + branch
	res, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: store,
	})
	if err != nil {
		t.Fatalf("ssh capture: %v", err)
	}
	if res.ObjectCount != 3 {
		t.Fatalf("ObjectCount = %d, want 3", res.ObjectCount)
	}

	// clean diff over ssh (tip unchanged)
	diff, err := (Plugin{}).DiffProtocol(cutting_garden_plugins.ProtocolDiffRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		Source:        mustParseURL(t, arg),
		RawSource:     arg,
	})
	if err != nil {
		t.Fatalf("ssh diff: %v", err)
	}
	if len(diff.Differences) != 0 {
		t.Fatalf("expected no drift over ssh, got %v", diff.Differences)
	}

	// restore-push over ssh into a bare "remote"
	bare := filepath.Join(t.TempDir(), "dest.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	pushURL := sshURL(bare)
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		Dest:          mustParseURL(t, pushURL),
		RawDest:       pushURL,
	}); err != nil {
		t.Fatalf("ssh restore-push: %v", err)
	}

	remote, err := git.PlainOpen(bare)
	if err != nil {
		t.Fatalf("open pushed bare repo: %v", err)
	}
	ref, err := remote.Reference(plumbing.NewBranchReferenceName(branch), false)
	if err != nil {
		t.Fatalf("pushed branch %s missing: %v", branch, err)
	}
	if ref.Hash().String() != tips[0] {
		t.Errorf("pushed tip = %q, want %q", ref.Hash().String(), tips[0])
	}
}

// genSigner returns a fresh ed25519 ssh signer and its public key.
func genSigner(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer, signer.PublicKey()
}

// startAgent serves an in-process ssh-agent holding a fresh key on a unix
// socket and points SSH_AUTH_SOCK at it.
func startAgent(t *testing.T) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("agent key: %v", err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatalf("agent add: %v", err)
	}

	// A short path under /tmp — unix socket paths have a ~108-char limit
	// that the worktree's .tmp/<longtestname> dir blows past.
	sockDir, err := os.MkdirTemp("", "cgssh")
	if err != nil {
		t.Fatalf("agent sock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "a.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("agent listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, conn) }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sockPath)
}

// startSSHGitServer starts a localhost ssh server that runs git's pack
// helpers for exec requests, accepting any key. Returns the port.
func startSSHGitServer(t *testing.T, host ssh.Signer) string {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(host)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ssh listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			nConn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go serveSSHConn(nConn, cfg)
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func serveSSHConn(nConn net.Conn, cfg *ssh.ServerConfig) (err error) {
	sConn, chans, reqs, nerr := ssh.NewServerConn(nConn, cfg)
	if nerr != nil {
		return nerr
	}
	defer errors.DeferredCloser(&err, sConn)
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, chReqs, aerr := newCh.Accept()
		if aerr != nil {
			return aerr
		}
		go handleSSHSession(ch, chReqs)
	}
	return nil
}

func handleSSHSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		// exec payload: a 4-byte length prefix followed by the command.
		cmd := string(req.Payload[4:])
		_ = req.Reply(true, nil)
		status := runGitService(ch, cmd)
		_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, byte(status)})
		_ = ch.Close()
		return
	}
}

// runGitService parses a `git-upload-pack '<path>'` /
// `git-receive-pack '<path>'` command and runs the helper with the ssh
// channel as its stdio. Returns the helper's exit code.
func runGitService(ch ssh.Channel, cmd string) int {
	fields := strings.SplitN(cmd, " ", 2)
	if len(fields) != 2 {
		return 1
	}
	service := fields[0]
	if service != "git-upload-pack" && service != "git-receive-pack" {
		return 1
	}
	path := strings.Trim(strings.TrimSpace(fields[1]), "'\"")

	c := exec.Command(service, path)
	c.Stdin = ch
	c.Stdout = ch
	c.Stderr = ch.Stderr()
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}
