package cutting_garden_plugin_git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// capturePluginWriter adapts a blob store to the capture_plugin.Writer
// the protocol capture drives.
func capturePluginWriter(store blob_stores.BlobStoreInitialized) capture_plugin.Writer {
	return capture_plugin.NewBlobStoreWriter(store)
}

// TestPlugin_CaptureRoot_RealGit_StoresEveryObject drives the plugin
// against a real `git` binary and a real local repository, asserting
// that every object `git cat-file --batch-all-objects` reports in the
// source is captured as its own entry under the type-qualified path,
// and that ref.txt records the real branch tip. Skipped when git is
// not on PATH.
func TestPlugin_CaptureRoot_RealGit_StoresEveryObject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)

	arg := "git:" + repo + "#main"
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})
	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, sink.failures)
	}

	// Collect the object oids the plugin stored, by type.
	gotByType := map[string]map[string]bool{
		"commit": {}, "tree": {}, "blob": {},
	}
	var refSeen bool
	for _, e := range result.Entries {
		if e.Path == refFileName {
			refSeen = true
			continue
		}
		typ, oid, ok := strings.Cut(e.Path, "/")
		if !ok {
			t.Errorf("entry path %q is not <type>/<oid>", e.Path)
			continue
		}
		if m, ok := gotByType[typ]; ok {
			m[oid] = true
		}
	}
	if !refSeen {
		t.Errorf("no ref.txt entry")
	}

	// Every object the source's odb reports MUST have been captured.
	for _, want := range realRepoObjects(t, repo) {
		if m, ok := gotByType[want.typ]; ok {
			if !m[want.oid] {
				t.Errorf("source object %s %s was not captured", want.typ, want.oid)
			}
		}
	}

	// At minimum a healthy single-commit repo has one commit, one tree,
	// and one blob.
	for _, typ := range []string{"commit", "tree", "blob"} {
		if len(gotByType[typ]) == 0 {
			t.Errorf("no %s objects captured", typ)
		}
	}
}

// TestCaptureProtocol_RealGit_TreeReferencesEveryObject drives the
// RFC 0002 protocol capture against a real local repo and asserts the
// payload node references every object in the source odb, each typed by
// git kind, and that the receipt tree is well-formed.
func TestCaptureProtocol_RealGit_TreeReferencesEveryObject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	w := newMemWriter()

	res, err := captureProtocol(context.Background(), w, repo, "main")
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}

	receipt := string(w.byDigest[res.ReceiptDigest])
	if nodeTypeOf(receipt) != "cutting_garden-capture-receipt-git-v1" {
		t.Fatalf("receipt type = %q", nodeTypeOf(receipt))
	}
	payloadRef := nodeRefs(receipt)["payload"]
	payloadDigest, _, _ := strings.Cut(payloadRef, "|")
	payload := string(w.byDigest[payloadDigest])
	prefs := nodeRefs(payload)

	srcObjs := realRepoObjects(t, repo)
	if res.ObjectCount != len(srcObjs) {
		t.Errorf("ObjectCount = %d, want %d", res.ObjectCount, len(srcObjs))
	}
	for _, o := range srcObjs {
		ref, ok := prefs[o.oid]
		if !ok {
			t.Errorf("payload missing object %s %s", o.typ, o.oid)
			continue
		}
		objDigest, objType, _ := strings.Cut(ref, "|")
		if objType != objectTypeString(o.typ) {
			t.Errorf("object %s ref type = %q, want %q", o.oid, objType, objectTypeString(o.typ))
		}
		if _, ok := w.byDigest[objDigest]; !ok {
			t.Errorf("object %s blob not stored", o.oid)
		}
	}
}

