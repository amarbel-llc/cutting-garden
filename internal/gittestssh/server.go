// Package gittestssh is an in-process ssh server that serves git's pack
// helpers (git upload-pack / receive-pack) over a real ssh transport. It
// exists to test cutting-garden's git ssh path — capture/diff/restore over
// ssh — without an external `sshd`, which the nix bats sandbox cannot run
// unprivileged (no privilege-separation user). `git` is test scaffolding
// here; the cutting-garden git plugin itself is pure-Go.
//
// The server accepts any public key (it is a localhost test server) and
// runs `git upload-pack`/`git receive-pack` for the corresponding exec
// requests, wiring the ssh channel to the helper's stdio exactly as real
// git-over-ssh does. It is used both by the in-process ssh E2E test and by
// the cutting-garden-test-git-sshd binary that backs the bats ssh lane.
package gittestssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os/exec"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Server is a running localhost git-over-ssh test server.
type Server struct {
	ln      net.Listener
	hostPub ssh.PublicKey
}

// Start launches the server on 127.0.0.1 with an ephemeral port and a
// freshly generated host key. The caller owns the returned Server and must
// Close it.
func Start() (*Server, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.Wrap(err)
	}

	s := &Server{ln: ln, hostPub: signer.PublicKey()}
	go s.acceptLoop(cfg)
	return s, nil
}

// Addr is the server's host:port (e.g. 127.0.0.1:54321).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops accepting connections.
func (s *Server) Close() error { return s.ln.Close() }

// KnownHostsLine returns an OpenSSH known_hosts line trusting this server's
// host key at its address — write it to a file and point SSH_KNOWN_HOSTS at
// it so go-git's host-key check passes.
func (s *Server) KnownHostsLine() string {
	return knownhosts.Line([]string{knownhosts.Normalize(s.Addr())}, s.hostPub)
}

func (s *Server) acceptLoop(cfg *ssh.ServerConfig) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go serveConn(conn, cfg)
	}
}

func serveConn(nConn net.Conn, cfg *ssh.ServerConfig) (err error) {
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
		go handleSession(ch, chReqs)
	}
	return nil
}

func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
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
// `git-receive-pack '<path>'` command and runs the corresponding git
// subcommand with the ssh channel as its stdio. Returns the helper's exit
// code. Using the `git <subcommand>` form needs only `git` on PATH (not
// the dashed helper binaries).
func runGitService(ch ssh.Channel, cmd string) int {
	fields := strings.SplitN(cmd, " ", 2)
	if len(fields) != 2 {
		return 1
	}
	var sub string
	switch fields[0] {
	case "git-upload-pack":
		sub = "upload-pack"
	case "git-receive-pack":
		sub = "receive-pack"
	default:
		return 1
	}
	path := strings.Trim(strings.TrimSpace(fields[1]), "'\"")

	c := exec.Command("git", sub, path)
	c.Stdin = ch
	c.Stdout = ch
	c.Stderr = ch.Stderr()
	if rerr := c.Run(); rerr != nil {
		if exitErr, ok := rerr.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}
