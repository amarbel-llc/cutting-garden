package cutting_garden_plugins

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_events"
)

// recordingReporter is a pointer-identity-comparable Reporter for tests.
// It embeds capture_events.Nop and overrides only what it records.
type recordingReporter struct {
	capture_events.Nop
	plans int
}

func (r *recordingReporter) Plan(ReportPlan) { r.plans++ }

func TestNopReporter_MethodsAreSafeNoOps(t *testing.T) {
	var r Reporter = NopReporter{}
	r.Plan(ReportPlan{Items: 10, Bytes: 100, Label: "x"})
	r.Progress(ReportProgress{Item: "f", Items: 1, Bytes: 10})
	r.Log("hello %s", "world")
	// Reaching here without panic is the assertion.
}

func TestReporterOrNop_NilYieldsUsableNoOp(t *testing.T) {
	r := ReporterOrNop(nil)
	if r == nil {
		t.Fatal("ReporterOrNop(nil) returned nil; want a usable no-op Reporter")
	}
	r.Plan(ReportPlan{})
	r.Progress(ReportProgress{})
	r.Log("ok")
}

func TestReporterOrNop_NonNilPassesThrough(t *testing.T) {
	rec := &recordingReporter{}
	if got := ReporterOrNop(rec); got != rec {
		t.Fatal("ReporterOrNop should return the same non-nil Reporter")
	}
}
