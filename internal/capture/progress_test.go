package capture

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_render_legacy"
	"github.com/amarbel-llc/cutting-garden/internal/capture_render_ndjson"
	"github.com/amarbel-llc/cutting-garden/internal/capture_render_tap"
)

func TestValidateProgress(t *testing.T) {
	for _, v := range []string{progressAuto, progressAlways, progressNever} {
		if err := validateProgress(v); err != nil {
			t.Errorf("validateProgress(%q) = %v, want nil", v, err)
		}
	}

	err := validateProgress("loud")
	if err == nil {
		t.Fatalf("validateProgress(%q) = nil, want error", "loud")
	}
	for _, want := range []string{"auto", "always", "never"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing allowed value %q", err.Error(), want)
		}
	}
}

func TestProgressActive(t *testing.T) {
	// A pipe write end is a *os.File that is not a TTY — the auto branch's
	// isatty probe returns false against it.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })

	t.Run("NeverIsAlwaysFalse", func(t *testing.T) {
		if progressActive(progressNever, w) {
			t.Errorf("never = true, want false")
		}
	})

	t.Run("AlwaysIsAlwaysTrue", func(t *testing.T) {
		if !progressActive(progressAlways, w) {
			t.Errorf("always = false, want true")
		}
	})

	t.Run("AutoNonTTYIsFalse", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		os.Unsetenv("NO_COLOR")
		if progressActive(progressAuto, w) {
			t.Errorf("auto on non-TTY pipe = true, want false")
		}
	})

	t.Run("AutoWithNoColorIsFalse", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if progressActive(progressAuto, w) {
			t.Errorf("auto with NO_COLOR set = true, want false")
		}
	})
}

func TestValidateFormat(t *testing.T) {
	for _, v := range []string{
		formatAuto, formatTAP, formatJSON, formatTAPLegacy, formatJSONLegacy,
	} {
		if err := validateFormat(v); err != nil {
			t.Errorf("validateFormat(%q) = %v, want nil", v, err)
		}
	}

	err := validateFormat("yaml")
	if err == nil {
		t.Fatalf("validateFormat(%q) = nil, want error", "yaml")
	}
	for _, want := range []string{
		"auto", "tap", "json", "tap-legacy", "json-legacy",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing allowed value %q", err.Error(), want)
		}
	}
}

// TestResolveFormat pins the auto-resolution semantics inherited from
// madder's output_format.Resolve: a non-TTY stdout (a pipe write end
// here) resolves auto to json; the TTY branch (isatty/Cygwin → tap)
// mirrors progressActive's probe and is not exercisable without a pty.
// Non-auto values pass through untouched.
func TestResolveFormat(t *testing.T) {
	_, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })

	if got := resolveFormat(formatAuto, w); got != formatJSON {
		t.Errorf("resolveFormat(auto, pipe) = %q, want %q", got, formatJSON)
	}

	for _, v := range []string{
		formatTAP, formatJSON, formatTAPLegacy, formatJSONLegacy,
	} {
		if got := resolveFormat(v, w); got != v {
			t.Errorf("resolveFormat(%q) = %q, want passthrough", v, got)
		}
	}
}

