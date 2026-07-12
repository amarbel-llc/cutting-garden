package capture_viewport

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"code.linenisgreat.com/cutting-garden/internal/capture_events"
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// fakeSender captures messages without a running tea.Program.
type fakeSender struct{ msgs []tea.Msg }

func (f *fakeSender) Send(m tea.Msg) { f.msgs = append(f.msgs, m) }

func TestProgramReporter_PlanBecomesOperationStarted(t *testing.T) {
	fs := &fakeSender{}
	NewReporter(fs).Plan(cgp.ReportPlan{Items: 42, Label: "walking ./src"})

	if len(fs.msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(fs.msgs))
	}
	got, ok := fs.msgs[0].(OperationStarted)
	if !ok {
		t.Fatalf("want OperationStarted, got %T", fs.msgs[0])
	}
	// The Plan label is deliberately dropped: the Model sets its title
	// from OperationStarted.Name, and a mid-phase Plan (git's "storing
	// git objects") would permanently clobber the run title. The phase
	// description already labels the live header; only the item total
	// flows through.
	if got.Total != 42 || got.Name != "" {
		t.Errorf("want OperationStarted{Total:42} with empty Name, got %+v", got)
	}
}

func TestProgramReporter_PlanLabelDoesNotClobberRunTitle(t *testing.T) {
	// End-to-end git-capture scenario at Model level: the plugin emits a
	// Plan{Label:...} mid-phase (incremental.go's "storing git objects");
	// after PhaseEnded + BatchDone the final frame must render the run
	// title, not the Plan label.
	fs := &fakeSender{}
	r := NewReporter(fs)
	r.PhaseStart("store 5 objects")
	r.Plan(cgp.ReportPlan{Items: 5, Label: "storing git objects"})
	r.Progress(cgp.ReportProgress{Item: "abc123", Items: 3})
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(nil)

	var tm tea.Model = New(WithTitle("capture x"))
	for _, msg := range fs.msgs {
		tm, _ = tm.Update(msg)
	}
	view := tm.View()
	if !strings.Contains(view, "capture x") {
		t.Errorf("final frame must show the run title; view:\n%s", view)
	}
	if strings.Contains(view, "storing") {
		t.Errorf("final frame must NOT show the Plan label; view:\n%s", view)
	}
}

func TestProgramReporter_LogBecomesLogLine(t *testing.T) {
	fs := &fakeSender{}
	NewReporter(fs).Log("hello %s", "world")

	if len(fs.msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(fs.msgs))
	}
	if got := fs.msgs[0].(LogLine); got.Text != "hello world" {
		t.Errorf("want %q, got %q", "hello world", got.Text)
	}
}

func TestProgramReporter_ProgressEmitsItemThenAdvance(t *testing.T) {
	fs := &fakeSender{}
	NewReporter(fs).Progress(cgp.ReportProgress{Item: "src/main.go", Items: 7})

	if len(fs.msgs) != 2 {
		t.Fatalf("want 2 msgs, got %d: %+v", len(fs.msgs), fs.msgs)
	}
	if _, ok := fs.msgs[0].(LogLine); !ok {
		t.Errorf("first msg want LogLine, got %T", fs.msgs[0])
	}
	op, ok := fs.msgs[1].(OperationProgress)
	if !ok || op.Current != 7 {
		t.Errorf("second msg want OperationProgress{Current:7}, got %+v", fs.msgs[1])
	}
}

func TestProgramReporter_ProgressForwardsBytes(t *testing.T) {
	fs := &fakeSender{}
	// A byte-only sample (streaming source): Item label present, Items 0,
	// with Bytes/BytesTotal set. The adapter must forward both byte fields.
	NewReporter(fs).Progress(cgp.ReportProgress{
		Item: "dQw4w9WgXcQ", Bytes: 1500, BytesTotal: 4096,
	})

	if len(fs.msgs) != 2 {
		t.Fatalf("want 2 msgs, got %d: %+v", len(fs.msgs), fs.msgs)
	}
	op, ok := fs.msgs[1].(OperationProgress)
	if !ok {
		t.Fatalf("second msg want OperationProgress, got %T", fs.msgs[1])
	}
	if op.Bytes != 1500 || op.BytesTotal != 4096 {
		t.Errorf("byte fields not forwarded: %+v", op)
	}
}

func TestProgramReporter_ProgressNoItemSkipsLogLine(t *testing.T) {
	fs := &fakeSender{}
	// No Item label: only the OperationProgress advance is sent.
	NewReporter(fs).Progress(cgp.ReportProgress{Bytes: 99, BytesTotal: 0})

	if len(fs.msgs) != 1 {
		t.Fatalf("want 1 msg (no LogLine without Item), got %d: %+v", len(fs.msgs), fs.msgs)
	}
	op, ok := fs.msgs[0].(OperationProgress)
	if !ok || op.Bytes != 99 {
		t.Errorf("want OperationProgress{Bytes:99}, got %+v", fs.msgs[0])
	}
}

func TestProgramReporter_PhaseEventsMapToMessages(t *testing.T) {
	fs := &fakeSender{}
	r := NewReporter(fs)
	r.PhaseStart("download")
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(nil)
	if len(fs.msgs) != 3 {
		t.Fatalf("want 3 msgs, got %d: %#v", len(fs.msgs), fs.msgs)
	}
	if got := fs.msgs[0].(PhaseStarted); got.Description != "download" {
		t.Errorf("PhaseStarted = %+v", got)
	}
	if got := fs.msgs[1].(PhaseEnded); !got.Verdict.OK || got.Description != "download" {
		t.Errorf("PhaseEnded = %+v", got)
	}
	if _, ok := fs.msgs[2].(BatchDone); !ok {
		t.Errorf("Finalize should send BatchDone, got %T", fs.msgs[2])
	}
}

// Compile-time proof the adapter satisfies the plugin Reporter contract.
var _ cgp.Reporter = (*ProgramReporter)(nil)
