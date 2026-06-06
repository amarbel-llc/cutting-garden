package capture_viewport

import (
	"errors"
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