func TestCaptureLabel(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"NoArgs", nil, "capture"},
		{"OnlyFlags", []string{"-format=json"}, "capture"},
		{"FirstPositional", []string{"./src"}, "capture ./src"},
		{"FlagThenPositional", []string{"-format=tap", "dir-a"}, "capture dir-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := captureLabel(tt.args); got != tt.want {
				t.Errorf("captureLabel(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestReporterLineWriter_SplitsOnCRAndLF(t *testing.T) {
	var got []string
	w := &reporterLineWriter{log: func(s string) { got = append(got, s) }}

	if _, err := io.WriteString(w, "alpha\nbeta\rgamma\n"); err != nil {
		t.Fatal(err)
	}

	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("logged %d segments %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReporterLineWriter_BuffersPartialSegment(t *testing.T) {
	var got []string
	w := &reporterLineWriter{log: func(s string) { got = append(got, s) }}

	io.WriteString(w, "# (blob_store: xyz) dia")
	if len(got) != 0 {
		t.Fatalf("partial segment flushed early: %q", got)
	}

	io.WriteString(w, "ling sftp host\n")
	if len(got) != 1 || got[0] != "# (blob_store: xyz) dialing sftp host" {
		t.Fatalf("got %q, want the joined segment", got)
	}
}

func TestReporterLineWriter_SkipsEmptySegments(t *testing.T) {
	var got []string
	w := &reporterLineWriter{log: func(s string) { got = append(got, s) }}

	io.WriteString(w, "\r\n\n   \n\r")
	if len(got) != 0 {
		t.Fatalf("empty/whitespace segments logged: %q", got)
	}
}

// TestReporterLineWriter_ConcurrentWrites drives Write from many goroutines —
// the blob store may chatter from its own goroutines — and asserts no segment
// is lost. Meaningful under -race: it pins that Write serializes its buffer.
func TestReporterLineWriter_ConcurrentWrites(t *testing.T) {
	var mu sync.Mutex
	var got []string
	w := &reporterLineWriter{log: func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	}}

	const writers, lines = 8, 50
	var wg sync.WaitGroup
	for g := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range lines {
				fmt.Fprintf(w, "writer-%d line-%d\n", g, i)
			}
		}()
	}
	wg.Wait()

	if len(got) != writers*lines {
		t.Fatalf("logged %d segments, want %d", len(got), writers*lines)
	}
}

// captureStdout redirects os.Stdout to a pipe for the duration of fn and
// returns everything written. The viewport-inactive branch of setupReporting
// constructs its sink against os.Stdout directly, so this is how we observe
// the rollback-path bytes.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	w.Close()
	out := <-done
	r.Close()
	return out
}

// forceNonTTYStderr replaces os.Stderr with a pipe write end so
// -progress=auto resolves inactive regardless of the test runner's
// environment, and clears NO_COLOR so the probe (not the env var)
// decides.
func forceNonTTYStderr(t *testing.T) {
	t.Helper()
	origErr := os.Stderr
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = ew
	t.Cleanup(func() {
		os.Stderr = origErr
		er.Close()
		ew.Close()
	})
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
}

// TestSetupPipeline_LegacyRollbackByteIdentity is the legacy-window
// rollback guarantee: on the *-legacy pipe path, -progress=never and
// -progress=auto (non-TTY stderr) MUST both take the inactive branch
// (no viewport, real legacy sink, finish routing through the bridge's
// no-op Finalize) and emit byte-identical stdout — the orchestrator's
// direct sink calls reach the wire exactly as before Stage B.
func TestSetupPipeline_LegacyRollbackByteIdentity(t *testing.T) {
	forceNonTTYStderr(t)

	run := func(mode string) string {
		cmd := &Capture{Format: formatJSONLegacy, Progress: mode}
		return captureStdout(t, func() {
			p := cmd.setupPipeline("capture .")
			if p.viewportActive {
				t.Errorf("mode=%q: viewport active, want inactive", mode)
			}
			if p.legacySink == nil {
				t.Fatalf("mode=%q: legacySink nil, want the legacy NDJSON sink", mode)
			}
			p.setStore("")
			p.receipt("", "sha256-abc", 3)
			p.finish(nil) // bridge Finalize is a no-op: no extra bytes
			p.closeLegacy()
		})
	}

	never := run(progressNever)
	auto := run(progressAuto)

	if never != auto {
		t.Fatalf("rollback byte-identity broken:\nnever=%q\nauto =%q", never, auto)
	}
	// Sanity: the receipt actually reached stdout (i.e. not the discard sink).
	if !strings.Contains(never, "sha256-abc") {
		t.Errorf("inactive sink did not write receipt to stdout: %q", never)
	}
	if !strings.Contains(never, "store_group_receipt") {
		t.Errorf("legacy wire shape missing from stdout: %q", never)
	}
}

