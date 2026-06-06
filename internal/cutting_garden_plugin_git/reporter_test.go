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

// TestCaptureProtocol_Reporter_EmitsPlanProgressLog drives a full
// capture of a small local repo through CaptureProtocol with a recording
// reporter and asserts the non-identity observability contract: exactly
// one Plan whose Items equals the object count, one monotonic Progress
// per object (1..N), and at least the clone + receipt phase Logs.
func TestCaptureProtocol_Reporter_EmitsPlanProgressLog(t *testing.T) {
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

	// Exactly one Plan, with Items == the object count.
	if len(rec.plans) != 1 {
		t.Fatalf("expected exactly 1 Plan, got %d: %v", len(rec.plans), rec.plans)
	}
	if got, want := rec.plans[0].Items, int64(res.ObjectCount); got != want {
		t.Errorf("Plan.Items = %d, want object count %d", got, want)
	}
	if rec.plans[0].Label == "" {
		t.Errorf("Plan.Label is empty")
	}

	// Progress fired once per object, monotonic 1..N.
	if got, want := len(rec.progress), res.ObjectCount; got != want {
		t.Fatalf("Progress called %d times, want %d (object count)", got, want)
	}
	for i, p := range rec.progress {
		if got, want := p.Items, int64(i+1); got != want {
			t.Errorf("Progress[%d].Items = %d, want %d (monotonic 1..N)", i, p.Items, want)
		}
		if p.Item == "" {
			t.Errorf("Progress[%d].Item is empty (expected an oid)", i)
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

func logsContainSubstr(logs []string, substr string) bool {
	for _, l := range logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