// TestRestoreProtocol_RealGit_RebuildsCheckedOutClone captures a local
// repo into a retaining in-memory store, restores it to a fresh
// directory, and asserts the result is a working clone checked out to
// the preserved branch with the original file contents and history tip.
func TestRestoreProtocol_RealGit_RebuildsCheckedOutClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	srcTip := strings.TrimSpace(git(t, repo, "rev-parse", "refs/heads/main"))

	store := newMemStore(t)
	res, err := captureProtocol(context.Background(), capturePluginWriter(store), repo, "main")
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		RawDest:       dest,
	}); err != nil {
		t.Fatalf("RestoreProtocol: %v", err)
	}

	// Checked out to the preserved branch at the captured tip.
	if got := strings.TrimSpace(git(t, dest, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("HEAD branch = %q, want main", got)
	}
	if got := strings.TrimSpace(git(t, dest, "rev-parse", "HEAD")); got != srcTip {
		t.Errorf("restored tip = %q, want %q", got, srcTip)
	}

	// Working tree materialized with original contents.
	readme, err := os.ReadFile(filepath.Join(dest, "README.md"))
	if err != nil {
		t.Fatalf("read restored README: %v", err)
	}
	if string(readme) != "hello\n" {
		t.Errorf("README.md = %q, want %q", readme, "hello\n")
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "nested.txt")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}

	// A clean checkout (no diff between index/worktree and HEAD).
	if status := strings.TrimSpace(git(t, dest, "status", "--porcelain")); status != "" {
		t.Errorf("restored worktree not clean:\n%s", status)
	}
}

// TestDiffProtocol_RealGit_DetectsTipDrift captures a repo, confirms a
// diff against the unchanged source reports no drift, then adds a commit
// and confirms the diff reports a tip move.
func TestDiffProtocol_RealGit_DetectsTipDrift(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	store := newMemStore(t)
	res, err := captureProtocol(context.Background(), capturePluginWriter(store), repo, "main")
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}

	arg := "git:" + repo + "#main"
	req := cutting_garden_plugins.ProtocolDiffRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		Source:        mustParseURL(t, arg),
		RawSource:     arg,
	}

	clean, err := (Plugin{}).DiffProtocol(req)
	if err != nil {
		t.Fatalf("DiffProtocol (clean): %v", err)
	}
	if len(clean.Differences) != 0 {
		t.Errorf("expected no drift, got %v", clean.Differences)
	}

	// Move the branch tip with a new commit.
	if err := os.WriteFile(filepath.Join(repo, "another.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "second")

	drifted, err := (Plugin{}).DiffProtocol(req)
	if err != nil {
		t.Fatalf("DiffProtocol (drifted): %v", err)
	}

	// Leads with the tip move.
	if len(drifted.Differences) == 0 ||
		!strings.HasPrefix(drifted.Differences[0], "M ") ||
		!strings.Contains(drifted.Differences[0], "tip") {
		t.Fatalf("expected leading M tip line, got %v", drifted.Differences)
	}

	// Object-level: the second commit added exactly a commit, the new
	// root tree, and the another.txt blob; history keeps every captured
	// object reachable, so nothing is deleted.
	var added, deleted int
	for _, d := range drifted.Differences[1:] {
		switch {
		case strings.HasPrefix(d, "A "):
			added++
		case strings.HasPrefix(d, "D "):
			deleted++
		}
	}
	if added != 3 {
		t.Errorf("expected 3 added objects, got %d: %v", added, drifted.Differences)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted objects, got %d: %v", deleted, drifted.Differences)
	}
}

type repoObject struct {
	oid string
	typ string
}

// realRepoObjects lists every object in repo's object database via
// `git cat-file --batch-all-objects --batch-check`.
func realRepoObjects(t *testing.T, repo string) []repoObject {
	t.Helper()
	out := git(t, repo, "cat-file", "--batch-all-objects",
		"--batch-check=%(objectname) %(objecttype)")
	var objs []repoObject
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		objs = append(objs, repoObject{oid: fields[0], typ: fields[1]})
	}
	return objs
}

// newLocalRepo creates a small git repo on `main` with one commit and
// returns its absolute path. It seeds a subdirectory so the capture
// crosses at least one nested tree object.
func newLocalRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested\n"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "initial")

	return dir
}

// git runs a real git command in dir and returns trimmed stdout,
// failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
