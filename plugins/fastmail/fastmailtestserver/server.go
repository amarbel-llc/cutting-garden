// Package fastmailtestserver is a minimal in-memory JMAP server for the
// fastmail plugin's tests (and any future bats lane). It answers the JMAP
// Session object, the Mailbox/get, Email/query (honoring inMailbox +
// collapseThreads), Email/get, and Thread/get method calls, and blob
// download — enough of RFC 8620/8621 for the plugin's traversal and facet
// round-trip. It also serves the WRITE subset the plugin emits: Mailbox/set
// create (with creation ids resolvable by a later call of the same request)
// and Email/set update with RFC 8620 §5.3 patch objects, refusing an update
// that would leave a message in no mailbox.
//
// It is a pure net/http listener (TCP, no socketpair) so it runs inside the
// nix test sandbox, mirroring caldavtestserver's shape (Start/URL/Close +
// seed helpers). It defines its OWN seed types rather than importing the
// fastmail package, so the plugin's internal (package fastmail) tests can
// import it without an import cycle. It is NOT a conformant JMAP
// implementation and is not shipped in any release.
package fastmailtestserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

// Address is one seeded sender address.
type Address struct {
	Name  string
	Email string
}

// Mailbox is one seeded mailbox. ParentID is "" for a top-level mailbox;
// Role is "" for a user tag mailbox.
type Mailbox struct {
	ID       string
	Name     string
	ParentID string
	Role     string
}

// Email is one seeded message. MailboxIDs and Keywords are sets; Raw is the
// verbatim RFC 5322 body served by blob download under BlobID.
type Email struct {
	ID            string
	ThreadID      string
	MailboxIDs    []string
	Keywords      []string
	From          []Address
	Subject       string
	ReceivedAt    string // ISO-8601, e.g. "2026-07-14T09:12:03Z"
	HasAttachment bool
	BlobID        string
	Raw           string
}

// Server is a running in-memory JMAP test server.
type Server struct {
	mu        sync.Mutex
	httptest  *httptest.Server
	accountID string
	mailboxes []Mailbox
	emails    []Email
	stateSeq  int
	// createSeq numbers Mailbox/set creations so a created mailbox's id is
	// deterministic (mb-new-1, mb-new-2, …) across a run.
	createSeq int
}

// Start launches a server for one mail account and returns it. Close it
// when done. Seed mailboxes and emails with AddMailbox / AddEmail.
func Start(accountID string) *Server {
	if accountID == "" {
		accountID = "acct-test"
	}
	s := &Server{accountID: accountID}
	s.httptest = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL is the server's base http URL.
func (s *Server) URL() string { return s.httptest.URL }

// SessionURL is the JMAP Session endpoint the plugin GETs — the value a
// test wires into the plugin via its resolveSessionURL seam.
func (s *Server) SessionURL() string { return s.httptest.URL + "/jmap/session" }

// AccountID is the mail account id.
func (s *Server) AccountID() string { return s.accountID }

// Close shuts the server down.
func (s *Server) Close() { s.httptest.Close() }

// AddMailbox seeds a mailbox.
func (s *Server) AddMailbox(id, name, parentID, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mailboxes = append(s.mailboxes, Mailbox{ID: id, Name: name, ParentID: parentID, Role: role})
	s.stateSeq++
}

// AddEmail seeds a message.
func (s *Server) AddEmail(e Email) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emails = append(s.emails, e)
	s.stateSeq++
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/jmap/session":
		s.session(w, r)
	case r.URL.Path == "/jmap/api/":
		s.api(w, r)
	case strings.HasPrefix(r.URL.Path, "/jmap/download/"):
		s.downloadBlob(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request) {
	base := s.httptest.URL
	resp := map[string]any{
		"apiUrl":      base + "/jmap/api/",
		"downloadUrl": base + "/jmap/download/{accountId}/{blobId}/{name}?accept={type}",
		"primaryAccounts": map[string]string{
			"urn:ietf:params:jmap:mail": s.accountID,
		},
		"state": "session-1",
	}
	writeJSON(w, resp)
}

// jmapReq is the method-call envelope the plugin POSTs.
type jmapReq struct {
	MethodCalls [][]json.RawMessage `json:"methodCalls"`
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	var req jmapReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// creations accumulates the creation id → assigned id map for THIS
	// request, so a later call can resolve a "#<creationId>" back-reference
	// to a record an earlier call created (RFC 8620 §5.3).
	creations := map[string]string{}

	responses := make([]any, 0, len(req.MethodCalls))
	for _, call := range req.MethodCalls {
		if len(call) < 3 {
			continue
		}
		var name, callID string
		_ = json.Unmarshal(call[0], &name)
		_ = json.Unmarshal(call[2], &callID)
		responseName, result := s.dispatch(name, call[1], creations)
		responses = append(responses, []any{responseName, result, callID})
	}

	writeJSON(w, map[string]any{
		"methodResponses": responses,
		"sessionState":    "session-1",
	})
}

// dispatch runs one method call, returning the RESPONSE name and its result.
// The response name is the method name on success and "error" for a
// method-level failure (RFC 8620 §3.6.1) — a method this fixture does not
// implement is an explicit error response, never a result object the client
// would silently decode into a zero value.
func (s *Server) dispatch(
	method string, args json.RawMessage, creations map[string]string,
) (string, any) {
	switch method {
	case "Mailbox/get":
		return method, s.mailboxGet()
	case "Email/query":
		return method, s.emailQuery(args)
	case "Email/get":
		return method, s.emailGet(args)
	case "Thread/get":
		return method, s.threadGet(args)
	case "Mailbox/set":
		return s.mailboxSet(args, creations)
	case "Email/set":
		return s.emailSet(args, creations)
	default:
		return "error", map[string]any{"type": "unknownMethod"}
	}
}

func (s *Server) mailboxGet() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]any, 0, len(s.mailboxes))
	for _, m := range s.mailboxes {
		emails, threads := s.membershipCounts(m.ID)
		list = append(list, map[string]any{
			"id":           m.ID,
			"name":         m.Name,
			"parentId":     nullable(m.ParentID),
			"role":         nullable(m.Role),
			"totalEmails":  emails,
			"totalThreads": threads,
		})
	}
	return map[string]any{"accountId": s.accountID, "state": s.state(), "list": list}
}

