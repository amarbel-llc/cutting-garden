// Package orgtestserver is a test-only, in-memory WebDAV server for the org
// plugin (FDR 0026, cutting-garden#307): the hermetic stand-in for Fastmail
// Files, which Orgzly syncs `.org` files through. It serves PROPFIND (Depth
// 0/1), GET, PUT, LOCK and UNLOCK behind HTTP Basic auth, and changes a
// file's ETag on every write. Like Fastmail Files (cutting-garden#267) it
// ignores conditional headers (If-Match and friends) but enforces LOCK: a
// PUT to a locked file without `If: (<token>)` is 423.
//
// ETags and modification times are deterministic: a global write counter,
// with times counted in seconds from a fixed epoch, so bats vectors stay
// stable. It is NOT shipped in the cutting-garden release.
package orgtestserver

import (
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Credentials the server accepts; anything else is 401.
const (
	Username = "test"
	Password = "test"
)

// epoch is the fixed base of every modification time.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type file struct {
	content []byte
	etag    string
	mod     time.Time
}

// Server is a running in-memory WebDAV server.
type Server struct {
	srv *httptest.Server

	mu     sync.Mutex
	files  map[string]*file // key: slash path without a leading slash
	writes int
	locks  map[string]string // path → lock token
	tokens int
}

// Lock takes an exclusive lock on p as another writer would, returning
// its token: tests use it to make the plugin meet a held lock.
func (s *Server) Lock(p string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lockLocked(strings.Trim(p, "/"))
}

// Unlock releases a lock taken with Lock.
func (s *Server) Unlock(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.locks, strings.Trim(p, "/"))
}

// Content returns a file's current bytes.
func (s *Server) Content(p string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[strings.Trim(p, "/")]; ok {
		return append([]byte(nil), f.content...)
	}
	return nil
}

func (s *Server) lockLocked(p string) string {
	s.tokens++
	token := "opaquelocktoken:t" + strconv.Itoa(s.tokens)
	s.locks[p] = token
	return token
}

// Start starts a server on an ephemeral loopback port.
func Start() *Server { return StartAt("") }

// StartAt starts a server on addr ("host:port"), or an ephemeral loopback
// port when addr is empty.
func StartAt(addr string) *Server {
	s := &Server{files: map[string]*file{}, locks: map[string]string{}}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(s.serveHTTP))
	if addr != "" {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			panic(fmt.Sprintf("orgtestserver: listen %s: %v", addr, err))
		}
		ts.Listener.Close()
		ts.Listener = l
	}
	ts.Start()
	s.srv = ts
	return s
}

