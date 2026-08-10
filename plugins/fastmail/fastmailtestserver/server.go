// Package fastmailtestserver is a minimal in-memory JMAP server for the
// fastmail plugin's tests (and any future bats lane). It answers the JMAP
// Session object, the Mailbox/get, Email/query (honoring inMailbox +
// collapseThreads), Email/get, and Thread/get method calls, and blob
// download — enough of RFC 8620/8621 for the plugin's read-only traversal
// and facet round-trip.
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

	responses := make([]any, 0, len(req.MethodCalls))
	for _, call := range req.MethodCalls {
		if len(call) < 3 {
			continue
		}
		var name, callID string
		_ = json.Unmarshal(call[0], &name)
		_ = json.Unmarshal(call[2], &callID)
		result := s.dispatch(name, call[1])
		responses = append(responses, []any{name, result, callID})
	}

	writeJSON(w, map[string]any{
		"methodResponses": responses,
		"sessionState":    "session-1",
	})
}

func (s *Server) dispatch(method string, args json.RawMessage) any {
	switch method {
	case "Mailbox/get":
		return s.mailboxGet()
	case "Email/query":
		return s.emailQuery(args)
	case "Email/get":
		return s.emailGet(args)
	case "Thread/get":
		return s.threadGet(args)
	default:
		return map[string]any{"type": "unknownMethod"}
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
