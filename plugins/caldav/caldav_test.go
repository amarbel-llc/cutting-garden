package caldav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// fakeCalDAV is a minimal in-memory CalDAV server for the plugin tests.
// It answers PROPFIND (one calendar collection), REPORT calendar-query
// (returning resources whose component matches the request), and PUT
// (recording the body). Resource hrefs are server-absolute paths under
// /dav/cal/.
type fakeCalDAV struct {
	// resources maps href -> body for the seeded VTODO/VEVENT objects.
	resources map[string]string
	// component maps href -> "VTODO"/"VEVENT" so REPORT can filter.
	component map[string]string
	// puts records every PUT the server received (href -> body).
	puts map[string]string
	// failComponent, when set, makes the REPORT for that component
	// ("VTODO"/"VEVENT") answer 500 — for exercising per-entry failures.
	failComponent string
}

func newFakeCalDAV() *fakeCalDAV {
	return &fakeCalDAV{
		resources: map[string]string{},
		component: map[string]string{},
		puts:      map[string]string{},
	}
}

func (f *fakeCalDAV) seed(href, component, body string) {
	f.resources[href] = body
	f.component[href] = component
}

// remove deletes a seeded resource — for exercising diff deletions.
func (f *fakeCalDAV) remove(href string) {
	delete(f.resources, href)
	delete(f.component, href)
}

// fakeEtag derives a resource's etag from its body, so a body change moves
// the etag (as a real server's would). A weak hash is fine: the test only
// needs distinct etags for distinct bodies and a stable etag for an
// unchanged body. Quoted per the getetag wire shape.
func fakeEtag(body string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(body); i++ {
		h ^= uint32(body[i])
		h *= 16777619
	}
	return fmt.Sprintf(`"%08x"`, h)
}

const calendarHref = "/dav/cal/"

