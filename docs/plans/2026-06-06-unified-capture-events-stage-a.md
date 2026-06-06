# Unified Capture Events — Stage A Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Land the unified `capture_events` contract and the viewport's persistent phase checkmarks (✓/✗/↷ lines that push the live region down), with the pipe wire format byte-identical to today.

**Architecture:** A new `internal/capture_events` package defines the one producer-facing `Stream` contract (phases as TAP test points + the existing Plan/Progress/Log + contract-complete Entry/Failure/Finalize). The old `Reporter` becomes type aliases over it (zero churn at request structs and plugin call sites). The viewport adapter implements the full Stream; `PhaseStart` resets the live region, `PhaseEnd` persists a verdict line via `tea.Println`. Plugins keep calling the legacy `Sink` for entries (wire untouched); Stage B migrates those. Design: `docs/plans/2026-06-06-unified-capture-events-tap-design.md`.

**Tech Stack:** Go 1.26 (devshell via `nix develop --command`), bubbletea v1.3.10 (`tea.Println`), existing capture_viewport/lipgloss.

**Rollback:** Stage A is additive: `-progress=never` / piped output is byte-identical (pinned by `TestSetupReporting_InactiveRollbackByteIdentity` + plugin byte-identity tests, all of which must pass UNCHANGED). Revert = drop the viewport phase wiring; the contract is inert without consumers.

---

## Conventions for every task

- Devshell: `nix develop --command go test ./internal/<pkg>/ -run <Name> -v`; whole-package `-race` runs at task end.
- Commits gpg-signed — if signing fails, STOP and report (never retry unsigned).
- All events are **semantics, not identity**: no event may influence entries, blob bytes, or receipts. The existing byte-identity tests are the enforcement; they must stay green and unchanged.
- Stage A wire guarantee: do NOT touch `capture_sink`, the `-format` paths, or any `sink.*` call site's behavior.

---

### Task 1: `internal/capture_events` — the contract

**Promotion criteria:** N/A (new package; the alias layer in Task 2 keeps the old names alive — they retire in Stage B).

**Files:**
- Create: `internal/capture_events/events.go`
- Test: `internal/capture_events/events_test.go`

**Step 1: Write the failing test** (`events_test.go`):

```go
package capture_events

import (
	"errors"
	"testing"
)

func TestNop_AllMethodsAreSafeNoOps(t *testing.T) {
	var s Stream = Nop{}
	s.PhaseStart("download")
	s.PhaseEnd(Verdict{OK: true})
	s.PhaseEnd(Verdict{OK: false, Diagnostic: map[string]any{"failed": 2}})
	s.PhaseEnd(Verdict{OK: true, Directive: &Directive{Kind: DirectiveSkip, Reason: "no changes"}})
	s.Entry(testEntry())
	s.Failure("src", errors.New("boom"))
	s.Log("hello %s", "world")
	s.Plan(ReportPlan{Items: 1})
	s.Progress(ReportProgress{Items: 1})
	s.Finalize(nil)
	s.Finalize(errors.New("late"))
	// Reaching here without panic is the assertion.
}

func TestOrNop_NilYieldsUsableNop(t *testing.T) {
	s := OrNop(nil)
	if s == nil {
		t.Fatal("OrNop(nil) returned nil")
	}
	s.PhaseStart("x")
	s.Log("ok")
}

func TestOrNop_NonNilPassesThrough(t *testing.T) {
	rec := &recordingStream{}
	if got := OrNop(rec); got != rec {
		t.Fatal("OrNop should return the same non-nil Stream")
	}
}

// recordingStream embeds Nop and overrides only what it records — the
// pattern all test recorders in this repo should use from now on.
type recordingStream struct {
	Nop
	phases []string
}

func (r *recordingStream) PhaseStart(desc string) { r.phases = append(r.phases, desc) }
```

`testEntry()` helper: construct a minimal `capture_receipt.EntryV1{Path: "f", Root: "r", Type: capture_receipt.TypeFile}`.

**Step 2: Run to verify it fails**

Run: `nix develop --command go test ./internal/capture_events/ -v`
Expected: compile FAIL (`undefined: Stream`, `Nop`, …).

**Step 3: Write minimal implementation** (`events.go`):

