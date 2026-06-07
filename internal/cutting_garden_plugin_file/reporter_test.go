package cutting_garden_plugin_file

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// recordingStream captures Stream events for assertions, mirroring the
// ytdlp plugin's recordingReporter. ops records the interleaving
// (phase_start / entry / failure / phase_end) so nesting can be
// asserted: every entry/failure must land between PhaseStart and
// PhaseEnd on the unified wire, or the renderers orphan it.
type recordingStream struct {
	capture_events.Nop
	ops         []string
	phaseStarts []string
	phaseEnds   []capture_events.Verdict
	entries     []capture_receipt.EntryV1
	failures    []streamFailure
}

type streamFailure struct {
	source string
	err    error
}

func (r *recordingStream) PhaseStart(description string) {
	r.ops = append(r.ops, "phase_start")
	r.phaseStarts = append(r.phaseStarts, description)
}

func (r *recordingStream) PhaseEnd(v capture_events.Verdict) {
	r.ops = append(r.ops, "phase_end")
	r.phaseEnds = append(r.phaseEnds, v)
}

func (r *recordingStream) Entry(e capture_receipt.EntryV1) {
	r.ops = append(r.ops, "entry")
	r.entries = append(r.entries, e)
}

func (r *recordingStream) Failure(source string, err error) {
	r.ops = append(r.ops, "failure")
	r.failures = append(r.failures, streamFailure{source: source, err: err})
}

var _ cutting_garden_plugins.Reporter = (*recordingStream)(nil)

func newDiscardStore() blob_stores.BlobStoreInitialized {
	return blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)
}

// writeWalkFixture builds a small tree: two files and a subdir with one
// file. 5 entries total (., a.txt, b.txt, sub, sub/c.txt).
func writeWalkFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"a.txt":     "alpha\n",
		"b.txt":     "beta\n",
		"sub/c.txt": "gamma\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCaptureRoot_EmitsWalkPhase pins the Stage B wire contract: the
// whole walk is wrapped in one phase labeled after the CLI arg, every
// entry nests inside it (PhaseStart strictly first, PhaseEnd strictly
// last), and the OK verdict carries the entry count.
func TestCaptureRoot_EmitsWalkPhase(t *testing.T) {
	dir := t.TempDir()
	writeWalkFixture(t, dir)

	rep := &recordingStream{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    &url.URL{Path: dir},
		RawArg:    "fixture-arg",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})
	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, rep.failures)
	}
	if len(result.Entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(result.Entries))
	}

	if len(rep.phaseStarts) != 1 || rep.phaseStarts[0] != "walk fixture-arg" {
		t.Fatalf("phaseStarts = %v, want exactly [walk fixture-arg]", rep.phaseStarts)
	}
	if len(rep.phaseEnds) != 1 {
		t.Fatalf("phaseEnds = %d, want 1: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	v := rep.phaseEnds[0]
	if !v.OK {
		t.Errorf("phaseEnds[0].OK = false, want true: %+v", v)
	}
	if got := v.Diagnostic["entries"]; got != 5 {
		t.Errorf("Diagnostic[%q] = %v, want 5", "entries", got)
	}

	// Nesting: phase_start first, phase_end last, every entry between.
	if len(rep.ops) < 3 {
		t.Fatalf("ops = %v, want phase_start + entries + phase_end", rep.ops)
	}
	if rep.ops[0] != "phase_start" {
		t.Errorf("ops[0] = %q, want phase_start (entries orphan otherwise)", rep.ops[0])
	}
	if last := rep.ops[len(rep.ops)-1]; last != "phase_end" {
		t.Errorf("ops[last] = %q, want phase_end", last)
	}
	for i, op := range rep.ops[1 : len(rep.ops)-1] {
		if op != "entry" {
			t.Errorf("ops[%d] = %q, want entry", i+1, op)
		}
	}
}

// TestCaptureRoot_WalkPhaseFailureVerdict pins the failing-walk wire:
// an unreadable file keeps the walk going but flips the phase verdict
// to not-OK with {entries, failed} counts, and the per-file Failure
// nests inside the phase.
func TestCaptureRoot_WalkPhaseFailureVerdict(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: chmod 000 files stay readable")
	}
	dir := t.TempDir()
	writeWalkFixture(t, dir)
	if err := os.Chmod(filepath.Join(dir, "b.txt"), 0o000); err != nil {
		t.Fatal(err)
	}

	rep := &recordingStream{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    &url.URL{Path: dir},
		RawArg:    "fixture-arg",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})
	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1; failures: %v", result.FailCount, rep.failures)
	}

	if len(rep.phaseEnds) != 1 {
		t.Fatalf("phaseEnds = %d, want 1: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	v := rep.phaseEnds[0]
	if v.OK {
		t.Errorf("phaseEnds[0].OK = true, want false with a failed entry")
	}
	if got := v.Diagnostic["entries"]; got != 4 {
		t.Errorf("Diagnostic[%q] = %v, want 4 (successes only)", "entries", got)
	}
	if got := v.Diagnostic["failed"]; got != 1 {
		t.Errorf("Diagnostic[%q] = %v, want 1", "failed", got)
	}

	// The failure nests inside the phase.
	if rep.ops[0] != "phase_start" || rep.ops[len(rep.ops)-1] != "phase_end" {
		t.Errorf("ops = %v, want failure bracketed by the phase", rep.ops)
	}
	if len(rep.failures) != 1 {
		t.Errorf("failures = %v, want exactly one", rep.failures)
	}
}