// URL is the server's root URL, ending in "/".
func (s *Server) URL() string { return s.srv.URL + "/" }

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Put creates or replaces the file at p (slash-separated, relative to the
// root), giving it a new ETag and modification time. It bypasses locks, as
// an unconditional uploader like Orgzly would.
func (s *Server) Put(p string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(p, content)
}

func (s *Server) putLocked(p string, content []byte) {
	s.writes++
	s.files[strings.Trim(p, "/")] = &file{
		content: append([]byte(nil), content...),
		etag:    strconv.Quote("e" + strconv.Itoa(s.writes)),
		mod:     epoch.Add(time.Duration(s.writes) * time.Second),
	}
}

// SeedDir loads every regular file under root, in lexical path order so
// the resulting ETags are deterministic.
func (s *Server) SeedDir(root string) error {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		s.Put(filepath.ToSlash(rel), content)
	}
	return nil
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if user, pass, ok := r.BasicAuth(); !ok || user != Username || pass != Password {
		w.Header().Set("WWW-Authenticate", `Basic realm="orgtestserver"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p := strings.Trim(path.Clean("/"+r.URL.Path), "/")
	switch r.Method {
	case "PROPFIND":
		s.propfind(w, r, p)
	case http.MethodGet:
		s.get(w, p)
	case http.MethodPut:
		s.put(w, r, p)
	case "LOCK":
		s.lock(w, p)
	case "UNLOCK":
		s.unlock(w, r, p)
	default:
		w.Header().Set("Allow", "OPTIONS, GET, PUT, PROPFIND, LOCK, UNLOCK")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// put writes the body, refusing with 423 when the file is locked and the
// request does not carry `If: (<token>)`. Conditional headers are ignored.
func (s *Server) put(w http.ResponseWriter, r *http.Request, p string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if token, locked := s.locks[p]; locked && !strings.Contains(r.Header.Get("If"), "<"+token+">") {
		http.Error(w, "locked", http.StatusLocked)
		return
	}
	s.putLocked(p, body)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) lock(w http.ResponseWriter, p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, locked := s.locks[p]; locked {
		http.Error(w, "locked", http.StatusLocked)
		return
	}
	token := s.lockLocked(p)
	w.Header().Set("Lock-Token", "<"+token+">")
	w.Header().Set("Content-Type", `application/xml; charset="utf-8"`)
	_, _ = io.WriteString(w, xml.Header+`<D:prop xmlns:D="DAV:"><D:lockdiscovery><D:activelock><D:locktoken><D:href>`+
		token+`</D:href></D:locktoken></D:activelock></D:lockdiscovery></D:prop>`)
}

func (s *Server) unlock(w http.ResponseWriter, r *http.Request, p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token, locked := s.locks[p]; !locked || r.Header.Get("Lock-Token") != "<"+token+">" {
		http.Error(w, "no such lock", http.StatusConflict)
		return
	}
	delete(s.locks, p)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) get(w http.ResponseWriter, p string) {
	s.mu.Lock()
	f, ok := s.files[p]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("ETag", f.etag)
	w.Header().Set("Last-Modified", f.mod.Format(http.TimeFormat))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(f.content)
}

// isDir reports whether p is the root or a prefix directory of some file.
// Callers hold s.mu.
func (s *Server) isDir(p string) bool {
	if p == "" {
		return true
	}
	for k := range s.files {
		if strings.HasPrefix(k, p+"/") {
			return true
		}
	}
	return false
}

type multistatus struct {
	XMLName   xml.Name   `xml:"D:multistatus"`
	XMLNS     string     `xml:"xmlns:D,attr"`
	Responses []response `xml:"D:response"`
}

type response struct {
	Href     string   `xml:"D:href"`
	Propstat propstat `xml:"D:propstat"`
}

type propstat struct {
	Prop   prop   `xml:"D:prop"`
	Status string `xml:"D:status"`
}

type prop struct {
	ResourceType     resourceType `xml:"D:resourcetype"`
	GetETag          string       `xml:"D:getetag,omitempty"`
	GetLastModified  string       `xml:"D:getlastmodified,omitempty"`
	GetContentLength string       `xml:"D:getcontentlength,omitempty"`
	DisplayName      string       `xml:"D:displayname,omitempty"`
}

type resourceType struct {
	Collection *struct{} `xml:"D:collection"`
}

// propfind answers Depth 0 (the resource itself) or Depth 1 (it plus its
// immediate children). "infinity" is refused, as servers commonly do.
func (s *Server) propfind(w http.ResponseWriter, r *http.Request, p string) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "1"
	}
	if depth != "0" && depth != "1" {
		http.Error(w, "Depth must be 0 or 1", http.StatusForbidden)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	f, isFile := s.files[p]
	if !isFile && !s.isDir(p) {
		http.NotFound(w, nil)
		return
	}

	ms := multistatus{XMLNS: "DAV:"}
	if isFile {
		ms.Responses = append(ms.Responses, fileResponse(p, f))
	} else {
		ms.Responses = append(ms.Responses, dirResponse(p))
		if depth == "1" {
			ms.Responses = append(ms.Responses, s.children(p)...)
		}
	}

	w.Header().Set("Content-Type", `application/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(ms)
}

// children lists dir's immediate files and subdirectories, sorted by href.
// Callers hold s.mu.
func (s *Server) children(dir string) []response {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	subdirs := map[string]bool{}
	var out []response
	for k, f := range s.files {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		if name, _, nested := strings.Cut(rest, "/"); nested {
			subdirs[prefix+name] = true
			continue
		}
		out = append(out, fileResponse(k, f))
	}
	for d := range subdirs {
		out = append(out, dirResponse(d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Href < out[j].Href })
	return out
}

func fileResponse(p string, f *file) response {
	return response{
		Href: hrefFor(p, false),
		Propstat: propstat{
			Prop: prop{
				GetETag:          f.etag,
				GetLastModified:  f.mod.Format(http.TimeFormat),
				GetContentLength: strconv.Itoa(len(f.content)),
				DisplayName:      path.Base(p),
			},
			Status: "HTTP/1.1 200 OK",
		},
	}
}

func dirResponse(p string) response {
	return response{
		Href: hrefFor(p, true),
		Propstat: propstat{
			Prop: prop{
				ResourceType: resourceType{Collection: &struct{}{}},
				DisplayName:  path.Base("/" + p),
			},
			Status: "HTTP/1.1 200 OK",
		},
	}
}

// hrefFor percent-escapes each segment of p into an absolute href;
// collections end in "/".
func hrefFor(p string, dir bool) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	href := "/" + strings.Join(segs, "/")
	if dir && !strings.HasSuffix(href, "/") {
		href += "/"
	}
	return href
}
