package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amarbel-llc/madder/go/pkgs/env_dir"
	"github.com/amarbel-llc/madder/go/pkgs/madder_env"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// setupCgEnvDir builds a cutting-garden-scoped env_dir rooted at a
// per-test tempdir by overriding XDG_STATE_HOME. The returned env_dir's
// GetXDG().State.MakePath(captureLogFileName) resolves under the
// tempdir.
func setupCgEnvDir(t *testing.T) env_dir.Env {
	t.Helper()
	state := filepath.Join(t.TempDir(), "xdg-state")
	t.Setenv("XDG_STATE_HOME", state)
	ctx := errors.MakeContextDefault()
	return env_dir.MakeDefault(
		ctx,
		env_dir.Config{EnvVarNames: madder_env.DefaultEnvVarNames},
		"cutting-garden",
	)
}

// discardNotice swallows appendCaptureLog's best-effort error
// reporting; these tests don't need to inspect it.
func discardNotice(string, ...any) {}

func TestAppendCaptureLog_EmptyEntriesIsNoop(t *testing.T) {
	cg := setupCgEnvDir(t)
	appendCaptureLog(cg, discardNotice, nil)

	path := cg.GetXDG().State.MakePath(captureLogFileName).String()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected captures.log to not exist for empty entries, got err=%v", err)
	}
}

func TestAppendCaptureLog_WritesNDJSON(t *testing.T) {
	cg := setupCgEnvDir(t)

	entries := []captureLogEntry{
		{
			Ts:        "2026-05-13T12:00:00Z",
			ReceiptID: "sha256-abc",
			StoreID:   "",
			Roots:     []string{"dir-a"},
		},
		{
			Ts:        "2026-05-13T12:00:01Z",
			ReceiptID: "sha256-def",
			StoreID:   ".test",
			Roots:     []string{"dir-b", "dir-c"},
		},
	}
	appendCaptureLog(cg, discardNotice, entries)

	path := cg.GetXDG().State.MakePath(captureLogFileName).String()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read captures.log: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2; raw=%q", len(lines), raw)
	}

	var got captureLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("decode line 0: %v", err)
	}
	if got.Ts != "2026-05-13T12:00:00Z" || got.ReceiptID != "sha256-abc" ||
		got.StoreID != "" ||
		len(got.Roots) != 1 || got.Roots[0] != "dir-a" {
		t.Errorf("entry 0 = %+v", got)
	}

	if err := json.Unmarshal([]byte(lines[1]), &got); err != nil {
		t.Fatalf("decode line 1: %v", err)
	}
	if got.ReceiptID != "sha256-def" || got.StoreID != ".test" ||
		len(got.Roots) != 2 || got.Roots[0] != "dir-b" || got.Roots[1] != "dir-c" {
		t.Errorf("entry 1 = %+v", got)
	}
}

func TestAppendCaptureLog_AppendsAcrossCalls(t *testing.T) {
	cg := setupCgEnvDir(t)

	appendCaptureLog(cg, discardNotice, []captureLogEntry{
		{Ts: "t1", ReceiptID: "a", Roots: []string{"."}},
	})
	appendCaptureLog(cg, discardNotice, []captureLogEntry{
		{Ts: "t2", ReceiptID: "b", Roots: []string{"."}},
	})

	path := cg.GetXDG().State.MakePath(captureLogFileName).String()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
}

func TestCaptureLogTimestamp_ParsesAsRFC3339UTC(t *testing.T) {
	ts := captureLogTimestamp()
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("captureLogTimestamp %q is not RFC3339: %v", ts, err)
	}
	if loc := parsed.Location(); loc != time.UTC {
		t.Errorf("captureLogTimestamp %q parsed location = %v, want UTC", ts, loc)
	}
}

func TestRootPaths(t *testing.T) {
	in := []captureRoot{
		{path: "dir-a"},
		{path: "dir-b/"},
		{path: "."},
	}
	out := rootPaths(in)
	if len(out) != 3 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0] != "dir-a" || out[1] != "dir-b/" || out[2] != "." {
		t.Errorf("got %v", out)
	}
}

func TestQuoteEmpty(t *testing.T) {
	if got := quoteEmpty(""); got != "(default)" {
		t.Errorf("quoteEmpty(\"\") = %q, want %q", got, "(default)")
	}
	if got := quoteEmpty(".foo"); got != ".foo" {
		t.Errorf("quoteEmpty(\".foo\") = %q, want %q", got, ".foo")
	}
}
