package capture_serve

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// announceTimeout bounds plugin bring-up: a child that has not produced
// its announce line within this window is killed and the launch fails.
// The v2→v1 fallback rides on bring-up failing FAST and unambiguously —
// a missing capture-serve subcommand exits (stdout EOF) in milliseconds;
// this deadline only catches a child that hangs silently.
const announceTimeout = 10 * time.Second

// shutdownGrace bounds Session.Close's wait for the child to exit after
// its shutdown signals (the shutdown notification RunBatch sent, the
// control-socket close, and stdin EOF) before escalating to SIGKILL.
const shutdownGrace = 5 * time.Second

// Session is a launched capture-serve plugin: the dialed control
// connection plus the child process whose lifecycle Close manages.
type Session struct {
	// Conn is the dialed control connection (hand it to RunBatch).
	Conn *net.UnixConn
	// Handshake is the validated announce the plugin printed.
	Handshake Handshake

	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// Launch spawns argv as a capture-serve plugin and completes the RFC
// 0008 launch handshake: export a fresh cookie, read the announce line
// from the child's stdout, validate, dial. Any failure — the subcommand
// missing (immediate exit, stdout EOF), stdout pollution, a cookie or
// version mismatch, or the announce deadline — kills the child and
// returns an error; every Launch error is a bring-up failure the caller
// MAY treat as "fall back to capture-plugin/v1". The child's stderr
// passes through for diagnostics; its stdin is held open (EOF on it is
// the child's shutdown signal, sent by Close).
func Launch(
	ctx context.Context, name string, args ...string,
) (*Session, error) {
	cookie, err := NewCookie()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), CookieEnv+"="+cookie)
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
		return nil, errors.Wrapf(err, "spawn %s", name)
	}

	type announced struct {
		h   Handshake
		err error
	}
	ch := make(chan announced, 1)
	go func() {
		h, aerr := ReadAnnounce(stdout, cookie)
		ch <- announced{h: h, err: aerr}
	}()

	abandon := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	select {
	case <-ctx.Done():
		abandon()
		return nil, errors.Wrapf(ctx.Err(), "launch %s", name)
	case <-time.After(announceTimeout):
		abandon()
		return nil, errors.ErrorWithStackf(
			"%s: no announce line within %s", name, announceTimeout,
		)
	case res := <-ch:
		if res.err != nil {
			abandon()
			return nil, errors.Wrapf(res.err, "launch %s", name)
		}
		conn, derr := DialAnnounced(res.h)
		if derr != nil {
			abandon()
			return nil, derr
		}
		return &Session{
			Conn:      conn,
			Handshake: res.h,
			cmd:       cmd,
			stdin:     stdin,
		}, nil
	}
}

// Close ends the session: it closes the control connection (a no-op
// when RunBatch's peer already did) and the child's stdin — both
// RFC 0008 shutdown signals — then waits shutdownGrace for a clean exit
// before SIGKILL. The returned error is the child's exit status; after
// a graceful shutdown notification it exits 0.
func (s *Session) Close() error {
	// Both closes may find their target already closed; the child's
	// exit status is the outcome that matters.
	_ = s.Conn.Close()
	_ = s.stdin.Close()

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
			return errors.Wrapf(err, "plugin killed after %s grace", shutdownGrace)
		}
		return nil
	}
}

// Run is the whole v2 client path: launch argv, drive one batch, tear
// the session down. Node blobs land in dest; the receipt tree is
// byte-identical to the in-process RFC 0002 form. A Launch error or an
// initialize error carrying CodeUnsupportedVersion is the caller's
// fall-back-to-v1 signal; the batch result and any batch error pass
// through from RunBatch.
func Run(
	ctx context.Context,
	dest capture_plugin.Writer,
	batch BatchParams,
	name string,
	args ...string,
) (BatchResult, error) {
	sess, err := Launch(ctx, name, args...)
	if err != nil {
		return BatchResult{}, err
	}

	result, err := RunBatch(ctx, sess.Conn, dest, batch)
	if cerr := sess.Close(); err == nil && cerr != nil {
		err = cerr
	}
	return result, err
}
