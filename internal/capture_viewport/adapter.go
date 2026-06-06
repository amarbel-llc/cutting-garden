package capture_viewport

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	cgp "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// sender is the subset of *tea.Program the adapter uses, narrowed so tests
// can inject a fake without a running program. *tea.Program satisfies it.
type sender interface {
	Send(tea.Msg)
}

// ProgramReporter implements cutting_garden_plugins.Reporter by translating
// each event into a viewport message and sending it to a bubbletea program.
// This is Layer 2 of the design — the adapter between the plugin event
// stream and the Model.
type ProgramReporter struct {
	p sender
}

var _ cgp.Reporter = ProgramReporter{}

// NewReporter wraps a *tea.Program (or any sender) as a Reporter.
func NewReporter(p sender) ProgramReporter { return ProgramReporter{p: p} }

func (r ProgramReporter) Plan(pl cgp.ReportPlan) {
	r.p.Send(OperationStarted{Name: pl.Label, Total: int(pl.Items)})
}

func (r ProgramReporter) Progress(pr cgp.ReportProgress) {
	// The Item line is sent unconditionally when present; the Model
	// dedupes consecutive identical lines, so a streaming source that
	// re-reports the same item label every tick (yt-dlp's video id)
	// collapses to one tail line while distinct labels (git hashes) all
	// land.
	if pr.Item != "" {
		r.p.Send(LogLine{Text: pr.Item})
	}
	r.p.Send(OperationProgress{
		Current:    int(pr.Items),
		Bytes:      pr.Bytes,
		BytesTotal: pr.BytesTotal,
	})
}

func (r ProgramReporter) Log(format string, args ...any) {
	r.p.Send(LogLine{Text: fmt.Sprintf(format, args...)})
}
