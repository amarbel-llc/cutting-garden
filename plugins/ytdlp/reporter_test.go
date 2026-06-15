package cutting_garden_plugin_ytdlp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_events"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_failures"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// recordingReporter captures Stream events for assertions. It embeds
// capture_events.Nop and records every Plan, Progress, PhaseStart,
// PhaseEnd, Entry, and Failure call in order so tests can assert
// per-artifact progress emission, monotonicity, phase verdicts, and
// (post-Stage-B) the per-entry result events.
type recordingReporter struct {
	capture_events.Nop
	plans       []cutting_garden_plugins.ReportPlan
	progress    []cutting_garden_plugins.ReportProgress
	phaseStarts []string
	phaseEnds   []capture_events.Verdict
	entries     []capture_receipt.EntryV1
	failures    []streamFailure
}

type streamFailure struct {
	source string
	err    error
}

func (r *recordingReporter) Entry(e capture_receipt.EntryV1) {
	r.entries = append(r.entries, e)
}

func (r *recordingReporter) Failure(source string, err error) {
	r.failures = append(r.failures, streamFailure{source: source, err: err})
}

func (r *recordingReporter) Plan(p cutting_garden_plugins.ReportPlan) {
	r.plans = append(r.plans, p)
}

func (r *recordingReporter) Progress(p cutting_garden_plugins.ReportProgress) {
	r.progress = append(r.progress, p)
}

func (r *recordingReporter) PhaseStart(description string) {
	r.phaseStarts = append(r.phaseStarts, description)
}

func (r *recordingReporter) PhaseEnd(v capture_events.Verdict) {
	r.phaseEnds = append(r.phaseEnds, v)
}

// writeArtifactFixture populates dir with a handful of fake artifact
// files (deterministic bytes so blob-ids are stable) and returns the
// rel-paths in the lexical order filepath.WalkDir visits them. No
// yt-dlp is needed — walkArtifacts only cares about regular files
// under outDir.
func writeArtifactFixture(t *testing.T, dir string) []string {
	t.Helper()
	files := map[string]string{
		"video.info.json": `{"id":"video"}`,
		"video.jpg":       "thumb-bytes",
		"video.mp4":       "media-bytes",
		"video.en.vtt":    "WEBVTT\n\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture %q: %v", name, err)
		}
	}
	// filepath.WalkDir visits entries in lexical order; mirror that so
	// progress-Item assertions line up with emission order.
	return []string{"video.en.vtt", "video.info.json", "video.jpg", "video.mp4"}
}

func TestWalkArtifacts_EmitsProgressPerArtifact(t *testing.T) {
	dir := t.TempDir()
	wantOrder := writeArtifactFixture(t, dir)

	rep := &recordingReporter{}
	const source = "https://youtu.be/video"
	entries, failures := walkArtifacts(
		context.Background(), newDiscardStore(), dir, source, rep,
	)
	if len(failures) != 0 {
		t.Fatalf("failures = %+v, want none", failures)
	}
	if len(entries) != len(wantOrder) {
		t.Fatalf("entries = %d, want %d: %v", len(entries), len(wantOrder), entryPaths(entries))
	}

	// The fixture files are all far below the 1 MiB progress stride, so
	// each artifact produces exactly one Progress tick: the final flush
	// from WriteFileBlobProgress carrying the file's full size. Larger
	// files would add intermediate ticks ("at least one per artifact").
	if len(rep.progress) != len(wantOrder) {
		t.Fatalf("progress events = %d, want %d (one final tick per sub-stride artifact): %v",
			len(rep.progress), len(wantOrder), rep.progress)
	}

	// Pre-compute per-file sizes so cumulative-Bytes assertions line up
	// with the lexical emission order.
	var phaseTotal int64
	sizes := make([]int64, len(wantOrder))
	for i, rel := range wantOrder {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("stat fixture %q: %v", rel, err)
		}
		sizes[i] = info.Size()
		phaseTotal += info.Size()
	}

	var wantBytes int64
	for i, p := range rep.progress {
		wantBytes += sizes[i]
		if wantItems := int64(i + 1); p.Items != wantItems {
			t.Errorf("progress[%d].Items = %d, want %d", i, p.Items, wantItems)
		}
		if p.Item != wantOrder[i] {
			t.Errorf("progress[%d].Item = %q, want %q", i, p.Item, wantOrder[i])
		}
		if p.Bytes != wantBytes {
			t.Errorf("progress[%d].Bytes = %d, want %d (cumulative)", i, p.Bytes, wantBytes)
		}
		if p.BytesTotal != phaseTotal {
			t.Errorf("progress[%d].BytesTotal = %d, want %d (phase total on every tick)",
				i, p.BytesTotal, phaseTotal)
		}
		if i > 0 && p.Items < rep.progress[i-1].Items {
			t.Errorf("progress[%d].Items = %d < prev %d (not monotonic)",
				i, p.Items, rep.progress[i-1].Items)
		}
	}
	if last := rep.progress[len(rep.progress)-1].Bytes; last != phaseTotal {
		t.Errorf("final Bytes = %d, want %d (sum of artifact sizes)", last, phaseTotal)
	}
}

