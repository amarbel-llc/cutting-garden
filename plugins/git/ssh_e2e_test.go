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
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/plugins/git/gittestssh"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"golang.org/x/crypto/ssh/agent"
)

// TestSSHRemote_CaptureDiffRestore is the ssh-transport E2E: capture, a
// clean diff, and a restore-push, all against a repo served over real ssh
// — exercising the plugin's actual ssh path including authMethod's
// ssh-agent auth and go-git's known_hosts host-key check. The server is the
// shared in-process gittestssh (the same one the bats ssh lane drives as a
// standalone binary).
func TestSSHRemote_CaptureDiffRestore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH (test ssh server runs git's pack helpers)")
	}

	// Every t.TempDir() is created BEFORE the server starts: t.Cleanup
	// runs LIFO, so the server's Close — which drains the git pack
	// helpers writing into these directories (#57) — must be registered
	// after them to run first.
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	src, branch, tips := buildRepo(t, map[string]string{"f.txt": "v1"})
	bare := filepath.Join(t.TempDir(), "dest.git")

	srv, err := gittestssh.Start()
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	addr := srv.Addr()

	// Trust the server's host key via SSH_KNOWN_HOSTS (go-git reads it).
	if err := os.WriteFile(knownHosts, []byte(srv.KnownHostsLine()+"\n"), 0o644); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)

	// Offer a key through an in-process ssh-agent (what authMethod's
	// NewSSHAgentAuth consumes); the server accepts any key.
	startAgent(t)

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

	// A short path under literally /tmp — unix socket paths have a
	// ~108-char limit. MkdirTemp("", ...) honors $TMPDIR, which the
	// worktree's .tmp/ (and the merge gate's nested nix-shell tmpdirs)
	// blows past, yielding bind: invalid argument. Pin the parent.
	sockDir, err := os.MkdirTemp("/tmp", "cgssh")
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
