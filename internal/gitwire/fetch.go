package gitwire

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"os/exec"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ErrUnsupportedTransport is returned for remotes whose transport
// gitwire does not implement (ssh, git://, dumb http). Callers treat it
// as a signal to fall back to a full clone.
var ErrUnsupportedTransport = stderrors.New("gitwire: unsupported transport")

// FetchDelta negotiates a fetch of want (a commit oid) from remote,
// advertising haves as objects already held, and unpacks the resulting
// self-contained pack into scratchGitDir (a directory already
// initialized with `git init`). Only the objects reachable from want but
// not from any have cross the wire.
//
// remote may be a local path (served via `git upload-pack`) or an
// http(s) URL (smart-HTTP). Other schemes yield ErrUnsupportedTransport.
func FetchDelta(
	ctx context.Context,
	remote, want string,
	haves []string,
	scratchGitDir string,
) (err error) {
	c, err := dial(ctx, remote)
	if err != nil {
		return err
	}
	defer errors.DeferredCloser(&err, c)

	advertised, err := c.advertise()
	if err != nil {
		return errors.Wrapf(err, "gitwire: read ref advertisement from %q", remote)
	}

	resp, err := c.request(want, haves, selectCaps(advertised))
	if err != nil {
		return errors.Wrapf(err, "gitwire: negotiate fetch from %q", remote)
	}

	if err := drainAcks(resp); err != nil {
		return errors.Wrap(err)
	}

	return unpackObjects(ctx, scratchGitDir, resp)
}

// selectCaps chooses the client capabilities to send: those we both
// support and the server advertised, deliberately EXCLUDING `thin-pack`
// (so the server sends a self-contained pack) and `side-band*` (so the
// pack follows the ack lines as a raw stream). `agent` is always
// appended; the server tolerates it regardless of advertisement.
func selectCaps(advertised map[string]bool) []string {
	var caps []string
	for _, c := range []string{"ofs-delta", "object-format=sha1"} {
		if advertised[c] {
			caps = append(caps, c)
		}
	}
	caps = append(caps, "agent=cutting-garden")
	return caps
}

// writeRequest emits the fetch-pack request: the wants (capabilities on
// the first want line), a flush, the haves, and `done`.
func writeRequest(w io.Writer, want string, haves, caps []string) error {
	first := "want " + want
	if len(caps) > 0 {
		first += " " + strings.Join(caps, " ")
	}
	if err := writePkt(w, first+"\n"); err != nil {
		return err
	}
	if err := writeFlush(w); err != nil {
		return err
	}
	for _, h := range haves {
		if err := writePkt(w, "have "+h+"\n"); err != nil {
			return err
		}
	}
	return writePkt(w, "done\n")
}

// drainAcks consumes the ACK/NAK pkt-lines that precede the packfile.
// Without multi_ack the server sends exactly one terminal ACK/NAK after
// `done`; this stops at it, leaving the reader positioned at the raw
// pack. An ERR line aborts.
func drainAcks(br *bufio.Reader) error {
	for {
		payload, isFlush, err := readPkt(br)
		if err != nil {
			return errors.Wrap(err)
		}
		if isFlush {
			continue
		}
		s := strings.TrimRight(string(payload), "\n")
		switch {
		case strings.HasPrefix(s, "ERR "):
			return errors.ErrorWithStackf("gitwire: server error: %s", strings.TrimPrefix(s, "ERR "))
		case s == "NAK":
			return nil
		case strings.HasPrefix(s, "ACK "):
			// "ACK <oid> continue" only appears under multi_ack, which we
			// do not request; any plain ACK is terminal.
			if !strings.HasSuffix(s, " continue") {
				return nil
			}
		}
	}
}

// unpackObjects pipes the raw pack remaining in br into
// `git unpack-objects`, exploding it into scratchGitDir's object
// database.
func unpackObjects(ctx context.Context, scratchGitDir string, br *bufio.Reader) error {
	bin, err := gitPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "-C", scratchGitDir, "unpack-objects", "-q")
	cmd.Stdin = br
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(err, "gitwire: unpack-objects: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func gitPath() (string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", errors.Wrapf(err, "gitwire: git not found on PATH")
	}
	return bin, nil
}
