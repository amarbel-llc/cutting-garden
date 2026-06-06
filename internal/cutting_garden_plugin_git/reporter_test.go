package cutting_garden_plugin_git

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// recordingReporter captures every Plan/Progress/Log call the plugin
// makes so a test can assert on the emitted observability without
// inspecting any blob bytes.
type recordingReporter struct {
	mu       sync.Mutex
	plans    []cutting_garden_plugins.ReportPlan
	progress []cutting_garden_plugins.ReportProgress
	logs     []string
}

func (r *recordingReporter) Plan(p cutting_garden_plugins.ReportPlan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans = append(r.plans, p)
}

func (r *recordingReporter) Progress(p cutting_garden_plugins.ReportProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress = append(r.progress, p)
}

func (r *recordingReporter) Log(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, format)
}

// TestCaptureProtocol_Reporter_EmitsStructuralPlanProgressLog drives a
// full capture of a small local repo through CaptureProtocol with a
// recording reporter and asserts the non-identity observability contract.
// Plan/Progress are framed over the STRUCTURAL skeleton (commit+tree
// objects) only — blobs are still written but not reported individually,
// since blob leaves dominate the object count and are too noisy. So:
// exactly one Plan whose Items equals the repo's commit+tree count, one
// monotonic Progress per structural object (1..N), each Item prefixed
// "commit "/"tree " (never "blob"), and at least the clone + receipt Logs.
func TestCaptureProtocol_Reporter_EmitsStructuralPlanProgressLog(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	store := newMemStore(t)
	rec := &recordingReporter{}

	res, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		BlobStore: store,
		RawArg:    "git:" + repo + "#main",
		Source:    mustParseURL(t, "git:"+repo+"#main"),
		Reporter:  rec,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol: %v", err)
	}

	// Independent oracle: the source repo's commit+tree count via real git.
	wantStructural := countCommitTreeObjects(t, repo)
	if wantStructural == 0 {
		t.Fatalf("test repo has no commit/tree objects")
	}
	// Sanity: structural count is strictly less than the total object count
	// (the repo has blobs), so this test would catch a regression to
	// per-object framing.
	if wantStructural >= res.ObjectCount {
		t.Fatalf("structural count %d not < total object count %d; repo lacks blobs?",
			wantStructural, res.ObjectCount)
	}

	// Exactly one Plan, with Items == the structural (commit+tree) count.
	if len(rec.plans) != 1 {
		t.Fatalf("expected exactly 1 Plan, got %d: %v", len(rec.plans), rec.plans)
	}
	if got, want := rec.plans[0].Items, int64(wantStructural); got != want {
		t.Errorf("Plan.Items = %d, want structural count %d", got, want)
	}
	if rec.plans[0].Label == "" {
		t.Errorf("Plan.Label is empty")
	}

	// Progress fired once per structural object, monotonic 1..N, each
	// labeled "commit "/"tree " (never "blob").
	if got, want := len(rec.progress), wantStructural; got != want {
		t.Fatalf("Progress called %d times, want %d (structural count)", got, want)
	}
	for i, p := range rec.progress {
		if got, want := p.Items, int64(i+1); got != want {
			t.Errorf("Progress[%d].Items = %d, want %d (monotonic 1..N)", i, p.Items, want)
		}
		if !strings.HasPrefix(p.Item, "commit ") && !strings.HasPrefix(p.Item, "tree ") {
			t.Errorf("Progress[%d].Item = %q, want a commit/tree prefix", i, p.Item)
		}
		if strings.HasPrefix(p.Item, "blob ") {
			t.Errorf("Progress[%d].Item = %q reports a blob (should be structural-only)", i, p.Item)
		}
	}

	// At least the clone and receipt phase Logs are present.
	if !logsContainSubstr(rec.logs, "cloning") {
		t.Errorf("expected a clone Log, got %v", rec.logs)
	}
	if !logsContainSubstr(rec.logs, "receipt") {
		t.Errorf("expected a receipt Log, got %v", rec.logs)
	}
}

// countCommitTreeObjects returns the number of commit and tree objects in
// repo's object database, via the real-git oracle — the expected
// structural Plan/Progress count.
func countCommitTreeObjects(t *testing.T, repo string) int {
	t.Helper()
	var n int
	for _, o := range realRepoObjects(t, repo) {
		if o.typ == "commit" || o.typ == "tree" {
			n++
		}
	}
	return n
}

// TestCaptureProtocol_Reporter_DoesNotAffectIdentity is the critical
// non-identity guarantee: capturing the SAME repo twice — once with a
// recording reporter, once with a nil Reporter — yields an IDENTICAL
// captured object graph. Emission is semantics, never identity.
//
// The comparison is on the payload digest, not the receipt digest: the
// receipt's outcome node carries the capture datetime (time.Now via
// capture_plugin.ReceiptParams.Now), so two captures at different wall
// clocks always differ at the receipt root regardless of the reporter.
// The payload node — the object-graph content this plugin emits — is the
// byte-stable identity (sorted by oid in writeGitReceipt); it is exactly
// what TestIncrementalCapture_RealGit_MatchesFullCapture compares.
func TestCaptureProtocol_Reporter_DoesNotAffectIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	arg := "git:" + repo + "#main"

	withStore := newMemStore(t)
	withReporter, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		BlobStore: withStore,
		RawArg:    arg,
		Source:    mustParseURL(t, arg),
		Reporter:  &recordingReporter{},
	})
	if err != nil {
		t.Fatalf("CaptureProtocol (with reporter): %v", err)
	}

	withoutStore := newMemStore(t)
	withoutReporter, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		BlobStore: withoutStore,
		RawArg:    arg,
		Source:    mustParseURL(t, arg),
		Reporter:  nil,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol (nil reporter): %v", err)
	}

	withPayload := receiptPayloadDigest(t, withStore, withReporter.ReceiptDigest)
	withoutPayload := receiptPayloadDigest(t, withoutStore, withoutReporter.ReceiptDigest)
	if withPayload != withoutPayload {
		t.Errorf("payload digest differs with vs without reporter:\n with    = %s\n without = %s",
			withPayload, withoutPayload)
	}
}

// TestProgressLogWriter_SplitsOnCRandLF is a pure unit test for the clone
// progress sideband adapter: go-git's server progress uses BOTH '\r'
// (in-place percent updates) and '\n' (phase end) as separators, so the
// writer must flush a trimmed log line on either, skipping empty segments,
// and keep any trailing partial segment buffered.
func TestProgressLogWriter_SplitsOnCRandLF(t *testing.T) {
	var got []string
	w := &progressLogWriter{log: func(s string) { got = append(got, s) }}

	in := "Counting objects: 5%\rCounting objects: 100%\nReceiving objects: 50%\rDone\n"
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := []string{
		"Counting objects: 5%",
		"Counting objects: 100%",
		"Receiving objects: 50%",
		"Done",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d log lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("log[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestProgressLogWriter_BuffersPartialSegment confirms a trailing segment
// with no terminator stays buffered across Writes (no premature flush)
// and is emitted once its terminator arrives.
func TestProgressLogWriter_BuffersPartialSegment(t *testing.T) {
	var got []string
	w := &progressLogWriter{log: func(s string) { got = append(got, s) }}

	if _, err := w.Write([]byte("Receiving ")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("partial segment flushed prematurely: %q", got)
	}
	if _, err := w.Write([]byte("objects: 100%\n")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if len(got) != 1 || got[0] != "Receiving objects: 100%" {
		t.Fatalf("got %q, want [\"Receiving objects: 100%%\"]", got)
	}
}

func logsContainSubstr(logs []string, substr string) bool {
	for _, l := range logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
