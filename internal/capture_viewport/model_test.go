package capture_viewport

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateAll(m tea.Model, msgs ...tea.Msg) tea.Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

func TestModel_TailKeepsLastNLines(t *testing.T) {
	got := updateAll(New(WithTailLines(3)),
		LogLine{Text: "a"}, LogLine{Text: "b"},
		LogLine{Text: "c"}, LogLine{Text: "d"},
	)
	view := got.View()
	if strings.Contains(view, "a") {
		t.Errorf("oldest line should have rolled off; view:\n%s", view)
	}
	for _, want := range []string{"b", "c", "d"} {
		if !strings.Contains(view, want) {
			t.Errorf("tail missing %q; view:\n%s", want, view)
		}
	}
}

func TestModel_CollapsesTailOnSuccessfulDone(t *testing.T) {
	got := updateAll(New(WithTitle("cap")),
		LogLine{Text: "noisy"},
		BatchDone{Err: nil},
	)
	view := got.View()
	if strings.Contains(view, "noisy") {
		t.Errorf("successful BatchDone should collapse the tail; view:\n%s", view)
	}
	if !strings.Contains(view, "cap") {
		t.Errorf("done view should show the title; view:\n%s", view)
	}
}

func TestModel_HoldsAndShowsErrorOnFailure(t *testing.T) {
	got := updateAll(New(WithTitle("cap")),
		BatchDone{Err: errors.New("boom")},
	)
	if view := got.View(); !strings.Contains(view, "boom") {
		t.Errorf("failed done should surface the error; view:\n%s", view)
	}
}

func TestModel_BarOnlyWhenTotalKnown(t *testing.T) {
	indeterminate := New(WithTitle("cap")).View()
	determinate := updateAll(New(WithTitle("cap")),
		OperationStarted{Name: "cap", Total: 10},
		OperationProgress{Current: 5, Total: 10},
	).View()

	if determinate == indeterminate {
		t.Fatalf("determinate view should differ from indeterminate:\n%s", determinate)
	}
	if !strings.Contains(determinate, "%") {
		t.Errorf("determinate view should render a percentage bar:\n%s", determinate)
	}
	if strings.Contains(indeterminate, "%") {
		t.Errorf("indeterminate view should not render a bar:\n%s", indeterminate)
	}
}

func TestModel_ByteBarWhenBytesTotalKnown(t *testing.T) {
	view := updateAll(New(WithTitle("dl")),
		OperationProgress{Bytes: 512 * 1024, BytesTotal: 1024 * 1024},
	).View()

	if !strings.Contains(view, "%") {
		t.Errorf("byte bar should render a percentage:\n%s", view)
	}
	// Humanized done/total appear alongside the bar.
	if !strings.Contains(view, "512.0 KiB") {
		t.Errorf("byte bar should show humanized done:\n%s", view)
	}
	if !strings.Contains(view, "1.0 MiB") {
		t.Errorf("byte bar should show humanized total:\n%s", view)
	}
}

func TestModel_ByteCounterWhenOnlyDoneKnown(t *testing.T) {
	// total_bytes is NA mid-stream: only bytesDone is set, so an
	// indeterminate counter (no bar, no "%") renders.
	view := updateAll(New(WithTitle("dl")),
		OperationProgress{Bytes: 2 * 1024 * 1024, BytesTotal: 0},
	).View()

	if strings.Contains(view, "%") {
		t.Errorf("byte counter must not render a bar:\n%s", view)
	}
	if !strings.Contains(view, "2.0 MiB") {
		t.Errorf("byte counter should show humanized done:\n%s", view)
	}
}

func TestModel_ItemBarTakesPrecedenceOverBytes(t *testing.T) {
	// When both an item total and a byte total are known, the item bar
	// wins (git path) and the byte humanization is not appended.
	view := updateAll(New(WithTitle("cap")),
		OperationStarted{Name: "cap", Total: 4},
		OperationProgress{Current: 2, Total: 4, Bytes: 999, BytesTotal: 4096},
	).View()

	if !strings.Contains(view, "%") {
		t.Errorf("item bar should render:\n%s", view)
	}
	if strings.Contains(view, "KiB") || strings.Contains(view, " B") {
		t.Errorf("byte humanization must not appear when item bar wins:\n%s", view)
	}
}