```go
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
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
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
// phase. Phases are flat (no nesting) in v1. Entry/Failure are
// contract-complete now but unwired in Stage A — plugins still report
// entries via the legacy capture_sink.Sink until Stage B migrates the
// renderers; emitting them here is harmless (Nop) either way.
type Stream interface {
	// PhaseStart begins a phase. Consumers reset per-phase live state
	// (bar, byte counters, tail) and label the in-progress display.
	PhaseStart(description string)

	// PhaseEnd completes the current phase with a verdict. Consumers
	// persist it (checkmark line / TAP test point / ndjson record).
	PhaseEnd(v Verdict)

	// Entry reports one successfully captured entry (a subtest of the
	// current phase in TAP terms). UNWIRED in Stage A — see above.
	Entry(e capture_receipt.EntryV1)

	// Failure reports a per-source failure (a failing subtest of the
	// current phase). UNWIRED in Stage A — see above.
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

func (Nop) PhaseStart(string)                 {}
func (Nop) PhaseEnd(Verdict)                  {}
func (Nop) Entry(capture_receipt.EntryV1)     {}
func (Nop) Failure(string, error)             {}
func (Nop) Log(string, ...any)                {}
func (Nop) Plan(ReportPlan)                   {}
func (Nop) Progress(ReportProgress)           {}
func (Nop) Finalize(error)                    {}

// OrNop returns s, or a Nop when s is nil.
func OrNop(s Stream) Stream {
	if s == nil {
		return Nop{}
	}
	return s
}
```

**Step 4: Run to verify pass**

Run: `nix develop --command go test ./internal/capture_events/ -v`
Expected: 3 PASS. Then `nix develop --command go build ./...` (still green — nothing imports it yet).

**Step 5: Commit**

```bash
git add internal/capture_events/
git commit -m "feat(capture_events): unified Stream contract — phases as TAP verdicts"
```

---

### Task 2: Alias the old Reporter onto the contract

**Promotion criteria:** the aliases retire in Stage B when call sites import capture_events directly.

**Files:**
- Modify: `internal/cutting_garden_plugins/reporter.go` (REPLACE its type/func definitions with aliases)
- Modify: test recorders across `internal/cutting_garden_plugins/reporter_test.go`, `internal/cutting_garden_plugin_git/reporter_test.go`, `internal/cutting_garden_plugin_ytdlp/reporter_test.go` (embed `capture_events.Nop`)
- Modify: `internal/capture_viewport/adapter.go` (implement the FULL Stream — see Step 3)

**Step 1: Rewrite `reporter.go` as the alias layer** (keep the package doc pointing at capture_events):

```go
package cutting_garden_plugins

import "github.com/amarbel-llc/cutting-garden/internal/capture_events"

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
```

