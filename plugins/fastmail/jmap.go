package fastmail

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

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

// parse splits a method-response tuple into its method name, its result
// object, and the call id it answers. A well-formed response is
// [name, result, callId]; a shorter tuple is a protocol error. The call id
// is what lets a multi-call request's responses be matched to their calls
// (a server may answer out of order, or emit an "error" response under the
// same call id), so it is decoded here with the rest of the tuple rather
// than re-derived by callers.
func (r jmapMethodResponse) parse() (
	name string, result json.RawMessage, callID string, err error,
) {
	if len(r) < 3 {
		return "", nil, "", errors.ErrorWithStackf(
			"fastmail plugin: malformed method response (len %d)", len(r),
		)
	}
	if err := json.Unmarshal(r[0], &name); err != nil {
		return "", nil, "", errors.Wrapf(err, "fastmail plugin: parse method-response name")
	}
	if err := json.Unmarshal(r[2], &callID); err != nil {
		return "", nil, "", errors.Wrapf(err, "fastmail plugin: parse method-response call id")
	}
	return name, r[1], callID, nil
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

// --- write methods (Email/set, Mailbox/set) ---

// setError is a JMAP SetError (RFC 8620 §5.3): why the server refused ONE
// record of a /set call. The plugin surfaces type and description verbatim
// so the user learns which record refused and why.
type setError struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Properties  []string `json:"properties"`
}

// emailPatch is one message's PatchObject: patch-pointer keys
// (`mailboxIds/<id>`, `keywords/<keyword>`) or whole-property keys
// (`mailboxIds`, `keywords`). A `mailboxIds/#<creationId>` key names a
// mailbox created earlier in the SAME request.
type emailPatch = map[string]any

type emailSetArgs struct {
	AccountID string                `json:"accountId"`
	Update    map[string]emailPatch `json:"update,omitempty"`
}

type emailSetResult struct {
	NewState   string                     `json:"newState"`
	Updated    map[string]json.RawMessage `json:"updated"`
	NotUpdated map[string]setError        `json:"notUpdated"`
}

// MailboxCreate is the subset of a new JMAP Mailbox the plugin creates: a
// name under a parent. ParentID is "" for a top-level mailbox (marshaled as
// JSON null) and may be a "#<creationId>" reference to a mailbox created
// earlier in the same request.
type MailboxCreate struct {
	Name     string
	ParentID string
}

type mailboxCreateWire struct {
	Name     string `json:"name"`
	ParentID any    `json:"parentId"`
}

type mailboxSetArgs struct {
	AccountID string                       `json:"accountId"`
	Create    map[string]mailboxCreateWire `json:"create,omitempty"`
}

type mailboxSetResult struct {
	NewState   string              `json:"newState"`
	Created    map[string]Mailbox  `json:"created"`
	NotCreated map[string]setError `json:"notCreated"`
}

// emailSetCall builds the Email/set method call applying updates.
func emailSetCall(accountID string, updates map[string]emailPatch, callID string) jmapMethodCall {
	return jmapMethodCall{
		"Email/set",
		emailSetArgs{AccountID: accountID, Update: updates},
		callID,
	}
}

// mailboxSetCall builds the Mailbox/set method call creating creates.
func mailboxSetCall(
	accountID string, creates map[string]MailboxCreate, callID string,
) jmapMethodCall {
	wire := make(map[string]mailboxCreateWire, len(creates))
	for creationID, create := range creates {
		var parent any
		if create.ParentID != "" {
			parent = create.ParentID
		}
		wire[creationID] = mailboxCreateWire{Name: create.Name, ParentID: parent}
	}
	return jmapMethodCall{
		"Mailbox/set",
		mailboxSetArgs{AccountID: accountID, Create: wire},
		callID,
	}
}

// emailSet applies one PatchObject per message in a single Email/set. Any
// refusal (notUpdated) is a bad request naming the message and the server's
// reason; JMAP applies /set records independently, so some updates may have
// landed before the refusal — the caller re-reads rather than assuming.
func (c *client) emailSet(ctx context.Context, updates map[string]emailPatch) error {
	if len(updates) == 0 {
		return nil
	}
	_, err := c.applyThreadPatch(ctx, threadPatchRequest{Updates: updates})
	return err
}