// TestSetupPipeline_SelectionTable pins the -format → pipeline shape
// mapping on the pipe path (stderr non-TTY → viewport inactive): the
// unified formats get their Stage B renderer with NO legacy sink; the
// *-legacy formats get the bridge over a real legacy sink; auto on a
// piped stdout resolves to json.
func TestSetupPipeline_SelectionTable(t *testing.T) {
	forceNonTTYStderr(t)

	tests := []struct {
		format     string
		wantStream string
		wantLegacy bool
	}{
		{formatTAP, "*capture_render_tap.Renderer", false},
		{formatJSON, "*capture_render_ndjson.Renderer", false},
		{formatTAPLegacy, "*capture_render_legacy.SinkBridge", true},
		{formatJSONLegacy, "*capture_render_legacy.SinkBridge", true},
		// captureStdout's pipe makes stdout non-TTY: auto → json.
		{formatAuto, "*capture_render_ndjson.Renderer", false},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			cmd := &Capture{Format: tt.format, Progress: progressNever}
			// Construct inside captureStdout: the TAP renderer writes its
			// version header at construction time.
			_ = captureStdout(t, func() {
				p := cmd.setupPipeline("capture .")
				defer p.finish(nil)
				defer p.closeLegacy()

				if p.viewportActive {
					t.Error("viewport active on the pipe path")
				}

				var gotStream string
				switch p.stream.(type) {
				case *capture_render_tap.Renderer:
					gotStream = "*capture_render_tap.Renderer"
				case *capture_render_ndjson.Renderer:
					gotStream = "*capture_render_ndjson.Renderer"
				case *capture_render_legacy.SinkBridge:
					gotStream = "*capture_render_legacy.SinkBridge"
				default:
					gotStream = fmt.Sprintf("%T", p.stream)
				}
				if gotStream != tt.wantStream {
					t.Errorf("stream = %s, want %s", gotStream, tt.wantStream)
				}

				if got := p.legacySink != nil; got != tt.wantLegacy {
					t.Errorf("legacySink non-nil = %v, want %v", got, tt.wantLegacy)
				}
			})
		})
	}
}

// TestSetupPipeline_ActiveFinishIdempotent pins the teardown guarantee
// behind Run's `defer p.finish(...)`: the active-mode finish is
// once-guarded, so the deferred call after an inline finish already ran
// is a safe no-op — no second BatchDone Send to a finished program and
// no second blocking wait on its (already closed) run channel.
func TestSetupPipeline_ActiveFinishIdempotent(t *testing.T) {
	origErr := os.Stderr
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = ew
	t.Cleanup(func() {
		os.Stderr = origErr
		er.Close()
		ew.Close()
	})
	// Drain render frames so the viewport never blocks on a full pipe.
	go func() { _, _ = io.Copy(io.Discard, er) }()

	cmd := &Capture{Format: formatAuto, Progress: progressAlways}
	p := cmd.setupPipeline("capture .")
	if !p.viewportActive {
		t.Fatal("progress=always: viewport inactive, want active")
	}

	p.finish(nil)
	// Second call models Run's deferred guard firing after the inline
	// call; sync.Once must make it return immediately without panicking.
	p.finish(errCaptureAborted)
}

// recordingStream embeds capture_events.Nop and records the ordered event
// trace — the standard test-recorder pattern for the Stream contract.
type recordingStream struct {
	capture_events.Nop
	events         []string
	lastDiagnostic map[string]any
}

func (r *recordingStream) PhaseStart(desc string) {
	r.events = append(r.events, "start:"+desc)
}

func (r *recordingStream) Log(format string, args ...any) {
	r.events = append(r.events, "log:"+fmt.Sprintf(format, args...))
}

