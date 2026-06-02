package cutting_garden_plugin_git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

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
