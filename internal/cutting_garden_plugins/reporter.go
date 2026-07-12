package cutting_garden_plugins

import "code.linenisgreat.com/cutting-garden/internal/capture_events"

// Reporter is the unified capture-events stream. The historical name is
// kept as an alias so request structs and plugin call sites read
// unchanged; new code should say capture_events.Stream. See
// internal/capture_events for the contract and semantics.
type Reporter = capture_events.Stream

type (
	ReportPlan     = capture_events.ReportPlan
	ReportProgress = capture_events.ReportProgress
)

// NopReporter is the no-op Stream.
type NopReporter = capture_events.Nop

// ReporterOrNop returns r, or a no-op Stream when r is nil.
func ReporterOrNop(r Reporter) Reporter { return capture_events.OrNop(r) }
