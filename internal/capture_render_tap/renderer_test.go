package capture_render_tap

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_events"
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/tap/go/pkgs/reader"
)

// scriptedRun drives the renderer through the canonical Stage B event
// script: a failing phase with entries and a failure subtest, a
// comment between phases, an empty OK phase, and a skip-directive
// phase. Plan/Progress are interleaved to pin their no-op behavior.
func scriptedRun(r *Renderer) {
	r.PhaseStart("capture src")
	r.Plan(capture_events.ReportPlan{Items: 3, Label: "walking"})
	r.Entry(capture_receipt.EntryV1{
		Path: "a.txt", Root: "src", Type: capture_receipt.TypeFile,
		Mode: 0o644, Size: 10, BlobId: "blake2b256-x",
	})
	r.Progress(capture_events.ReportProgress{Item: "a.txt", Items: 1})
	r.Entry(capture_receipt.EntryV1{
		Path: ".", Root: "src", Type: capture_receipt.TypeDir, Mode: 0o755,
	})
	r.Failure("src/bad.txt", errors.New("permission denied"))
	r.PhaseEnd(capture_events.Verdict{
		OK:         false,
		Diagnostic: map[string]any{"error": "walk aborted"},
	})

	r.Log("between %s", "phases")

	r.PhaseStart("receipt")
	r.PhaseEnd(capture_events.Verdict{OK: true})

	r.PhaseStart("ytdlp")
	r.PhaseEnd(capture_events.Verdict{
		OK: true,
		Directive: &capture_events.Directive{
			Kind: capture_events.DirectiveSkip, Reason: "no urls",
		},
	})
}

func TestRenderer_GoldenScript(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	scriptedRun(r)
	r.Finalize(nil)

	expected := `TAP version 14
    # Subtest: capture src
    ok 1 - src/a.txt file mode=0644 size=10 blob=blake2b256-x
    ok 2 - src dir mode=0755
    not ok 3 - src/bad.txt
      ---
      message: permission denied
      severity: fail
      ...
    1..3
not ok 1 - capture src
  ---
  error: walk aborted
  ...
# between phases
ok 2 - receipt
ok 3 - ytdlp # SKIP no urls
1..3
`

	if buf.String() != expected {
		t.Errorf("golden mismatch\nexpected:\n%s\ngot:\n%s", expected, buf.String())
	}
}

func TestRenderer_GoldenScriptValidatesAsTAP14(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	scriptedRun(r)
	r.Finalize(nil)

	rd := reader.NewReader(strings.NewReader(buf.String()))
	summary := rd.Summary()
	if !summary.Valid {
		for _, d := range rd.Diagnostics() {
			t.Errorf("diagnostic: line %d: %s: %s", d.Line, d.Severity, d.Message)
		}
		t.Fatalf("renderer output did not validate as TAP-14:\n%s", buf.String())
	}
}

func TestRenderer_FinalizeErrorBailsOutThenPlans(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)

	r.PhaseStart("capture src")
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(errors.New("store unavailable"))

	expected := `TAP version 14
ok 1 - capture src
Bail out! store unavailable
1..1
`

	if buf.String() != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, buf.String())
	}
}

func TestRenderer_PhaseEndTodoDirective(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)

	r.PhaseStart("git incremental")
	r.PhaseEnd(capture_events.Verdict{
		OK: false,
		Directive: &capture_events.Directive{
			Kind: capture_events.DirectiveTodo, Reason: "fell back to full capture",
		},
		Diagnostic: map[string]any{"error": "fetch failed"},
	})
	r.Finalize(nil)

	expected := `TAP version 14
not ok 1 - git incremental # TODO fell back to full capture
1..1
`

	if buf.String() != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, buf.String())
	}
}

func TestRenderer_PhaseEndOKWithDiagnostic(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)

	r.PhaseStart("receipt")
	r.PhaseEnd(capture_events.Verdict{
		OK: true,
		Diagnostic: map[string]any{
			"store":      ".work",
			"receipt_id": "blake2b256-r",
			"count":      3,
		},
	})
	r.Finalize(nil)

	expected := `TAP version 14
ok 1 - receipt
  ---
  count: 3
  receipt_id: blake2b256-r
  store: .work
  ...
1..1
`

	if buf.String() != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, buf.String())
	}
}

// TestRenderer_EntryTextByteEqualToLegacy pins the per-entry text to the
// legacy sink's formatter for every entry type: the subtest line must be
// exactly "ok N - " + capture_sink.FormatTAPEntry(e).
func TestRenderer_EntryTextByteEqualToLegacy(t *testing.T) {
	entries := []capture_receipt.EntryV1{
		{Path: "a.txt", Root: "src", Type: capture_receipt.TypeFile, Mode: 0o600, Size: 42, BlobId: "blake2b256-y"},
		{Path: "sub", Root: "src", Type: capture_receipt.TypeDir, Mode: 0o755},
		{Path: "link", Root: "src", Type: capture_receipt.TypeSymlink, Mode: 0o777, Target: "../bar"},
		{Path: "dev", Root: "src", Type: capture_receipt.TypeOther, Mode: 0o644},
		{Path: ".", Root: "src", Type: capture_receipt.TypeDir, Mode: 0o700},
	}

	for _, e := range entries {
		var buf bytes.Buffer
		r := New(&buf)
		r.PhaseStart("phase")
		r.Entry(e)
		r.PhaseEnd(capture_events.Verdict{OK: true})
		r.Finalize(nil)

		want := "    ok 1 - " + capture_sink.FormatTAPEntry(e) + "\n"
		if !strings.Contains(buf.String(), want) {
			t.Errorf("entry %+v: expected line %q in:\n%s", e, want, buf.String())
		}
	}
}

func TestRenderer_EmptyPhaseEmitsNoSubtestBlock(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)

	r.PhaseStart("empty")
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(nil)

	if strings.Contains(buf.String(), "# Subtest:") {
		t.Errorf("empty phase must not open a subtest block:\n%s", buf.String())
	}
}

func TestRenderer_ImplementsStream(t *testing.T) {
	var _ capture_events.Stream = New(&bytes.Buffer{})
}
