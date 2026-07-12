package capture_render_legacy

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_events"
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/capture_sink"
)

// recordingSink records every Sink method call in order so the bridge
// tests can assert exact forwarding (Entry/Failure) and exact silence
// (everything else).
type recordingSink struct {
	calls    []string
	entries  []capture_receipt.EntryV1
	failures []recordedFailure
}

type recordedFailure struct {
	source string
	err    error
}

func (s *recordingSink) SetStore(store string) { s.calls = append(s.calls, "SetStore") }

func (s *recordingSink) Entry(e capture_receipt.EntryV1) {
	s.calls = append(s.calls, "Entry")
	s.entries = append(s.entries, e)
}

func (s *recordingSink) StoreGroupReceipt(string, int) {
	s.calls = append(s.calls, "StoreGroupReceipt")
}

func (s *recordingSink) Notice(string, ...any) { s.calls = append(s.calls, "Notice") }

func (s *recordingSink) Failure(source string, err error) {
	s.calls = append(s.calls, "Failure")
	s.failures = append(s.failures, recordedFailure{source: source, err: err})
}

func (s *recordingSink) Finalize() { s.calls = append(s.calls, "Finalize") }

func testEntry() capture_receipt.EntryV1 {
	return capture_receipt.EntryV1{
		Path:   "src/main.go",
		Root:   ".",
		Type:   capture_receipt.TypeFile,
		Mode:   fs.FileMode(0o644),
		Size:   42,
		BlobId: "sha256-abc",
	}
}

func TestSinkBridge_ForwardsEntryAndFailureExactly(t *testing.T) {
	rec := &recordingSink{}
	b := NewSinkBridge(rec)

	entry := testEntry()
	failErr := errors.New("open failed")

	b.Entry(entry)
	b.Failure("src/broken.go", failErr)

	if len(rec.entries) != 1 || rec.entries[0] != entry {
		t.Errorf("Entry not forwarded 1:1: got %+v, want %+v", rec.entries, entry)
	}
	if len(rec.failures) != 1 {
		t.Fatalf("Failure calls = %d, want 1", len(rec.failures))
	}
	if rec.failures[0].source != "src/broken.go" || rec.failures[0].err != failErr {
		t.Errorf("Failure args = (%q, %v), want (%q, %v)",
			rec.failures[0].source, rec.failures[0].err, "src/broken.go", failErr)
	}

	wantCalls := []string{"Entry", "Failure"}
	if len(rec.calls) != len(wantCalls) {
		t.Fatalf("sink calls = %v, want %v", rec.calls, wantCalls)
	}
	for i := range wantCalls {
		if rec.calls[i] != wantCalls[i] {
			t.Errorf("calls[%d] = %q, want %q", i, rec.calls[i], wantCalls[i])
		}
	}
}

func TestSinkBridge_EverythingElseIsANoOp(t *testing.T) {
	rec := &recordingSink{}
	b := NewSinkBridge(rec)

	b.PhaseStart("download something")
	b.PhaseEnd(capture_events.Verdict{OK: true})
	b.PhaseEnd(capture_events.Verdict{
		OK:         false,
		Directive:  &capture_events.Directive{Kind: capture_events.DirectiveTodo, Reason: "r"},
		Diagnostic: map[string]any{"error": "boom"},
	})
	b.Plan(capture_events.ReportPlan{Items: 7, Bytes: 99, Label: "l"})
	b.Progress(capture_events.ReportProgress{Item: "i", Items: 1, Bytes: 2, BytesTotal: 3})
	b.Log("notice-ish %s", "message")
	b.Finalize(nil)
	b.Finalize(errors.New("aborted"))

	if len(rec.calls) != 0 {
		t.Errorf("phase/plan/progress/log/finalize reached the sink: %v", rec.calls)
	}
}

// driveLegacyScript emits the same orchestrator + per-entry sequence a
// pipe-path capture produces. ef is the per-entry emitter: the sink
// itself in the direct run, the bridge in the bridged run.
type entryFailer interface {
	Entry(capture_receipt.EntryV1)
	Failure(source string, err error)
}

func driveLegacyScript(s capture_sink.Sink, ef entryFailer, withStreamNoise func()) {
	s.SetStore("")
	s.Notice("%s", "switched to blob-store-id: storeA")
	if withStreamNoise != nil {
		withStreamNoise()
	}
	ef.Entry(testEntry())
	ef.Entry(capture_receipt.EntryV1{
		Path: "docs", Root: ".", Type: capture_receipt.TypeDir,
		Mode: fs.ModeDir | 0o755,
	})
	ef.Failure("src/broken.go", errors.New("permission denied"))
	s.StoreGroupReceipt("sha256-receipt", 2)
	s.Finalize()
}

// TestSinkBridge_WireByteIdentityThroughRealSinks is the Stage B Task 1
// guarantee: routing Entry/Failure through the bridge — with the full
// set of stream events (phases, plan, progress, logs, finalize)
// interleaved — produces byte-identical output to calling the legacy
// sinks directly, for both the TAP and NDJSON wire formats.
func TestSinkBridge_WireByteIdentityThroughRealSinks(t *testing.T) {
	t.Run("TAP", func(t *testing.T) {
		var direct bytes.Buffer
		ds := capture_sink.NewTAP(&direct)
		driveLegacyScript(ds, ds, nil)

		var bridged bytes.Buffer
		bs := capture_sink.NewTAP(&bridged)
		b := NewSinkBridge(bs)
		driveLegacyScript(bs, b, func() {
			b.PhaseStart("walk .")
			b.Plan(capture_events.ReportPlan{Items: 3})
			b.Progress(capture_events.ReportProgress{Item: "src/main.go", Items: 1})
			b.Log("receipt store=%s id=%s count=%d", "", "sha256-receipt", 2)
			b.PhaseEnd(capture_events.Verdict{OK: true})
			b.Finalize(nil)
		})

		if direct.String() != bridged.String() {
			t.Errorf("TAP wire diverged:\ndirect:\n%s\nbridged:\n%s",
				direct.String(), bridged.String())
		}
	})

	t.Run("NDJSON", func(t *testing.T) {
		var directOut, directErr bytes.Buffer
		ds := capture_sink.NewNDJSON(&directOut, &directErr)
		driveLegacyScript(ds, ds, nil)

		var bridgedOut, bridgedErr bytes.Buffer
		bs := capture_sink.NewNDJSON(&bridgedOut, &bridgedErr)
		b := NewSinkBridge(bs)
		driveLegacyScript(bs, b, func() {
			b.PhaseStart("walk .")
			b.Log("a log line that must not reach the wire")
			b.PhaseEnd(capture_events.Verdict{OK: true})
			b.Finalize(nil)
		})

		if directOut.String() != bridgedOut.String() {
			t.Errorf("NDJSON stdout diverged:\ndirect:\n%s\nbridged:\n%s",
				directOut.String(), bridgedOut.String())
		}
		if directErr.String() != bridgedErr.String() {
			t.Errorf("NDJSON stderr diverged:\ndirect:\n%s\nbridged:\n%s",
				directErr.String(), bridgedErr.String())
		}
	})
}
