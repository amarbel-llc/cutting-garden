package fastmail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// capabilityMail is the JMAP mail capability URN whose primary account id
// is the account this plugin operates on (RFC 8621).
const capabilityMail = "urn:ietf:params:jmap:mail"

// defaultSessionURL is Fastmail's fixed JMAP Session endpoint. Because the
// API host is fixed (FDR 0024), the `fastmail://<account>/` URL's host slot
// names a CONFIG account, not a hostname, and the session endpoint is this
// constant — never derived from the account name.
const defaultSessionURL = "https://api.fastmail.com/jmap/session"

// resolveSessionURL maps a configured account name to its JMAP Session
// endpoint. Production always uses the fixed Fastmail host; it is a package
// var solely so tests can point the plugin at an in-memory JMAP server
// (fastmailtestserver) without a real network. Overriding it is the only
// test seam — every other code path is exercised unchanged.
var resolveSessionURL = func(account string) string {
	return defaultSessionURL
}

// Session is the subset of the JMAP Session object (RFC 8620 §2) this
// plugin consumes: the API URL to POST method calls at, the blob download
// URL template, the mail account id, and the session state token.
type Session struct {
	// APIURL is the endpoint method calls POST to.
	APIURL string `json:"apiUrl"`
	// DownloadURL is the RFC 8620 §6.2 blob-download URI template, with
	// {accountId}, {blobId}, {name}, and {type} placeholders.
	DownloadURL string `json:"downloadUrl"`
	// PrimaryAccounts maps a capability URN to the account id that is its
	// primary account; the mail capability's entry is the account this
	// plugin uses.
	PrimaryAccounts map[string]string `json:"primaryAccounts"`
	// State is the session state string (RFC 8620 §2), a coarse change
	// token over the whole session.
	State string `json:"state"`
}

// AccountID returns the mail-capability primary account id — the accountId
// every Mailbox/Email/Thread method call carries.
func (s *Session) AccountID() string {
	return s.PrimaryAccounts[capabilityMail]
}

// downloadURL fills the session's download URI template for one blob. Each
// placeholder is URL-path-escaped so an id or name with a reserved rune
// still resolves; {type} is the accept media-type hint.
func (s *Session) downloadURL(blobID, name, accept string) string {
	r := strings.NewReplacer(
		"{accountId}", pathEscape(s.AccountID()),
		"{blobId}", pathEscape(blobID),
		"{name}", pathEscape(name),
		"{type}", pathEscape(accept),
	)
	return r.Replace(s.DownloadURL)
}

// ensureSession fetches the JMAP Session object once and memoizes it for
// the client's life. Account configuration and the session's advertised
// endpoints are effectively static for a process, and a long-lived `mcp`
// server would otherwise re-fetch the session on every method call.
func (c *client) ensureSession(ctx context.Context) (*Session, error) {
	if c.session != nil {
		return c.session, nil
	}
	sess, err := c.fetchSession(ctx)
	if err != nil {
		return nil, err
	}
	c.session = sess
	return sess, nil
}

// fetchSession GETs the JMAP Session object from the session endpoint. It
// is the one request that does not go through apiUrl (there is no apiUrl
// until the session is known).
func (c *client) fetchSession(ctx context.Context) (sess *Session, err error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, c.sessionURL, nil,
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: GET %s", c.sessionURL)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.ErrorWithStackf(
			"fastmail plugin: GET %s: status %d: %s",
			c.sessionURL, resp.StatusCode, snippet(data),
		)
	}

	var parsed Session
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: parse session from %s", c.sessionURL)
	}
	if parsed.APIURL == "" {
		return nil, errors.ErrorWithStackf(
			"fastmail plugin: session from %s advertises no apiUrl", c.sessionURL,
		)
	}
	if parsed.AccountID() == "" {
		return nil, errors.ErrorWithStackf(
			"fastmail plugin: session from %s advertises no mail account", c.sessionURL,
		)
	}
	return &parsed, nil
}