(The old struct definitions, method sets, and doc text are DELETED — they now live in capture_events. Type aliases keep every existing reference — request structs, plugins, the orchestrator, ProgramReporter's `var _` assertion — compiling, EXCEPT implementations of the interface, which now need 5 more methods.)

**Step 2: Fix the implementations** (compile-driven; run `nix develop --command go build ./...` and follow the errors):
- `internal/capture_viewport/adapter.go` — `ProgramReporter` gains the missing methods as TEMPORARY no-ops in this task (`func (r ProgramReporter) PhaseStart(string) {}` etc. for PhaseStart/PhaseEnd/Entry/Failure/Finalize) with a `// Task 3 wires these.` comment. The `var _ cgp.Reporter = ProgramReporter{}` assertion keeps it honest.
- Every test `recordingReporter`: change to embed the Nop and keep its recording overrides, e.g.:
  ```go
  type recordingReporter struct {
      capture_events.Nop
      plans      []cgp.ReportPlan
      progresses []cgp.ReportProgress
      logs       []string
  }
  ```
  (drop their now-inherited empty methods; keep the overrides that append).

**Step 3: Run the full suite**

Run: `nix develop --command go test ./... | tail -20`
Expected: ALL packages PASS — this task is behavior-neutral. Specifically confirm `TestWalkArtifacts_ByteIdentityAcrossReporters`, `TestCaptureProtocol_Reporter_DoesNotAffectIdentity`, and `TestSetupReporting_InactiveRollbackByteIdentity` pass UNCHANGED (no edits to those test bodies).

**Step 4: Commit**

```bash
git add internal/cutting_garden_plugins/reporter.go internal/capture_viewport/adapter.go internal/cutting_garden_plugins/reporter_test.go internal/cutting_garden_plugin_git/reporter_test.go internal/cutting_garden_plugin_ytdlp/reporter_test.go
git commit -m "refactor(plugins): Reporter becomes an alias of capture_events.Stream"
```

---

### Task 3: Viewport phase rendering (the checkmarks)

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/capture_viewport/messages.go` (two new messages)
- Modify: `internal/capture_viewport/model.go` (handle them; persist via `tea.Println`)
- Modify: `internal/capture_viewport/adapter.go` (wire PhaseStart/PhaseEnd/Finalize)
- Test: `internal/capture_viewport/model_test.go`, `adapter_test.go`

**Step 1: Write the failing tests** (append to `model_test.go`):

```go
func TestModel_PhaseStartedResetsLiveState(t *testing.T) {
	m := updateAll(New(WithTitle("capture")),
		LogLine{Text: "old tail"},
		OperationProgress{Current: 5, Total: 10, Bytes: 100, BytesTotal: 200},
		PhaseStarted{Description: "write artifacts"},
	)
	view := m.View()
	if strings.Contains(view, "old tail") {
		t.Errorf("PhaseStarted should clear the tail; view:\n%s", view)
	}
	if !strings.Contains(view, "write artifacts") {
		t.Errorf("PhaseStarted should retitle the header; view:\n%s", view)
	}
	if strings.Contains(view, "%") {
		t.Errorf("PhaseStarted should reset bar/byte state (no stale bar); view:\n%s", view)
	}
}

func TestModel_PhaseEndedOKEmitsPersistAndResets(t *testing.T) {
	m := New(WithTitle("capture"))
	var tm tea.Model = m
	tm, _ = tm.Update(PhaseStarted{Description: "download"})
	tm, _ = tm.Update(LogLine{Text: "tick"})
	tm2, cmd := tm.Update(PhaseEnded{Description: "download", Verdict: VerdictView{OK: true}})
	if cmd == nil {
		t.Fatal("PhaseEnded must return a tea.Println cmd (the persistent line)")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("executing the cmd should produce a print message")
	}
	if view := tm2.View(); strings.Contains(view, "tick") {
		t.Errorf("ok phase should collapse the tail; view:\n%s", view)
	}
}

func TestModel_PhaseEndedFailHoldsTailInPersist(t *testing.T) {
	var tm tea.Model = New(WithTitle("capture"))
	tm, _ = tm.Update(PhaseStarted{Description: "write"})
	tm, _ = tm.Update(LogLine{Text: "artifact-3 failed io"})
	_, cmd := tm.Update(PhaseEnded{
		Description: "write",
		Verdict:     VerdictView{OK: false, Diagnostic: map[string]any{"failed": 2, "entries": 4}},
	})
	if cmd == nil {
		t.Fatal("failing PhaseEnded must persist")
	}
	out := fmt.Sprint(cmd())
	for _, want := range []string{"artifact-3 failed io", "✗", "write", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("persisted failure output missing %q:\n%s", want, out)
		}
	}
}