// membershipCounts computes a mailbox's DIRECT membership counts from the
// seeded emails: totalEmails is the number of emails whose mailboxIds
// include mailboxID, totalThreads the number of distinct threads among
// them. Caller holds s.mu.
func (s *Server) membershipCounts(mailboxID string) (emails, threads int) {
	seenThread := map[string]bool{}
	for _, e := range s.emails {
		if !contains(e.MailboxIDs, mailboxID) {
			continue
		}
		emails++
		if !seenThread[e.ThreadID] {
			seenThread[e.ThreadID] = true
			threads++
		}
	}
	return emails, threads
}

type emailQueryArgs struct {
	Filter struct {
		InMailbox string `json:"inMailbox"`
	} `json:"filter"`
	CollapseThreads bool `json:"collapseThreads"`
	Position        int  `json:"position"`
	Limit           int  `json:"limit"`
}

func (s *Server) emailQuery(raw json.RawMessage) any {
	var args emailQueryArgs
	_ = json.Unmarshal(raw, &args)

	s.mu.Lock()
	matched := make([]Email, 0, len(s.emails))
	for _, e := range s.emails {
		if args.Filter.InMailbox != "" && !contains(e.MailboxIDs, args.Filter.InMailbox) {
			continue
		}
		matched = append(matched, e)
	}
	s.mu.Unlock()

	// Newest-first.
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].ReceivedAt > matched[j].ReceivedAt
	})

	var reps []Email
	if args.CollapseThreads {
		seen := map[string]bool{}
		for _, e := range matched {
			if seen[e.ThreadID] {
				continue // matched is newest-first, so the first per thread is newest
			}
			seen[e.ThreadID] = true
			reps = append(reps, e)
		}
	} else {
		reps = matched
	}

	total := len(reps)
	reps = page(reps, args.Position, args.Limit)
	ids := make([]string, len(reps))
	for i, e := range reps {
		ids[i] = e.ID
	}
	return map[string]any{
		"accountId":  s.accountID,
		"queryState": s.state(),
		"position":   args.Position,
		"total":      total,
		"ids":        ids,
	}
}

type emailGetArgs struct {
	IDs []string `json:"ids"`
}

