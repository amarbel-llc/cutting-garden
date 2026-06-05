package cutting_garden_plugins

// Reporter carries non-identity observability (plan / progress / log) from
// a capture, restore, or diff plugin to the orchestrator. It is the
// in-process analogue of RFC 0006's JSON-RPC notifications (see
// docs/plans/2026-06-05-capture-progress-protocol-design.md).
//
// These events are SEMANTICS, NOT IDENTITY: an implementation MUST NOT let
// them influence blob bytes, receipt shape, or any returned result. A
// Reporter is opt-in (the #50 SourceValidator capability ethos): a nil
// Reporter is valid and means "no observability"; plugins MAY omit any or
// all calls. Use ReporterOrNop at the call site to drop the nil check.
type Reporter interface {
	// Plan reports an up-front estimate of the work about to be done.
	// Called at most once, before any Progress. Optional — a plugin that
	// cannot estimate (streaming sources) never calls it, and the consumer
	// falls back to an indeterminate display.
	Plan(ReportPlan)

	// Progress reports incremental advancement, called many times as work
	// proceeds. ReportProgress.Items SHOULD be monotonic non-decreasing.
	Progress(ReportProgress)

	// Log emits a freeform human-readable line for the consumer's tail.
	// Signature mirrors fmt.Printf; pass "%s" when holding a pre-formatted
	// string.
	Log(format string, args ...any)
}

// ReportPlan is the up-front work estimate. A zero field means "unknown":
// Items == 0 yields an indeterminate display rather than a bar.
type ReportPlan struct {
	Items int64  // estimated total operations (e.g. filesystem entries)
	Bytes int64  // estimated total bytes
	Label string // human-readable scope, e.g. "walking ./src"
}

// ReportProgress is one incremental advancement sample.
type ReportProgress struct {
	Item  string // current item label, e.g. "src/main.go"
	Items int64  // operations completed so far (the bar numerator)
	Bytes int64  // bytes processed so far
}

// NopReporter is a Reporter whose methods do nothing — the default when no
// consumer is attached, so plugin code can report unconditionally.
type NopReporter struct{}

func (NopReporter) Plan(ReportPlan)         {}
func (NopReporter) Progress(ReportProgress) {}
func (NopReporter) Log(string, ...any)      {}

// ReporterOrNop returns r, or a NopReporter when r is nil.
func ReporterOrNop(r Reporter) Reporter {
	if r == nil {
		return NopReporter{}
	}
	return r
}
