package org

import (
	"bytes"
	"context"
	"encoding/xml"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// davEntry is one PROPFIND response entry, with its path relative to the
// account's base folder ("" for the base itself; folders end in "/").
type davEntry struct {
	rel     string
	dir     bool
	etag    string
	modTime time.Time
}

// davClient is a minimal read-only WebDAV client: PROPFIND (Depth 0/1)
// and GET with Basic auth. The caldav plugin's client is package-private,
// and a shared DAV package is cutting-garden#54's open layer question.
type davClient struct {
	base     *url.URL
	username string
	password string
	http     *http.Client
}

// passwords memoizes each account's resolved password for the process:
// resolving can mean a `piggy pass show` subprocess, and a long-lived
// `cg mcp` would otherwise pay it on every call. Reset with the config.
var (
	passwordsMu sync.Mutex
	passwords   = map[string]string{}
)

func resetPasswords() {
	passwordsMu.Lock()
	defer passwordsMu.Unlock()
	passwords = map[string]string{}
}

func accountPassword(a account) (string, error) {
	passwordsMu.Lock()
	defer passwordsMu.Unlock()
	if p, ok := passwords[a.Name]; ok {
		return p, nil
	}
	p, err := a.Password()
	if err != nil {
		return "", errors.Wrapf(err, "org plugin: account %q", a.Name)
	}
	passwords[a.Name] = p
	return p, nil
}

func newDavClient(a account) (*davClient, error) {
	password, err := accountPassword(a)
	if err != nil {
		return nil, err
	}
	return &davClient{
		base:     a.base,
		username: a.Username,
		password: password,
		// 25 s per request keeps a quote's GET + PUT inside its 60 s lock.
		http: &http.Client{Timeout: 25 * time.Second},
	}, nil
}

func (c *davClient) resolve(rel string) *url.URL {
	u := *c.base
	u.Path = c.base.Path + rel
	u.RawPath = ""
	return &u
}

func (c *davClient) do(ctx context.Context, method, rel string, header http.Header, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.resolve(rel).String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return c.http.Do(req)
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:"><D:prop>
<D:resourcetype/><D:getetag/><D:getlastmodified/>
</D:prop></D:propfind>`

type msResponse struct {
	Href     string `xml:"href"`
	Propstat []struct {
		Status string `xml:"status"`
		Prop   struct {
			ResourceType struct {
				Collection *struct{} `xml:"collection"`
			} `xml:"resourcetype"`
			ETag         string `xml:"getetag"`
			LastModified string `xml:"getlastmodified"`
		} `xml:"prop"`
	} `xml:"propstat"`
}

// propfind lists rel (a folder ending in "/", or a file) at depth "0" or
// "1". The entry for rel itself is included.
func (c *davClient) propfind(ctx context.Context, rel, depth string) (out []davEntry, err error) {
	resp, err := c.do(ctx, "PROPFIND", rel, http.Header{
		"Depth":        {depth},
		"Content-Type": {`application/xml; charset="utf-8"`},
	}, []byte(propfindBody))
	if err != nil {
		return nil, errors.Wrapf(err, "org plugin: PROPFIND %s", rel)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusError("PROPFIND", rel, resp)
	}
	var ms struct {
		Responses []msResponse `xml:"response"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, errors.Wrapf(err, "org plugin: PROPFIND %s: bad multistatus", rel)
	}

	for _, r := range ms.Responses {
		entry, ok := c.entryFor(r)
		if ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

// entryFor converts a response to a davEntry relative to the base, taking
// the first 200 propstat. Entries outside the base are dropped.
func (c *davClient) entryFor(r msResponse) (davEntry, bool) {
	href, err := url.Parse(strings.TrimSpace(r.Href))
	if err != nil {
		return davEntry{}, false
	}
	p := href.Path
	if !strings.HasPrefix(p, c.base.Path) {
		return davEntry{}, false
	}
	e := davEntry{rel: strings.TrimPrefix(p, c.base.Path)}
	for _, ps := range r.Propstat {
		if !strings.Contains(ps.Status, " 200") {
			continue
		}
		e.dir = ps.Prop.ResourceType.Collection != nil
		e.etag = ps.Prop.ETag
		e.modTime, _ = http.ParseTime(ps.Prop.LastModified)
		break
	}
	if e.dir && e.rel != "" && !strings.HasSuffix(e.rel, "/") {
		e.rel += "/"
	}
	return e, true
}

// get fetches a file's bytes and ETag.
func (c *davClient) get(ctx context.Context, rel string) (body []byte, etag string, err error) {
	resp, err := c.do(ctx, http.MethodGet, rel, nil, nil)
	if err != nil {
		return nil, "", errors.Wrapf(err, "org plugin: GET %s", rel)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, "", statusError("GET", rel, resp)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", errors.Wrapf(err, "org plugin: GET %s: truncated body", rel)
	}
	return body, resp.Header.Get("ETag"), nil
}

// errLocked reports that another writer holds the file's lock (423).
var errLocked = stderrors.New("locked by another writer")

const lockBody = `<?xml version="1.0" encoding="utf-8"?>
<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope>
<D:locktype><D:write/></D:locktype></D:lockinfo>`

// lock takes an exclusive write lock on rel with a 60 s timeout, so a
// crashed writer's lock expires on its own (cutting-garden#267).
func (c *davClient) lock(ctx context.Context, rel string) (token string, err error) {
	resp, err := c.do(ctx, "LOCK", rel, http.Header{
		"Timeout":      {"Second-60"},
		"Depth":        {"0"},
		"Content-Type": {`application/xml; charset="utf-8"`},
	}, []byte(lockBody))
	if err != nil {
		return "", errors.Wrapf(err, "org plugin: LOCK %s", rel)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusLocked:
		return "", errLocked
	default:
		return "", statusError("LOCK", rel, resp)
	}
	token = strings.Trim(strings.TrimSpace(resp.Header.Get("Lock-Token")), "<>")
	if token == "" {
		return "", errors.ErrorWithStackf("org plugin: LOCK %s: no Lock-Token in the response", rel)
	}
	return token, nil
}

// outcomeUnknown marks a PUT that may or may not have landed: a 5xx, or no
// answer after the body was sent. It must not be retried blindly — a retry
// could apply the change twice — so the caller re-reads the file instead.
type outcomeUnknown struct{ msg string }

func (e outcomeUnknown) Error() string { return e.msg }

// put uploads body under the lock token.
func (c *davClient) put(ctx context.Context, rel string, body []byte, token string) (err error) {
	resp, err := c.do(ctx, http.MethodPut, rel, http.Header{
		"If":           {"(<" + token + ">)"},
		"Content-Type": {"text/plain; charset=utf-8"},
	}, body)
	if err != nil {
		return outcomeUnknown{msg: fmt.Sprintf(
			"org plugin: PUT %s: outcome unknown (%v); re-read the file before retrying", rel, err)}
	}
	defer errors.DeferredCloser(&err, resp.Body)
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode >= 500:
		return outcomeUnknown{msg: fmt.Sprintf(
			"org plugin: PUT %s: %s: outcome unknown; re-read the file before retrying", rel, resp.Status)}
	default:
		return statusError("PUT", rel, resp)
	}
}

// unlock releases the lock. Failures are left to the lock's timeout.
func (c *davClient) unlock(ctx context.Context, rel, token string) {
	resp, err := c.do(ctx, "UNLOCK", rel, http.Header{"Lock-Token": {"<" + token + ">"}}, nil)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// authFailure marks a 401/403 so callers that tolerate per-file trouble
// still fail on bad credentials.
type authFailure struct{ msg string }

func (e authFailure) Error() string { return e.msg }

func isAuthFailure(err error) bool {
	var af authFailure
	return stderrors.As(err, &af)
}

// statusError names the failing request and its status: 404 is a bad
// request (a stale path), 401/403 an authFailure.
func statusError(method, rel string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	msg := fmt.Sprintf("org plugin: %s %s: %s", method, rel, resp.Status)
	if s := strings.TrimSpace(string(snippet)); s != "" {
		msg += ": " + s
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return errors.BadRequestf("%s", msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return authFailure{msg: msg}
	default:
		return errors.ErrorWithStackf("%s", msg)
	}
}

// fileStem is a file's base name without ".org".
func fileStem(rel string) string { return strings.TrimSuffix(path.Base(rel), ".org") }
