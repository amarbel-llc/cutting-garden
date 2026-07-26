package traversal_serve

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// announceTimeout bounds plugin bring-up: a child that has not produced
// its announce line within this window is killed and the launch fails.
// A missing traversal-serve subcommand exits (stdout EOF) in
// milliseconds; this deadline only catches a child that hangs silently.
// It also bounds the initialize exchange after the dial, for the same
// reason. RFC 0013 has no fallback protocol — a bring-up failure is
// simply "plugin unavailable" for the affected scheme(s).
const announceTimeout = 10 * time.Second

// shutdownGrace bounds Session.Close's wait for the child to exit after
// its shutdown signals (the shutdown notification, the stream close,
// and stdin EOF) before escalating to SIGKILL (RFC 0013 §Session
// lifecycle).
const shutdownGrace = 5 * time.Second

// Session is a launched traversal plugin: the initialized JSON-RPC
// peer plus the child process whose lifecycle Close manages. The host
// is the sole request initiator in v1, so the peer carries no handler;
// drive the session with Call.
type Session struct {
	// Init is the plugin's validated initialize declaration — schemes,
	// capabilities, node types, facets, bodies — stable for the
	// session's lifetime (RFC 0013 §Handshake).
	Init InitializeResult

	cmd   *exec.Cmd
	stdin io.WriteCloser
	peer  *Peer

	closeOnce sync.Once
	closeErr  error
}

// Launch spawns argv as a traversal plugin and completes the RFC 0013
// bring-up: export a fresh cookie, read the announce line from the
// child's stdout under announceTimeout, validate the version token,
// dial, and initialize (passing configTOML through, validating the
// schema echo). Any failure — the subcommand missing (immediate exit,
// stdout EOF), stdout pollution, a cookie/version/schema mismatch, or a
// deadline — kills the child, reaps it, and returns an error: with no
// fallback protocol, every Launch error means "plugin unavailable". The
// child's stderr passes through for diagnostics; its stdin is held open
// (EOF on it is a shutdown signal, sent by Close).
func Launch(
	ctx context.Context, argv []string, configTOML string,
) (*Session, error) {
	session, err := LaunchWithoutInitialize(ctx, argv)
	if err != nil {
		return nil, err
	}

	initCtx, cancelInit := context.WithTimeout(ctx, announceTimeout)
	defer cancelInit()

	var init InitializeResult
	err = session.Call(initCtx, MethodInitialize, InitializeParams{
		ProtocolVersions: []string{SchemaV1},
		ConfigTOML:       configTOML,
	}, &init)
	if err != nil {
		_ = session.Close()
		return nil, errors.Wrapf(err, "initialize %s", argv[0])
	}

	if init.Schema != SchemaV1 {
		_ = session.Close()
		return nil, errors.ErrorWithStackf(
			"%s: initialize schema %q, want %q",
			argv[0], init.Schema, SchemaV1,
		)
	}

	session.Init = init

	return session, nil
}