func (f *fakeCalDAV) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PROPFIND":
			f.propfind(w, r)
		case "REPORT":
			f.report(w, r)
		case "GET":
			body, ok := f.resources[r.URL.Path]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, body)
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			f.puts[r.URL.Path] = string(body)
			// Register the PUT so a subsequent REPORT/GET sees it — this
			// makes a restore→diff round-trip observable. Component is
			// inferred from the body.
			f.resources[r.URL.Path] = string(body)
			if strings.Contains(string(body), "BEGIN:VEVENT") {
				f.component[r.URL.Path] = "VEVENT"
			} else {
				f.component[r.URL.Path] = "VTODO"
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (f *fakeCalDAV) propfind(w http.ResponseWriter, r *http.Request) {
	_, isObject := f.resources[r.URL.Path]

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	if isObject {
		// A PROPFIND on a seeded object path is a single non-collection
		// resource, so discoverCalendars finds no calendar and traversal
		// classifies the node as a leaf — matching a real server's reply
		// for one .ics resource.
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
</d:multistatus>`, calendarHref)
}

func (f *fakeCalDAV) report(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	want := "VTODO"
	if strings.Contains(string(body), `name="VEVENT"`) {
		want = "VEVENT"
	}

	if f.failComponent != "" && want == f.failComponent {
		http.Error(w, "report boom", http.StatusInternalServerError)
		return
	}

	hrefs := make([]string, 0, len(f.resources))
	for href := range f.resources {
		hrefs = append(hrefs, href)
	}
	sort.Strings(hrefs)

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	sb.WriteString(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` + "\n")
	for _, href := range hrefs {
		if f.component[href] != want {
			continue
		}
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
`, href, fakeEtag(f.resources[href]), f.resources[href])
	}
	sb.WriteString(`</d:multistatus>`)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, sb.String())
}

func vtodo(uid, summary string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nEND:VTODO\nEND:VCALENDAR\n"
}

func vevent(uid, summary string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nEND:VEVENT\nEND:VCALENDAR\n"
}

// startFake spins up a seeded fake CalDAV server and returns it plus the
// opaque `caldav:` source argument pointing at its calendar root.
func startFake(t *testing.T) (*fakeCalDAV, string) {
	t.Helper()
	f := newFakeCalDAV()
	f.seed("/dav/cal/task1.ics", "VTODO", vtodo("task1", "Buy milk"))
	f.seed("/dav/cal/task2.ics", "VTODO", vtodo("task2", "Walk dog"))
	f.seed("/dav/cal/event1.ics", "VEVENT", vevent("event1", "Standup"))

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)

	return f, "caldav:" + srv.URL + "/dav/"
}

// startFakeEmpty spins up a fake CalDAV server that advertises the same
// calendar collection (so its collection name matches what a capture from
// startFake recorded) but holds NO resources — a fresh restore target.
func startFakeEmpty(t *testing.T) (*fakeCalDAV, string) {
	t.Helper()
	f := newFakeCalDAV()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return f, "caldav:" + srv.URL + "/dav/"
}

func TestCaptureRoot_CapturesAllResources(t *testing.T) {
	_, arg := startFake(t)

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newMemStore(t),
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0", result.FailCount)
	}
	gotPaths := entryPaths(result.Entries)
	wantPaths := []string{"dav/cal/event1.ics", "dav/cal/task1.ics", "dav/cal/task2.ics"}
	sort.Strings(gotPaths)
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("paths = %v, want %v", gotPaths, wantPaths)
	}
	for _, e := range result.Entries {
		if e.Type != capture_receipt.TypeFile {
			t.Errorf("entry %q: Type = %q, want file", e.Path, e.Type)
		}
		if e.BlobId == "" {
			t.Errorf("entry %q: empty BlobId", e.Path)
		}
		if !strings.HasPrefix(e.Root, "http://") {
			t.Errorf("entry %q: Root = %q, want origin URL", e.Path, e.Root)
		}
		if e.Mode.Perm() != resourceMode {
			t.Errorf("entry %q: Mode = %v, want %v", e.Path, e.Mode.Perm(), fmtMode(resourceMode))
		}
	}
}

func fmtMode(m int) string { return fmt.Sprintf("%o", m) }

func TestScanForDiff_MatchesCapture(t *testing.T) {
	_, arg := startFake(t)
	store := newMemStore(t)

	captured := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: store,
	})

	diffEntries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            mustParseURL(t, arg),
		RawDir:         arg,
		BlobStore:      store,
		ReceiptEntries: captured.Entries,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	if !sameEntrySet(captured.Entries, diffEntries) {
		t.Errorf("diff entries differ from capture:\n  capture: %v\n  diff:    %v",
			entrySummary(captured.Entries), entrySummary(diffEntries))
	}
}

func TestRestore_PutsResourcesBack(t *testing.T) {
	f, arg := startFake(t)
	store := newMemStore(t)

	captured := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: store,
	})
	if captured.FailCount != 0 {
		t.Fatalf("capture FailCount = %d", captured.FailCount)
	}

	// Restore to the same endpoint; the fake records every PUT.
	if err := (Plugin{}).Restore(cutting_garden_plugins.RestoreRequest{
		Context:   context.Background(),
		Entries:   captured.Entries,
		BlobStore: store,
		Dest:      mustParseURL(t, arg),
		RawDest:   arg,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if len(f.puts) != 3 {
		t.Fatalf("server received %d PUTs, want 3: %v", len(f.puts), f.puts)
	}
	for href, original := range f.resources {
		got, ok := f.puts[href]
		if !ok {
			t.Errorf("no PUT recorded for %s", href)
			continue
		}
		if got != original {
			t.Errorf("PUT body for %s mismatch:\n got: %q\nwant: %q", href, got, original)
		}
	}
}

func entrySummary(es []capture_receipt.EntryV1) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path + "@" + e.BlobId
	}
	sort.Strings(out)
	return out
}

func entryPaths(es []capture_receipt.EntryV1) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

func sameEntrySet(a, b []capture_receipt.EntryV1) bool {
	if len(a) != len(b) {
		return false
	}
	have := map[string]string{}
	for _, e := range b {
		have[e.Path] = e.BlobId
	}
	for _, e := range a {
		if have[e.Path] != e.BlobId {
			return false
		}
	}
	return true
}
