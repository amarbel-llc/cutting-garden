// Package capture_failures owns the cutting_garden-capture_failures-v1
// wire format: the durable record of WHICH entries failed (and why)
// during a capture, written alongside the success receipt whenever
// failCount > 0 or the run was aborted. Design:
// docs/plans/2026-06-07-capture-failure-receipt-design.md.
//
// The package follows the same horizontal-versioning convention as
// capture_receipt: each wire-format version is a self-contained data
// shape; the hyphence type-tag line is the dispatch key.
package capture_failures

// Blob is the lowest-common-denominator return type for a parsed
// failure receipt across all wire versions. A successful parse returns
// a concrete *V1; the dispatcher narrows by type-id.
type Blob any

// TypeTagV1 is the hyphence `! type-string` written at the top of
// every v1 failure receipt.
const TypeTagV1 = "cutting_garden-capture_failures-v1"

// Outcome values for Meta.Outcome.
const (
	// OutcomeFailures: the run completed; some entries failed.
	OutcomeFailures = "failures"
	// OutcomeAborted: a signal cut the run short (failures, if any,
	// are still listed). Aborted wins when both apply.
	OutcomeAborted = "aborted"
)

// Op values for FailureV1.Op — the operation that failed.
const (
	OpWalk         = "walk"
	OpStat         = "stat"
	OpReadlink     = "readlink"
	OpBlobWrite    = "blob-write"
	OpReceiptWrite = "receipt-write"
	OpPlugin       = "plugin"
)

// Meta is the failure receipt's hyphence metadata block.
type Meta struct {
	Ts       string   // RFC3339 UTC
	Outcome  string   // OutcomeFailures | OutcomeAborted
	Signal   string   // signal name; "" unless aborted by signal
	Receipt  string   // markl id of the paired success receipt; "" if its write failed
	Roots    []string // the capture's root args for this store group
	Captured int64
	Failed   int64
}

// FailureV1 is one NDJSON body line.
type FailureV1 struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Op    string `json:"op"`
	Error string `json:"error"`
}

// V1 is the in-memory form of a v1 failure receipt.
type V1 struct {
	Meta     Meta
	Failures []FailureV1
}