func (s *Server) emailGet(raw json.RawMessage) any {
	var args emailGetArgs
	_ = json.Unmarshal(raw, &args)

	s.mu.Lock()
	defer s.mu.Unlock()
	byID := map[string]Email{}
	for _, e := range s.emails {
		byID[e.ID] = e
	}
	list := make([]any, 0, len(args.IDs))
	notFound := []string{}
	for _, id := range args.IDs {
		e, ok := byID[id]
		if !ok {
			notFound = append(notFound, id)
			continue
		}
		list = append(list, emailJSON(e))
	}
	return map[string]any{
		"accountId": s.accountID,
		"state":     s.state(),
		"list":      list,
		"notFound":  notFound,
	}
}

type threadGetArgs struct {
	IDs []string `json:"ids"`
}

func (s *Server) threadGet(raw json.RawMessage) any {
	var args threadGetArgs
	_ = json.Unmarshal(raw, &args)

	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]any, 0, len(args.IDs))
	notFound := []string{}
	for _, tid := range args.IDs {
		members := make([]Email, 0)
		for _, e := range s.emails {
			if e.ThreadID == tid {
				members = append(members, e)
			}
		}
		if len(members) == 0 {
			notFound = append(notFound, tid)
			continue
		}
		// Thread member ids in receivedAt (oldest-first) order.
		sort.SliceStable(members, func(i, j int) bool {
			return members[i].ReceivedAt < members[j].ReceivedAt
		})
		ids := make([]string, len(members))
		for i, m := range members {
			ids[i] = m.ID
		}
		list = append(list, map[string]any{"id": tid, "emailIds": ids})
	}
	return map[string]any{
		"accountId": s.accountID,
		"state":     s.state(),
		"list":      list,
		"notFound":  notFound,
	}
}

// --- write surface: Mailbox/set create, Email/set update ---

// setErr is a JMAP SetError (RFC 8620 §5.3): why ONE record was refused.
type setErr struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Properties  []string `json:"properties,omitempty"`
}

type mailboxSetArgs struct {
	Create  map[string]json.RawMessage `json:"create"`
	Update  map[string]json.RawMessage `json:"update"`
	Destroy []string                   `json:"destroy"`
}

type mailboxCreateArgs struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parentId"`
}

