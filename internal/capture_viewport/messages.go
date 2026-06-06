// Package capture_viewport is cutting-garden's WET copy of the raw Model
// tier of purse-first FDR 0010's operation_viewport: a bubbletea model
// that renders a spinner + rolling log tail + (when a total is known) a
// progress bar. The Run/RunBatch + PTY helpers from FDR 0010 are
// intentionally omitted — cutting-garden's plugins are in-process and emit
// structured events, so there is no child PTY to scan. Upstreaming this to
// dewey/pkgs/operation_viewport is a tracked follow-up.
package capture_viewport

// Message types mirror FDR 0010's vocabulary so this copy can be absorbed
// upstream. They are delivered to the Model via tea.Program.Send.

// LogLine appends one line to the rolling tail.
type LogLine struct{ Text string }

// OperationStarted (re)labels the header and, when Total > 0, arms the bar.
type OperationStarted struct {
	Name  string // label, e.g. "capture ./src"
	Index int    // 1-based position in a batch; 0 when not batched
	Total int    // total operations; 0 = unknown (indeterminate)
}

// OperationProgress advances the bar numerator. Current/Total drive the
// item-count bar (e.g. git's structural-object count); Bytes/BytesTotal
// drive the byte bar (e.g. a yt-dlp stream download). A consumer may set
// either pair, both, or neither — the Model's View precedence (items >
// byte bar > indeterminate byte counter) decides what renders.
type OperationProgress struct {
	Current    int   // item numerator
	Total      int   // item denominator; 0 leaves the existing total unchanged
	Bytes      int64 // bytes processed so far
	BytesTotal int64 // total bytes; 0 leaves the existing byte total unchanged
}

// OperationDone ends one operation: success collapses its tail, failure
// holds it and records the error.
type OperationDone struct{ Err error }

// PhaseStarted begins a phase: retitle the header and reset all
// per-phase live state (tail, bar, bytes).
type PhaseStarted struct{ Description string }

// DirectiveView / VerdictView mirror capture_events.Directive/Verdict
// for the view layer (no dependency inversion; the adapter converts).
type DirectiveView struct{ Kind, Reason string }

type VerdictView struct {
	OK         bool
	Directive  *DirectiveView
	Diagnostic map[string]any
}

// PhaseEnded completes a phase: persist a verdict line above the live
// region (tea.Println) and reset per-phase state. Description is carried
// here too so an end without a start still renders something sensible.
type PhaseEnded struct {
	Description string
	Verdict     VerdictView
}

// BatchDone ends the whole run and quits the program.
type BatchDone struct{ Err error }
