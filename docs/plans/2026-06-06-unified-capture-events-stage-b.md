# Unified Capture Events — Stage B Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Flip the capture sinks onto the unified `capture_events.Stream` — phases as top-level TAP test points with entries nested as subtests, the custom NDJSON schema replaced by tap-ndjson records — behind a dual-format legacy window.

**Architecture:** Plugins stop calling `capture_sink.Sink` directly; entries/failures flow over the Stream. Exactly ONE renderer consumes the Stream per run: the viewport (TTY), the new **tap-text renderer** (tap's exported `Writer` with `Subtest()`), the new **tap-ndjson renderer** (canonical structs from tap's `pkgs/ndjson`, gated on tap#33), or the **legacy bridge** (forwards Entry/Failure 1:1 to the old sinks — byte-identical output for `-format tap-legacy|json-legacy` during the window). Design: `docs/plans/2026-06-06-unified-capture-events-tap-design.md` (Stage B + rollback sections).

**Tech Stack:** Go 1.26 devshell; `github.com/amarbel-llc/tap/go/pkgs/writer` (already imported); `pkgs/ndjson` (NEW — tap#33, handshake with tap/brave-aspen); bats under `zz-tests_bats/`.

**Rollback:** Dual-format window. `-format tap-legacy` / `json-legacy` reproduce today's exact wire via the bridge + untouched `capture_sink`. **Promotion criteria** (records in the design doc): all bats migrated + user pipelines confirmed + ~2 weeks with no `-legacy` use → delete the legacy formats, the bridge, and `capture_sink` in one commit. Rollback during the window = a `-format` flag flip per invocation.

---

## Conventions

- Devshell commands; gpg-signed commits (STOP on signing failure); `-race` per touched package at each task end.
- Identity guarantee unchanged: receipts/blob bytes are never influenced by events (pinned tests stay green throughout).
- **Gating: RESOLVED before execution.** tap#33 was already satisfied on tap's pushed master — `go/pkgs/ndjson` exists (façade commits d354001 + 64d3059, master tip `5094a06`, verified == origin/master by tap/brave-aspen). It exports TestRecord, DirectiveValue, PlanRecord, BailoutRecord, SummaryRecord, SummaryDiagnostic, Output, WriteAll/WriteSplit, Aggregator. tap IS a bridged flake input (CLAUDE.md): Task 4's first step is `nix flake update tap` + `nix develop --command go get github.com/amarbel-llc/tap/go@5094a06` + `just update-go`, then git add flake.lock/go.mod/go.sum/gomod2nix.toml.

---

### Task 1: The legacy bridge + Entry/Failure migration

**Promotion criteria:** the bridge + `capture_sink` retire when the legacy formats are removed (post-window).

**Files:**
- Create: `internal/capture_events/sink_bridge.go` + test (or in `internal/capture/` if importing capture_sink from capture_events creates a cycle — check: capture_sink imports capture_receipt + tap; capture_events imports capture_receipt. capture_events→capture_sink would be NEW; prefer the bridge in a new `internal/capture_render_legacy/` package to keep capture_events dependency-free).
- Modify: `internal/cutting_garden_plugin_file/plugin.go` (walkRoot: `sink.Entry/Failure` → `stream.Entry/Failure`; CaptureRoot threads the stream), `internal/cutting_garden_plugin_ytdlp/capture.go` (same for its sink call sites), `internal/cutting_garden_plugins/plugin.go` (REMOVE the `Sink` field from `CaptureRootRequest` once nothing reads it — compile-driven), `internal/capture/capture.go` (construct the bridge; pass it as the Reporter/Stream on the pipe path; keep `SetStore`/`Notice`/`StoreGroupReceipt`/`Finalize` as DIRECT orchestrator→legacy-sink calls in their exact current order).
- Tests: bridge unit test (Entry/Failure forward 1:1, everything else no-op); ALL byte-identity pins + `TestSetupReporting_InactiveRollbackByteIdentity` must pass — in Stage B Task 1 the pipe output MUST remain byte-identical (assert by running a capture fixture before/after if the existing pins don't already cover entry ordering; extend the non-TTY golden if needed).

**Bridge shape:**

```go
// Package capture_render_legacy bridges the unified Stream onto the
// historical capture_sink.Sink so the legacy wire formats stay
// byte-identical during the dual-format window. Phases, Plan, Progress,
// Log, and Finalize are DELIBERATE no-ops here: the legacy formats never
// carried them, and adding lines would break byte-identity. SetStore /
// Notice / StoreGroupReceipt / Finalize(sink) remain direct orchestrator
// calls on the wrapped sink, exactly as before Stage B.
type SinkBridge struct {
	capture_events.Nop
	sink capture_sink.Sink
}

func NewSinkBridge(s capture_sink.Sink) *SinkBridge { return &SinkBridge{sink: s} }
func (b *SinkBridge) Entry(e capture_receipt.EntryV1) { b.sink.Entry(e) }
func (b *SinkBridge) Failure(src string, err error)   { b.sink.Failure(src, err) }
```

Orchestrator note: on the pipe path the Stream given to plugins = the bridge; on the TTY path = the viewport adapter (whose Entry/Failure are no-ops until Tasks 3-4 give pipe renderers their own treatment — the TTY tail intentionally does NOT show per-entry lines; entries are progress-bar fodder there).

Steps: failing bridge test → bridge → migrate fs walkRoot (its `sink` param becomes the stream; signature change ripples to its callers/tests) → migrate ytdlp call sites → remove `CaptureRootRequest.Sink` compile-driven → full suite + byte-identity. Commit: `refactor(capture): entries flow over the Stream; legacy sinks behind a bridge`.

---

### Task 2: git fetch-fallback verdict → TODO directive

**Files:** `internal/cutting_garden_plugin_git/incremental.go` + reporter_test.go.

The incremental fetch-failure currently emits `PhaseEnd{OK:false, Diagnostic:{error}}` then soft-falls-back to a successful full capture — in Stage B's TAP output that's a bare `not ok` in a passing run (strict harnesses fail it). Change to TAP's tolerated-failure form:

```go
r.PhaseEnd(capture_events.Verdict{
	OK:        false,
	Directive: &capture_events.Directive{Kind: capture_events.DirectiveTodo, Reason: "fell back to full capture"},
	Diagnostic: map[string]any{"error": ferr.Error()},
})
```

Viewport renders it via the existing directive branch (dim `↷ … # TODO fell back to full capture`). Test: assert the directive kind/reason + diagnostic on the fetch-failure path (the existing harness can simulate a fetch failure — if it can't cheaply, assert at the unit level on the emission site per the existing test patterns). Pinned tests unchanged. Commit: `fix(git): incremental fetch fallback is a TODO verdict, not a bare failure`.

---

### Task 3: tap-text renderer

**Promotion criteria:** replaces the legacy tapSink as the `-format tap` default in Task 5.

**Files:** Create `internal/capture_render_tap/renderer.go` + test.

A `capture_events.Stream` implementation over tap's exported writer (`tap "github.com/amarbel-llc/tap/go/pkgs/writer"`):

- `PhaseStart(desc)`: opens a pending phase (hold desc; open a `tw.Subtest(desc)` writer for the phase's entries lazily on first Entry/Failure).
- `Entry(e)`: subtest `Ok(<legacy formatTAPEntry text>)` under the current phase (reuse/lift the exact `formatTAPEntry`/`joinRootPath` formatting from capture_sink so per-entry text stays familiar).
- `Failure(src, err)`: subtest `NotOk(src, <tap_diagnostics.FromError(err)>)` (same diagnostic shaping as the legacy sink).
- `PhaseEnd(v)`: close the subtest writer (its plan), then the parent verdict: `Ok(desc)` / `NotOk(desc, diag)` / `Skip(desc, reason)` / `Todo(desc, reason)` per `v` (directive precedence as in the viewport; Diagnostic → the writer's diagnostic forms). tap.Writer auto-numbers (renderer-local counters by design).
- `Log(...)`: `tw.Comment(...)`.
- `Plan/Progress`: no-ops (ephemeral).
- `Finalize(err)`: `err != nil → tw.BailOut("%v", err)`; then `tw.Plan()` (trailing plan over the phase count).
- Orchestrator's receipt phase (see Task 5 note) carries `{store, receipt_id, count}` in its Verdict.Diagnostic so the machine-readable receipt survives the legacy sink's retirement.

Tests: golden-string tests driving the renderer with a scripted event sequence (two phases, entries incl. a failure subtest, a skip phase, log comments, finalize) asserting the exact TAP-14 text (subtest indentation, directives, trailing plan). Verify against tap's own writer behavior rather than hand-rolled expectations where possible. `-race`. Commit: `feat(capture_render_tap): TAP-text renderer for the unified stream`.

---

### Task 4: tap-ndjson renderer — GATED on tap#33

**Files:** dep bump (go.mod/go.sum, + flake input if tap is bridged — CHECK gomod.nix); create `internal/capture_render_ndjson/renderer.go` + test.

After brave-aspen's sha arrives: bump, then implement a Stream renderer emitting tap-ndjson records line-per-record with a `json.Encoder`:

- Per phase: buffer a `ndjson.TestRecord{Type: "test", N: <renderer counter>, Description: desc, ...}`; entries nest as `Subtest` records — **failures ALWAYS appended; successes appended up to `successSubtestCap = 1000` (TUNING LEVER)**, beyond it dropped with the truncation noted in the diagnostic. Every phase record's `Diagnostic` merges the verdict diagnostic with `{"entries": N, "failed": M}` (+ `"subtests_truncated": true` when capped).
- Entry mapping: `TestRecord{Description: <root/path>, OK: true, Diagnostic: {type, mode, size, blob_id|target, store}}`; Failure: `{Description: src, OK: false, Diagnostic: {error}}`. Line: 0 (no source lines — document).
- Directives → `ndjson.DirectiveValue{Kind, Reason}`.
- `Finalize(err)`: emit `PlanRecord{Count: phases}`, then `BailoutRecord` if err != nil, then `SummaryRecord` with passed/failed/skipped/todo/total/plan_count/bailed/valid=true/diagnostics=[] computed from the phase verdicts.
- `Log`: dropped (schema has no comment record — design decision, documented).

Tests: golden NDJSON line tests for the scripted sequence; a cap test (1002 entries → 1000 success subtests + truncation marker + correct counts); cross-check one record against tap's `format_ndjson.bats` example shapes (field names/null conventions — all fields present, null via pointers/nil slices per the spec). Commit: `feat(capture_render_ndjson): tap-ndjson renderer (failures-always, successes-capped)`.

---

### Task 5: `-format` rework + the dual-format window

**Files:** `internal/capture/capture.go` (+ progress.go helpers), orchestrator wiring; manpage/help text.

- Replace the madder `output_format.Format` flag with a cg-local format value: `auto|tap|json|tap-legacy|json-legacy` (string flag + validate like `-progress`). `auto` keeps today's TTY-resolution semantics: TTY→`tap`, pipe→`json` — except when the progress viewport is ACTIVE, which continues to suppress pipe rendering exactly as today.
- Renderer selection: `tap` → capture_render_tap; `json` → capture_render_ndjson; `*-legacy` → legacy sinks via the bridge (+ the direct SetStore/Notice/StoreGroupReceipt/Finalize calls preserved ONLY on the legacy path — factor the legacy-only calls behind the same selection).
- Enrich the orchestrator's receipt phase: `PhaseEnd(Verdict{OK: true, Diagnostic: map[string]any{"store": label, "receipt_id": id, "count": n}})` so the new formats carry the receipt id machine-readably (legacy keeps `StoreGroupReceipt`).
- Flag help documents the window + that `-legacy` forms are deprecated-on-arrival with the promotion criteria.
- Tests: validate-format; selection table test; legacy path byte-identity (the existing pins + a golden run via the bridge); new-format smoke through `Run` with piped stdout.
Commit: `feat(capture): -format gains tap|json (unified renderers) with *-legacy window`.

---

### Task 6: bats wire migration

**Files:** `zz-tests_bats/` (enumerate: every .bats asserting capture stdout shapes — capture/restore/diff lanes; the implementer lists them first and reports).

- Existing assertions on the OLD shapes: switch the invocation to `-format tap-legacy`/`json-legacy` (proving the window) OR migrate the assertion to the new shape — migrate the PRIMARY lanes to the new default formats (subtest-indented TAP; tap-ndjson records parsed with jq: `.type=="test"`, `.subtest`, `.diagnostic.receipt_id`), keep ONE legacy lane per format as the window's regression net (delete with the window).
- `nix build .#bats-capture` green.
Commit: `test(bats): migrate capture wire assertions to tap/tap-ndjson; legacy window lanes`.

---

### Task 7: Whole-tree verification + docs

- `go build ./...`, `go test -race ./...`, vet, gofmt (pre-existing import-order drift in capture_receipt/command may be swept here or reported).
- Design doc: mark Stage B implemented; record the promotion criteria + the date the window opened; note the Log-dropped-in-ndjson and Line:0 decisions.
- Manual eyeball: `cg capture … | cat` (new TAP), `-format json | jq .`, `-format tap-legacy` (old), TTY viewport unchanged.
- Final holistic review + merge.

---

## Out of scope

Legacy-format REMOVAL (separate post-window commit per promotion criteria); RFC 0006 wire notifications (carry tap-ndjson records — spec update after Stage B proves the shapes); restore/diff phase adoption.
