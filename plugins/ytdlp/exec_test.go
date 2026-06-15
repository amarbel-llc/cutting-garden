package cutting_garden_plugin_ytdlp

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestRouteLine_ClassifiesSentinelVsLog exercises the pure line-router
// directly: sentinel progress lines reach onProgress (decoded), every
// other line reaches onLog. This is the unit-level guard for the
// stdout-routing contract independent of any subprocess.
func TestRouteLine_ClassifiesSentinelVsLog(t *testing.T) {
	var (
		samples []progressSample
		logs    []string
	)
	onProgress := func(s progressSample) { samples = append(samples, s) }
	onLog := func(line string) { logs = append(logs, line) }

	routeLine("CGP\t1024\t4096\t\tabc123", onProgress, onLog)
	routeLine("[download] Destination: video.mp4", onProgress, onLog)
	routeLine("[youtube] abc123: Downloading webpage", onProgress, onLog)

	if len(samples) != 1 {
		t.Fatalf("want 1 progress sample, got %d: %+v", len(samples), samples)
	}
	if samples[0] != (progressSample{Downloaded: 1024, Total: 4096, ID: "abc123"}) {
		t.Errorf("decoded sample = %+v", samples[0])
	}
	if len(logs) != 2 {
		t.Fatalf("want 2 log lines, got %d: %v", len(logs), logs)
	}
}

func TestRouteLine_NilCallbacksSafe(t *testing.T) {
	// Both nil: must not panic on either branch.
	routeLine("CGP\t1\t2\t\tx", nil, nil)
	routeLine("plain status", nil, nil)
}

// safeRecorder is a goroutine-safe callback recorder. runYtdlp invokes
// onProgress/onLog from two concurrent scanner goroutines, so the test
// sink must lock.
type safeRecorder struct {
	mu       sync.Mutex
	samples  []progressSample
	logLines []string
}

func (r *safeRecorder) progress(s progressSample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, s)
}

func (r *safeRecorder) log(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logLines = append(r.logLines, line)
}

// stdoutProgressYtdlpScript writes sentinel progress lines to STDOUT
// (where yt-dlp's --progress-template lands) plus a [download] status
// line to stdout, and a single diagnostic to stderr. This is the H2
// regression guard: runYtdlp must scan stdout for the sentinel, not just
// stderr. Exits 0 so the happy path is exercised.
const stdoutProgressYtdlpScript = `#!/bin/sh
printf 'CGP\t1024\t4096\t\tabc123\n'
printf 'CGP\t4096\t4096\t\tabc123\n'
echo "[download] 100% of 4.00KiB"
echo "fake-yt-dlp: a stderr diagnostic" >&2
exit 0
`

func TestRunYtdlp_ScansStdoutForSentinel(t *testing.T) {
	installFakeYtdlp(t, stdoutProgressYtdlpScript)

	rec := &safeRecorder{}
	err := runYtdlp(
		context.Background(),
		t.TempDir(),
		[]string{"-o", "ignored"},
		rec.progress,
		rec.log,
	)
	if err != nil {
		t.Fatalf("runYtdlp: %v", err)
	}

	// onProgress fired for the two stdout sentinel lines, decoded.
	if len(rec.samples) != 2 {
		t.Fatalf("want 2 progress samples from stdout, got %d: %+v", len(rec.samples), rec.samples)
	}
	wantSamples := map[int64]int64{1024: 4096, 4096: 4096} // downloaded->total
	for _, s := range rec.samples {
		if s.ID != "abc123" {
			t.Errorf("sample ID = %q, want abc123", s.ID)
		}
		if total, ok := wantSamples[s.Downloaded]; !ok || s.Total != total {
			t.Errorf("unexpected sample %+v", s)
		}
	}

	// onLog received the stdout status line AND the stderr diagnostic;
	// it must NOT have received either sentinel line.
	var sawDownload, sawDiagnostic bool
	for _, l := range rec.logLines {
		if l == "[download] 100% of 4.00KiB" {
			sawDownload = true
		}
		if l == "fake-yt-dlp: a stderr diagnostic" {
			sawDiagnostic = true
		}
		if strings.HasPrefix(l, progressLinePrefix) {
			t.Errorf("sentinel line leaked to onLog: %q", l)
		}
	}
	if !sawDownload {
		t.Errorf("stdout status line not routed to onLog; got %v", rec.logLines)
	}
	if !sawDiagnostic {
		t.Errorf("stderr diagnostic not routed to onLog; got %v", rec.logLines)
	}
}

// stderrFailYtdlpScript writes a sentinel to stdout (which must still be
// drained) and a diagnostic to stderr, then exits non-zero. Verifies the
// stderr tail still surfaces in the error after the stdout/stderr
// concurrent-drain rework.
const stderrFailYtdlpScript = `#!/bin/sh
printf 'CGP\t512\t1024\t\tvid\n'
echo "fake-yt-dlp: simulated geo-block" >&2
exit 1
`

func TestRunYtdlp_NonZeroExit_SurfacesStderrTail(t *testing.T) {
	installFakeYtdlp(t, stderrFailYtdlpScript)

	rec := &safeRecorder{}
	err := runYtdlp(
		context.Background(),
		t.TempDir(),
		[]string{"-o", "ignored"},
		rec.progress,
		rec.log,
	)
	if err == nil {
		t.Fatal("runYtdlp returned nil error on non-zero exit")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stderr-tail:") {
		t.Errorf("error %q missing 'stderr-tail:' marker", msg)
	}
	if !strings.Contains(msg, "simulated geo-block") {
		t.Errorf("error %q missing stderr diagnostic", msg)
	}
	// The stdout sentinel was still drained and decoded before Wait.
	if len(rec.samples) != 1 || rec.samples[0].ID != "vid" {
		t.Errorf("stdout sentinel not drained on failure path: %+v", rec.samples)
	}
}
