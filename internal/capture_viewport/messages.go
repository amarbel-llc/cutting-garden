// Package capture_viewport adapts cutting-garden's capture event stream to
// the shared CRAP-2 viewport (github.com/amarbel-llc/crap/go-crap/viewport).
//
// The viewport Model and its message vocabulary used to live here (a WET copy
// of purse-first FDR 0010's operation_viewport). They have been extracted into
// crap's go-crap/viewport package; this package now re-exports them as type
// aliases and keeps only the cutting-garden-specific adapter (ProgramReporter
// in adapter.go), which translates capture_events into viewport messages.
package capture_viewport

import vp "github.com/amarbel-llc/crap/go-crap/viewport"

// Message types delivered to the Model via tea.Program.Send. Aliased to the
// shared viewport package so the adapter (and external callers) keep using
// the capture_viewport-qualified names unchanged.
type (
	LogLine           = vp.LogLine
	OperationStarted  = vp.OperationStarted
	OperationProgress = vp.OperationProgress
	OperationDone     = vp.OperationDone
	PhaseStarted      = vp.PhaseStarted
	DirectiveView     = vp.DirectiveView
	VerdictView       = vp.VerdictView
	PhaseEnded        = vp.PhaseEnded
	BatchDone         = vp.BatchDone
)
