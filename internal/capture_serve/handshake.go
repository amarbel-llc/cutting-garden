package capture_serve

import (
	"io"
	"net"

	"code.linenisgreat.com/cutting-garden/internal/plugin_handshake"
)

// The RFC 0008 launch handshake, go-plugin style (the pattern established
// by madder RFC 0001): the orchestrator exports a magic cookie, spawns the
// plugin's capture-serve subcommand, and the plugin binds a unixpacket
// rendezvous socket and announces it as ONE stdout line the orchestrator
// parses, cookie-validates, and dials. stdout carries nothing else;
// stdin EOF / a shutdown notification / SIGTERM end the session.
//
// The protocol-independent logic lives in plugin_handshake; this file
// pins RFC 0008's launch identity and delegates.
const (
	// CookieEnv is the magic-cookie environment variable. A plugin
	// invoked without it MUST exit non-zero without printing to stdout —
	// the guard against accidental direct invocation.
	CookieEnv = "CAPTURE_PLUGIN_COOKIE"

	// HandshakeNetwork is the announce line's network field: the
	// rendezvous socket is always SOCK_SEQPACKET ("unixpacket"), so one
	// SCM_RIGHTS fd associates unambiguously with one JSON-RPC datagram.
	//
	// Darwin/XNU never implemented AF_UNIX SOCK_SEQPACKET — ListenRendezvous
	// fails there with EPROTONOSUPPORT, so v2 bring-up always fails and
	// Launch's caller always falls back to v1 on that platform (a graceful,
	// intentional degradation — see RFC 0008 §Open questions and
	// cutting-garden#137). Not a bug to "fix" without a wire-protocol change.
	HandshakeNetwork = "unixpacket"

	// HandshakeSubprotocol terminates the announce line, naming the
	// protocol spoken over the dialed socket.
	HandshakeSubprotocol = "capture-plugin"

	// announceFields is the exact field count of the announce line:
	// cookie|version|network|address|metadata|subprotocol.
	announceFields = 6

	// sunPathMax conservatively bounds an AF_UNIX socket path. sun_path
	// is ~108 bytes on Linux; the margin absorbs the NUL and platform
	// variance. The Phase 0 spike's design finding: a deep $TMPDIR
	// overflows this, so rendezvous sockets bind at SHORT paths.
	sunPathMax = 100
)

// proto is RFC 0008's launch identity, driving the shared handshake
// logic in plugin_handshake.
var proto = plugin_handshake.Protocol{
	CookieEnv:    CookieEnv,
	Network:      HandshakeNetwork,
	Subprotocol:  HandshakeSubprotocol,
	SocketPrefix: "cg-serve-",
}

// Handshake is the parsed announce line — everything the orchestrator
// needs to dial the plugin's rendezvous socket.
type Handshake = plugin_handshake.Handshake

// NewCookie returns a fresh random magic cookie for one plugin launch.
func NewCookie() (string, error) {
	return proto.NewCookie()
}

// CookieFromEnv reads the launch cookie a plugin must echo on its
// announce line. Absence means the process was not launched by an
// orchestrator: the caller MUST exit non-zero without touching stdout.
func CookieFromEnv() (string, error) {
	return proto.CookieFromEnv()
}

// AnnounceLine renders the single stdout line a plugin prints once its
// rendezvous socket is listening (newline included).
func AnnounceLine(cookie string, h Handshake) (string, error) {
	return proto.AnnounceLine(cookie, h)
}

// ParseAnnounceLine validates one stdout line against the launch cookie
// and parses it. Any line that is not a well-formed announce echoing the
// cookie — a log line, a wrong field count, a foreign subprotocol — is a
// rejected handshake: the orchestrator kills the child and falls back.
func ParseAnnounceLine(line, wantCookie string) (Handshake, error) {
	return proto.ParseAnnounceLine(line, wantCookie)
}

// ReadAnnounce reads the plugin's FIRST stdout line and parses it as the
// announce. Anything else on that first line rejects the handshake —
// stdout is protocol-only under RFC 0008, so pollution is a bring-up
// failure (which the caller treats as "fall back to v1"). Deadlines are
// the caller's job (kill the child on timeout); this blocks until a line
// or EOF arrives. (The underlying read reports the usual io.EOF failure
// as a fresh error, not a wrap — dewey's errors refuses to wrap bare
// io.EOF by policy.)
func ReadAnnounce(stdout io.Reader, wantCookie string) (Handshake, error) {
	return proto.ReadAnnounce(stdout, wantCookie)
}

// ListenRendezvous binds a fresh unixpacket rendezvous socket in a new
// 0700 temp directory at a path short enough for sun_path. It prefers
// $XDG_RUNTIME_DIR (user-private tmpfs) and falls back to /tmp; a base
// whose paths would overflow sun_path is skipped (the Phase 0 finding —
// never bind inside a deeply-nested worktree tmp). cleanup closes the
// listener and removes the directory (unlinking the socket).
func ListenRendezvous() (
	ln *net.UnixListener, socketPath string, cleanup func(), err error,
) {
	return proto.ListenRendezvous()
}

// DialAnnounced connects to the socket a validated announce named.
func DialAnnounced(h Handshake) (*net.UnixConn, error) {
	return proto.DialAnnounced(h)
}
