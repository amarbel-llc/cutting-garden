package gitwire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestFetchDelta_LocalTransport_FetchesOnlyDelta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "a@b.c")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "one")
	old := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(repo, "two.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "two")
	newTip := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))

	scratch := t.TempDir()
	git(t, scratch, "init", "-q")

	if err := FetchDelta(context.Background(), repo, newTip, []string{old}, scratch); err != nil {
		t.Fatalf("FetchDelta: %v", err)
	}

	// Exactly the delta crossed: the new commit, the new root tree, and
	// the two.txt blob — three objects, and nothing reachable from `old`.
	out := git(t, scratch, "cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype)")
	types := map[string]int{}
	got := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		got[f[0]] = true
		types[f[1]]++
	}

	if !got[newTip] {
		t.Errorf("scratch missing the new commit %s", newTip)
	}
	if got[old] {
		t.Errorf("scratch should not contain the have %s", old)
	}
	if types["commit"] != 1 || types["tree"] != 1 || types["blob"] != 1 {
		t.Errorf("unexpected delta object set: %v (objects: %v)", types, got)
	}
}

func TestFetchDelta_UnsupportedTransport(t *testing.T) {
	err := FetchDelta(context.Background(), "git@github.com:owner/repo", "x", nil, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Errorf("scp-like remote: got %v, want unsupported transport", err)
	}
}
