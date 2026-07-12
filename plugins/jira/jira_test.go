package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// fakeJira is a minimal in-memory Jira Cloud REST server for the plugin
// tests. It answers POST /rest/api/3/search/jql (returning the seeded
// issues whose project matches the JQL), GET /rest/api/3/issue/KEY, and
// GET /rest/api/3/project/search.
type fakeJira struct {
	// issues maps issue key -> its raw JSON body.
	issues map[string]string
	// projects is the ordered list of project keys to advertise.
	projects []string
}

func newFakeJira() *fakeJira {
	return &fakeJira{issues: map[string]string{}}
}

func (f *fakeJira) seed(key, summary string) {
	f.issues[key] = `{"key":"` + key + `","fields":{"summary":"` + summary + `"}}`
}

var jqlProjectRe = regexp.MustCompile(`project = "([^"]+)"`)

func (f *fakeJira) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			JQL string `json:"jql"`
		}
		_ = json.Unmarshal(body, &req)
		want := ""
		if m := jqlProjectRe.FindStringSubmatch(req.JQL); m != nil {
			want = m[1]
		}
		var keys []string
		for k := range f.issues {
			if projectOfKey(k) == want {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var raws []json.RawMessage
		for _, k := range keys {
			raws = append(raws, json.RawMessage(f.issues[k]))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issues": raws})
	})
	mux.HandleFunc("/rest/api/3/issue/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
		raw, ok := f.issues[key]
		if !ok {
			http.Error(w, `{"errorMessages":["not found"]}`, http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, raw)
	})
	mux.HandleFunc("/rest/api/3/project/search", func(w http.ResponseWriter, _ *http.Request) {
		var values []map[string]any
		for _, p := range f.projects {
			values = append(values, map[string]any{"key": p, "name": p + " Project"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"values": values, "isLast": true})
	})
	return mux
}

func startFake(t *testing.T) (*fakeJira, string) {
	t.Helper()
	f := newFakeJira()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	// Opaque form reaches the plain-HTTP test server.
	return f, "jira:" + srv.URL
}

func sortedPaths(entries []capture_receipt.EntryV1) []string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	sort.Strings(paths)
	return paths
}

func TestCaptureRoot_Project(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-1", "First")
	f.seed("PROJ-2", "Second")
	f.seed("OTHER-1", "Elsewhere")

	store := newMemStore(t)
	res := (Plugin{}).CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, baseURI+"/PROJ"),
		RawArg:    baseURI + "/PROJ",
		BlobStore: store,
	})
	if res.FailCount != 0 {
		t.Fatalf("FailCount = %d, failures = %v", res.FailCount, res.Failures)
	}
	got := sortedPaths(res.Entries)
	want := []string{"PROJ/PROJ-1.json", "PROJ/PROJ-2.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v", got, want)
	}
	for _, e := range res.Entries {
		if e.Type != capture_receipt.TypeFile || e.BlobId == "" {
			t.Errorf("entry %+v: want file type with blob id", e)
		}
	}
}

func TestCaptureRoot_SingleIssue(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-7", "Lonely")

	store := newMemStore(t)
	res := (Plugin{}).CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, baseURI+"/PROJ/PROJ-7"),
		RawArg:    baseURI + "/PROJ/PROJ-7",
		BlobStore: store,
	})
	if res.FailCount != 0 || len(res.Entries) != 1 {
		t.Fatalf("got %d entries, %d failures", len(res.Entries), res.FailCount)
	}
	if res.Entries[0].Path != "PROJ/PROJ-7.json" {
		t.Errorf("path = %q, want PROJ/PROJ-7.json", res.Entries[0].Path)
	}
}

func TestCaptureRoot_AllProjects(t *testing.T) {
	f, baseURI := startFake(t)
	f.projects = []string{"PROJ", "OTHER"}
	f.seed("PROJ-1", "a")
	f.seed("OTHER-9", "b")

	store := newMemStore(t)
	res := (Plugin{}).CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, baseURI),
		RawArg:    baseURI,
		BlobStore: store,
	})
	if res.FailCount != 0 {
		t.Fatalf("FailCount = %d, failures = %v", res.FailCount, res.Failures)
	}
	got := sortedPaths(res.Entries)
	want := []string{"OTHER/OTHER-9.json", "PROJ/PROJ-1.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestScanForDiff_MatchesCapture(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-1", "First")
	f.seed("PROJ-2", "Second")

	store := newMemStore(t)
	cap := (Plugin{}).CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, baseURI+"/PROJ"),
		RawArg:    baseURI + "/PROJ",
		BlobStore: store,
	})

	scan, err := (Plugin{}).ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:   context.Background(),
		Dir:       mustParseURL(t, baseURI+"/PROJ"),
		RawDir:    baseURI + "/PROJ",
		BlobStore: store,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	// Same paths and same blob ids: an unchanged project diffs clean.
	capByPath := map[string]string{}
	for _, e := range cap.Entries {
		capByPath[e.Path] = e.BlobId
	}
	if len(scan) != len(cap.Entries) {
		t.Fatalf("scan has %d entries, capture had %d", len(scan), len(cap.Entries))
	}
	for _, e := range scan {
		if capByPath[e.Path] != e.BlobId {
			t.Errorf("blob id drift at %s: capture %s, scan %s",
				e.Path, capByPath[e.Path], e.BlobId)
		}
	}
}

func TestListRoots_ProjectsAndIssues(t *testing.T) {
	f, baseURI := startFake(t)
	f.projects = []string{"PROJ"}
	f.seed("PROJ-1", "First")
	f.seed("PROJ-2", "Second")

	p := Plugin{}

	// Root → projects (containers).
	projNodes, err := p.ListRoots(context.Background(), mustParseURL(t, baseURI))
	if err != nil {
		t.Fatalf("ListRoots(root): %v", err)
	}
	if len(projNodes) != 1 || projNodes[0].Type != typeProject {
		t.Fatalf("project nodes = %+v", projNodes)
	}

	// Project → issues (leaves).
	issueNodes, err := p.ListRoots(context.Background(), projNodes[0].URI)
	if err != nil {
		t.Fatalf("ListRoots(project): %v", err)
	}
	if len(issueNodes) != 2 {
		t.Fatalf("want 2 issue nodes, got %d", len(issueNodes))
	}
	for _, n := range issueNodes {
		if n.Type != typeIssue {
			t.Errorf("node %s type = %q, want %q", n.Name, n.Type, typeIssue)
		}
	}

	// Issue → leaf, no children.
	leaf, err := p.ListRoots(context.Background(), issueNodes[0].URI)
	if err != nil {
		t.Fatalf("ListRoots(issue): %v", err)
	}
	if len(leaf) != 0 {
		t.Errorf("issue leaf has %d children, want 0", len(leaf))
	}
}
