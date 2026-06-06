package cutting_garden_plugin_ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// recordingReporter captures Reporter events for assertions. It embeds
// capture_events.Nop and records every Plan and Progress call in order
// so tests can assert per-artifact progress emission and monotonicity.
type recordingReporter struct {
	capture_events.Nop
	plans    []cutting_garden_plugins.ReportPlan
	progress []cutting_garden_plugins.ReportProgress
}

func (r *recordingReporter) Plan(p cutting_garden_plugins.ReportPlan) {
	r.plans = append(r.plans, p)
}

func (r *recordingReporter) Progress(p cutting_garden_plugins.ReportProgress) {
	r.progress = append(r.progress, p)
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
	entries, failCount := walkArtifacts(
		context.Background(), newDiscardStore(), dir, source, &recordingSink{}, rep,
	)
	if failCount != 0 {
		t.Fatalf("failCount = %d, want 0", failCount)
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
		context.Background(), newDiscardStore(), dir, source, &recordingSink{}, &recordingReporter{},
	)
	withNil, failB := walkArtifacts(
		context.Background(), newDiscardStore(), dir, source, &recordingSink{}, nil,
	)
	if failA != 0 || failB != 0 {
		t.Fatalf("failCounts = %d, %d, want 0, 0", failA, failB)
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

// Compile-time assertion that recordingReporter satisfies the Reporter
// interface; if the interface grows, this test file fails to build with
// a clear message rather than a confusing call-site error.
var _ cutting_garden_plugins.Reporter = (*recordingReporter)(nil)
