package fastmail

import (
	"context"
	"encoding/json"
	"net/url"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// --- JSON-RPC-shaped request/response envelope (RFC 8620 §3) ---

// jmapRequest is the method-call envelope POSTed to the session's apiUrl.
type jmapRequest struct {
	Using       []string         `json:"using"`
	MethodCalls []jmapMethodCall `json:"methodCalls"`
}

// jmapMethodCall is one [name, args, callId] invocation. JMAP encodes it as
// a heterogeneous 3-element array, so it marshals to one.
type jmapMethodCall struct {
	Name   string
	Args   any
	CallID string
}

func (m jmapMethodCall) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{m.Name, m.Args, m.CallID})
}

// jmapResponse is the method-response envelope returned by apiUrl.
type jmapResponse struct {
	MethodResponses []jmapMethodResponse `json:"methodResponses"`
	SessionState    string               `json:"sessionState"`
}

// jmapMethodResponse is one [name, result, callId] response tuple, decoded
// as raw elements so the result object can be unmarshaled into a typed
// value by the caller.
type jmapMethodResponse []json.RawMessage

// parse splits a method-response tuple into its method name and its result
// object. A well-formed response is [name, result, callId]; a shorter tuple
// is a protocol error.
func (r jmapMethodResponse) parse() (name string, result json.RawMessage, err error) {
	if len(r) < 2 {
		return "", nil, errors.ErrorWithStackf(
			"fastmail plugin: malformed method response (len %d)", len(r),
		)
	}
	if err := json.Unmarshal(r[0], &name); err != nil {
		return "", nil, errors.Wrapf(err, "fastmail plugin: parse method-response name")
	}
	return name, r[1], nil
}

// --- JMAP data types (the subset this plugin reads) ---

// Mailbox is the subset of a JMAP Mailbox (RFC 8621 §2) the plugin uses. In
// Fastmail's labels mode a Mailbox acts as a multi-assignment label; the
// tree nests via ParentID (empty for a top-level mailbox — JSON null
// decodes to ""), and Role is one of inbox/archive/sent/drafts/junk/trash
// (empty for a user tag mailbox).
type Mailbox struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	Role     string `json:"role"`
	// TotalThreads and TotalEmails are the mailbox's DIRECT membership counts
	// (RFC 8621 §2) — threads/messages tagged at this exact level, not a
	// recursive subtree rollup. Mailbox/get with properties null returns them
	// with every other property, so they cost no extra fetch.
	TotalThreads int `json:"totalThreads"`
	TotalEmails  int `json:"totalEmails"`
}

// EmailAddress is one JMAP address object (name + email).
type EmailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Email is the subset of a JMAP Email (RFC 8621 §4) the plugin reads: every
// facet-bearing field arrives in one Email/get, so a thread's facet values
// are always in hand at list time with no per-node fetch (RFC 0012's
// cheapness rule).
type Email struct {
	ID            string          `json:"id"`
	ThreadID      string          `json:"threadId"`
	MailboxIDs    map[string]bool `json:"mailboxIds"`
	Keywords      map[string]bool `json:"keywords"`
	From          []EmailAddress  `json:"from"`
	Subject       string          `json:"subject"`
	ReceivedAt    string          `json:"receivedAt"`
	HasAttachment bool            `json:"hasAttachment"`
	BlobID        string          `json:"blobId"`
}

// Thread is a JMAP Thread (RFC 8621 §3): an id and its member email ids in
// receivedAt order.
type Thread struct {
	ID       string   `json:"id"`
	EmailIDs []string `json:"emailIds"`
}

// emailFacetProps is the Email/get property set that supplies every facet
// and listing field the plugin derives — fetched in one call per thread
// member set.
var emailFacetProps = []string{
	"id", "threadId", "mailboxIds", "keywords",
	"from", "subject", "receivedAt", "hasAttachment", "blobId",
}

// emailListProps is the lighter Email/get property set for the cheap thread
// listing (ListRoots): just enough to name a thread by its representative
// message.
var emailListProps = []string{"id", "threadId", "subject", "receivedAt"}

// --- typed method wrappers ---

