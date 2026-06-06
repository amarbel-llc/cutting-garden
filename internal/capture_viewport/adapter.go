package capture_viewport

import (
	"fmt"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
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
//
// Pointer receiver: the adapter tracks the current phase description
// (PhaseStart stamps it, PhaseEnd carries it into the PhaseEnded message)
// so the persisted verdict line names the phase explicitly rather than
// relying on whatever the Model's header happens to say at end time.
type ProgramReporter struct {
	p sender

	// mu guards lastPhase — the Stream contract requires tolerating
	// concurrent calls even though phases are emitted sequentially in
	// practice.
	mu        sync.Mutex
	lastPhase string
}

var _ cgp.Reporter = (*ProgramReporter)(nil)

// NewReporter wraps a *tea.Program (or any sender) as a Reporter.
func NewReporter(p sender) *ProgramReporter { return &ProgramReporter{p: p} }

func (r *ProgramReporter) Plan(pl cgp.ReportPlan) {
	r.p.Send(OperationStarted{Name: pl.Label, Total: int(pl.Items)})
}

func (r *ProgramReporter) Progress(pr cgp.ReportProgress) {
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

func (r *ProgramReporter) Log(format string, args ...any) {
	r.p.Send(LogLine{Text: fmt.Sprintf(format, args...)})
}

func (r *ProgramReporter) PhaseStart(description string) {
	r.mu.Lock()
	r.lastPhase = description
	r.mu.Unlock()
	r.p.Send(PhaseStarted{Description: description})
}

func (r *ProgramReporter) PhaseEnd(v capture_events.Verdict) {
	r.mu.Lock()
	desc := r.lastPhase
	r.lastPhase = ""
	r.mu.Unlock()
	var d *DirectiveView
	if v.Directive != nil {
		d = &DirectiveView{Kind: v.Directive.Kind, Reason: v.Directive.Reason}
	}
	r.p.Send(PhaseEnded{
		Description: desc,
		Verdict:     VerdictView{OK: v.OK, Directive: d, Diagnostic: v.Diagnostic},
	})
}

// Entry/Failure stay no-ops in Stage A (entries still flow through the
// legacy Sink; Stage B routes them to the renderers).
func (r *ProgramReporter) Entry(capture_receipt.EntryV1) {}
func (r *ProgramReporter) Failure(string, error)         {}

func (r *ProgramReporter) Finalize(err error) { r.p.Send(BatchDone{Err: err}) }