// LaunchWithoutInitialize performs the RFC 0013 bring-up UP TO the dial
// — spawn, cookie, announce, version check — and returns a connected
// Session on which initialize has NOT been issued (Session.Init is the
// zero value). Production hosts use Launch, which layers the initialize
// exchange and its validation on top; this half exists for the #186
// conformance driver, where initialize is itself under test and its raw
// result must reach the assertions rather than being consumed by
// bring-up validation.
func LaunchWithoutInitialize(
	ctx context.Context, argv []string,
) (*Session, error) {
	if len(argv) == 0 {
		return nil, errors.ErrorWithStackf("traversal launch: empty argv")
	}

	cookie, err := proto.NewCookie()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// The plugin inherits this process's environment AND working
	// directory (no cmd.Dir). madder resolves a blob-store write's
	// temp-staging dir by walking UP the working directory for a
	// `.madder` (git-`.git` style), falling back to the HOME-derived XDG
	// location only when the walk finds nothing. A spawned wire plugin
	// manages its OWN store (a newsblur/fj plugin's blobs live under its
	// own HOME), so an inherited working directory it never chose must
	// NOT decide its cache root: on a host that runs cutting-garden under
	// one state dir and the plugin's store under another (systemd
	// ReadWritePaths bind mounts), the walk finds the HOST's `.madder`
	// and every plugin blob write then hardlinks temp→dest ACROSS the
	// mount boundary and fails EXDEV, silently (circus#131).
	// MADDER_XDG_USER_LOCATION_ONLY disables the walk-up so the plugin
	// uses its own HOME/XDG location — same mount as its store, so the
	// hardlink stays intra-device. This is the same var cutting-garden's
	// own tests and mcp.bats already set to keep the walk-up off.
	//
	// Scoped to this transport deliberately: the capture-plugin spawns
	// (internal/capture_serve, internal/capture_wire) share this
	// inherited-cwd shape but pipe blobs to the HOST's store, not a
	// self-store, so the same var there could redirect cutting-garden's
	// OWN blob resolution — a separate question, not folded in here.
	cmd.Env = append(
		os.Environ(),
		CookieEnv+"="+cookie,
		"MADDER_XDG_USER_LOCATION_ONLY=1",
	)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Wrapf(err, "spawn %s", argv[0])
	}

	type announced struct {
		h   Handshake
		err error
	}
	ch := make(chan announced, 1)
	go func() {
		h, aerr := proto.ReadAnnounce(stdout, cookie)
		ch <- announced{h: h, err: aerr}
	}()

	abandon := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	var handshake Handshake
	select {
	case <-ctx.Done():
		abandon()
		return nil, errors.Wrapf(ctx.Err(), "launch %s", argv[0])
	case <-time.After(announceTimeout):
		abandon()
		return nil, errors.ErrorWithStackf(
			"%s: no announce line within %s", argv[0], announceTimeout,
		)
	case res := <-ch:
		if res.err != nil {
			abandon()
			return nil, errors.Wrapf(res.err, "launch %s", argv[0])
		}
		handshake = res.h
	}

	// A foreign version token in the announce is a bring-up failure:
	// v1 is the only schema this host speaks, and RFC 0013 defines no
	// fallback to negotiate down to.
	if handshake.Version != SchemaV1 {
		abandon()
		return nil, errors.ErrorWithStackf(
			"%s: announce version %q, want %q",
			argv[0], handshake.Version, SchemaV1,
		)
	}

	conn, err := proto.DialAnnounced(handshake)
	if err != nil {
		abandon()
		return nil, err
	}

	// No handler: the host is the sole request initiator in v1
	// (RFC 0013 §Framing).
	peer := NewPeer(conn)

	return &Session{
		cmd:   cmd,
		stdin: stdin,
		peer:  peer,
	}, nil
}

// Call sends method(params) over the session and decodes the response's
// result into result (which may be nil to discard it). A JSON-RPC error
// response comes back as *RPCError (branch on it with CodeOf); a dead
// session fails every Call with the peer's terminal error.
func (s *Session) Call(
	ctx context.Context, method string, params, result any,
) error {
	return s.peer.Call(ctx, method, params, result)
}

// Done closes once the session's stream has ended (the child died or
// the connection dropped) — the liveness probe the adapter's
// respawn-once policy consults before reusing a cached session.
func (s *Session) Done() <-chan struct{} { return s.peer.Done() }

// Close ends the session gracefully: a best-effort shutdown
// notification, then stdin close and stream close (the redundant
// signals — RFC 0013 §Session lifecycle), then up to shutdownGrace for
// a clean exit before SIGKILL. Idempotent: subsequent calls return the
// first call's result. The returned error is the child's exit status;
// after a graceful shutdown it exits 0.
func (s *Session) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.teardown() })
	return s.closeErr
}

func (s *Session) teardown() error {
	// Best-effort: on a dead stream the notification write fails and
	// the remaining signals (stdin EOF, stream close) carry shutdown.
	_ = s.peer.Notify(MethodShutdown, struct{}{})
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	_ = s.peer.Close()

	// An in-process session (adapter tests construct Session around a
	// peer with no child process) has nothing to reap: the peer close
	// above was the whole teardown.
	if s.cmd == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return errors.Wrapf(err, "plugin exit")
		}
		return nil
	case <-time.After(shutdownGrace):
		_ = s.cmd.Process.Kill()
		if err := <-done; err != nil {
			return errors.Wrapf(
				err, "plugin killed after %s grace", shutdownGrace,
			)
		}
		return nil
	}
}
