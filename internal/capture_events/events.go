// Package capture_events is the unified producer-facing observability
// contract for capture/restore/diff: phases modeled as TAP test points
// (see docs/plans/2026-06-06-unified-capture-events-tap-design.md and
// amarbel-llc/tap doc/tap-ndjson.7.scd), plus the ephemeral progress
// events the -progress viewport renders.
//
// Every event is SEMANTICS, NOT IDENTITY: implementations and emitters
// MUST NOT let events influence entries, blob bytes, or receipts. A nil
// Stream is valid ("no observability"); use OrNop at call sites.
// Implementations MUST tolerate concurrent calls (producers may emit
// from multiple goroutines).
package capture_events

import (
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

// Directive kinds mirror tap-ndjson's directive.kind values.
const (
	DirectiveSkip = "skip"
	DirectiveTodo = "todo"
)

// Directive mirrors the tap-ndjson directive object {kind, reason}.
type Directive struct {
	Kind   string // DirectiveSkip or DirectiveTodo
	Reason string
}

// Verdict is the completion record of a phase — the in-process shape of
// a tap-ndjson `test` record's verdict-bearing fields. The phase number
// (`n`) is assigned renderer-side (tap text writers auto-number; the
// Stage-B ndjson renderer keeps its own counter), so it does not appear
// here.
type Verdict struct {
	OK         bool
	Directive  *Directive     // nil = no directive
	Diagnostic map[string]any // nil = no diagnostic; rendered as YAML-ish
}

// ReportPlan is the up-front work estimate for the live progress bar.
// Items == 0 means unknown (indeterminate display). Distinct from the
// TAP plan record (which counts phases and is renderer-derived).
type ReportPlan struct {
	Items int64
	Bytes int64
	Label string
}

// ReportProgress is one incremental advancement sample.
type ReportProgress struct {
	Item       string
	Items      int64
	Bytes      int64
	BytesTotal int64
}

// Stream is the unified event contract. Phase events delimit TAP test
// points: events between PhaseStart and PhaseEnd attribute to that
// phase. Phases are flat (no nesting) in v1. Entry/Failure are the
// per-entry result events: plugins emit them here (Stage B); the
// pipe-path consumer is capture_render_legacy's bridge onto the
// legacy sinks until the unified renderers land.
type Stream interface {
	// PhaseStart begins a phase. Consumers reset per-phase live state
	// (bar, byte counters, tail) and label the in-progress display.
	PhaseStart(description string)

	// PhaseEnd completes the current phase with a verdict. Consumers
	// persist it (checkmark line / TAP test point / ndjson record).
	PhaseEnd(v Verdict)

	// Entry reports one successfully captured entry (a subtest of the
	// current phase in TAP terms).
	Entry(e capture_receipt.EntryV1)

	// Failure reports a per-source failure (a failing subtest of the
	// current phase).
	Failure(source string, err error)

	// Log emits a freeform human line (TAP comment / viewport tail).
	// fmt.Printf signature; pass "%s" for pre-formatted strings.
	Log(format string, args ...any)

	// Plan reports the ephemeral progress-bar estimate (≤1×, before any
	// Progress). NOT a TAP plan.
	Plan(p ReportPlan)

	// Progress reports incremental advancement (bar numerator / bytes).
	Progress(p ReportProgress)

	// Finalize ends the whole run; err != nil marks it failed/aborted.
	Finalize(err error)
}

// Nop is a Stream whose methods do nothing. Embed it in partial
// implementations (test recorders, single-purpose consumers) so they
// only override what they handle.
type Nop struct{}

func (Nop) PhaseStart(string)             {}
func (Nop) PhaseEnd(Verdict)              {}
func (Nop) Entry(capture_receipt.EntryV1) {}
func (Nop) Failure(string, error)         {}
func (Nop) Log(string, ...any)            {}
func (Nop) Plan(ReportPlan)               {}
func (Nop) Progress(ReportProgress)       {}
func (Nop) Finalize(error)                {}

// OrNop returns s, or a Nop when s is nil.
func OrNop(s Stream) Stream {
	if s == nil {
		return Nop{}
	}
	return s
}
