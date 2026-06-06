// Package capture_render_legacy bridges the unified
// capture_events.Stream onto the historical capture_sink.Sink so the
// legacy wire formats stay byte-identical during the Stage B
// dual-format window (see
// docs/plans/2026-06-06-unified-capture-events-tap-design.md
// §Rollback). The bridge retires together with capture_sink when the
// legacy formats are removed post-window.
package capture_render_legacy

import (
	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
)

// SinkBridge forwards the Stream's per-entry events (Entry, Failure)
// 1:1 to the wrapped legacy sink. Phases, Plan, Progress, Log, and
// Finalize are DELIBERATE no-ops: the legacy formats never carried
// them, and adding lines would break byte-identity. SetStore / Notice /
// StoreGroupReceipt / Finalize(sink) remain direct orchestrator calls
// on the wrapped sink, exactly as before Stage B.
//
// Log being a no-op means the one plugin-side message that moved from
// sink.Notice to stream.Log — the ytdlp tempdir-cleanup-failure notice,
// which fires only when post-capture cleanup fails — no longer reaches
// legacy piped output. Accepted: that path is rare and failure-only,
// and forwarding Log→Notice instead would leak the orchestrator's
// receipt/failure Log echoes onto the wire of every run.
//
// Concurrency: the wrapped Sink is single-threaded by contract;
// capture's walk emits sequentially (exactly as it called the sink
// directly before Stage B), so the bridge adds no locking.
type SinkBridge struct {
	capture_events.Nop
	sink capture_sink.Sink
}

// NewSinkBridge wraps s as the pipe-path Stream.
func NewSinkBridge(s capture_sink.Sink) *SinkBridge { return &SinkBridge{sink: s} }

func (b *SinkBridge) Entry(e capture_receipt.EntryV1) { b.sink.Entry(e) }

func (b *SinkBridge) Failure(source string, err error) { b.sink.Failure(source, err) }
