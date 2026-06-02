package gitwire

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// conn is one upload-pack session: advertise the server's refs/caps,
// then send a single want/have/done request and read the response
// stream (ack lines followed by the raw pack).
type conn interface {
	advertise() (capabilities map[string]bool, err error)
	request(want string, haves, caps []string) (*bufio.Reader, error)
	io.Closer
}

// dial selects a transport for remote. Local paths are served by
// spawning `git upload-pack`; http(s) URLs use smart-HTTP. ssh/git
// schemes and scp-like remotes are unsupported.
func dial(ctx context.Context, remote string) (conn, error) {
	switch {
	case strings.HasPrefix(remote, "http://"), strings.HasPrefix(remote, "https://"):
		return newHTTPConn(ctx, remote), nil
	case strings.Contains(remote, "://"), isSCPLike(remote):
		return nil, ErrUnsupportedTransport
	default:
		return dialLocal(ctx, remote)
	}
}

// isSCPLike reports whether remote is git's scp-style syntax
// (user@host:path) — a ':' appearing before any '/'.
func isSCPLike(remote string) bool {
	i := strings.IndexByte(remote, ':')
	if i < 0 {
		return false
	}
	slash := strings.IndexByte(remote, '/')
	return strings.Contains(remote[:i], "@") && (slash < 0 || i < slash)
}

// readAdvertRefs reads ref-advertisement pkt-lines up to the flush and
// returns the capability set parsed from the first ref line (the
// NUL-separated tail).
func readAdvertRefs(br *bufio.Reader) (map[string]bool, error) {
	caps := map[string]bool{}
	first := true
	for {
		payload, isFlush, err := readPkt(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err)
		}
		if isFlush {
			break
		}
		if first {
			first = false
			if i := bytes.IndexByte(payload, 0); i >= 0 {
				for _, c := range strings.Fields(string(payload[i+1:])) {
					caps[strings.TrimRight(c, "\n")] = true
				}
			}
		}
	}
	return caps, nil
}

// localConn drives `git upload-pack <dir>` over its stdio.
type localConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stderr *bytes.Buffer
}

func dialLocal(ctx context.Context, dir string) (*localConn, error) {
	bin, err := gitPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "upload-pack", dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(err)
	}
	return &localConn{cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout), stderr: &stderr}, nil
}

func (c *localConn) advertise() (map[string]bool, error) {
	caps, err := readAdvertRefs(c.out)
	if err != nil {
		return nil, errors.Wrapf(err, "upload-pack: %s", strings.TrimSpace(c.stderr.String()))
	}
	return caps, nil
}

func (c *localConn) request(want string, haves, caps []string) (*bufio.Reader, error) {
	if err := writeRequest(c.stdin, want, haves, caps); err != nil {
		return nil, err
	}
	if err := c.stdin.Close(); err != nil {
		return nil, errors.Wrap(err)
	}
	return c.out, nil
}

func (c *localConn) Close() error {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	return nil
}

// httpConn drives smart-HTTP upload-pack.
type httpConn struct {
	ctx      context.Context
	base     string
	client   *http.Client
	lastBody io.ReadCloser
}

func newHTTPConn(ctx context.Context, remote string) *httpConn {
	return &httpConn{
		ctx:    ctx,
		base:   strings.TrimSuffix(remote, "/"),
		client: http.DefaultClient,
	}
}

func (c *httpConn) advertise() (caps map[string]bool, err error) {
	url := c.base + "/info/refs?service=git-upload-pack"
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, errors.ErrorWithStackf("gitwire: info/refs status %d", resp.StatusCode)
	}

	br := bufio.NewReader(resp.Body)
	// Smart-HTTP prefixes the advertisement with a "# service=…" pkt-line
	// followed by a flush; consume both before reading the refs.
	payload, isFlush, err := readPkt(br)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if !isFlush && bytes.HasPrefix(payload, []byte("#")) {
		if _, _, err := readPkt(br); err != nil {
			return nil, errors.Wrap(err)
		}
	}
	return readAdvertRefs(br)
}

func (c *httpConn) request(want string, haves, caps []string) (*bufio.Reader, error) {
	var body bytes.Buffer
	if err := writeRequest(&body, want, haves, caps); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		c.ctx, http.MethodPost, c.base+"/git-upload-pack", &body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Accept", "application/x-git-upload-pack-result")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.ErrorWithStackf("gitwire: git-upload-pack status %d", resp.StatusCode)
	}
	c.lastBody = resp.Body
	return bufio.NewReader(resp.Body), nil
}

func (c *httpConn) Close() error {
	if c.lastBody != nil {
		return c.lastBody.Close()
	}
	return nil
}