// mailboxSet creates mailboxes in a single Mailbox/set, returning each
// creation id's server-assigned mailbox id. A refusal (notCreated) is a bad
// request naming the creation id and the server's reason; the returned map
// still carries whatever DID get created, since JMAP applies /set records
// independently and the caller must be able to report them.
func (c *client) mailboxSet(
	ctx context.Context, creates map[string]MailboxCreate,
) (map[string]string, error) {
	if len(creates) == 0 {
		return map[string]string{}, nil
	}
	res, err := c.applyThreadPatch(ctx, threadPatchRequest{Creates: creates})
	return res.CreatedIDs, err
}

// threadPatchRequest is ONE apply: the mailboxes to create plus the per-message
// patches that may reference them. Creates land first in the same JMAP
// request, so an Updates patch key may be `mailboxIds/#<creationId>`.
type threadPatchRequest struct {
	Creates map[string]MailboxCreate
	Updates map[string]emailPatch
}

// threadPatchResult reports what the apply created: creation id →
// server-assigned mailbox id. Never nil.
type threadPatchResult struct {
	CreatedIDs map[string]string
}

// applyThreadPatch issues one JMAP request carrying (at most) a Mailbox/set
// create followed by an Email/set update, so a patch that both creates a
// label and applies it travels as a single request — the creation-id
// back-reference (`#<creationId>`) is only resolvable within one request
// (RFC 8620 §5.3). An empty request issues nothing.
//
// The back-reference rides as a patch-object KEY (`mailboxIds/#c1`) rather
// than as an id value, which RFC 8620 §5.3 does not spell out. That form is
// verified against the live Fastmail API (2026-09-22): one request creating
// a mailbox as `c1` and patching a message with `{"mailboxIds/#c1": true}`
// answered `created: {c1: {id: …}}` and `updated: {<id>: null}`, and the
// readback carried the new mailbox. No two-request fallback is needed.
//
// Note this is not a transaction: JMAP /set methods apply per record, and a
// refused Email/set update does not roll back a created mailbox. The
// refusal names every refused record so the caller can re-read and retry.
func (c *client) applyThreadPatch(
	ctx context.Context, req threadPatchRequest,
) (threadPatchResult, error) {
	out := threadPatchResult{CreatedIDs: map[string]string{}}
	if len(req.Creates) == 0 && len(req.Updates) == 0 {
		return out, nil
	}

	sess, err := c.ensureSession(ctx)
	if err != nil {
		return out, err
	}
	accountID := sess.AccountID()

	const (
		createCallID = "create"
		updateCallID = "update"
	)
	calls := make([]jmapMethodCall, 0, 2)
	if len(req.Creates) > 0 {
		calls = append(calls, mailboxSetCall(accountID, req.Creates, createCallID))
	}
	if len(req.Updates) > 0 {
		calls = append(calls, emailSetCall(accountID, req.Updates, updateCallID))
	}

	responses, err := c.callMany(ctx, calls)
	if err != nil {
		return out, err
	}

	if len(req.Creates) > 0 {
		var created mailboxSetResult
		if err := decodeResponseFor(
			responses, "Mailbox/set", createCallID, &created,
		); err != nil {
			return out, err
		}
		for creationID, mailbox := range created.Created {
			out.CreatedIDs[creationID] = mailbox.ID
		}
		if err := refusedRecords("Mailbox/set", created.NotCreated); err != nil {
			return out, err
		}
	}

	if len(req.Updates) > 0 {
		var updated emailSetResult
		if err := decodeResponseFor(
			responses, "Email/set", updateCallID, &updated,
		); err != nil {
			return out, err
		}
		if err := refusedRecords("Email/set", updated.NotUpdated); err != nil {
			return out, err
		}
	}
	return out, nil
}

// refusedRecords turns a /set method's SetError map into ONE bad request
// naming every refused record with the server's type and description — the
// user must learn WHICH message or mailbox refused and why. Ids are sorted
// so the message is deterministic.
func refusedRecords(method string, errs map[string]setError) error {
	if len(errs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(errs))
	for id := range errs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		refusal := errs[id]
		kind := refusal.Type
		if kind == "" {
			kind = "(no type)"
		}
		part := id + ": " + kind
		if refusal.Description != "" {
			part += ": " + refusal.Description
		}
		if len(refusal.Properties) > 0 {
			part += " (" + strings.Join(refusal.Properties, ", ") + ")"
		}
		parts = append(parts, part)
	}
	return errors.BadRequestf(
		"fastmail plugin: %s refused %s", method, strings.Join(parts, "; "),
	)
}

// pathEscape percent-escapes a URL path segment for the download URL
// template — a blob id or message name with a reserved rune still resolves.
func pathEscape(s string) string { return url.PathEscape(s) }
