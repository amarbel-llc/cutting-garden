package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_log"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
)

// testServer wires a server with in-memory blob/receipt hooks plus a
// temp captures.log, and mounts it on an httptest.Server.
type testServer struct {
	*server
	http     *httptest.Server
	mu       sync.Mutex
	receipts [][]capture_receipt.EntryV1
	logPath  string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "captures.log")
	ts := &testServer{logPath: logPath}
	s := &server{
		info:           deviceInfo{Alias: "test", Version: protocolVersion},
		captureLogPath: logPath,
		storeName:      "",
		log:            func(string, ...any) {},
	}
	s.writeBlob = func(_ context.Context, r io.Reader) (string, int64, error) {
		data, err := io.ReadAll(r)
		if err != nil {
			return "", 0, err
		}
		sum := sha256.Sum256(data)
		return "blob-" + hex.EncodeToString(sum[:8]), int64(len(data)), nil
	}
	s.writeReceipt = func(entries []capture_receipt.EntryV1) (string, error) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.receipts = append(ts.receipts, entries)
		return "receipt-" + randToken(), nil
	}
	ts.server = s
	ts.http = httptest.NewServer(s.handler())
	t.Cleanup(ts.http.Close)
	return ts
}

func (ts *testServer) prepare(t *testing.T, files map[string]fileMeta) *http.Response {
	t.Helper()
	body, _ := json.Marshal(prepareUploadRequest{
		Info:  deviceInfo{Alias: "sender"},
		Files: files,
	})
	u := ts.http.URL + apiPrefix + "/prepare-upload"
	resp, err := http.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("prepare-upload: %v", err)
	}
	return resp
}

func (ts *testServer) upload(t *testing.T, sessionID, fileID, token string, data []byte) *http.Response {
	t.Helper()
	u := ts.http.URL + apiPrefix + "/upload?" + url.Values{
		"sessionId": {sessionID},
		"fileId":    {fileID},
		"token":     {token},
	}.Encode()
	resp, err := http.Post(u, "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

func decodePrepare(t *testing.T, resp *http.Response) prepareUploadResponse {
	t.Helper()
	defer resp.Body.Close()
	var out prepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	return out
}

func TestServe_FullTransfer_WritesReceiptAndLog(t *testing.T) {
	ts := newTestServer(t)

	files := map[string]fileMeta{
		"f1": {ID: "f1", FileName: "a.txt", Size: 3},
		"f2": {ID: "f2", FileName: "docs/b.txt", Size: 5},
	}
	resp := ts.prepare(t, files)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prepare status = %d, want 200", resp.StatusCode)
	}
	pr := decodePrepare(t, resp)
	if pr.SessionID == "" || len(pr.Files) != 2 {
		t.Fatalf("bad prepare response: %+v", pr)
	}

	r1 := ts.upload(t, pr.SessionID, "f1", pr.Files["f1"], []byte("abc"))
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("upload f1 status = %d, want 200", r1.StatusCode)
	}
	r1.Body.Close()

	r2 := ts.upload(t, pr.SessionID, "f2", pr.Files["f2"], []byte("hello"))
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("upload f2 status = %d, want 200", r2.StatusCode)
	}
	r2.Body.Close()

	// Exactly one receipt, folding 2 files + 1 synthesized dir entry.
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.receipts) != 1 {
		t.Fatalf("got %d receipts, want 1", len(ts.receipts))
	}
	got := ts.receipts[0]
	var paths []string
	var dirs int
	for _, e := range got {
		paths = append(paths, e.Type+":"+e.Path)
		if e.Type == capture_receipt.TypeDir {
			dirs++
		}
	}
	sort.Strings(paths)
	if dirs != 1 {
		t.Fatalf("want 1 dir entry, got %d (%v)", dirs, paths)
	}
	want := []string{"dir:docs", "file:a.txt", "file:docs/b.txt"}
	if len(paths) != len(want) {
		t.Fatalf("entries = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("entries = %v, want %v", paths, want)
		}
	}

	// captures.log got one NDJSON line with a receipt id.
	data, err := os.ReadFile(ts.logPath)
	if err != nil {
		t.Fatalf("read captures.log: %v", err)
	}
	var entry capture_log.Entry
	if err := json.Unmarshal(bytes.TrimSpace(data), &entry); err != nil {
		t.Fatalf("parse captures.log line %q: %v", data, err)
	}
	if entry.ReceiptID == "" {
		t.Fatalf("captures.log entry missing receipt id: %+v", entry)
	}
}

func TestServe_SecondSessionConflicts(t *testing.T) {
	ts := newTestServer(t)
	files := map[string]fileMeta{"f1": {ID: "f1", FileName: "a.txt", Size: 1}}

	resp := ts.prepare(t, files)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first prepare = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp2 := ts.prepare(t, files)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second prepare = %d, want 409", resp2.StatusCode)
	}
}

