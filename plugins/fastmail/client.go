package fastmail

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// requestTimeout caps every JMAP HTTP round-trip. The command's cancelable
// context still aborts in-flight requests earlier on SIGINT/SIGTERM; this
// is the upper bound for an unresponsive server.
const requestTimeout = 30 * time.Second

// jmapUsing is the JMAP capability set every request advertises: core
// (RFC 8620) plus mail (RFC 8621). v1 is Mail only, so no contacts / notes
// / masked-email capabilities are requested.
var jmapUsing = []string{
	"urn:ietf:params:jmap:core",
	"urn:ietf:params:jmap:mail",
}

// client is a session-driven JMAP client. Given a session endpoint URL and
// a bearer token it fetches the JMAP Session object once (memoized for the
// client's life), then routes method calls at the session's advertised
// apiUrl and blob downloads at its downloadUrl. It carries no mail parser:
// capture/read treat a message as an opaque message/rfc822 blob plus its
// structured JMAP Email JSON.
type client struct {
	sessionURL string
	token      string
	http       *http.Client

	session *Session // memoized; nil until the first ensureSession
}

func newClient(sessionURL, token string) *client {
	return &client{
		sessionURL: sessionURL,
		token:      token,
		http:       &http.Client{Timeout: requestTimeout},
	}
}

// authorize stamps the bearer token on a request. Fastmail JMAP auth is an
// API token presented as `Authorization: Bearer …`.
func (c *client) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// call issues ONE JMAP method call (method + args) at the session's apiUrl
// and unmarshals the method's result object into out. ctx is honored so a
// cancel unwinds the in-flight request promptly. It resolves the session
// first (a cheap memoized GET after the first call).
func (c *client) call(
	ctx context.Context,
	method string,
	args any,
	out any,
) error {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return err
	}

	reqEnvelope := jmapRequest{
		Using: jmapUsing,
		// One method call, call id "0" — Slice 1 issues single-method
		// requests (no result back-references), so the response's sole
		// method response is always at index 0.
		MethodCalls: []jmapMethodCall{{method, args, "0"}},
	}
	body, err := json.Marshal(reqEnvelope)
	if err != nil {
		return errors.Wrapf(err, "fastmail plugin: marshal %s request", method)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, sess.APIURL, bytes.NewReader(body),
	)
	if err != nil {
		return errors.Wrap(err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.Wrapf(err, "fastmail plugin: POST %s (%s)", sess.APIURL, method)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		return errors.ErrorWithStackf(
			"fastmail plugin: POST %s (%s): status %d: %s",
			sess.APIURL, method, resp.StatusCode, snippet(data),
		)
	}

	var envelope jmapResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return errors.Wrapf(err, "fastmail plugin: parse %s response", method)
	}
	if len(envelope.MethodResponses) == 0 {
		return errors.ErrorWithStackf(
			"fastmail plugin: %s: empty methodResponses", method,
		)
	}

	name, result, err := envelope.MethodResponses[0].parse()
	if err != nil {
		return err
	}
	if name == "error" {
		return errors.ErrorWithStackf(
			"fastmail plugin: %s: server error: %s", method, snippet(result),
		)
	}
	if err := json.Unmarshal(result, out); err != nil {
		return errors.Wrapf(err, "fastmail plugin: parse %s result", method)
	}
	return nil
}

// download GETs a blob at the session's resolved download URL and returns
// its verbatim bytes. accept is the media type hint the URL template's
// {type} slot is filled with (message/rfc822 for a raw message).
func (c *client) download(
	ctx context.Context,
	blobID, name, accept string,
) (data []byte, err error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	url := sess.downloadURL(blobID, name, accept)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: GET %s", url)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.ErrorWithStackf(
			"fastmail plugin: GET %s: status %d: %s",
			url, resp.StatusCode, snippet(data),
		)
	}
	return data, nil
}

// snippet trims an error-body excerpt so diagnostics stay readable.
func snippet(b []byte) string {
	const max = 256
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
