// Package caldavtestserver is a minimal in-memory CalDAV server for tests
// and the bats lane. It answers PROPFIND (one calendar collection), REPORT
// calendar-query (calendar-data + getetag, filtered by component), GET
// (verbatim body), and PUT (records the body and registers it so a
// subsequent REPORT/GET sees it).
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

// Server is a running in-memory CalDAV test server.
type Server struct {
	mu        sync.Mutex
	httptest  *httptest.Server
	resources map[string]string // href -> verbatim text/calendar body
	component map[string]string // href -> "VTODO"/"VEVENT"
	// CalendarPath is the server-absolute path of the single calendar
	// collection this server advertises (e.g. "/dav/cal/").
	CalendarPath string
}

// Start launches a server advertising one calendar collection at
// calendarPath (default "/dav/cal/" when empty) and returns it. Close it
// when done.
func Start(calendarPath string) *Server {
	if calendarPath == "" {
		calendarPath = "/dav/cal/"
	}
	s := &Server{
		resources:    map[string]string{},
		component:    map[string]string{},
		CalendarPath: calendarPath,
	}
	s.httptest = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
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
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) propfind(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, isObject := s.resources[r.URL.Path]
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

	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>
        <d:displayname>Personal</d:displayname>
        <d:resourcetype><d:collection/><c:calendar/></d:resourcetype>
        <c:supported-calendar-component-set>
          <c:comp name="VTODO"/>
          <c:comp name="VEVENT"/>
        </c:supported-calendar-component-set>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, s.CalendarPath)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	reqBody, _ := io.ReadAll(r.Body)
	want := "VTODO"
	if strings.Contains(string(reqBody), `name="VEVENT"`) {
		want = "VEVENT"
	}

	s.mu.Lock()
	hrefs := make([]string, 0, len(s.resources))
	for href := range s.resources {
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
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.resources[r.URL.Path] = string(body)
	if strings.Contains(string(body), "BEGIN:VEVENT") {
		s.component[r.URL.Path] = "VEVENT"
	} else {
		s.component[r.URL.Path] = "VTODO"
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
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