type mailboxGetArgs struct {
	AccountID  string `json:"accountId"`
	IDs        any    `json:"ids"`        // null → all mailboxes
	Properties any    `json:"properties"` // null → all properties
}

type mailboxGetResult struct {
	List []Mailbox `json:"list"`
}

// mailboxGetAll fetches every mailbox in the account (Mailbox/get with null
// ids). The whole tree is small and needed both to enumerate roots and to
// resolve a mailbox name-path / a member email's mailbox roles and tags.
func (c *client) mailboxGetAll(ctx context.Context) ([]Mailbox, error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	var out mailboxGetResult
	if err := c.call(ctx, "Mailbox/get",
		mailboxGetArgs{AccountID: sess.AccountID()}, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

type emailFilter struct {
	InMailbox string `json:"inMailbox,omitempty"`
}

type emailSort struct {
	Property    string `json:"property"`
	IsAscending bool   `json:"isAscending"`
}

type emailQueryArgs struct {
	AccountID       string      `json:"accountId"`
	Filter          emailFilter `json:"filter"`
	Sort            []emailSort `json:"sort"`
	CollapseThreads bool        `json:"collapseThreads"`
	Position        int         `json:"position"`
	Limit           int         `json:"limit"`
	CalculateTotal  bool        `json:"calculateTotal"`
}

type emailQueryResult struct {
	QueryState string   `json:"queryState"`
	Total      int64    `json:"total"`
	IDs        []string `json:"ids"`
}

// emailQuery runs Email/query scoped to one mailbox, newest-first, with
// threads collapsed to one representative email per thread — the primary
// organizing unit (FDR 0024). It returns the representative email ids, the
// thread total, and the queryState token.
func (c *client) emailQuery(
	ctx context.Context, mailboxID string, position, limit int,
) (ids []string, total int64, err error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, 0, err
	}
	var out emailQueryResult
	args := emailQueryArgs{
		AccountID:       sess.AccountID(),
		Filter:          emailFilter{InMailbox: mailboxID},
		Sort:            []emailSort{{Property: "receivedAt", IsAscending: false}},
		CollapseThreads: true,
		Position:        position,
		Limit:           limit,
		CalculateTotal:  true,
	}
	if err := c.call(ctx, "Email/query", args, &out); err != nil {
		return nil, 0, err
	}
	return out.IDs, out.Total, nil
}

type emailGetArgs struct {
	AccountID  string   `json:"accountId"`
	IDs        []string `json:"ids"`
	Properties []string `json:"properties,omitempty"`
}

type emailGetResult struct {
	State string  `json:"state"`
	List  []Email `json:"list"`
}

// emailGet fetches the given emails with the given properties.
func (c *client) emailGet(
	ctx context.Context, ids, properties []string,
) ([]Email, error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var out emailGetResult
	args := emailGetArgs{AccountID: sess.AccountID(), IDs: ids, Properties: properties}
	if err := c.call(ctx, "Email/get", args, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// emailState returns the account-global Email type state (RFC 8620 §1.5) —
// an Email/get with no ids yields the current state cheaply. It moves on
// ANY email change (new, deleted, or keyword/mailbox mutation), so it is
// the ideal facet-version token: it never misses a facet-relevant change,
// and an over-broad token only causes a safe extra recompute.
func (c *client) emailState(ctx context.Context) (string, error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return "", err
	}
	var out emailGetResult
	args := emailGetArgs{AccountID: sess.AccountID(), IDs: []string{}}
	if err := c.call(ctx, "Email/get", args, &out); err != nil {
		return "", err
	}
	return out.State, nil
}

type threadGetArgs struct {
	AccountID string   `json:"accountId"`
	IDs       []string `json:"ids"`
}

type threadGetResult struct {
	List []Thread `json:"list"`
}

// threadGet fetches the given threads (their member email id lists).
func (c *client) threadGet(ctx context.Context, ids []string) ([]Thread, error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var out threadGetResult
	if err := c.call(ctx, "Thread/get",
		threadGetArgs{AccountID: sess.AccountID(), IDs: ids}, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// pathEscape percent-escapes a URL path segment for the download URL
// template — a blob id or message name with a reserved rune still resolves.
func pathEscape(s string) string { return url.PathEscape(s) }
