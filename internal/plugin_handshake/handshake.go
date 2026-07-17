// Package plugin_handshake implements the protocol-independent half of
// the go-plugin-style launch handshake (the pattern established by
// madder RFC 0001): the orchestrator exports a magic cookie, spawns the
// plugin's serve subcommand, and the plugin binds a rendezvous socket
// and announces it as ONE stdout line the orchestrator parses,
// cookie-validates, and dials. stdout carries nothing else; stdin EOF /
// a shutdown notification / SIGTERM end the session.
//
// Each transport (capture_serve for RFC 0008, traversal_serve for
// RFC 0013) declares one Protocol value pinning its launch identity;
// everything protocol-specific — cookie env var, socket family,
// subprotocol token — lives in that value, nothing here.
package plugin_handshake

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

const (
	// announceFields is the exact field count of the announce line:
	// cookie|version|network|address|metadata|subprotocol.
	announceFields = 6

	// sunPathMax conservatively bounds an AF_UNIX socket path. sun_path
	// is ~108 bytes on Linux; the margin absorbs the NUL and platform
	// variance. The Phase 0 spike's design finding: a deep $TMPDIR
	// overflows this, so rendezvous sockets bind at SHORT paths.
	sunPathMax = 100
)

// Protocol pins one plugin transport's launch identity: which cookie env
// authenticates the child, which socket family the rendezvous uses, and
// which subprotocol token terminates the announce line. capture_serve
// (RFC 0008) and traversal_serve (RFC 0013) each declare one.
type Protocol struct {
	CookieEnv    string // e.g. "CAPTURE_PLUGIN_COOKIE"
	Network      string // "unixpacket" (RFC 0008) or "unix" (RFC 0013)
	Subprotocol  string // e.g. "capture-plugin"
	SocketPrefix string // MkdirTemp prefix, e.g. "cg-serve-"
}

// Handshake is the parsed announce line — everything the orchestrator
// needs to dial the plugin's rendezvous socket.
type Handshake struct {
	// Version is the protocol token the plugin speaks.
	Version string
	// Network is the socket family; MUST be the Protocol's Network.
	Network string
	// Address is the rendezvous socket path.
	Address string
	// Metadata is free-form plugin metadata; MAY be empty. MUST NOT
	// contain '|' or newlines.
	Metadata string
}

// NewCookie returns a fresh random magic cookie for one plugin launch.
func (p Protocol) NewCookie() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.Wrap(err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// CookieFromEnv reads the launch cookie a plugin must echo on its
// announce line. Absence means the process was not launched by an
// orchestrator: the caller MUST exit non-zero without touching stdout.
func (p Protocol) CookieFromEnv() (string, error) {
	cookie := os.Getenv(p.CookieEnv)
	if cookie == "" {
		return "", errors.ErrorWithStackf(
			"%s is not set: not launched by an orchestrator",
			p.CookieEnv,
		)
	}
	return cookie, nil
}

// AnnounceLine renders the single stdout line a plugin prints once its
// rendezvous socket is listening (newline included).
func (p Protocol) AnnounceLine(cookie string, h Handshake) (string, error) {
	for name, field := range map[string]string{
		"cookie":   cookie,
		"version":  h.Version,
		"network":  h.Network,
		"address":  h.Address,
		"metadata": h.Metadata,
	} {
		if strings.ContainsAny(field, "|\n") {
			return "", errors.ErrorWithStackf(
				"announce %s field %q contains a delimiter", name, field,
			)
		}
	}
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%s\n",
		cookie, h.Version, h.Network, h.Address, h.Metadata,
		p.Subprotocol,
	), nil
}

