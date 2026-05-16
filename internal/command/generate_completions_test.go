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

func TestGenerateCompletions_AliasesGetTheirOwnStubs(t *testing.T) {
	// Aliases must produce per-shell stubs with their own binary
	// name baked in — bash's `complete -F _<name> <name>` must
	// reference the alias, otherwise `<alias> <TAB>` won't trigger.
	// The stubs CANNOT be symlinks: the contents differ per name.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddAlias("dm")

	if err := u.GenerateCompletions(dir); err != nil {
		t.Fatalf("GenerateCompletions: %v", err)
	}

	// Canonical stubs exist and reference the canonical binary name.
	canonicalBash, err := os.ReadFile(filepath.Join(
		dir, "share", "bash-completion", "completions", "demo"))
	if err != nil {
		t.Fatalf("canonical bash stub missing: %v", err)
	}
	if !strings.Contains(string(canonicalBash), "complete -F _demo demo") {
		t.Errorf("canonical bash stub doesn't reference demo: %s", canonicalBash)
	}

	// Alias stubs exist for all three shells and reference the alias
	// name, NOT the canonical name.
	aliasBash, err := os.ReadFile(filepath.Join(
		dir, "share", "bash-completion", "completions", "dm"))
	if err != nil {
		t.Fatalf("alias bash stub missing: %v", err)
	}
	got := string(aliasBash)
	if !strings.Contains(got, "complete -F _dm dm") {
		t.Errorf("alias bash stub doesn't register `dm` completion: %s", got)
	}
	if !strings.Contains(got, "dm complete --bash-style") {
		t.Errorf("alias bash stub doesn't call `dm` binary: %s", got)
	}
	if strings.Contains(got, "demo complete") {
		t.Errorf("alias bash stub leaks canonical name `demo`: %s", got)
	}

	aliasFish, err := os.ReadFile(filepath.Join(
		dir, "share", "fish", "vendor_completions.d", "dm.fish"))
	if err != nil {
		t.Fatalf("alias fish stub missing: %v", err)
	}
	if !strings.Contains(string(aliasFish), "dm complete --bash-style") {
		t.Errorf("alias fish stub doesn't call `dm`: %s", aliasFish)
	}

	aliasZsh, err := os.ReadFile(filepath.Join(
		dir, "share", "zsh", "site-functions", "_dm"))
	if err != nil {
		t.Fatalf("alias zsh stub missing: %v", err)
	}
	if !strings.Contains(string(aliasZsh), "#compdef dm") {
		t.Errorf("alias zsh stub doesn't declare `dm` compdef: %s", aliasZsh)
	}
}
