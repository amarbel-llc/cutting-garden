package traversal_serve

import (
	"net"

	"code.linenisgreat.com/cutting-garden/internal/plugin_handshake"
)

// The RFC 0013 launch handshake, go-plugin style (the pattern
// established by madder RFC 0001): the host exports a magic cookie,
// spawns the plugin's traversal-serve subcommand, and the plugin binds
// an AF_UNIX SOCK_STREAM rendezvous socket and announces it as ONE
// stdout line the host parses, cookie-validates, and dials. stdout
// carries nothing else; stdin EOF / a shutdown notification / SIGTERM
// end the session.
//
// The protocol-independent logic lives in plugin_handshake; this file
// pins RFC 0013's launch identity and delegates.
const (
	// CookieEnv is the magic-cookie environment variable (RFC 0013
	// §Launch and rendezvous). A plugin invoked without it MUST exit
	// non-zero without printing to stdout — the guard against
	// accidental direct invocation.
	CookieEnv = "TRAVERSAL_PLUGIN_COOKIE"

	// HandshakeNetwork is the announce line's network field: the
	// rendezvous socket is AF_UNIX SOCK_STREAM ("unix") — a byte
	// stream, unlike RFC 0008's SEQPACKET, because NDJSON framing
	// needs no datagram boundaries and streams work on every platform
	// (including darwin, where SEQPACKET does not exist).
	HandshakeNetwork = "unix"

	// HandshakeSubprotocol terminates the announce line, naming the
	// protocol spoken over the dialed socket.
	HandshakeSubprotocol = "traversal-plugin"
)

// proto is RFC 0013's launch identity, driving the shared handshake
// logic in plugin_handshake.
var proto = plugin_handshake.Protocol{
	CookieEnv:    CookieEnv,
	Network:      HandshakeNetwork,
	Subprotocol:  HandshakeSubprotocol,
	SocketPrefix: "cg-trav-",
}

// Handshake is the parsed announce line — everything the host needs to
// dial the plugin's rendezvous socket.
type Handshake = plugin_handshake.Handshake

// CookieFromEnv reads the launch cookie a plugin must echo on its
// announce line. Absence means the process was not launched by a host:
// the caller MUST exit non-zero without touching stdout.
func CookieFromEnv() (string, error) {
	return proto.CookieFromEnv()
}

// AnnounceLine renders the single stdout line a plugin prints once its
// rendezvous socket is listening (newline included).
func AnnounceLine(cookie string, h Handshake) (string, error) {
	return proto.AnnounceLine(cookie, h)
}

// ListenRendezvous binds a fresh unix stream rendezvous socket in a new
// 0700 temp directory at a path short enough for sun_path. It prefers
// $XDG_RUNTIME_DIR (user-private tmpfs) and falls back to /tmp; a base
// whose paths would overflow sun_path is skipped (never bind inside a
// deeply-nested worktree tmp). cleanup closes the listener and removes
// the directory (unlinking the socket).
func ListenRendezvous() (
	ln *net.UnixListener, socketPath string, cleanup func(), err error,
) {
	return proto.ListenRendezvous()
}
