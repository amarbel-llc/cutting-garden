// Package capture_render_ndjson renders the unified
// capture_events.Stream as tap-ndjson records (amarbel-llc/tap
// doc/tap-ndjson.7.scd) — the Stage B successor to capture_sink's
// jsonSink for `-format json`. Each phase becomes one top-level `test`
// record with its entries and failures nested in `subtest`; records
// stream line-per-record as phases complete (buffering is per-phase
// only).
//
// Mapping notes pinned against tap's pkgs/ndjson facade:
//
//   - Records reuse tap's canonical structs (TestRecord,
//     DirectiveValue, BailoutRecord, SummaryRecord), so field names,
//     order, and the all-fields-present/null-when-absent conventions
//     match tap's wire by construction. The one exception is the plan
//     record: the facade does not (yet) re-export internal PlanRecord
//     (Output.Plan references it, but the type is unnameable outside
//     tap), so planRecord below is a field-order-identical local copy.
//   - Entry subtests: Description is the Root/Path join (the same text
//     basis as the TAP forms via capture_sink.JoinRootPath); the
//     per-type metadata that FormatTAPEntry flattens into text stays
//     structured in Diagnostic as string values {type, mode,
//     size, blob_id | target}, mirroring the legacy jsonSink's
//     per-type field selection.
//   - Failures are subtests {Description: source, OK: false,
//     Diagnostic: {error}} and are ALWAYS appended; success subtests
//     are appended only up to successSubtestCap, beyond which they are
//     dropped and the phase diagnostic gains "subtests_truncated".
//     Every phase diagnostic merges the verdict diagnostic with the
//     full {"entries", "failed"} counts, so totals survive truncation.
//   - Line is always 0: events carry no source-line provenance (there
//     is no TAP text document for lines to index into).
//   - Log is deliberately dropped: tap-ndjson has no comment record
//     type, and inventing one would break consumers validating
//     against the schema's closed `type` set.
//   - Plan/Progress are deliberate no-ops: they are ephemeral
//     progress-bar events for the -progress viewport, not records.
//   - Finalize emits the trailing plan record (count = phases), a
//     bailout record when err != nil, then the summary computed from
//     the phase verdicts (directive precedence mirrors tap's own
//     Aggregator.Finalize: skip/todo count before ok/fail). Encode
//     errors are ignored throughout, like the legacy sink: events are
//     semantics, not identity, and the Stream API is fire-and-forget.
package capture_render_ndjson

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/tap/go/pkgs/ndjson"
)

// successSubtestCap bounds how many SUCCESS entries are retained as
// subtest records per phase; failures are always retained. TUNING
// LEVER: large captures emit one success per filesystem entry, and the
// whole phase record is buffered in memory until PhaseEnd — raise for
// more per-entry fidelity in the wire output, lower to bound memory
// and record size. The phase diagnostic's entries/failed counts are
// exact regardless.
const successSubtestCap = 1000

// planRecord mirrors tap-ndjson's plan record {type, count}. Local
// copy because tap's pkgs/ndjson facade does not re-export the
// internal PlanRecord type; field order matches the internal struct so
// the encoded line is byte-identical to tap's.
type planRecord struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// Renderer is a capture_events.Stream emitting tap-ndjson records. The
// mutex satisfies the Stream contract's concurrency tolerance.
type Renderer struct {
	mu  sync.Mutex
	enc *json.Encoder

	// Per-phase state, reset by PhaseStart/PhaseEnd.
	phase     string
	subtests  []ndjson.TestRecord
	entries   int
	failures  int
	truncated bool

	// Run tallies for the trailing plan + summary.
	phases  int
	passed  int
	failed  int
	skipped int
	todo    int
}

var _ capture_events.Stream = (*Renderer)(nil)

// New constructs a Renderer writing NDJSON lines to w. Each completed
// phase is encoded immediately; the caller must invoke Finalize to
// emit the trailing plan and summary records.
func New(w io.Writer) *Renderer {
	enc := json.NewEncoder(w)
	// Match tap's own WriteAll encoder configuration.
	enc.SetEscapeHTML(false)
	return &Renderer{enc: enc}
}

func (r *Renderer) PhaseStart(description string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = description
	r.subtests = nil
	r.entries = 0
	r.failures = 0
	r.truncated = false
}

