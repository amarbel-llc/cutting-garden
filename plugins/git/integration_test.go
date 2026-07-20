package cutting_garden_plugin_git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
)

// capturePluginWriter adapts a blob store to the capture_plugin.Writer
// the protocol capture drives.
func capturePluginWriter(store blob_stores.BlobStoreInitialized) capture_plugin.Writer {
	return capture_plugin.NewBlobStoreWriter(store)
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

	res, err := captureProtocol(context.Background(), w, repo, "main", cutting_garden_plugins.NopReporter{})
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
	srcTip := strings.TrimSpace(gitCLI(t, repo, "rev-parse", "refs/heads/main"))

	store := newMemStore(t)
	res, err := captureProtocol(context.Background(), capturePluginWriter(store), repo, "main", cutting_garden_plugins.NopReporter{})
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
	if got := strings.TrimSpace(gitCLI(t, dest, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("HEAD branch = %q, want main", got)
	}
	if got := strings.TrimSpace(gitCLI(t, dest, "rev-parse", "HEAD")); got != srcTip {
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
	if status := strings.TrimSpace(gitCLI(t, dest, "status", "--porcelain")); status != "" {
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
	res, err := captureProtocol(context.Background(), capturePluginWriter(store), repo, "main", cutting_garden_plugins.NopReporter{})
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
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "second")

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

// TestIncrementalCapture_RealGit_MatchesFullCapture captures a repo,
// advances its branch, then re-captures incrementally from the prior
// receipt — fetching only the delta — and asserts the result is
// byte-identical (same payload digest) to a full capture of the advanced
// state, and that it restores correctly.
func TestIncrementalCapture_RealGit_MatchesFullCapture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	store := newMemStore(t)

	// Full capture of state 1.
	res1, err := captureProtocol(context.Background(), capturePluginWriter(store), repo, "main", cutting_garden_plugins.NopReporter{})
	if err != nil {
		t.Fatalf("full capture 1: %v", err)
	}

	// Unchanged re-capture reuses the prior object set exactly.
	resSame, ok, err := tryIncrementalCapture(
		context.Background(), store, capturePluginWriter(store), repo, "main", res1.ReceiptDigest, cutting_garden_plugins.NopReporter{},
	)
	if err != nil || !ok {
		t.Fatalf("incremental (unchanged): ok=%v err=%v", ok, err)
	}
	if receiptPayloadDigest(t, store, resSame.ReceiptDigest) != receiptPayloadDigest(t, store, res1.ReceiptDigest) {
		t.Errorf("unchanged re-capture changed the payload digest")
	}

	// Advance the branch.
	if err := os.WriteFile(filepath.Join(repo, "another.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "second")
	newTip := strings.TrimSpace(gitCLI(t, repo, "rev-parse", "refs/heads/main"))

	// Incremental capture from the prior receipt (delta fetch).
	resInc, ok, err := tryIncrementalCapture(
		context.Background(), store, capturePluginWriter(store), repo, "main", res1.ReceiptDigest, cutting_garden_plugins.NopReporter{},
	)
	if err != nil || !ok {
		t.Fatalf("incremental capture: ok=%v err=%v", ok, err)
	}

	// Full capture of the advanced state into a separate store.
	storeFull := newMemStore(t)
	resFull, err := captureProtocol(context.Background(), capturePluginWriter(storeFull), repo, "main", cutting_garden_plugins.NopReporter{})
	if err != nil {
		t.Fatalf("full capture 2: %v", err)
	}

	// The incremental and full captures of the identical state produce the
	// identical payload node (same objects, sorted; same tip/branch/count).
	if got, want := receiptPayloadDigest(t, store, resInc.ReceiptDigest),
		receiptPayloadDigest(t, storeFull, resFull.ReceiptDigest); got != want {
		t.Errorf("incremental payload digest %s != full %s", got, want)
	}
	if got, want := receiptObjectOids(t, store, resInc.ReceiptDigest),
		receiptObjectOids(t, storeFull, resFull.ReceiptDigest); !equalStrings(got, want) {
		t.Errorf("object sets differ:\n inc=%v\nfull=%v", got, want)
	}

	// The incrementally-built receipt restores to a working clone at the
	// advanced tip (all objects — prior + delta — are in the store).
	dest := filepath.Join(t.TempDir(), "restored")
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: resInc.ReceiptDigest,
		RawDest:       dest,
	}); err != nil {
		t.Fatalf("restore incremental receipt: %v", err)
	}
	if got := strings.TrimSpace(gitCLI(t, dest, "rev-parse", "HEAD")); got != newTip {
		t.Errorf("restored tip = %q, want %q", got, newTip)
	}
	if _, err := os.Stat(filepath.Join(dest, "another.txt")); err != nil {
		t.Errorf("restored worktree missing another.txt: %v", err)
	}
}

func receiptPayloadDigest(t *testing.T, store blob_stores.BlobStoreInitialized, receiptDigest string) string {
	t.Helper()
	n, err := capture_plugin.ReadNode(store, receiptDigest)
	if err != nil {
		t.Fatalf("read receipt %s: %v", receiptDigest, err)
	}
	r, ok := n.RefByAlias("payload")
	if !ok {
		t.Fatalf("receipt %s has no payload ref", receiptDigest)
	}
	return r.Digest
}

func receiptObjectOids(t *testing.T, store blob_stores.BlobStoreInitialized, receiptDigest string) []string {
	t.Helper()
	payload, _, err := loadReceiptPayload(store, receiptDigest)
	if err != nil {
		t.Fatalf("load payload of %s: %v", receiptDigest, err)
	}
	oids := make([]string, 0, len(payload.Refs))
	for _, r := range payload.Refs {
		oids = append(oids, r.Alias)
	}
	sort.Strings(oids)
	return oids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type repoObject struct {
	oid string
	typ string
}

// realRepoObjects lists every object in repo's object database via
// `git cat-file --batch-all-objects --batch-check`.
func realRepoObjects(t *testing.T, repo string) []repoObject {
	t.Helper()
	out := gitCLI(t, repo, "cat-file", "--batch-all-objects",
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

	gitCLI(t, dir, "init", "-q", "-b", "main")
	gitCLI(t, dir, "config", "user.email", "test@example.com")
	gitCLI(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested\n"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	gitCLI(t, dir, "add", "-A")
	gitCLI(t, dir, "commit", "-q", "-m", "initial")

	return dir
}

// gitCLI runs a real git command in dir and returns trimmed stdout,
// failing the test on error. Used only by the real-git cross-check tests;
// the plugin itself no longer shells out to git.
func gitCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
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