func TestWalkArtifacts_ByteIdentityAcrossReporters(t *testing.T) {
	dir := t.TempDir()
	writeArtifactFixture(t, dir)
	const source = "https://youtu.be/video"

	// Run once with a recording reporter, once with a nil reporter.
	// Both must produce byte-identical entry sets — the Reporter is
	// observability only and MUST NOT influence blob-ids, paths, or sizes.
	withReporter, failA := walkArtifacts(
		context.Background(), newDiscardStore(), dir, source, &recordingReporter{},
	)
	withNil, failB := walkArtifacts(
		context.Background(), newDiscardStore(), dir, source, nil,
	)
	if len(failA) != 0 || len(failB) != 0 {
		t.Fatalf("failures = %+v, %+v, want none", failA, failB)
	}
	if len(withReporter) != len(withNil) {
		t.Fatalf("entry count differs: reporter=%d nil=%d", len(withReporter), len(withNil))
	}
	for i := range withReporter {
		a, b := withReporter[i], withNil[i]
		if a.BlobId != b.BlobId || a.Path != b.Path || a.Size != b.Size {
			t.Errorf("entry[%d] differs:\n  reporter: {Path:%q Size:%d BlobId:%q}\n  nil:      {Path:%q Size:%d BlobId:%q}",
				i, a.Path, a.Size, a.BlobId, b.Path, b.Size, b.BlobId)
		}
	}
}

func TestCaptureRoot_EmitsDownloadAndWritePhases(t *testing.T) {
	withFakeYtdlp(t)

	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "ytdlp:https://youtu.be/dQw4w9WgXcQ"),
		RawArg:    "ytdlp:https://youtu.be/dQw4w9WgXcQ",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})
	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0", result.FailCount)
	}

	if len(rep.phaseStarts) != 2 {
		t.Fatalf("phaseStarts = %d, want 2 (download + write): %v",
			len(rep.phaseStarts), rep.phaseStarts)
	}
	if !strings.HasPrefix(rep.phaseStarts[0], "download ") {
		t.Errorf("phaseStarts[0] = %q, want prefix %q", rep.phaseStarts[0], "download ")
	}
	if !strings.Contains(rep.phaseStarts[0], "https://youtu.be/dQw4w9WgXcQ") {
		t.Errorf("phaseStarts[0] = %q, want the resolved source URL", rep.phaseStarts[0])
	}
	if !strings.HasPrefix(rep.phaseStarts[1], "write ") {
		t.Errorf("phaseStarts[1] = %q, want prefix %q", rep.phaseStarts[1], "write ")
	}

	if len(rep.phaseEnds) != 2 {
		t.Fatalf("phaseEnds = %d, want 2: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	for i, v := range rep.phaseEnds {
		if !v.OK {
			t.Errorf("phaseEnds[%d].OK = false, want true: %+v", i, v)
		}
	}
}