func TestModel_PhaseEndedSkipRendersDirective(t *testing.T) {
	var tm tea.Model = New(WithTitle("capture"))
	_, cmd := tm.Update(PhaseEnded{
		Description: "store objects",
		Verdict:     VerdictView{OK: true, Directive: &DirectiveView{Kind: "skip", Reason: "no changes"}},
	})
	out := fmt.Sprint(cmd())
	for _, want := range []string{"store objects", "SKIP", "no changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("skip persist missing %q:\n%s", want, out)
		}
	}
}
```

(Inspecting `tea.Println`'s message: the Cmd returns bubbletea's unexported `printLineMessage`, whose `fmt.Sprint` includes the body text — if `fmt.Sprint(cmd())` does NOT surface the text in practice, relax those assertions to non-nil cmd + state checks and note it. Add `fmt` to the test imports.)

And in `adapter_test.go`:

```go
func TestProgramReporter_PhaseEventsMapToMessages(t *testing.T) {
	fs := &fakeSender{}
	r := NewReporter(fs)
	r.PhaseStart("download")
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(nil)
	if len(fs.msgs) != 3 {
		t.Fatalf("want 3 msgs, got %d: %#v", len(fs.msgs), fs.msgs)
	}
	if got := fs.msgs[0].(PhaseStarted); got.Description != "download" {
		t.Errorf("PhaseStarted = %+v", got)
	}
	if got := fs.msgs[1].(PhaseEnded); !got.Verdict.OK || got.Description != "download" {
		t.Errorf("PhaseEnded = %+v", got)
	}
	if _, ok := fs.msgs[2].(BatchDone); !ok {
		t.Errorf("Finalize should send BatchDone, got %T", fs.msgs[2])
	}
}
```

**Step 2: Run to verify failure** (undefined `PhaseStarted`/`PhaseEnded`/`VerdictView`…).

**Step 3: Implement.**

`messages.go` — add (keep viewport decoupled from capture_events via view-local types):

```go
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
```

`model.go` — track the current phase description (reuse/replace the title when set), and:

```go
case PhaseStarted:
	m.title = msg.Description
	m.resetPhase()
	return m, nil

case PhaseEnded:
	line := m.renderPhaseEnd(msg)
	m.resetPhase()
	return m, tea.Println(line)
```

with helpers:

```go
// resetPhase clears all per-phase live state. This is the designed
// phase-boundary reset (retires the cg#56 bytesDone phase-bleed).
func (m *Model) resetPhase() {
	m.tail = nil
	m.current, m.total = 0, 0
	m.bytesDone, m.bytesTotal = 0, 0
}

// renderPhaseEnd builds the persistent multi-line string for a phase
// verdict, following tap's TTY-viewport FDR: ok collapses to one green
// line; failure holds the tail (persisted above) + red line + a
// YAML-ish diagnostic; a skip/todo directive renders dim with the
// directive text.
func (m Model) renderPhaseEnd(msg PhaseEnded) string {
	desc := msg.Description
	if desc == "" {
		desc = m.title
	}
	switch {
	case msg.Verdict.Directive != nil:
		d := msg.Verdict.Directive
		return tailStyle.Render(fmt.Sprintf("↷ %s # %s %s",
			desc, strings.ToUpper(d.Kind), d.Reason))
	case msg.Verdict.OK:
		return successStyle.Render("✓ " + desc)
	default:
		var b strings.Builder
		for _, l := range m.tail { // hold the tail: persist it above
			b.WriteString(tailStyle.Render("│ " + l))
			b.WriteByte('\n')
		}
		b.WriteString(failStyle.Render("✗ " + desc))
		for _, k := range sortedKeys(msg.Verdict.Diagnostic) {
			b.WriteByte('\n')
			b.WriteString(failStyle.Render(fmt.Sprintf("  %s: %v", k, msg.Verdict.Diagnostic[k])))
		}
		return b.String()
	}
}
```

(`sortedKeys`: tiny helper, `sort.Strings` over map keys — deterministic rendering. Add `fmt`, `sort` imports.)

`adapter.go` — replace the Task-2 stubs:

```go
func (r ProgramReporter) PhaseStart(description string) {
	r.p.Send(PhaseStarted{Description: description})
}

func (r ProgramReporter) PhaseEnd(v capture_events.Verdict) {
	var d *DirectiveView
	if v.Directive != nil {
		d = &DirectiveView{Kind: v.Directive.Kind, Reason: v.Directive.Reason}
	}
	r.p.Send(PhaseEnded{Verdict: VerdictView{OK: v.OK, Directive: d, Diagnostic: v.Diagnostic}})
}

// Entry/Failure stay no-ops in Stage A (entries still flow through the
// legacy Sink; Stage B routes them to the renderers).