// ParseAnnounceLine validates one stdout line against the launch cookie
// and parses it. Any line that is not a well-formed announce echoing the
// cookie — a log line, a wrong field count, a foreign subprotocol — is a
// rejected handshake: the orchestrator kills the child and falls back.
func (p Protocol) ParseAnnounceLine(line, wantCookie string) (Handshake, error) {
	line = strings.TrimSuffix(line, "\n")
	fields := strings.Split(line, "|")
	if len(fields) != announceFields {
		return Handshake{}, errors.ErrorWithStackf(
			"announce line has %d fields, want %d (stdout pollution?)",
			len(fields), announceFields,
		)
	}
	if fields[0] != wantCookie {
		return Handshake{}, errors.ErrorWithStackf(
			"announce cookie mismatch",
		)
	}
	if fields[5] != p.Subprotocol {
		return Handshake{}, errors.ErrorWithStackf(
			"announce subprotocol %q, want %q",
			fields[5], p.Subprotocol,
		)
	}
	h := Handshake{
		Version:  fields[1],
		Network:  fields[2],
		Address:  fields[3],
		Metadata: fields[4],
	}
	if h.Version == "" || h.Network == "" || h.Address == "" {
		return Handshake{}, errors.ErrorWithStackf(
			"announce line has empty version/network/address",
		)
	}
	return h, nil
}

// ReadAnnounce reads the plugin's FIRST stdout line and parses it as the
// announce. Anything else on that first line rejects the handshake —
// stdout is protocol-only, so pollution is a bring-up failure. Deadlines
// are the caller's job (kill the child on timeout); this blocks until a
// line or EOF arrives.
func (p Protocol) ReadAnnounce(
	stdout io.Reader, wantCookie string,
) (Handshake, error) {
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		// A fresh error, not a wrap: the usual failure here is io.EOF (the
		// child exited before announcing), which dewey's errors refuses to
		// wrap by policy.
		return Handshake{}, errors.ErrorWithStackf(
			"read announce line (child exited before announcing?): %s", err,
		)
	}
	return p.ParseAnnounceLine(line, wantCookie)
}

// ListenRendezvous binds a fresh rendezvous socket of the Protocol's
// family in a new 0700 temp directory at a path short enough for
// sun_path. It prefers $XDG_RUNTIME_DIR (user-private tmpfs) and falls
// back to /tmp; a base whose paths would overflow sun_path is skipped
// (the Phase 0 finding — never bind inside a deeply-nested worktree
// tmp). cleanup closes the listener and removes the directory
// (unlinking the socket).
func (p Protocol) ListenRendezvous() (
	ln *net.UnixListener, socketPath string, cleanup func(), err error,
) {
	bases := []string{os.Getenv("XDG_RUNTIME_DIR"), "/tmp"}
	var lastErr error
	for _, base := range bases {
		if base == "" {
			continue
		}
		dir, derr := os.MkdirTemp(base, p.SocketPrefix)
		if derr != nil {
			lastErr = derr
			continue
		}
		sock := filepath.Join(dir, "s")
		if len(sock) > sunPathMax {
			os.RemoveAll(dir)
			lastErr = errors.ErrorWithStackf(
				"rendezvous path %q exceeds sun_path bound", sock,
			)
			continue
		}
		l, lerr := net.Listen(p.Network, sock)
		if lerr != nil {
			os.RemoveAll(dir)
			lastErr = lerr
			continue
		}
		unixLn := l.(*net.UnixListener)
		return unixLn, sock, func() {
			unixLn.Close()
			os.RemoveAll(dir)
		}, nil
	}
	return nil, "", nil, errors.Wrapf(
		lastErr, "no usable rendezvous socket base",
	)
}

// DialAnnounced connects to the socket a validated announce named.
func (p Protocol) DialAnnounced(h Handshake) (*net.UnixConn, error) {
	if h.Network != p.Network {
		return nil, errors.ErrorWithStackf(
			"announce network %q, want %q", h.Network, p.Network,
		)
	}
	conn, err := net.Dial(h.Network, h.Address)
	if err != nil {
		return nil, errors.Wrapf(err, "dial announced socket %s", h.Address)
	}
	return conn.(*net.UnixConn), nil
}