func (r *Renderer) PhaseEnd(v capture_events.Verdict) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.phases++
	// All fields explicitly set: tap-ndjson records carry every field,
	// null/absent expressed via nils (and Line pinned to 0 — see the
	// package comment).
	rec := ndjson.TestRecord{
		Type:        "test",
		N:           r.phases,
		Description: r.phase,
		OK:          v.OK,
		Directive:   nil,
		Diagnostic:  r.phaseDiagnostic(v.Diagnostic),
		Output:      nil,
		Subtest:     r.subtests,
		Line:        0,
	}
	if v.Directive != nil {
		rec.Directive = &ndjson.DirectiveValue{
			Kind:   v.Directive.Kind,
			Reason: v.Directive.Reason,
		}
	}

	// Tally for the summary; precedence mirrors tap's
	// Aggregator.Finalize (directives count before ok/fail).
	switch {
	case v.Directive != nil && v.Directive.Kind == capture_events.DirectiveSkip:
		r.skipped++
	case v.Directive != nil && v.Directive.Kind == capture_events.DirectiveTodo:
		r.todo++
	case v.OK:
		r.passed++
	default:
		r.failed++
	}

	_ = r.enc.Encode(rec)

	r.phase = ""
	r.subtests = nil
	r.entries = 0
	r.failures = 0
	r.truncated = false
}

func (r *Renderer) Entry(e capture_receipt.EntryV1) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries++
	if r.entries > successSubtestCap {
		r.truncated = true
		return
	}
	r.appendSubtest(ndjson.TestRecord{
		Type:        "test",
		Description: capture_sink.JoinRootPath(e.Root, e.Path),
		OK:          true,
		Directive:   nil,
		Diagnostic:  entryDiagnostic(e),
		Output:      nil,
		Subtest:     nil,
		Line:        0,
	})
}

func (r *Renderer) Failure(source string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures++
	r.appendSubtest(ndjson.TestRecord{
		Type:        "test",
		Description: source,
		OK:          false,
		Directive:   nil,
		Diagnostic:  map[string]any{"error": err.Error()},
		Output:      nil,
		Subtest:     nil,
		Line:        0,
	})
}

// Log is a deliberate drop: tap-ndjson has no comment record type.
func (r *Renderer) Log(string, ...any) {}

// Plan is a no-op: ReportPlan is the ephemeral progress-bar estimate,
// not the trailing plan record (which Finalize derives from the phase
// count).
func (r *Renderer) Plan(capture_events.ReportPlan) {}

// Progress is a no-op: incremental advancement is viewport-only.
func (r *Renderer) Progress(capture_events.ReportProgress) {}

func (r *Renderer) Finalize(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = r.enc.Encode(planRecord{Type: "plan", Count: r.phases})
	if err != nil {
		_ = r.enc.Encode(ndjson.BailoutRecord{
			Type:    "bailout",
			Message: err.Error(),
			Line:    0,
		})
	}

	total := r.passed + r.failed + r.skipped + r.todo
	_ = r.enc.Encode(ndjson.SummaryRecord{
		Type:        "summary",
		Passed:      r.passed,
		Failed:      r.failed,
		Skipped:     r.skipped,
		Todo:        r.todo,
		Total:       total,
		PlanCount:   total,
		Bailed:      err != nil,
		Valid:       true,
		Diagnostics: []ndjson.SummaryDiagnostic{},
	})
}

// appendSubtest numbers rec densely within the phase's retained
// subtests and appends it. Caller holds r.mu.
func (r *Renderer) appendSubtest(rec ndjson.TestRecord) {
	rec.N = len(r.subtests) + 1
	r.subtests = append(r.subtests, rec)
}

// phaseDiagnostic merges the verdict diagnostic with the phase's
// entry/failure counts (and the truncation marker when the success cap
// was exceeded). Always non-nil: every phase record carries its
// counts. Caller holds r.mu.
func (r *Renderer) phaseDiagnostic(verdict map[string]any) map[string]any {
	diag := make(map[string]any, len(verdict)+3)
	for k, v := range verdict {
		diag[k] = v
	}
	diag["entries"] = r.entries
	diag["failed"] = r.failures
	if r.truncated {
		diag["subtests_truncated"] = true
	}
	return diag
}

// entryDiagnostic carries the per-type entry metadata as string
// values, mirroring the legacy jsonSink's field selection: size and
// blob_id apply to regular files; target applies to symlinks.
func entryDiagnostic(e capture_receipt.EntryV1) map[string]any {
	diag := map[string]any{
		"type": e.Type,
		"mode": fmt.Sprintf("%04o", e.Mode.Perm()),
	}
	switch e.Type {
	case capture_receipt.TypeFile:
		diag["size"] = strconv.FormatInt(e.Size, 10)
		diag["blob_id"] = e.BlobId
	case capture_receipt.TypeSymlink:
		diag["target"] = e.Target
	}
	return diag
}