func (r ProgramReporter) Finalize(err error) { r.p.Send(BatchDone{Err: err}) }
```

NOTE: the adapter does not know the phase description at PhaseEnd time — either track it in the adapter (it's per-capture, single-threaded enough) OR have the Model fill from `m.title` (the `desc == ""` fallback above). Simplest: rely on the Model fallback; set `PhaseEnded.Description` only in tests. If the reviewer prefers explicitness, the adapter may hold `lastPhase string` (needs pointer receiver — then keep the `var _` assertion on `*ProgramReporter` and update `NewReporter` to return the pointer). Implementer's choice; document it.

**Step 4: Run to verify pass**

Run: `nix develop --command go test -race ./internal/capture_viewport/ -v`
Expected: all PASS (new + existing).

**Step 5: Commit**

```bash
git add internal/capture_viewport/
git commit -m "feat(capture_viewport): persistent phase verdict lines (collapse-on-ok, hold-on-fail, skip)"
```

---

### Task 4: Orchestrator — Finalize via the stream + receipt phases

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/capture/capture.go`
- Test: `internal/capture/progress_test.go` (extend), existing rollback tests UNCHANGED

**Step 1: Failing test** — extend `progress_test.go`: drive `setupReporting` in ACTIVE mode (the idempotent-finish test's harness), assert that `finish(err)` still results in exactly one `BatchDone` (now routed through the adapter's `Finalize`) — i.e. the Once semantics survive the refactor. Also a small test that the orchestrator emits a receipt phase: factor the receipt-reporting into a helper `reportReceipt(stream cgp.Reporter, storeName string, receiptID string, count int)` that calls `stream.PhaseStart(...)` + `stream.PhaseEnd(Verdict{OK: true})` + the existing `reporter.Log` line, and unit-test it with a recordingStream (embed `capture_events.Nop`).

**Step 2: Run to verify failure.**

**Step 3: Implement:**
- `setupReporting`'s active `finish` closure: replace `p.Send(capture_viewport.BatchDone{Err: err})` with the stream's `Finalize(err)` (the adapter sends BatchDone). Keep the `sync.Once` + `<-runDone` EXACTLY as-is.
- At both store-group receipt sites (protocol ~L142-ish and EntryV1 ~L208-ish, after `sink.StoreGroupReceipt` + the existing `reporter.Log`): call the new `reportReceipt` helper — `PhaseStart(fmt.Sprintf("receipt store=%s", storeLabel))`, then `PhaseEnd(capture_events.Verdict{OK: true})`. (Keep the stdout receipt reprint and `sink.StoreGroupReceipt` untouched.)
- Inactive mode: stream is Nop — all of this is a no-op; the rollback path is untouched by construction.

**Step 4: Run**

Run: `nix develop --command go test -race ./internal/capture/ -v`
Expected: ALL pass, including `TestSetupReporting_InactiveRollbackByteIdentity` and `TestSetupReporting_ActiveFinishIdempotent` UNCHANGED.

**Step 5: Commit**

```bash
git add internal/capture/
git commit -m "feat(capture): receipt phases + Finalize routed through the event stream"
```

---

### Task 5: ytdlp phase emissions

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/cutting_garden_plugin_ytdlp/capture.go`
- Test: `internal/cutting_garden_plugin_ytdlp/reporter_test.go`

**Step 1: Failing test** — extend the recording reporter (embed Nop) with `phaseStarts []string` and `phaseEnds []capture_events.Verdict`; drive `walkArtifacts` (and, where the harness permits, `CaptureRoot` with the fake yt-dlp stub) asserting:
- download path: PhaseStart "download <source>" before exec; PhaseEnd{OK:true} after a successful run (use the stub).
- write path: PhaseStart `write N artifacts (X MiB)`; on a fully-successful walk, PhaseEnd{OK:true}; with an injected failing file, PhaseEnd{OK:false, Diagnostic:{"entries": total, "failed": m}}.
- Byte-identity test stays green UNCHANGED.

**Step 2: Run to verify failure.**

**Step 3: Implement** in `capture.go`:
- Around `runYtdlp`: `r.PhaseStart("download " + source)` before; on success `r.PhaseEnd(capture_events.Verdict{OK: true})`; on error, `r.PhaseEnd(capture_events.Verdict{OK: false, Diagnostic: map[string]any{"error": err.Error()}})` before the existing failure handling (keep `sink.Failure` exactly as-is).
- The write phase: replace the `r.Log("downloaded, writing %d artifacts (%.1f MiB)")` line with `r.PhaseStart(fmt.Sprintf("write %d artifacts (%.1f MiB)", len(files), mib))` at the same point; after the loop, `r.PhaseEnd(...)` — OK when phase failCount == 0, else not-OK with the counts diagnostic. (The per-tick `Progress` emissions are unchanged.)

**Step 4: Run**

Run: `nix develop --command go test -race ./internal/cutting_garden_plugin_ytdlp/ -v`
Expected: PASS incl. byte-identity.

**Step 5: Commit**

```bash
git add internal/cutting_garden_plugin_ytdlp/
git commit -m "feat(ytdlp): download + write phases on the event stream"
```

---

### Task 6: git phase emissions

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/cutting_garden_plugin_git/protocol.go`, `internal/cutting_garden_plugin_git/incremental.go`
- Test: `internal/cutting_garden_plugin_git/reporter_test.go`

