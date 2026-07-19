// Package caldavtestserver is a minimal in-memory CalDAV server for tests
// and the bats lane. It answers PROPFIND (one calendar collection by
// default, or several once AddCalendar registers more — the
// cutting-garden#162 calendar-home discovery scenario), REPORT
// calendar-query (calendar-data + getetag, filtered by component and scoped
// to the requested collection), GET (verbatim body), and PUT (records the
// body and registers it so a subsequent REPORT/GET sees it).
//
// It exists because Radicale cannot start under the nix test sandbox (it
// calls socket.socketpair(AF_UNIX), which the sandbox blocks — see
// amarbel-llc/dodder#117). This server is a pure net/http listener (TCP,
// no socketpair), so it runs in-sandbox. It is the standalone form of the
// caldav plugin's in-Go test fake, shared by `cmd/cutting-garden-caldav-
// testserver` (the coproc the bats lane spawns) and the plugin's Go tests.
//
// It is NOT a conformant CalDAV implementation and NOT shipped in the
// cutting-garden release — only enough of RFC 4791 for the plugin's
// capture/diff/restore round-trip.
package caldavtestserver

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

// Calendar describes one calendar collection the server advertises via
// PROPFIND: its server-absolute path and its `displayname` DAV prop. Used
// to seed multiple calendars under one calendar-home so the
// principal/calendar-home discovery path (cutting-garden#162) has a
// server to discover against — a single-calendar server cannot exercise
// "PROPFIND the home, get back N distinctly-named children".
type Calendar struct {
	Path        string
	DisplayName string
}

// Server is a running in-memory CalDAV test server.
type Server struct {
	mu        sync.Mutex
	httptest  *httptest.Server
	resources map[string]string // href -> verbatim text/calendar body
	component map[string]string // href -> "VTODO"/"VEVENT"
	// CalendarPath is the server-absolute path of the FIRST calendar
	// collection this server advertises (e.g. "/dav/cal/") — kept for
	// backward compatibility with single-calendar callers. Calendars holds
	// every advertised collection, including this one.
	CalendarPath string
	// Calendars is every calendar collection this server advertises.
	// PROPFIND on a non-object, non-self path returns all of them, so a
	// request at a calendar-home path discovers every entry here (mirroring
	// discoverCalendars' "base is not itself a calendar" branch); a request
	// at one calendar's own path is picked out of the same list by the
	// client-side self-match (see AddCalendar).
	Calendars []Calendar
}

