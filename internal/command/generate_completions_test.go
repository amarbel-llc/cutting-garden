package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCompletions_BashStub(t *testing.T) {
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	if err := u.GenerateCompletions(dir); err != nil {
		t.Fatalf("GenerateCompletions: %v", err)
	}
	path := filepath.Join(dir, "share", "bash-completion", "completions", "demo")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing bash stub: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"_demo()",
		"demo complete --bash-style",
		"complete -F _demo demo",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bash stub missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestGenerateCompletions_FishStub(t *testing.T) {
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	if err := u.GenerateCompletions(dir); err != nil {
		t.Fatalf("GenerateCompletions: %v", err)
	}
	path := filepath.Join(dir, "share", "fish", "vendor_completions.d", "demo.fish")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing fish stub: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "demo complete --bash-style") {
		t.Errorf("fish stub does not delegate to complete subcommand: %q", got)
	}
}

func TestGenerateCompletions_ZshStub(t *testing.T) {
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	if err := u.GenerateCompletions(dir); err != nil {
		t.Fatalf("GenerateCompletions: %v", err)
	}
	path := filepath.Join(dir, "share", "zsh", "site-functions", "_demo")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing zsh stub: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "demo complete") {
		t.Errorf("zsh stub does not delegate to complete subcommand: %q", got)
	}
}
