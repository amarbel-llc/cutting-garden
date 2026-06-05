package capture_viewport

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	cgp "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
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
	if got.Total != 42 || got.Name != "walking ./src" {
		t.Errorf("unexpected OperationStarted: %+v", got)
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

// Compile-time proof the adapter satisfies the plugin Reporter contract.
var _ cgp.Reporter = ProgramReporter{}
