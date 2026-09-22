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

// callMany issues a whole JMAP request — one or more method calls, in the
// given order — at the session's apiUrl, and returns the raw method
// responses. It is the ONE place that knows the request/response wire shape;
// everything typed sits above it (call for a single method, decodeResponseFor
// to pick one response out of a multi-call request). Ordering matters: a
// later call may reference an earlier one's creation ids as "#<creationId>"
// (RFC 8620 §5.3), which is how applyThreadPatch puts a Mailbox/set create
// and the Email/set that names it in one atomic-enough request.
//
// A server MAY answer one call with several responses, or with an "error"
// response carrying the same call id, so the responses are returned verbatim
// rather than positionally zipped to the calls. ctx is honored so a cancel
// unwinds the in-flight request promptly; the session resolves first (a
// cheap memoized GET after the first call).
func (c *client) callMany(
	ctx context.Context,
	calls []jmapMethodCall,
) (responses []jmapMethodResponse, err error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	label := methodLabel(calls)

	body, err := json.Marshal(jmapRequest{Using: jmapUsing, MethodCalls: calls})
	if err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: marshal %s request", label)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, sess.APIURL, bytes.NewReader(body),
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: POST %s (%s)", sess.APIURL, label)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.ErrorWithStackf(
			"fastmail plugin: POST %s (%s): status %d: %s",
			sess.APIURL, label, resp.StatusCode, snippet(data),
		)
	}

	var envelope jmapResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: parse %s response", label)
	}
	return envelope.MethodResponses, nil
}

// call issues ONE JMAP method call (method + args) at the session's apiUrl
// and unmarshals the method's result object into out — the single-call
// wrapper over callMany that every read path uses. The call id is "0" and
// the sole method response is always at index 0.
func (c *client) call(
	ctx context.Context,
	method string,
	args any,
	out any,
) error {
	responses, err := c.callMany(ctx, []jmapMethodCall{{method, args, "0"}})
	if err != nil {
		return err
	}
	if len(responses) == 0 {
		return errors.ErrorWithStackf(
			"fastmail plugin: %s: empty methodResponses", method,
		)
	}
	return decodeMethodResult(responses[0], method, out)
}

// decodeMethodResult unmarshals one method response's result object into
// out, surfacing an "error" response (RFC 8620 §3.6.1) as a plugin error
// rather than letting it unmarshal into a zero value.
func decodeMethodResult(
	response jmapMethodResponse, method string, out any,
) error {
	name, result, _, err := response.parse()
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

// decodeResponseFor picks the response carrying callID out of a multi-call
// request's responses and decodes it into out. method names the call for
// diagnostics only. A missing response is a protocol error: the server
// answered a request without answering one of its calls.
func decodeResponseFor(
	responses []jmapMethodResponse, method, callID string, out any,
) error {
	for _, response := range responses {
		_, _, id, err := response.parse()
		if err != nil {
			return err
		}
		if id != callID {
			continue
		}
		return decodeMethodResult(response, method, out)
	}
	return errors.ErrorWithStackf(
		"fastmail plugin: %s: no method response for call id %q", method, callID,
	)
}

// methodLabel names a request's method calls for a diagnostic (e.g.
// "Mailbox/set+Email/set").
func methodLabel(calls []jmapMethodCall) string {
	names := make([]string, len(calls))
	for i, call := range calls {
		names[i] = call.Name
	}
	return strings.Join(names, "+")
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
