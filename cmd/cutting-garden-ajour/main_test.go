package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stencilWide = `[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree] [-> file:/nix/store !fs-path-v1 subtree]]]]`

const stencilNarrow = `[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]`

const callNixStore = `{"Path": [{"ID": "moxy"}, {"ID": "folio"}, {"ID": "read"}],
 "Values": [[{"Type": "fs-path-v1", "ID": "file:/nix/store/x"}]], "Anchor": 1}`

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRun_FlagOrderIsStackOrder pins that --stencil and --patch layers stack
// in command-line order, bottom first, whichever flag names them: the layer
// index a refusal names is the flag's position among all layer flags.
func TestRun_FlagOrderIsStackOrder(t *testing.T) {
	dir := t.TempDir()
	wide := writeFile(t, dir, "wide.espalier", stencilWide)
	narrow := writeFile(t, dir, "narrow.espalier", stencilNarrow)
	call := writeFile(t, dir, "call.json", callNixStore)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--stencil", wide, "--stencil", narrow, "--call", call}, "layer 1 (stencil): no allowance"},
		{[]string{"--stencil", narrow, "--stencil", wide, "--call", call}, "layer 0 (stencil): no allowance"},
		{[]string{"--stencil", wide, "--patch", writeFile(t, dir, "p.espalier", `[moxy [-> folio [-> read [-> file:/nix !fs-path-v1 subtree deny]]]]`), "--stencil", narrow, "--call", call}, "layer 1 (patch): deny"},
	} {
		var stdout, stderr strings.Builder
		code := run(tc.args, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Fatalf("run(%v) = %d, want 1 (refused); stderr %q", tc.args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), tc.want) {
			t.Fatalf("run(%v) stdout = %q, want a line %q", tc.args, stdout.String(), tc.want)
		}
	}
}

// TestRun_BadInputIsUsage pins exit 64 (EX_USAGE) for every parse or shape
// error, and that the error goes to stderr.
func TestRun_BadInputIsUsage(t *testing.T) {
	dir := t.TempDir()
	stencil := writeFile(t, dir, "s.espalier", stencilNarrow)
	call := writeFile(t, dir, "call.json", callNixStore)
	for _, args := range [][]string{
		{"--stencil", stencil},
		{"--call", call, "extra"},
		{"--stencil", writeFile(t, dir, "bad.espalier", `[x [a, b]]`), "--call", call},
		{"--stencil", writeFile(t, dir, "head.espalier", "---\n- role = x\n---\n[x]"), "--call", call},
		{"--stencil", stencil, "--call", writeFile(t, dir, "bad.json", `{"Nope": 1}`)},
		{"--stencil", filepath.Join(dir, "missing"), "--call", call},
		{"--bogus"},
	} {
		var stdout, stderr strings.Builder
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 64 {
			t.Fatalf("run(%v) = %d, want 64; stdout %q stderr %q", args, code, stdout.String(), stderr.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("run(%v) printed no error", args)
		}
	}
}
