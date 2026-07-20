package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_failures"
	"code.linenisgreat.com/cutting-garden/internal/capture_log"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/madder/go/pkgs/blob_store_configs"
	"code.linenisgreat.com/madder/go/pkgs/directory_layout"
	"code.linenisgreat.com/madder/go/pkgs/ids"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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
	return driveCapture(t, args...)
}

// driveCapture is runCaptureViaUtility without the XDG isolation, for
// callers that pre-seed the isolated scope themselves (e.g.
// initDefaultBlobStore for end-to-end store-backed runs).
func driveCapture(t *testing.T, args ...string) (string, int) {
	t.Helper()
	forceNonTTYStderr(t)

	var code int
	out := captureStdout(t, func() {
		u := command.MakeUtility("cg-test", nil)
		u.AddCmd("capture", New())
		code = u.Run(append([]string{"cg-test", "capture"}, args...))
	})
	return out, code
}

// initDefaultBlobStore writes a minimal local hash-bucketed store
// config into the isolated madder XDG scope so end-to-end Run tests
// have a real default store to capture into — the Go-test analogue of
// the bats lanes' `madder init -encryption none default`. Call after
// isolateXDG.
func initDefaultBlobStore(t *testing.T) {
	t.Helper()
	storeDir := filepath.Join(
		os.Getenv("XDG_DATA_HOME"), "madder", "blob_stores", "default",
	)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	file, err := os.Create(
		filepath.Join(storeDir, directory_layout.FileNameBlobStoreConfig),
	)
	if err != nil {
		t.Fatal(err)
	}

	typed := &blob_store_configs.TypedConfig{
		Type: ids.GetOrPanic(ids.TypeTomlBlobStoreConfigV3).TypeStruct,
		Blob: &blob_store_configs.TomlV3{
			HashTypeId:      "sha256",
			HashBuckets:     []int{2},
			CompressionType: "none",
		},
	}
	if _, err := blob_store_configs.EncodeWithDigest(typed, file); err != nil {
		t.Fatalf("encode blob_store-config: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
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
// per-arg failure as a failing phase point (plus its comment echo), the
// Finalize(planErr) bailout, and the trailing plan — parseable TAP-14
// with no legacy sink involved.
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
	if !strings.Contains(out, "not ok 1 - loose.txt") {
		t.Errorf("missing per-arg failing phase point in %q", out)
	}
	if !strings.Contains(out, "Bail out!") {
		t.Errorf("missing bailout from Finalize(planErr) in %q", out)
	}
	if last := lines[len(lines)-1]; last != "1..1" {
		t.Errorf("last line = %q, want trailing plan 1..1", last)
	}
}

// TestRun_SmokeJSON is the tap-ndjson sibling: every stdout line must
// parse as JSON. The per-arg classify failure surfaces as a failing
// test record whose diagnostic carries the error machine-readably
// (ndjson drops Log), then the bailout record with the plan error and
// a trailing summary marked bailed.
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
	if len(records) != 3 {
		t.Fatalf("got %d records, want failing test + bailout + summary; out=%q", len(records), out)
	}

	failing := records[0]
	if failing["type"] != "test" || failing["ok"] != false ||
		failing["description"] != "loose.txt" {
		t.Errorf("records[0] = %v, want a failing test record for loose.txt", failing)
	}
	diag, _ := failing["diagnostic"].(map[string]any)
	if errText, _ := diag["error"].(string); errText == "" {
		t.Errorf("records[0].diagnostic = %v, want a non-empty error", diag)
	}

	if records[1]["type"] != "bailout" {
		t.Errorf("records[1].type = %v, want bailout", records[1]["type"])
	}
	if msg, _ := records[1]["message"].(string); !strings.Contains(msg, "no usable directories") {
		t.Errorf("bailout message = %v, want the plan error", records[1]["message"])
	}
	summary := records[2]
	if summary["type"] != "summary" || summary["bailed"] != true ||
		summary["failed"] != float64(1) {
		t.Errorf("records[2] = %v, want a bailed summary with failed=1", summary)
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

// TestPipeline_FailurePhaseWireJSON pins the per-arg failure wire on
// the unified json renderer: failurePhase brackets the legacy/log
// emission in a failing phase whose diagnostic carries the error
// machine-readably (ndjson drops Log), and the summary counts it.
func TestPipeline_FailurePhaseWireJSON(t *testing.T) {
	forceNonTTYStderr(t)

	cmd := &Capture{Format: formatJSON, Progress: progressNever}
	out := captureStdout(t, func() {
		p := cmd.setupPipeline("capture loose.txt")
		p.failurePhase("loose.txt", fmt.Errorf("not a directory"))
		p.finish(nil)
		p.closeLegacy()
	})

	records := parseNDJSON(t, out)
	if len(records) != 2 {
		t.Fatalf("got %d records, want failing test + summary; out=%q", len(records), out)
	}

	test := records[0]
	if test["type"] != "test" || test["ok"] != false ||
		test["description"] != "loose.txt" {
		t.Fatalf("records[0] = %v, want a failing test record for loose.txt", test)
	}
	diag, _ := test["diagnostic"].(map[string]any)
	if diag["error"] != "not a directory" {
		t.Errorf("diagnostic = %v, want error=%q", diag, "not a directory")
	}

	summary := records[1]
	if summary["type"] != "summary" || summary["failed"] != float64(1) {
		t.Errorf("records[1] = %v, want a summary with failed=1", summary)
	}
}

// TestRun_UnreadableFileWritesFailureReceipt drives Run end-to-end
// against a real local blob store: a fixture tree with one unreadable
// file yields the success receipt AND a durable failure receipt — the
// `failures store=` line carries its id, the blob round-trips via
// capture_failures.Read with the failed path, and the exit code stays
// driven by failCount exactly as before (2, runtime trouble).
func TestRun_UnreadableFileWritesFailureReceipt(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: chmod 000 files stay readable")
	}
	isolateXDG(t)
	initDefaultBlobStore(t)

	work := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(work, "a.txt"), []byte("ok\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(work, "b.txt")
	if err := os.WriteFile(unreadable, []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadable, 0o644); err != nil {
			t.Errorf("restore fixture perms: %v", err)
		}
	})
	t.Chdir(work)

	out, code := driveCapture(t, "-format=tap", ".")

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (failed entries); out=%q", code, out)
	}
	if !strings.Contains(out, "receipt store=") {
		t.Errorf("missing success receipt line in %q", out)
	}

	m := regexp.MustCompile(
		`failures store=\S+ id=(\S+) count=1`,
	).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("missing `failures store=... id=... count=1` line in %q", out)
	}

	var failuresID markl.Id
	if err := failuresID.Set(m[1]); err != nil {
		t.Fatalf("failures id %q does not parse: %v", m[1], err)
	}

	ctx := errors.MakeContextDefault()
	env := command_components.MakeBlobStoreEnv(ctx)
	v, err := capture_failures.Read(env.GetDefaultBlobStore(), &failuresID)
	if err != nil {
		t.Fatalf("read failure receipt blob: %v", err)
	}

	if v.Meta.Outcome != capture_failures.OutcomeFailures {
		t.Errorf("Outcome = %q, want %q",
			v.Meta.Outcome, capture_failures.OutcomeFailures)
	}
	if v.Meta.Signal != "" {
		t.Errorf("Signal = %q, want \"\"", v.Meta.Signal)
	}
	if v.Meta.Receipt == "" {
		t.Error("Meta.Receipt is empty, want the paired success-receipt id")
	}
	if v.Meta.Failed != 1 || len(v.Failures) != 1 {
		t.Fatalf("Failed = %d, len(Failures) = %d, want 1/1; %+v",
			v.Meta.Failed, len(v.Failures), v.Failures)
	}
	f := v.Failures[0]
	if f.Op != capture_failures.OpBlobWrite {
		t.Errorf("Failures[0].Op = %q, want %q",
			f.Op, capture_failures.OpBlobWrite)
	}
	if !strings.HasSuffix(f.Path, "b.txt") {
		t.Errorf("Failures[0].Path = %q, want suffix b.txt", f.Path)
	}
	if f.Error == "" {
		t.Error("Failures[0].Error is empty")
	}
	if len(v.Meta.Roots) != 1 || v.Meta.Roots[0] != "." {
		t.Errorf("Roots = %v, want [.]", v.Meta.Roots)
	}

	// captures.log: the group's entry carries outcome +
	// failure_receipt_id pointing at the failure receipt.
	logRaw, err := os.ReadFile(filepath.Join(
		os.Getenv("XDG_STATE_HOME"), "cutting-garden", capture_log.FileName,
	))
	if err != nil {
		t.Fatalf("read captures.log: %v", err)
	}
	var logEntry captureLogEntry
	if err := json.Unmarshal(bytes.TrimSpace(logRaw), &logEntry); err != nil {
		t.Fatalf("parse captures.log %q: %v", logRaw, err)
	}
	if logEntry.Outcome != capture_failures.OutcomeFailures {
		t.Errorf("log Outcome = %q, want %q",
			logEntry.Outcome, capture_failures.OutcomeFailures)
	}
	if logEntry.FailureReceiptID != m[1] {
		t.Errorf("log FailureReceiptID = %q, want %q",
			logEntry.FailureReceiptID, m[1])
	}
}

// TestPipeline_FailurePhaseWireTAP is the TAP sibling: a failing test
// point with the error diagnostic, plus the legacy comment echo.
func TestPipeline_FailurePhaseWireTAP(t *testing.T) {
	forceNonTTYStderr(t)

	cmd := &Capture{Format: formatTAP, Progress: progressNever}
	out := captureStdout(t, func() {
		p := cmd.setupPipeline("capture loose.txt")
		p.failurePhase("loose.txt", fmt.Errorf("not a directory"))
		p.finish(nil)
		p.closeLegacy()
	})

	if !strings.Contains(out, "not ok 1 - loose.txt") {
		t.Errorf("missing failing phase point in %q", out)
	}
	if !strings.Contains(out, "# failure: loose.txt: not a directory") {
		t.Errorf("missing failure comment echo in %q", out)
	}
	if !strings.Contains(out, "not a directory") {
		t.Errorf("missing error diagnostic in %q", out)
	}
	if !strings.HasSuffix(out, "1..1\n") {
		t.Errorf("missing trailing plan 1..1 in %q", out)
	}
}
