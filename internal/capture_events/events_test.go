package capture_events

import (
	"errors"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

func TestNop_AllMethodsAreSafeNoOps(t *testing.T) {
	var s Stream = Nop{}
	s.PhaseStart("download")
	s.PhaseEnd(Verdict{OK: true})
	s.PhaseEnd(Verdict{OK: false, Diagnostic: map[string]any{"failed": 2}})
	s.PhaseEnd(Verdict{OK: true, Directive: &Directive{Kind: DirectiveSkip, Reason: "no changes"}})
	s.Entry(testEntry())
	s.Failure("src", errors.New("boom"))
	s.Log("hello %s", "world")
	s.Plan(ReportPlan{Items: 1})
	s.Progress(ReportProgress{Items: 1})
	s.Finalize(nil)
	s.Finalize(errors.New("late"))
	// Reaching here without panic is the assertion.
}

func TestOrNop_NilYieldsUsableNop(t *testing.T) {
	s := OrNop(nil)
	if s == nil {
		t.Fatal("OrNop(nil) returned nil")
	}
	s.PhaseStart("x")
	s.Log("ok")
}

func TestOrNop_NonNilPassesThrough(t *testing.T) {
	rec := &recordingStream{}
	if got := OrNop(rec); got != rec {
		t.Fatal("OrNop should return the same non-nil Stream")
	}
}

// recordingStream embeds Nop and overrides only what it records — the
// pattern all test recorders in this repo should use from now on.
type recordingStream struct {
	Nop
	phases []string
}

func (r *recordingStream) PhaseStart(desc string) { r.phases = append(r.phases, desc) }

func testEntry() capture_receipt.EntryV1 {
	return capture_receipt.EntryV1{Path: "f", Root: "r", Type: capture_receipt.TypeFile}
}