func TestServe_EmptyFilesNoContent(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.prepare(t, map[string]fileMeta{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty prepare = %d, want 204", resp.StatusCode)
	}
}

func TestServe_UnsafeFileNameRejected(t *testing.T) {
	ts := newTestServer(t)
	files := map[string]fileMeta{
		"f1": {ID: "f1", FileName: "../escape.txt", Size: 1},
	}
	pr := decodePrepare(t, ts.prepare(t, files))

	resp := ts.upload(t, pr.SessionID, "f1", pr.Files["f1"], []byte("x"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe upload = %d, want 400", resp.StatusCode)
	}
}

func TestServe_LastFileFailureFinalizesSession(t *testing.T) {
	ts := newTestServer(t)
	files := map[string]fileMeta{
		"f1": {ID: "f1", FileName: "good.txt", Size: 3},
		"f2": {ID: "f2", FileName: "../escape.txt", Size: 3},
	}
	pr := decodePrepare(t, ts.prepare(t, files))

	ts.upload(t, pr.SessionID, "f1", pr.Files["f1"], []byte("abc")).Body.Close()

	// The session's LAST pending file fails sanitization. The session
	// must finalize (folding the one good file) and release the
	// single-session slot — not wedge every future prepare-upload.
	bad := ts.upload(t, pr.SessionID, "f2", pr.Files["f2"], []byte("xyz"))
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe upload = %d, want 400", bad.StatusCode)
	}

	ts.mu.Lock()
	if len(ts.receipts) != 1 || len(ts.receipts[0]) != 1 {
		t.Fatalf("receipts = %+v, want one receipt with one entry", ts.receipts)
	}
	ts.mu.Unlock()

	next := ts.prepare(t, map[string]fileMeta{
		"g1": {ID: "g1", FileName: "next.txt", Size: 1},
	})
	defer next.Body.Close()
	if next.StatusCode != http.StatusOK {
		t.Fatalf("prepare after failed session = %d, want 200 (receiver wedged)",
			next.StatusCode)
	}
}

func TestServe_AllFilesFailedReleasesSessionWithoutReceipt(t *testing.T) {
	ts := newTestServer(t)
	files := map[string]fileMeta{
		"f1": {ID: "f1", FileName: "../only.txt", Size: 1},
	}
	pr := decodePrepare(t, ts.prepare(t, files))

	bad := ts.upload(t, pr.SessionID, "f1", pr.Files["f1"], []byte("x"))
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe upload = %d, want 400", bad.StatusCode)
	}

	ts.mu.Lock()
	if len(ts.receipts) != 0 {
		t.Fatalf("receipts = %+v, want none for an all-failed session", ts.receipts)
	}
	ts.mu.Unlock()

	next := ts.prepare(t, map[string]fileMeta{
		"g1": {ID: "g1", FileName: "next.txt", Size: 1},
	})
	defer next.Body.Close()
	if next.StatusCode != http.StatusOK {
		t.Fatalf("prepare after all-failed session = %d, want 200 (receiver wedged)",
			next.StatusCode)
	}
}

func TestServe_PrepareUploadBodyTooLargeRejected(t *testing.T) {
	ts := newTestServer(t)

	// A body over the 1 MiB cap must be rejected, not decoded.
	huge := bytes.Repeat([]byte("a"), maxPrepareUploadBody+1)
	resp, err := http.Post(ts.http.URL+apiPrefix+"/prepare-upload",
		"application/json", bytes.NewReader(huge))
	if err != nil {
		t.Fatalf("prepare-upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized prepare = %d, want 400", resp.StatusCode)
	}
}

func TestServe_CancelWritesPartialReceipt(t *testing.T) {
	ts := newTestServer(t)
	files := map[string]fileMeta{
		"f1": {ID: "f1", FileName: "a.txt", Size: 3},
		"f2": {ID: "f2", FileName: "b.txt", Size: 3},
	}
	pr := decodePrepare(t, ts.prepare(t, files))

	// Upload only one of two files, then cancel.
	ts.upload(t, pr.SessionID, "f1", pr.Files["f1"], []byte("abc")).Body.Close()

	cancelURL := ts.http.URL + apiPrefix + "/cancel?sessionId=" + pr.SessionID
	resp, err := http.Post(cancelURL, "", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	resp.Body.Close()

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.receipts) != 1 {
		t.Fatalf("got %d receipts, want 1 partial receipt", len(ts.receipts))
	}
	if n := len(ts.receipts[0]); n != 1 {
		t.Fatalf("partial receipt has %d entries, want 1", n)
	}
}

func TestServe_InfoAndRegister(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.http.URL + apiPrefix + "/info")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	defer resp.Body.Close()
	var info deviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.Alias != "test" || info.Version != protocolVersion {
		t.Fatalf("unexpected info: %+v", info)
	}

	reg, err := http.Post(ts.http.URL+apiPrefix+"/register", "application/json",
		bytes.NewReader([]byte(`{"alias":"sender"}`)))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.Body.Close()
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register = %d, want 200", reg.StatusCode)
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"a.txt", "a.txt", false},
		{"docs/sub/b.txt", "docs/sub/b.txt", false},
		{"docs\\win.txt", "docs/win.txt", false},
		{"./a.txt", "a.txt", false},
		{"", "", true},
		{"/abs.txt", "", true},
		{"../escape", "", true},
		{"a/../../escape", "", true},
		{"..", "", true},
	}
	for _, c := range cases {
		got, err := sanitizeFileName(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("sanitizeFileName(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sanitizeFileName(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWithDirEntries_DeepNesting(t *testing.T) {
	files := []capture_receipt.EntryV1{
		{Path: "x/y/z/file.txt", Root: ".", Type: capture_receipt.TypeFile},
		{Path: "top.txt", Root: ".", Type: capture_receipt.TypeFile},
	}
	out := withDirEntries(files)
	dirs := map[string]bool{}
	for _, e := range out {
		if e.Type == capture_receipt.TypeDir {
			dirs[e.Path] = true
		}
	}
	for _, want := range []string{"x", "x/y", "x/y/z"} {
		if !dirs[want] {
			t.Errorf("missing synthesized dir %q in %v", want, dirs)
		}
	}
	if len(dirs) != 3 {
		t.Errorf("got %d dir entries, want 3: %v", len(dirs), dirs)
	}
}
