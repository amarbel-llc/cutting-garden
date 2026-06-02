package cutting_garden_plugin_git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// buildRepo creates a real on-disk git repository using the go-git library
// (no `git` binary), applying each commit spec in order. A commit spec maps
// worktree-relative paths to their contents; every spec is staged and
// committed with a fixed author so oids are deterministic across runs. It
// returns the repo directory, the resolved default branch, and the tip oid
// of each commit in order — the building block for capture/diff/restore
// fixtures.
func buildRepo(t *testing.T, commits ...map[string]string) (dir, branch string, tips []string) {
	t.Helper()
	dir = t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	when := time.Unix(1_600_000_000, 0).UTC()
	for i, files := range commits {
		for name, content := range files {
			full := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir for %s: %v", name, err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			if _, err := wt.Add(name); err != nil {
				t.Fatalf("add %s: %v", name, err)
			}
		}
		h, err := wt.Commit("commit", &git.CommitOptions{
			Author: &object.Signature{Name: "fixture", Email: "fixture@example.com", When: when},
		})
		if err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
		tips = append(tips, h.String())
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	return dir, head.Name().Short(), tips
}

// appendCommit adds one commit to an existing repo (opening it fresh),
// advancing the checked-out branch, and returns the new tip oid. Used to
// simulate upstream drift between a capture and a later diff/recapture.
func appendCommit(t *testing.T, dir string, files map[string]string) (tip string) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	h, err := wt.Commit("commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "fixture",
			Email: "fixture@example.com",
			When:  time.Unix(1_600_000_100, 0).UTC(),
		},
	})
	if err != nil {
		t.Fatalf("append commit: %v", err)
	}
	return h.String()
}