func TestCaptureRoot_DownloadFailureVerdict(t *testing.T) {
	installFakeYtdlp(t, failingYtdlpScript)

	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "ytdlp:https://youtu.be/abc"),
		RawArg:    "ytdlp:https://youtu.be/abc",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})
	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}

	if len(rep.phaseStarts) != 1 || !strings.HasPrefix(rep.phaseStarts[0], "download ") {
		t.Fatalf("phaseStarts = %v, want exactly the download phase", rep.phaseStarts)
	}
	if len(rep.phaseEnds) != 1 {
		t.Fatalf("phaseEnds = %d, want 1: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	v := rep.phaseEnds[0]
	if v.OK {
		t.Errorf("phaseEnds[0].OK = true, want false on yt-dlp failure")
	}
	errVal, ok := v.Diagnostic["error"].(string)
	if !ok || errVal == "" {
		t.Errorf("Diagnostic[%q] = %v, want non-empty error string", "error", v.Diagnostic["error"])
	}
}

func TestWalkArtifacts_EmitsWritePhase(t *testing.T) {
	dir := t.TempDir()
	wantOrder := writeArtifactFixture(t, dir)

	rep := &recordingReporter{}
	_, failures := walkArtifacts(
		context.Background(), newDiscardStore(), dir, "https://youtu.be/video", rep,
	)
	if len(failures) != 0 {
		t.Fatalf("failures = %+v, want none", failures)
	}

	if len(rep.phaseStarts) != 1 {
		t.Fatalf("phaseStarts = %d, want 1: %v", len(rep.phaseStarts), rep.phaseStarts)
	}
	wantPrefix := fmt.Sprintf("write %d artifacts", len(wantOrder))
	if !strings.HasPrefix(rep.phaseStarts[0], wantPrefix) {
		t.Errorf("phaseStarts[0] = %q, want prefix %q", rep.phaseStarts[0], wantPrefix)
	}
	if len(rep.phaseEnds) != 1 || !rep.phaseEnds[0].OK {
		t.Fatalf("phaseEnds = %+v, want exactly one OK verdict", rep.phaseEnds)
	}
}

func TestWalkArtifacts_WritePhaseFailureVerdict(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: chmod 000 files stay readable")
	}
	dir := t.TempDir()
	wantOrder := writeArtifactFixture(t, dir)

	// Make one artifact unreadable so its pass-2 blob write fails while
	// pass-1 stat (and thus the phase total) still sees it.
	if err := os.Chmod(filepath.Join(dir, "video.mp4"), 0o000); err != nil {
		t.Fatalf("chmod fixture: %v", err)
	}

	rep := &recordingReporter{}
	entries, failures := walkArtifacts(
		context.Background(), newDiscardStore(), dir, "https://youtu.be/video", rep,
	)
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want exactly one; stream: %v", failures, rep.failures)
	}
	if len(entries) != len(wantOrder)-1 {
		t.Fatalf("entries = %d, want %d", len(entries), len(wantOrder)-1)
	}

	// Task-2 contract: the blob-write failure carries per-entry detail.
	f := failures[0]
	if f.Op != capture_failures.OpBlobWrite {
		t.Errorf("failures[0].Op = %q, want %q", f.Op, capture_failures.OpBlobWrite)
	}
	if !strings.HasSuffix(f.Path, "video.mp4") {
		t.Errorf("failures[0].Path = %q, want suffix %q", f.Path, "video.mp4")
	}
	if f.Root != "https://youtu.be/video" {
		t.Errorf("failures[0].Root = %q, want the source URL", f.Root)
	}
	if f.Error == "" {
		t.Errorf("failures[0].Error is empty, want the blob-write error text")
	}

	if len(rep.phaseEnds) != 1 {
		t.Fatalf("phaseEnds = %d, want 1: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	v := rep.phaseEnds[0]
	if v.OK {
		t.Errorf("phaseEnds[0].OK = true, want false with a failed artifact")
	}
	if got := v.Diagnostic["entries"]; got != len(wantOrder) {
		t.Errorf("Diagnostic[%q] = %v, want %d", "entries", got, len(wantOrder))
	}
	if got := v.Diagnostic["failed"]; got != 1 {
		t.Errorf("Diagnostic[%q] = %v, want 1", "failed", got)
	}
}

// Compile-time assertion that recordingReporter satisfies the Reporter
// interface; if the interface grows, this test file fails to build with
// a clear message rather than a confusing call-site error.
var _ cutting_garden_plugins.Reporter = (*recordingReporter)(nil)
