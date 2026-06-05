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