// mailboxSet serves Mailbox/set CREATE only. Each creation gets a
// deterministic id (mb-new-N in creation-id order) recorded in the
// request-scoped creations map, so a later call in the same request can
// name it as "#<creationId>". update and destroy are refused outright —
// the fixture is honest about what it implements.
func (s *Server) mailboxSet(
	raw json.RawMessage, creations map[string]string,
) (string, any) {
	var args mailboxSetArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "error", map[string]any{
			"type": "invalidArguments", "description": err.Error(),
		}
	}
	if len(args.Update) > 0 || len(args.Destroy) > 0 {
		return "error", map[string]any{
			"type":        "invalidArguments",
			"description": "fastmailtestserver: Mailbox/set serves create only",
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	created := map[string]any{}
	notCreated := map[string]setErr{}
	// Sorted so a creation naming another creation of the SAME call
	// resolves deterministically.
	for _, creationID := range sortedKeys(args.Create) {
		var create mailboxCreateArgs
		if err := json.Unmarshal(args.Create[creationID], &create); err != nil {
			notCreated[creationID] = setErr{
				Type: "invalidProperties", Description: err.Error(),
			}
			continue
		}
		if create.Name == "" {
			notCreated[creationID] = setErr{
				Type:       "invalidProperties",
				Properties: []string{"name"}, Description: "name is required",
			}
			continue
		}
		parentID := ""
		if create.ParentID != nil {
			parentID = *create.ParentID
		}
		resolved, ok := s.resolveMailboxRef(parentID, creations)
		if !ok {
			notCreated[creationID] = setErr{
				Type:       "invalidProperties",
				Properties: []string{"parentId"},
				Description: fmt.Sprintf(
					"unknown parentId %q", parentID,
				),
			}
			continue
		}

		s.createSeq++
		id := fmt.Sprintf("mb-new-%d", s.createSeq)
		s.mailboxes = append(s.mailboxes, Mailbox{
			ID: id, Name: create.Name, ParentID: resolved,
		})
		s.stateSeq++
		creations[creationID] = id
		created[creationID] = map[string]any{
			"id":           id,
			"name":         create.Name,
			"parentId":     nullable(resolved),
			"role":         nil,
			"totalEmails":  0,
			"totalThreads": 0,
		}
	}

	return "Mailbox/set", map[string]any{
		"accountId":  s.accountID,
		"newState":   s.state(),
		"created":    created,
		"notCreated": notCreated,
	}
}

type emailSetArgs struct {
	Create  map[string]json.RawMessage            `json:"create"`
	Update  map[string]map[string]json.RawMessage `json:"update"`
	Destroy []string                              `json:"destroy"`
}

// emailSet serves Email/set UPDATE only, applying RFC 8620 §5.3 patch
// objects restricted to the pointers this plugin emits: whole-property
// `mailboxIds` / `keywords` replacement and one-level `mailboxIds/<id>` /
// `keywords/<keyword>` membership patches (value true to set, null to
// clear). Anything else is an invalidPatch SetError rather than a silent
// no-op. Records apply independently, as in real JMAP: a refusal leaves
// that message untouched and does not roll back the others.
func (s *Server) emailSet(
	raw json.RawMessage, creations map[string]string,
) (string, any) {
	var args emailSetArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "error", map[string]any{
			"type": "invalidArguments", "description": err.Error(),
		}
	}
	if len(args.Create) > 0 || len(args.Destroy) > 0 {
		return "error", map[string]any{
			"type":        "invalidArguments",
			"description": "fastmailtestserver: Email/set serves update only",
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	updated := map[string]any{}
	notUpdated := map[string]setErr{}
	for _, id := range sortedKeys(args.Update) {
		index := -1
		for i := range s.emails {
			if s.emails[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			notUpdated[id] = setErr{
				Type: "notFound", Description: "no such email",
			}
			continue
		}
		patched, refusal := s.applyEmailPatch(
			s.emails[index], args.Update[id], creations,
		)
		if refusal != nil {
			notUpdated[id] = *refusal
			continue
		}
		s.emails[index] = patched
		s.stateSeq++
		updated[id] = nil
	}

	return "Email/set", map[string]any{
		"accountId":  s.accountID,
		"newState":   s.state(),
		"updated":    updated,
		"notUpdated": notUpdated,
	}
}

// applyEmailPatch folds one PatchObject onto a message, returning either the
// patched message or the SetError refusing it. Caller holds s.mu.
func (s *Server) applyEmailPatch(
	email Email, patch map[string]json.RawMessage, creations map[string]string,
) (Email, *setErr) {
	mailboxIDs := setOf(email.MailboxIDs)
	keywords := setOf(email.Keywords)

	// RFC 8620 §5.3: no patch pointer may be a prefix of another.
	for _, property := range []string{"mailboxIds", "keywords"} {
		if _, whole := patch[property]; !whole {
			continue
		}
		for key := range patch {
			if strings.HasPrefix(key, property+"/") {
				return email, &setErr{
					Type:       "invalidPatch",
					Properties: []string{property},
					Description: fmt.Sprintf(
						"patch key %q is a prefix of %q", property, key,
					),
				}
			}
		}
	}

	for _, key := range sortedKeys(patch) {
		property, pointer, nested := strings.Cut(key, "/")
		var target map[string]bool
		switch property {
		case "mailboxIds":
			target = mailboxIDs
		case "keywords":
			target = keywords
		default:
			return email, &setErr{
				Type:        "invalidPatch",
				Properties:  []string{key},
				Description: fmt.Sprintf("unsupported patch key %q", key),
			}
		}

		if !nested {
			replacement, ok := decodeMembershipSet(patch[key])
			if !ok {
				return email, &setErr{
					Type:       "invalidPatch",
					Properties: []string{key},
					Description: fmt.Sprintf(
						"%s must be an object whose every value is true", key,
					),
				}
			}
			if property == "mailboxIds" {
				// Resolved exactly as in the one-level branch below, so a
				// whole-object replacement may name a mailbox created
				// earlier in the same request as "#<creationId>" too.
				resolved := make(map[string]bool, len(replacement))
				for candidate := range replacement {
					id, resolvable := s.resolveMailboxRef(candidate, creations)
					if !resolvable || id == "" {
						return email, &setErr{
							Type:       "invalidProperties",
							Properties: []string{key},
							Description: fmt.Sprintf(
								"unknown mailbox %q", candidate,
							),
						}
					}
					resolved[id] = true
				}
				mailboxIDs = resolved
			} else {
				keywords = replacement
			}
			continue
		}

		on, ok := decodePatchFlag(patch[key])
		if !ok {
			return email, &setErr{
				Type:       "invalidPatch",
				Properties: []string{key},
				Description: fmt.Sprintf(
					"patch key %q must be true or null", key,
				),
			}
		}
		if property == "mailboxIds" {
			resolved, resolvable := s.resolveMailboxRef(pointer, creations)
			if !resolvable || resolved == "" {
				return email, &setErr{
					Type:        "invalidProperties",
					Properties:  []string{key},
					Description: fmt.Sprintf("unknown mailbox %q", pointer),
				}
			}
			pointer = resolved
		}
		if on {
			target[pointer] = true
		} else {
			delete(target, pointer)
		}
	}

	if len(mailboxIDs) == 0 {
		return email, &setErr{
			Type:        "invalidProperties",
			Properties:  []string{"mailboxIds"},
			Description: "a message must be in at least one mailbox",
		}
	}

	email.MailboxIDs = sortedKeys(mailboxIDs)
	email.Keywords = sortedKeys(keywords)
	return email, nil
}

// resolveMailboxRef resolves a mailbox reference: "" is the null parent (a
// top-level mailbox), "#<creationId>" a mailbox created earlier in the same
// request, anything else an existing mailbox id. Caller holds s.mu.
func (s *Server) resolveMailboxRef(
	ref string, creations map[string]string,
) (string, bool) {
	switch {
	case ref == "":
		return "", true
	case strings.HasPrefix(ref, "#"):
		id, ok := creations[strings.TrimPrefix(ref, "#")]
		return id, ok
	default:
		return ref, s.mailboxExists(ref)
	}
}

// mailboxExists reports whether a mailbox id is seeded or created. Caller
// holds s.mu.
func (s *Server) mailboxExists(id string) bool {
	for _, m := range s.mailboxes {
		if m.ID == id {
			return true
		}
	}
	return false
}

// decodePatchFlag reads a one-level patch value: JSON true sets the
// membership, JSON null clears it. Anything else (notably false, which
// RFC 8621 forbids in a mailboxIds/keywords object) is not a valid patch.
func decodePatchFlag(raw json.RawMessage) (on, ok bool) {
	var value *bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	if value == nil {
		return false, true
	}
	if !*value {
		return false, false
	}
	return true, true
}

// decodeMembershipSet reads a whole-property replacement value: an object
// whose every value is true (RFC 8621 §4.1).
func decodeMembershipSet(raw json.RawMessage) (map[string]bool, bool) {
	var value map[string]bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	for _, on := range value {
		if !on {
			return nil, false
		}
	}
	if value == nil {
		value = map[string]bool{}
	}
	return value, true
}

func setOf(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) downloadBlob(w http.ResponseWriter, r *http.Request) {
	// Path: /jmap/download/{accountId}/{blobId}/{name}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/jmap/download/"), "/")
	if len(parts) < 2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	blobID := parts[1]

	s.mu.Lock()
	var body string
	found := false
	for _, e := range s.emails {
		if e.BlobID == blobID {
			body, found = e.Raw, true
			break
		}
	}
	s.mu.Unlock()

	if !found {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "message/rfc822")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// emailJSON renders one seeded email as its JMAP Email object.
func emailJSON(e Email) map[string]any {
	mailboxIDs := map[string]bool{}
	for _, id := range e.MailboxIDs {
		mailboxIDs[id] = true
	}
	keywords := map[string]bool{}
	for _, k := range e.Keywords {
		keywords[k] = true
	}
	from := make([]map[string]any, len(e.From))
	for i, a := range e.From {
		from[i] = map[string]any{"name": a.Name, "email": a.Email}
	}
	return map[string]any{
		"id":            e.ID,
		"threadId":      e.ThreadID,
		"mailboxIds":    mailboxIDs,
		"keywords":      keywords,
		"from":          from,
		"subject":       e.Subject,
		"receivedAt":    e.ReceivedAt,
		"hasAttachment": e.HasAttachment,
		"blobId":        e.BlobID,
	}
}

// state is the current Email/Mailbox type state, bumped on every seed so a
// change moves the FacetVersion token.
func (s *Server) state() string { return fmt.Sprintf("state-%d", s.stateSeq) }

func page[T any](items []T, position, limit int) []T {
	if position < 0 {
		position = 0
	}
	if position >= len(items) {
		return nil
	}
	items = items[position:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
