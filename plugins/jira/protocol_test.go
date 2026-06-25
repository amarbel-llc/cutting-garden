package jira

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// seedFull seeds an issue with an `updated` timestamp plus a description and
// one comment, so the protocol capture has real sub-objects to decompose
// into description / comment leaf nodes. The fakeJira handlers return the
// whole body regardless of the requested field selector, so the bodiless
// `updated` probe still finds fields.updated here.
func (f *fakeJira) seedFull(key, summary, updated, description, commentBody string) {
	body := map[string]any{
		"key": key,
		"fields": map[string]any{
			"summary":     summary,
			"updated":     updated,
			"description": map[string]any{"type": "doc", "version": 1, "text": description},
			"comment": map[string]any{
				"comments": []any{
					map[string]any{"id": "100", "body": commentBody},
				},
				"total": 1,
			},
		},
	}
	raw, _ := json.Marshal(body)
	f.issues[key] = string(raw)
}

// captureProtocol runs a protocol capture against the fake and returns the
// result, failing the test on error.
func captureProtocol(t *testing.T, store blob_stores.BlobStoreInitialized, baseURI, path, prior string) cutting_garden_plugins.ProtocolCaptureResult {
	t.Helper()
	res, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:            context.Background(),
		Source:             mustParseURL(t, baseURI+path),
		RawArg:             baseURI + path,
		BlobStore:          store,
		PriorReceiptDigest: prior,
		BinaryVersion:      "test",
	})
	if err != nil {
		t.Fatalf("CaptureProtocol(%s): %v", path, err)
	}
	return res
}

// childByType returns the first child ref of node whose type matches, and
// whether one was found.
func childByType(node capture_plugin.Node, typeString string) (capture_plugin.Ref, bool) {
	for _, r := range node.Refs {
		if r.TypeString == typeString {
			return r, true
		}
	}
	return capture_plugin.Ref{}, false
}

// childAlias returns the first child ref of node with the given alias.
func childAlias(node capture_plugin.Node, alias string) (capture_plugin.Ref, bool) {
	r, ok := node.RefByAlias(alias)
	return r, ok
}

// TestCaptureProtocol_TreeShape walks the emitted merkle tree end to end:
// receipt → payload(site) → projects → project → issues → issue →
// {fields, description, comment}, asserting each node's type. This is the
// "emits an RFC 0002 merkle receipt for one project / the node types are
// declared" half of the FDR 0019 promotion bar, in unit form.
func TestCaptureProtocol_TreeShape(t *testing.T) {
	f, baseURI := startFake(t)
	f.seedFull("PROJ-1", "First", "2026-06-01T00:00:00.000+0000", "desc one", "comment one")

	store := newMemStore(t)
	res := captureProtocol(t, store, baseURI, "/PROJ", "")
	if res.ReceiptDigest == "" {
		t.Fatal("empty receipt digest")
	}

	receipt, err := capture_plugin.ReadNode(store, res.ReceiptDigest)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != captureKind {
		t.Fatalf("receipt type = %q, want jira kind", receipt.Type)
	}

	// receipt → payload (site)
	payloadRef, ok := childAlias(receipt, "payload")
	if !ok || payloadRef.TypeString != typeSite {
		t.Fatalf("payload ref = %+v, want %s", payloadRef, typeSite)
	}
	site := readNode(t, store, payloadRef.Digest)

	// site → projects
	projectsRef, ok := childByType(site, typeProjects)
	if !ok {
		t.Fatalf("site has no projects child: %+v", site.Refs)
	}
	projects := readNode(t, store, projectsRef.Digest)

	// projects → project (aliased by key)
	projectRef, ok := childAlias(projects, "PROJ")
	if !ok || projectRef.TypeString != typeProjectNode {
		t.Fatalf("project ref = %+v, want %s aliased PROJ", projectRef, typeProjectNode)
	}
	project := readNode(t, store, projectRef.Digest)

	// project → issues
	issuesRef, ok := childByType(project, typeIssues)
	if !ok {
		t.Fatalf("project has no issues child: %+v", project.Refs)
	}
	issues := readNode(t, store, issuesRef.Digest)

	// issues → issue (aliased by key)
	issueRef, ok := childAlias(issues, "PROJ-1")
	if !ok || issueRef.TypeString != typeIssueNode {
		t.Fatalf("issue ref = %+v, want %s aliased PROJ-1", issueRef, typeIssueNode)
	}
	issue := readNode(t, store, issueRef.Digest)

	// issue → fields, description, comment/100
	if r, ok := childAlias(issue, "fields"); !ok || r.TypeString != typeIssueFields {
		t.Errorf("issue fields child = %+v, want %s", r, typeIssueFields)
	}
	if r, ok := childAlias(issue, "description"); !ok || r.TypeString != typeDescription {
		t.Errorf("issue description child = %+v, want %s", r, typeDescription)
	}
	if r, ok := childAlias(issue, "comment/100"); !ok || r.TypeString != typeComment {
		t.Errorf("issue comment child = %+v, want %s", r, typeComment)
	}
}

