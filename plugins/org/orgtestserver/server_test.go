package orgtestserver

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func do(t *testing.T, method, url, depth string, auth bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	if auth {
		req.SetBasicAuth(Username, Password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func newSeeded(t *testing.T) *Server {
	t.Helper()
	s := Start()
	t.Cleanup(s.Close)
	s.Put("_inbox.org", []byte("* TODO a\n"))
	s.Put("projects/reno.org", []byte("* b\n"))
	s.Put("projects/old notes.org", []byte("* c\n"))
	return s
}

func TestAuthRequired(t *testing.T) {
	s := newSeeded(t)
	if resp, _ := do(t, http.MethodGet, s.URL()+"_inbox.org", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGet(t *testing.T) {
	s := newSeeded(t)
	resp, body := do(t, http.MethodGet, s.URL()+"_inbox.org", "", true)
	if resp.StatusCode != http.StatusOK || body != "* TODO a\n" || resp.Header.Get("ETag") != `"e1"` {
		t.Errorf("status=%d etag=%s body=%q", resp.StatusCode, resp.Header.Get("ETag"), body)
	}
	if resp, _ := do(t, http.MethodGet, s.URL()+"missing.org", "", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing: status = %d", resp.StatusCode)
	}
}

func TestPutChangesETag(t *testing.T) {
	s := newSeeded(t)
	s.Put("_inbox.org", []byte("* TODO a\n* b\n"))
	resp, _ := do(t, http.MethodGet, s.URL()+"_inbox.org", "", true)
	if got := resp.Header.Get("ETag"); got != `"e4"` {
		t.Errorf("ETag after rewrite = %s, want \"e4\"", got)
	}
}

func TestPropfindDepth1ListsImmediateChildren(t *testing.T) {
	s := newSeeded(t)
	resp, body := do(t, "PROPFIND", s.URL(), "1", true)
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{
		"<D:href>/</D:href>",
		"<D:href>/_inbox.org</D:href>",
		"<D:href>/projects/</D:href>",
		`<D:getetag>&#34;e1&#34;</D:getetag>`,
		"<D:collection></D:collection>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "reno.org") {
		t.Errorf("Depth 1 at the root must not list nested files:\n%s", body)
	}

	_, sub := do(t, "PROPFIND", s.URL()+"projects/", "1", true)
	if !strings.Contains(sub, "<D:href>/projects/old%20notes.org</D:href>") {
		t.Errorf("subfolder listing lacks the escaped file:\n%s", sub)
	}
}

func TestPropfindDepth0AndRefusals(t *testing.T) {
	s := newSeeded(t)
	_, body := do(t, "PROPFIND", s.URL()+"projects/", "0", true)
	if strings.Count(body, "<D:response>") != 1 {
		t.Errorf("Depth 0 should return one response:\n%s", body)
	}
	if resp, _ := do(t, "PROPFIND", s.URL(), "infinity", true); resp.StatusCode != http.StatusForbidden {
		t.Errorf("Depth infinity: status = %d, want 403", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodDelete, s.URL()+"_inbox.org", "", true); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: status = %d, want 405", resp.StatusCode)
	}
}

func putWith(t *testing.T, url, ifHeader, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(Username, Password)
	if ifHeader != "" {
		req.Header.Set("If", ifHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestLockEnforcedConditionalHeadersIgnored(t *testing.T) {
	s := newSeeded(t)
	target := s.URL() + "_inbox.org"

	// Like Fastmail Files: a stale If-Match is ignored, the write lands.
	req, _ := http.NewRequest(http.MethodPut, target, strings.NewReader("x\n"))
	req.SetBasicAuth(Username, Password)
	req.Header.Set("If-Match", `"stale"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || string(s.Content("_inbox.org")) != "x\n" {
		t.Fatalf("If-Match should be ignored: status %d", resp.StatusCode)
	}

	lockResp, _ := do(t, "LOCK", target, "", true)
	token := lockResp.Header.Get("Lock-Token")
	if lockResp.StatusCode != http.StatusOK || token == "" {
		t.Fatalf("LOCK: status %d token %q", lockResp.StatusCode, token)
	}
	if again, _ := do(t, "LOCK", target, "", true); again.StatusCode != http.StatusLocked {
		t.Errorf("second LOCK = %d, want 423", again.StatusCode)
	}
	if code := putWith(t, target, "", "y\n"); code != http.StatusLocked {
		t.Errorf("PUT without token = %d, want 423", code)
	}
	if code := putWith(t, target, "("+token+")", "y\n"); code != http.StatusNoContent {
		t.Errorf("PUT with token = %d, want 204", code)
	}

	req, _ = http.NewRequest("UNLOCK", target, nil)
	req.SetBasicAuth(Username, Password)
	req.Header.Set("Lock-Token", token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("UNLOCK = %d", resp.StatusCode)
	}
	if code := putWith(t, target, "", "z\n"); code != http.StatusNoContent {
		t.Errorf("PUT after unlock = %d", code)
	}
}