**Step 1: Failing test** — extend the recording reporter; assert on a real local capture:
- full path: PhaseStart `clone <remote> (<branch>)` (the existing "cloning…" Log may fold into this), PhaseEnd{OK:true} after clone; PhaseStart `store N objects` (N = structural count, matching the Plan), PhaseEnd{OK:true} after the write loop.
- incremental same-tip path: a PhaseEnd with `Directive{Kind: "skip", Reason: "no changes since prior capture"}` (replacing the bare Log).
- incremental delta path: clone-equivalent fetch phase + `store N delta objects` phase.
- Existing structural Plan/Progress assertions + the byte-identity test stay green (Plan/Progress calls unchanged).

**Step 2: Run to verify failure.**

**Step 3: Implement** — wrap the existing emission points (the Logs stay where they add detail, e.g. the resolved-tip Log remains inside the clone phase):
- `cloneBranchToMemory` call site: `r.PhaseStart(fmt.Sprintf("clone %s (%s)", remote, branchLabel(branch)))` before; `r.PhaseEnd(Verdict{OK: true})` after success (on error, not-OK with `{"error": err.Error()}` then propagate as today).
- `storeAllObjects`: PhaseStart `fmt.Sprintf("store %d objects", structuralCount)` right after the pre-count (so the count is real), PhaseEnd OK after the loop. Same shape in `storeDeltaObjects` (`store %d delta objects`).
- incremental no-change: replace the `r.Log("no changes since prior capture")` with `r.PhaseEnd(capture_events.Verdict{OK: true, Directive: &capture_events.Directive{Kind: capture_events.DirectiveSkip, Reason: "no changes since prior capture"}})` — preceded by a `r.PhaseStart("check prior capture")` at the top of the incremental probe so the skip has a phase to close.

**Step 4: Run**

Run: `nix develop --command go test -race ./internal/cutting_garden_plugin_git/ -v`
Expected: PASS incl. byte-identity + structural assertions.

**Step 5: Commit**

```bash
git add internal/cutting_garden_plugin_git/
git commit -m "feat(git): clone/store phases on the event stream (skip on unchanged)"
```

---

### Task 7: Whole-tree verification + design-doc cross-link

**Files:**
- Modify: `docs/plans/2026-06-06-unified-capture-events-tap-design.md` (mark Stage A "implemented @ <sha range>" in the Stage A section)

**Steps:**
1. `nix develop --command go build ./...` — clean.
2. `nix develop --command go test -race ./... ` — ALL green (the known cg#57 flake excepted; if it flakes, re-run that package once and note it).
3. `nix develop --command go vet ./...` + `gofmt -l internal/` — clean (pre-existing drift outside the diff excepted).
4. Manual eyeball available via `just debug-capture-fixture-progress` (fs emits no phases yet — expect only the receipt phase ✓) and a ytdlp/git capture with `-progress=always` (expect download/write or clone/store checkmarks persisting above the live bar).
5. Update the design doc's Stage A section status line; commit `docs(plan): mark Stage A implemented`.

---

## Out of scope (Stage B — separate plan)

tap `pkgs/ndjson` export + version bump; TAP-text sink rework (phases as top-level points, entries via `Subtest()`); custom NDJSON schema retirement (failures-always/successes-capped nesting); bats wire migration; dual-format window (`-format tap-legacy`/`json-legacy`).