func TestModel_DedupesConsecutiveIdenticalLogLines(t *testing.T) {
	// yt-dlp re-reports the same video id every tick; the tail should
	// collapse consecutive identical lines to one.
	got := updateAll(New(WithTailLines(5)),
		LogLine{Text: "dQw4w9WgXcQ"},
		LogLine{Text: "dQw4w9WgXcQ"},
	)
	m, ok := got.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", got)
	}
	if len(m.tail) != 1 {
		t.Errorf("consecutive identical LogLines should collapse to one; tail = %v", m.tail)
	}
}

func TestModel_DistinctLogLinesAllLand(t *testing.T) {
	// git emits distinct hashes; none are deduped.
	got := updateAll(New(WithTailLines(5)),
		LogLine{Text: "aaa"},
		LogLine{Text: "bbb"},
		LogLine{Text: "aaa"}, // non-consecutive repeat: kept
	)
	m := got.(Model)
	if len(m.tail) != 3 {
		t.Errorf("distinct (and non-consecutive repeat) lines should all land; tail = %v", m.tail)
	}
}

func TestModel_PhaseStartedResetsLiveState(t *testing.T) {
	m := updateAll(New(WithTitle("capture")),
		LogLine{Text: "old tail"},
		OperationProgress{Current: 5, Total: 10, Bytes: 100, BytesTotal: 200},
		PhaseStarted{Description: "write artifacts"},
	)
	view := m.View()
	if strings.Contains(view, "old tail") {
		t.Errorf("PhaseStarted should clear the tail; view:\n%s", view)
	}
	if !strings.Contains(view, "write artifacts") {
		t.Errorf("PhaseStarted should retitle the header; view:\n%s", view)
	}
	if strings.Contains(view, "%") {
		t.Errorf("PhaseStarted should reset bar/byte state (no stale bar); view:\n%s", view)
	}
}

func TestModel_PhaseEndedOKEmitsPersistAndResets(t *testing.T) {
	m := New(WithTitle("capture"))
	var tm tea.Model = m
	tm, _ = tm.Update(PhaseStarted{Description: "download"})
	tm, _ = tm.Update(LogLine{Text: "tick"})
	tm2, cmd := tm.Update(PhaseEnded{Description: "download", Verdict: VerdictView{OK: true}})
	if cmd == nil {
		t.Fatal("PhaseEnded must return a tea.Println cmd (the persistent line)")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("executing the cmd should produce a print message")
	}
	if view := tm2.View(); strings.Contains(view, "tick") {
		t.Errorf("ok phase should collapse the tail; view:\n%s", view)
	}
}

func TestModel_PhaseEndedFailHoldsTailInPersist(t *testing.T) {
	var tm tea.Model = New(WithTitle("capture"))
	tm, _ = tm.Update(PhaseStarted{Description: "write"})
	tm, _ = tm.Update(LogLine{Text: "artifact-3 failed io"})
	_, cmd := tm.Update(PhaseEnded{
		Description: "write",
		Verdict:     VerdictView{OK: false, Diagnostic: map[string]any{"failed": 2, "entries": 4}},
	})
	if cmd == nil {
		t.Fatal("failing PhaseEnded must persist")
	}
	out := fmt.Sprint(cmd())
	for _, want := range []string{"artifact-3 failed io", "✗", "write", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("persisted failure output missing %q:\n%s", want, out)
		}
	}
}

func TestModel_PhaseEndedSkipRendersDirective(t *testing.T) {
	var tm tea.Model = New(WithTitle("capture"))
	_, cmd := tm.Update(PhaseEnded{
		Description: "store objects",
		Verdict:     VerdictView{OK: true, Directive: &DirectiveView{Kind: "skip", Reason: "no changes"}},
	})
	out := fmt.Sprint(cmd())
	for _, want := range []string{"store objects", "SKIP", "no changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("skip persist missing %q:\n%s", want, out)
		}
	}
}

func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{int64(1024) * 1024 * 1024 * 1024, "1.0 TiB"},
	}
	for _, tc := range cases {
		if got := humanizeBytes(tc.in); got != tc.want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