// Start launches a server advertising one calendar collection at
// calendarPath (default "/dav/cal/" when empty) and returns it. Close it
// when done. Call AddCalendar afterward to seed additional calendars under
// the same calendar-home for multi-calendar discovery tests.
func Start(calendarPath string) *Server {
	if calendarPath == "" {
		calendarPath = "/dav/cal/"
	}
	s := &Server{
		resources:    map[string]string{},
		component:    map[string]string{},
		CalendarPath: calendarPath,
		Calendars:    []Calendar{{Path: calendarPath, DisplayName: "Personal"}},
	}
	s.httptest = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// AddCalendar registers an additional calendar collection at path,
// advertised with the given displayname. A PROPFIND at a calendar-home
// path (anything that isn't one calendar's own path) then discovers every
// registered calendar — the caldav#162 principal/calendar-home discovery
// scenario. The plugin's traversal (ListRoots) surfaces each discovered
// calendar's displayname as its Node.Name, which is also the #120 friendly-
// label win: a discovered calendar is labeled by its DAV displayname rather
// than an opaque path segment.
func (s *Server) AddCalendar(path, displayName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calendars = append(s.Calendars, Calendar{Path: path, DisplayName: displayName})
}

// URL is the server's base http URL (no trailing path).
func (s *Server) URL() string { return s.httptest.URL }

// Close shuts the server down.
func (s *Server) Close() { s.httptest.Close() }

// Seed adds a resource (a VTODO/VEVENT body) at a server-absolute href.
func (s *Server) Seed(href, component, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[href] = body
	s.component[href] = component
}

// Remove deletes a seeded resource.
func (s *Server) Remove(href string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resources, href)
	delete(s.component, href)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "PROPFIND":
		s.propfind(w, r)
	case "REPORT":
		s.report(w, r)
	case "GET":
		s.get(w, r)
	case "PUT":
		s.put(w, r)
	case "DELETE":
		s.delete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) propfind(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, isObject := s.resources[r.URL.Path]
	calendars := append([]Calendar(nil), s.Calendars...)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	if isObject {
		// A PROPFIND on a seeded object path describes a single
		// non-collection resource (no <d:collection/>, no <c:calendar/>),
		// so discoverCalendars finds no calendar here and traversal
		// classifies it as a leaf — matching a real server's reply for an
		// individual .ics resource (vs the always-calendar reply a coarser
		// fake would give).
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, r.URL.Path)
		return
	}

	// Every other PROPFIND (whether at a calendar-home path or at one
	// calendar's own path) answers with every registered calendar. The
	// caller doesn't need path-based filtering here: discoverCalendars
	// (plugins/caldav/client.go) already picks the self-match out of
	// whatever the response contains when the request targets one
	// calendar's own path, and treats every entry as a discovered child
	// otherwise — so returning the full set both ways exercises both
	// branches correctly.
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	sb.WriteString(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` + "\n")
	for _, cal := range calendars {
		fmt.Fprintf(&sb, `  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>
        <d:displayname>%s</d:displayname>
        <d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
        <c:supported-calendar-component-set>
          <c:comp name="VTODO"/>
          <c:comp name="VEVENT"/>
        </c:supported-calendar-component-set>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
`, cal.Path, cal.DisplayName)
	}
	sb.WriteString(`</d:multistatus>`)
	_, _ = io.WriteString(w, sb.String())
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	reqBody, _ := io.ReadAll(r.Body)
	want := "VTODO"
	switch {
	case strings.Contains(string(reqBody), `name="VEVENT"`):
		want = "VEVENT"
	case strings.Contains(string(reqBody), `name="VJOURNAL"`):
		want = "VJOURNAL"
	}

	s.mu.Lock()
	hrefs := make([]string, 0, len(s.resources))
	for href := range s.resources {
		// Scope to the calendar collection the REPORT targeted: with
		// multiple calendars registered (AddCalendar), a resource under a
		// sibling calendar must not appear in this collection's listing.
		if !strings.HasPrefix(href, r.URL.Path) {
			continue
		}
		if s.component[href] == want {
			hrefs = append(hrefs, href)
		}
	}
	bodies := make(map[string]string, len(hrefs))
	for _, h := range hrefs {
		bodies[h] = s.resources[h]
	}
	s.mu.Unlock()
	sort.Strings(hrefs)

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	sb.WriteString(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` + "\n")
	for _, href := range hrefs {
		fmt.Fprintf(&sb, `  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <c:calendar-data>%s</c:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
`, href, Etag(bodies[href]), bodies[href])
	}
	sb.WriteString(`</d:multistatus>`)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, sb.String())
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	body, ok := s.resources[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, exists := s.resources[r.URL.Path]
	// Honor the strict create/update preconditions (RFC 7232): If-None-Match:
	// * fails on an existing resource (create), If-Match: * on a missing one
	// (update).
	if r.Header.Get("If-None-Match") == "*" && exists {
		s.mu.Unlock()
		http.Error(w, "exists", http.StatusPreconditionFailed)
		return
	}
	if r.Header.Get("If-Match") == "*" && !exists {
		s.mu.Unlock()
		http.Error(w, "absent", http.StatusPreconditionFailed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.resources[r.URL.Path] = string(body)
	switch {
	case strings.Contains(string(body), "BEGIN:VEVENT"):
		s.component[r.URL.Path] = "VEVENT"
	case strings.Contains(string(body), "BEGIN:VJOURNAL"):
		s.component[r.URL.Path] = "VJOURNAL"
	default:
		s.component[r.URL.Path] = "VTODO"
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, ok := s.resources[r.URL.Path]
	if ok {
		delete(s.resources, r.URL.Path)
		delete(s.component, r.URL.Path)
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Etag derives a resource's etag from its body, so a body change moves the
// etag (as a real server's would). Exported so the in-Go test fake and any
// caller can compute the same value. Quoted per the getetag wire shape.
func Etag(body string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(body); i++ {
		h ^= uint32(body[i])
		h *= 16777619
	}
	return fmt.Sprintf(`"%08x"`, h)
}
