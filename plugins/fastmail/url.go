package fastmail

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// nodeKind classifies a `fastmail://` URI into one of the five node kinds
// the tree is built from. Classification is PURE (no network): the URI
// carries everything needed to place it.
type nodeKind int

const (
	kindAccountRoot nodeKind = iota // fastmail://acct/
	kindMailbox                     // fastmail://acct/area/finance/
	kindThread                      // fastmail://acct/area/finance/?thread=T5
	kindEmail                       // fastmail://acct/area/finance/?thread=T5&email=E9
	kindRaw                         // fastmail://acct/area/finance/?thread=T5&email=E9&raw=1
)

// nodeRef is a classified `fastmail://` URI: the config account (the URI's
// host slot — NOT a hostname, since the JMAP API host is fixed), the
// containing mailbox name-path, and the thread/email discriminators.
//
// The mailbox path stays as readable name segments (matching FDR 0024's
// `fastmail://personal/area/finance/receipts/` form); the thread and email
// ids ride in query parameters (`?thread=…&email=…&raw=1`) so a URI is
// classifiable WITHOUT resolving the mailbox name-path against the server —
// an opaque threadId is otherwise indistinguishable from a child-mailbox
// name as a trailing path segment.
type nodeRef struct {
	kind        nodeKind
	account     string
	mailboxPath []string
	threadID    string
	emailID     string
}

// classifyURI parses and classifies a `fastmail://` URI. It rejects an
// unknown scheme, an empty account, or an incoherent thread/email/raw
// combination as bad requests (errors.Is400BadRequest true) — CALLER
// mistakes, not plugin failures. It does NOT check that the account is
// configured (that needs package state); resolveClient does.
func classifyURI(u *url.URL) (nodeRef, error) {
	if u.Scheme != schemeFastmail {
		return nodeRef{}, errors.BadRequestf(
			"fastmail plugin: unsupported scheme %q in %q", u.Scheme, u.String(),
		)
	}
	if u.Host == "" {
		return nodeRef{}, errors.BadRequestf(
			"fastmail plugin: empty account in %q\n"+
				"hint: pass `fastmail://<account>/`", u.String(),
		)
	}

	segs, err := pathSegments(u)
	if err != nil {
		return nodeRef{}, err
	}

	q := u.Query()
	thread := q.Get("thread")
	email := q.Get("email")
	_, hasRaw := q["raw"]

	ref := nodeRef{
		account:     u.Host,
		mailboxPath: segs,
		threadID:    thread,
		emailID:     email,
	}
	switch {
	case hasRaw:
		if thread == "" || email == "" {
			return nodeRef{}, errors.BadRequestf(
				"fastmail plugin: raw node %q requires thread and email", u.String(),
			)
		}
		ref.kind = kindRaw
	case email != "":
		if thread == "" {
			return nodeRef{}, errors.BadRequestf(
				"fastmail plugin: email node %q requires a thread", u.String(),
			)
		}
		ref.kind = kindEmail
	case thread != "":
		ref.kind = kindThread
	case len(segs) == 0:
		ref.kind = kindAccountRoot
	default:
		ref.kind = kindMailbox
	}
	return ref, nil
}

// pathSegments splits a URI's escaped path into decoded name segments. An
// unescapable segment is a bad request.
func pathSegments(u *url.URL) ([]string, error) {
	p := strings.Trim(u.EscapedPath(), "/")
	if p == "" {
		return nil, nil
	}
	parts := strings.Split(p, "/")
	segs := make([]string, len(parts))
	for i, part := range parts {
		s, err := url.PathUnescape(part)
		if err != nil {
			return nil, errors.BadRequestf(
				"fastmail plugin: bad path segment %q in %q: %s", part, u.String(), err,
			)
		}
		segs[i] = s
	}
	return segs, nil
}

// --- URI minting (the inverse of classifyURI) ---

// accountRootURI mints the credential-free account-root URI
// `fastmail://<account>/`.
func accountRootURI(account string) *url.URL {
	u := &url.URL{Scheme: schemeFastmail, Host: account}
	setPathSegments(u, nil)
	return u
}

// mailboxURI mints `fastmail://<account>/<seg…>/` for a mailbox name-path.
func mailboxURI(account string, path []string) *url.URL {
	u := &url.URL{Scheme: schemeFastmail, Host: account}
	setPathSegments(u, path)
	return u
}

// threadURI mints a thread node under a mailbox path.
func threadURI(account string, mailboxPath []string, threadID string) *url.URL {
	u := mailboxURI(account, mailboxPath)
	q := url.Values{}
	q.Set("thread", threadID)
	u.RawQuery = q.Encode()
	return u
}

// emailURI mints an email node under a thread.
func emailURI(account string, mailboxPath []string, threadID, emailID string) *url.URL {
	u := mailboxURI(account, mailboxPath)
	q := url.Values{}
	q.Set("thread", threadID)
	q.Set("email", emailID)
	u.RawQuery = q.Encode()
	return u
}

// rawURI mints the raw-message leaf under an email.
func rawURI(account string, mailboxPath []string, threadID, emailID string) *url.URL {
	u := mailboxURI(account, mailboxPath)
	q := url.Values{}
	q.Set("thread", threadID)
	q.Set("email", emailID)
	q.Set("raw", "1")
	u.RawQuery = q.Encode()
	return u
}

// setPathSegments installs path (percent-escaping each segment so a name
// with a reserved rune stays one segment) as u's path, keeping Path and
// RawPath consistent so url.String() round-trips.
func setPathSegments(u *url.URL, segs []string) {
	escaped := make([]string, len(segs))
	for i, s := range segs {
		escaped[i] = url.PathEscape(s)
	}
	raw := "/" + strings.Join(escaped, "/")
	parsed, err := url.Parse(raw)
	if err != nil {
		// Escaped input always parses; fall back to the decoded form.
		u.Path = raw
		return
	}
	u.Path = parsed.Path
	u.RawPath = parsed.RawPath
}