// TestCaptureProtocol_SubtreeReuse is the severing half of the promotion
// bar: a second capture with one issue's `updated` advanced and one
// unchanged must graft the unchanged issue's issue-node digest verbatim
// (zero re-fetch) and rebuild only the changed one.
func TestCaptureProtocol_SubtreeReuse(t *testing.T) {
	f, baseURI := startFake(t)
	f.seedFull("PROJ-1", "First", "2026-06-01T00:00:00.000+0000", "d1", "c1")
	f.seedFull("PROJ-2", "Second", "2026-06-01T00:00:00.000+0000", "d2", "c2")

	store := newMemStore(t)
	first := captureProtocol(t, store, baseURI, "/PROJ", "")
	firstDigests := issueNodeDigests(t, store, first.ReceiptDigest)

	// Advance PROJ-2's updated; leave PROJ-1 unchanged.
	f.seedFull("PROJ-2", "Second edited", "2026-06-09T12:00:00.000+0000", "d2b", "c2b")

	second := captureProtocol(t, store, baseURI, "/PROJ", first.ReceiptDigest)
	secondDigests := issueNodeDigests(t, store, second.ReceiptDigest)

	if firstDigests["PROJ-1"] == "" || secondDigests["PROJ-1"] == "" {
		t.Fatal("missing PROJ-1 issue-node digest")
	}
	if firstDigests["PROJ-1"] != secondDigests["PROJ-1"] {
		t.Errorf("PROJ-1 unchanged but issue-node digest moved: %s -> %s",
			firstDigests["PROJ-1"], secondDigests["PROJ-1"])
	}
	if firstDigests["PROJ-2"] == secondDigests["PROJ-2"] {
		t.Errorf("PROJ-2 changed but issue-node digest stayed %s", firstDigests["PROJ-2"])
	}
}

// TestDiffProtocol_AMD asserts the bodiless diff reports added / modified /
// deleted by issue key against a prior receipt.
func TestDiffProtocol_AMD(t *testing.T) {
	f, baseURI := startFake(t)
	f.seedFull("PROJ-1", "keep", "2026-06-01T00:00:00.000+0000", "d", "c")
	f.seedFull("PROJ-2", "change me", "2026-06-01T00:00:00.000+0000", "d", "c")
	f.seedFull("PROJ-3", "delete me", "2026-06-01T00:00:00.000+0000", "d", "c")

	store := newMemStore(t)
	captured := captureProtocol(t, store, baseURI, "/PROJ", "")

	// Mutate the live fake: PROJ-2 updated advances, PROJ-3 removed,
	// PROJ-4 added.
	f.seedFull("PROJ-2", "changed", "2026-06-09T00:00:00.000+0000", "d", "c")
	delete(f.issues, "PROJ-3")
	f.seedFull("PROJ-4", "new", "2026-06-09T00:00:00.000+0000", "d", "c")

	res, err := (Plugin{}).DiffProtocol(cutting_garden_plugins.ProtocolDiffRequest{
		Context:       context.Background(),
		Source:        mustParseURL(t, baseURI+"/PROJ"),
		RawSource:     baseURI + "/PROJ",
		BlobStore:     store,
		ReceiptDigest: captured.ReceiptDigest,
	})
	if err != nil {
		t.Fatalf("DiffProtocol: %v", err)
	}

	got := map[string]bool{}
	for _, d := range res.Differences {
		got[d] = true
	}
	for _, want := range []string{"A PROJ-4", "M PROJ-2", "D PROJ-3"} {
		if !got[want] {
			t.Errorf("missing diff line %q in %v", want, res.Differences)
		}
	}
	if got["M PROJ-1"] || got["A PROJ-1"] || got["D PROJ-1"] {
		t.Errorf("unchanged PROJ-1 should not appear: %v", res.Differences)
	}
}

