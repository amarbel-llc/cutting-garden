// Package capture_render_tap renders the unified capture_events.Stream
// as TAP-14 text — the Stage B successor to capture_sink's tapSink for
// `-format tap`. Each phase becomes one top-level test point; the
// phase's entries and failures nest as a subtest block emitted BEFORE
// the parent point (the TAP-14 subtest form, mirroring how tap's own
// writer.WriteAll sequences Subtest → child Plan → parent point).
//
// Mapping notes pinned against tap's writer API (pkgs/writer):
//
//   - tap.Writer auto-numbers test points, so the renderer keeps no
//     counters; subtest children number independently.
//   - Writer.NotOk takes map[string]string. Verdict.Diagnostic is
//     map[string]any; values are flattened with fmt.Sprintf("%v") —
//     the same rendering yaml_diagnostic applies to its Extras, so OK
//     and not-OK diagnostics agree byte-for-byte.
//   - An OK verdict WITH a diagnostic uses Writer.OkDiag with the map
//     as YAMLDiagnostic.Extras, so machine-readable payloads (the
//     orchestrator's receipt phase: store/receipt_id/count) survive.
//   - Directive verdicts render via Writer.Skip / Writer.Todo and DROP
//     Verdict.Diagnostic: the writer's Todo has no diagnostics variant,
//     and skip is kept symmetric. Unknown directive kinds fall through
//     to the plain OK/not-OK rendering.
//   - Plan/Progress are deliberate no-ops: they are ephemeral
//     progress-bar events for the -progress viewport, not TAP records.
//   - Finalize does not close a still-open subtest block: producers end
//     phases before finalizing, and on a bailout an unterminated block
//     correctly reads as "aborted mid-phase".
package capture_render_tap

import (
	"fmt"
	"io"
	"sync"

	"code.linenisgreat.com/cutting-garden/internal/capture_events"
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/madder/go/pkgs/tap_diagnostics"
	tap "github.com/amarbel-llc/tap/go/pkgs/writer"
	"github.com/amarbel-llc/tap/go/pkgs/yaml_diagnostic"
)

// Renderer is a capture_events.Stream rendering TAP-14 text. The mutex
// satisfies the Stream contract's concurrency tolerance (tap.Writer is
// not internally synchronized).
type Renderer struct {
	mu sync.Mutex
	tw *tap.Writer

	// phase is the pending phase description from PhaseStart; sub is
	// the phase's subtest writer, opened lazily on the first Entry or
	// Failure so an entry-less phase gets no subtest block.
	phase string
	sub   *tap.Writer
}

var _ capture_events.Stream = (*Renderer)(nil)

// New constructs a Renderer writing TAP-14 text to w. The "TAP version
// 14" header is emitted immediately; the caller must invoke Finalize to
// emit the trailing plan.
func New(w io.Writer) *Renderer {
	return &Renderer{tw: tap.NewWriter(w)}
}

func (r *Renderer) PhaseStart(description string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = description
	r.sub = nil
}

func (r *Renderer) PhaseEnd(v capture_events.Verdict) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sub != nil {
		r.sub.Plan()
		r.sub = nil
	}

	desc := r.phase
	r.phase = ""

	if v.Directive != nil {
		switch v.Directive.Kind {
		case capture_events.DirectiveSkip:
			r.tw.Skip(desc, v.Directive.Reason)
			return
		case capture_events.DirectiveTodo:
			r.tw.Todo(desc, v.Directive.Reason)
			return
		}
		// Unknown directive kinds fall through to the plain verdict.
	}

	if v.OK {
		if len(v.Diagnostic) > 0 {
			r.tw.OkDiag(desc, &yaml_diagnostic.YAMLDiagnostic{Extras: v.Diagnostic})
		} else {
			r.tw.Ok(desc)
		}
		return
	}

	r.tw.NotOk(desc, stringifyDiagnostic(v.Diagnostic))
}

func (r *Renderer) Entry(e capture_receipt.EntryV1) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureSubtest()
	r.sub.Ok(capture_sink.FormatTAPEntry(e))
}

func (r *Renderer) Failure(source string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureSubtest()
	r.sub.NotOk(source, tap_diagnostics.FromError(err))
}

func (r *Renderer) Log(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tw.Comment(format, args...)
}

// Plan is a no-op: ReportPlan is the ephemeral progress-bar estimate,
// not the TAP plan (which Finalize derives from the phase count).
func (r *Renderer) Plan(capture_events.ReportPlan) {}

// Progress is a no-op: incremental advancement is viewport-only.
func (r *Renderer) Progress(capture_events.ReportProgress) {}

func (r *Renderer) Finalize(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.tw.BailOut("%v", err)
	}
	r.tw.Plan()
}

// ensureSubtest lazily opens the current phase's subtest block. Caller
// holds r.mu.
func (r *Renderer) ensureSubtest() {
	if r.sub == nil {
		r.sub = r.tw.Subtest("%s", r.phase)
	}
}

// stringifyDiagnostic flattens a Verdict.Diagnostic into the
// map[string]string form Writer.NotOk takes, using the same %v
// rendering yaml_diagnostic applies to Extras.
func stringifyDiagnostic(diag map[string]any) map[string]string {
	if len(diag) == 0 {
		return nil
	}
	out := make(map[string]string, len(diag))
	for k, v := range diag {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
