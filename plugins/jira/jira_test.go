package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
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
	// patched records the last PUT field payload per issue key (the
	// non-status half of a PatchNode), for write assertions.
	patched map[string]map[string]any
	// transitioned records the applied transition ids per issue key, in
	// order (the status half of a PatchNode).
	transitioned map[string][]string
	// nextNum assigns deterministic keys to created issues (PROJECT-N).
	nextNum int
}

func newFakeJira() *fakeJira {
	return &fakeJira{
		issues:       map[string]string{},
		patched:      map[string]map[string]any{},
		transitioned: map[string][]string{},
	}
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
	// POST /rest/api/3/issue creates an issue: assign a deterministic key
	// under the requested project and echo it back (Jira assigns keys). The
	// exact (trailing-slash-free) path plus the method pattern keeps this
	// distinct from the /issue/{key} subtree below and avoids the
	// redirect-to-/issue/ that would turn the POST into a GET.
	mux.HandleFunc("POST /rest/api/3/issue", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Fields struct {
				Project struct {
					Key string `json:"key"`
				} `json:"project"`
				Summary string `json:"summary"`
			} `json:"fields"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		f.nextNum++
		key := req.Fields.Project.Key + "-" + strconv.Itoa(f.nextNum)
		f.issues[key] = `{"key":"` + key + `","fields":{"summary":"` +
			req.Fields.Summary + `"}}`
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"key": key})
	})
	mux.HandleFunc("/rest/api/3/issue/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")

		// The transitions subresource: GET lists the canned transitions, POST
		// applies one (recorded by id).
		if key, ok := strings.CutSuffix(rest, "/transitions"); ok {
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"transitions": []map[string]any{
						{"id": "21", "name": "Start", "to": map[string]any{"name": "In Progress"}},
						{"id": "31", "name": "Finish", "to": map[string]any{"name": "Done"}},
					},
				})
			case http.MethodPost:
				var req struct {
					Transition struct {
						ID string `json:"id"`
					} `json:"transition"`
				}
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &req)
				f.transitioned[key] = append(f.transitioned[key], req.Transition.ID)
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		key := rest
		switch r.Method {
		case http.MethodGet:
			raw, ok := f.issues[key]
			if !ok {
				http.Error(w, `{"errorMessages":["not found"]}`, http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, raw)
		case http.MethodPut:
			if _, ok := f.issues[key]; !ok {
				http.Error(w, `{"errorMessages":["not found"]}`, http.StatusNotFound)
				return
			}
			var req struct {
				Fields map[string]any `json:"fields"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)
			f.patched[key] = req.Fields
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if _, ok := f.issues[key]; !ok {
				http.Error(w, `{"errorMessages":["not found"]}`, http.StatusNotFound)
				return
			}
			delete(f.issues, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
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
	// Opaque form reaches the plain-HTTP test server.
	return f, "jira:" + startFakeServer(t, f)
}

// startFakeServer starts an httptest server for an already-seeded fakeJira
// (facet_test.go seeds issues directly before starting the server) and
// returns its base URL. startFake is the common case (fresh, empty fake);
// this is the seed-then-serve variant.
func startFakeServer(t *testing.T, f *fakeJira) string {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv.URL
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