// TestCaptureProtocol_ObjectCountIsIssues asserts ObjectCount reports the
// number of issues captured (payload objects), not the merkle scaffolding
// node count — matching the caldav/git per-receipt "objects" semantics.
func TestCaptureProtocol_ObjectCountIsIssues(t *testing.T) {
	f, baseURI := startFake(t)
	f.seedFull("PROJ-1", "a", "2026-06-01T00:00:00.000+0000", "d", "c")
	f.seedFull("PROJ-2", "b", "2026-06-01T00:00:00.000+0000", "d", "c")

	store := newMemStore(t)
	res := captureProtocol(t, store, baseURI, "/PROJ", "")
	if res.ObjectCount != 2 {
		t.Errorf("ObjectCount = %d, want 2 (issues captured, not node count)", res.ObjectCount)
	}
}

// TestDecompose_CommentWithoutIDStableAcrossDeletion guards the code-review
// fix: a comment with no Jira id is keyed by a content hash, not its array
// position, so deleting an earlier comment must NOT shift the surviving
// comment's alias (a positional index would, spuriously rehashing it).
func TestDecompose_CommentWithoutIDStableAcrossDeletion(t *testing.T) {
	// rawIssue builds an issue whose fields.comment.comments is the given
	// list of raw comment JSON objects.
	rawIssue := func(key string, comments ...string) issue {
		body := `{"key":"` + key + `","fields":{"comment":{"comments":[` +
			strings.Join(comments, ",") + `]}}}`
		canon, err := canonicalJSON([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return issue{key: key, data: canon}
	}
	idless := `{"body":"survivor"}`
	withID := `{"id":"100","body":"doomed"}`

	before, err := decomposeIssue(rawIssue("PROJ-1", withID, idless))
	if err != nil {
		t.Fatal(err)
	}
	after, err := decomposeIssue(rawIssue("PROJ-1", idless))
	if err != nil {
		t.Fatal(err)
	}

	aliasOf := func(d decomposedIssue, bodySubstr string) string {
		for _, c := range d.comments {
			if strings.Contains(string(c.body), bodySubstr) {
				return c.id
			}
		}
		t.Fatalf("no comment containing %q", bodySubstr)
		return ""
	}
	if a, b := aliasOf(before, "survivor"), aliasOf(after, "survivor"); a != b {
		t.Errorf("id-less comment alias shifted after a sibling deletion: %s -> %s", a, b)
	}
}

// readNode reads and parses one node, failing the test on error.
func readNode(t *testing.T, store blob_stores.BlobStoreInitialized, digest string) capture_plugin.Node {
	t.Helper()
	n, err := capture_plugin.ReadNode(store, digest)
	if err != nil {
		t.Fatalf("read node %s: %v", digest, err)
	}
	return n
}

// issueNodeDigests walks a receipt to the issues collection and returns a
// map of issue key → issue-node markl digest.
func issueNodeDigests(t *testing.T, store blob_stores.BlobStoreInitialized, receiptDigest string) map[string]string {
	t.Helper()
	receipt := readNode(t, store, receiptDigest)
	payloadRef, _ := receipt.RefByAlias("payload")
	site := readNode(t, store, payloadRef.Digest)
	projectsRef, _ := childByType(site, typeProjects)
	projects := readNode(t, store, projectsRef.Digest)
	out := map[string]string{}
	for _, projRef := range projects.Refs {
		project := readNode(t, store, projRef.Digest)
		issuesRef, _ := childByType(project, typeIssues)
		issues := readNode(t, store, issuesRef.Digest)
		for _, issueRef := range issues.Refs {
			out[issueRef.Alias] = issueRef.Digest
		}
	}
	return out
}
