package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"
)

// isolateXDG points every XDG base dir at a per-test tempdir and opts
// out of madder's cwd walk-up resolution, so env construction never
// touches the developer's real madder scope. Mirrors
// command_components' test helper of the same name.
func isolateXDG(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	for _, v := range []string{
		"XDG_DATA_HOME",
		"XDG_CONFIG_HOME",
		"XDG_STATE_HOME",
		"XDG_CACHE_HOME",
		"XDG_RUNTIME_DIR",
	} {
		t.Setenv(v, filepath.Join(tmp, v))
	}
	t.Setenv("MADDER_XDG_USER_LOCATION_ONLY", "1")
}

// runCaptureViaUtility drives the capture subcommand end-to-end through
// the command.Utility dispatch (flag parsing included) with stdout
// piped, returning the captured stdout and exit code. The isolated XDG
// scope has NO initialized blob store, so only store-free paths (arg
// classification, plan errors, the renderer's bailout/summary tail) are
// reachable — the success wire is pinned at the pipeline level below
// and end-to-end by the bats lanes.
func runCaptureViaUtility(t *testing.T, args ...string) (string, int) {
	t.Helper()
	isolateXDG(t)
	forceNonTTYStderr(t)

	var code int
	out := captureStdout(t, func() {
		u := command.MakeUtility("cg-test", nil)
		u.AddCmd("capture", New())
		code = u.Run(append([]string{"cg-test", "capture"}, args...))
	})
	return out, code
}

// TestRun_InvalidFormatIsUsageError pins validateFormat's wiring at the
// top of Run: a bad -format value exits EX_USAGE before any pipeline
// or env construction.
func TestRun_InvalidFormatIsUsageError(t *testing.T) {
	out, code := runCaptureViaUtility(t, "-format=yaml")
	if code != 64 {
		t.Errorf("exit code = %d, want 64 (EX_USAGE)", code)
	}
	if out != "" {
		t.Errorf("stdout should be empty before pipeline construction, got %q", out)
	}
}

// TestRun_SmokeTAP drives Run end-to-end on `-format tap` with a piped
// stdout: a file argument classifies as a failure and the plan errors
// out, so the unified TAP renderer must produce the version header, the
// failure Log echoes as comments, the Finalize(planErr) bailout, and
// the trailing plan — parseable TAP-14 with no legacy sink involved.
func TestRun_SmokeTAP(t *testing.T) {
	work := t.TempDir()
	loose := filepath.Join(work, "loose.txt")
	if err := os.WriteFile(loose, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	out, code := runCaptureViaUtility(t, "-format=tap", "loose.txt")

	if code != 64 {
		t.Errorf("exit code = %d, want 64", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[0] != "TAP version 14" {
		t.Fatalf("first line = %q, want TAP version header; out=%q", lines[0], out)
	}
	if !strings.Contains(out, "# failure: loose.txt:") {
		t.Errorf("missing failure comment echo in %q", out)
	}
	if !strings.Contains(out, "Bail out!") {
		t.Errorf("missing bailout from Finalize(planErr) in %q", out)
	}
	if last := lines[len(lines)-1]; last != "1..0" {
		t.Errorf("last line = %q, want trailing plan 1..0", last)
	}
}

// TestRun_SmokeJSON is the tap-ndjson sibling: every stdout line must
// parse as JSON, with the bailout record carrying the plan error and a
// trailing summary marked bailed.
func TestRun_SmokeJSON(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(work, "loose.txt"), []byte("x\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	out, code := runCaptureViaUtility(t, "-format=json", "loose.txt")

	if code != 64 {
		t.Errorf("exit code = %d, want 64", code)
	}

	records := parseNDJSON(t, out)
	if len(records) != 2 {
		t.Fatalf("got %d records, want bailout + summary; out=%q", len(records), out)
	}
	if records[0]["type"] != "bailout" {
		t.Errorf("records[0].type = %v, want bailout", records[0]["type"])
	}
	if msg, _ := records[0]["message"].(string); !strings.Contains(msg, "no usable directories") {
		t.Errorf("bailout message = %v, want the plan error", records[0]["message"])
	}
	summary := records[1]
	if summary["type"] != "summary" || summary["bailed"] != true {
		t.Errorf("records[1] = %v, want a bailed summary", summary)
	}
}

// parseNDJSON decodes each non-empty stdout line as a JSON object.
func parseNDJSON(t *testing.T, out string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not JSON: %v; line=%q", i, err, line)
		}
		records = append(records, rec)
	}
	return records
}

// TestPipeline_SuccessWireTAP pins the success-path receipt wire on the
// unified TAP renderer at the pipeline level (the store-free stand-in
// for a successful capture: the orchestrator's setStore + receipt +
// finish sequence). The receipt phase point carries the
// store/receipt_id/count diagnostic.
func TestPipeline_SuccessWireTAP(t *testing.T) {
	forceNonTTYStderr(t)

	cmd := &Capture{Format: formatTAP, Progress: progressNever}
	out := captureStdout(t, func() {
		p := cmd.setupPipeline("capture .")
		p.setStore("")
		p.receipt("", "blake2b256-abc", 2)
		p.finish(nil)
		p.closeLegacy()
	})

	if !strings.HasPrefix(out, "TAP version 14\n") {
		t.Fatalf("missing TAP header: %q", out)
	}
	if !strings.Contains(out, "ok 1 - receipt store=(default)") {
		t.Errorf("missing receipt phase point in %q", out)
	}
	if !strings.Contains(out, "receipt_id") || !strings.Contains(out, "blake2b256-abc") {
		t.Errorf("receipt diagnostic missing from %q", out)
	}
	if !strings.HasSuffix(out, "1..1\n") {
		t.Errorf("missing trailing plan 1..1 in %q", out)
	}
}

// TestPipeline_SuccessWireJSON is the tap-ndjson sibling: the receipt
// phase becomes a test record whose diagnostic carries the receipt
// machine-readably, followed by a clean summary.
func TestPipeline_SuccessWireJSON(t *testing.T) {
	forceNonTTYStderr(t)

	cmd := &Capture{Format: formatJSON, Progress: progressNever}
	out := captureStdout(t, func() {
		p := cmd.setupPipeline("capture .")
		p.setStore("")
		p.receipt("", "blake2b256-abc", 2)
		p.finish(nil)
		p.closeLegacy()
	})

	records := parseNDJSON(t, out)
	if len(records) != 2 {
		t.Fatalf("got %d records, want test + summary; out=%q", len(records), out)
	}

	test := records[0]
	if test["type"] != "test" || test["ok"] != true {
		t.Fatalf("records[0] = %v, want a passing test record", test)
	}
	diag, _ := test["diagnostic"].(map[string]any)
	if diag["receipt_id"] != "blake2b256-abc" || diag["store"] != "(default)" {
		t.Errorf("receipt diagnostic = %v, want receipt_id/store", diag)
	}
	if diag["count"] != float64(2) {
		t.Errorf("diagnostic count = %v, want 2", diag["count"])
	}

	summary := records[1]
	if summary["type"] != "summary" || summary["bailed"] != false ||
		summary["passed"] != float64(1) {
		t.Errorf("records[1] = %v, want a clean summary with passed=1", summary)
	}
}