func (r *recordingStream) PhaseEnd(v capture_events.Verdict) {
	r.events = append(r.events, fmt.Sprintf("end:ok=%v diag=%v", v.OK,
		v.Diagnostic != nil))
	r.lastDiagnostic = v.Diagnostic
}

func (r *recordingStream) Finalize(err error) {
	r.events = append(r.events, fmt.Sprintf("finalize:err=%v", err))
}

// TestMakeFinish_RoutesThroughFinalizeOnce pins the Task-4 refactor: the
// active-mode finish closure routes its terminal event through the stream's
// Finalize (the adapter sends BatchDone) instead of a direct program Send,
// and the sync.Once guarantees exactly one Finalize no matter how many
// times finish fires (inline call + Run's deferred guard).
func TestMakeFinish_RoutesThroughFinalizeOnce(t *testing.T) {
	rec := &recordingStream{}
	runDone := make(chan struct{})
	close(runDone) // render loop already exited; the wait must not block

	finish := makeFinish(rec, runDone)
	finish(nil)
	finish(errCaptureAborted) // models the deferred guard: must be a no-op

	want := []string{"finalize:err=<nil>"}
	if len(rec.events) != len(want) || rec.events[0] != want[0] {
		t.Fatalf("events = %q, want exactly %q", rec.events, want)
	}
}

// TestMakeFinish_NilRunDoneSkipsWait pins the pipe-path variant: with no
// render loop to wait on (runDone nil — the unified and legacy pipe
// paths), finish must finalize once and return immediately instead of
// blocking on a nil channel.
func TestMakeFinish_NilRunDoneSkipsWait(t *testing.T) {
	rec := &recordingStream{}
	finish := makeFinish(rec, nil)

	returned := make(chan struct{})
	go func() {
		finish(nil)
		finish(errCaptureAborted) // deferred-guard model: must be a no-op
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("finish blocked with nil runDone")
	}

	want := []string{"finalize:err=<nil>"}
	if len(rec.events) != len(want) || rec.events[0] != want[0] {
		t.Fatalf("events = %q, want exactly %q", rec.events, want)
	}
}

// TestMakeFinish_WaitsForRunDone pins the second half of the Once body: the
// first finish call must block until the program's render loop exits
// (runDone closes), so the final frame flushes before Run sets exit codes.
func TestMakeFinish_WaitsForRunDone(t *testing.T) {
	rec := &recordingStream{}
	runDone := make(chan struct{})
	finish := makeFinish(rec, runDone)

	returned := make(chan struct{})
	go func() {
		finish(nil)
		close(returned)
	}()

	select {
	case <-returned:
		t.Fatal("finish returned before runDone closed")
	case <-time.After(10 * time.Millisecond):
		// still blocked — the wait is in place
	}
	close(runDone)
	<-returned
}

func TestReportReceipt_EmitsPhaseAroundLog(t *testing.T) {
	rec := &recordingStream{}
	reportReceipt(rec, "storeA", "blake3:receipt", 42)

	want := []string{
		"start:receipt store=storeA",
		"log:receipt store=storeA id=blake3:receipt count=42",
		"end:ok=true diag=true",
	}
	if len(rec.events) != len(want) {
		t.Fatalf("events = %q, want %q", rec.events, want)
	}
	for i := range want {
		if rec.events[i] != want[i] {
			t.Errorf("events[%d] = %q, want %q", i, rec.events[i], want[i])
		}
	}

	// The verdict diagnostic carries the receipt machine-readably for
	// the unified formats (legacy keeps StoreGroupReceipt).
	diag := rec.lastDiagnostic
	if diag["store"] != "storeA" || diag["receipt_id"] != "blake3:receipt" ||
		diag["count"] != 42 {
		t.Errorf("receipt diagnostic = %v, want store/receipt_id/count", diag)
	}
}
