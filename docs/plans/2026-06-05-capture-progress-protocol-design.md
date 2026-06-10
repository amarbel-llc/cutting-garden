---
status: proposed
date: 2026-06-05
issues:
  - amarbel-llc/cutting-garden#28  # explore: charmbracelet TTY progress
  - amarbel-llc/cutting-garden#26  # capture/diff TAP color via amarbel-llc/tap
  - amarbel-llc/cutting-garden#10  # tree-capture pre-walk estimate + huh confirm-gate
builds-on:
  - docs/rfcs/0002-capture-plugin-protocol.md
  - amarbel-llc/cutting-garden#51  # RFC 0008 — JSON-RPC + FD-passed blob transport (proposed; renumbered from 0007)
  - amarbel-llc/cutting-garden#50  # RFC 0005 — protocol-only plugin resolution (proposed)
  - amarbel-llc/purse-first docs/features/0010-operation-viewport.md (proposed)
---

# Capture progress / plan / log: protocol notifications + TTY viewport

## Abstract

Three deferred cutting-garden issues are facets of one missing concept: a
**non-identity observability stream** that a capture/restore/diff plugin
emits and cutting-garden renders however the output sink demands. This
design adds that concept in two documents:

- **RFC 0006** — three new message kinds (`plan`, `progress`, `log`) as a
  normative wire contract: JSON-RPC notifications layered on the v2
  transport (#51), plus a 1:1 in-process Go `Reporter` interface.
- **FDR 0009** — how `cutting-garden capture|restore|diff` consume the
  stream: a WET TTY viewport (log tail + progress bar) modeled on
  purse-first FDR 0010, the `-progress` / `-color` flag surface, the #10
  large-capture confirm-gate, and TAP color (#26).

The events are **semantics, not identity** (mirroring dewey's
[purse-first#113] framing for HTTP status): they never touch blob bytes,
receipt shape, or `BatchOutput`, so they sit entirely outside RFC 0002's
byte-equivalence conformance.

The end UX is the real arbiter of the rendering details. The RFC's wire /
message contract is firm; the FDR's rendering choices are explicitly
prototype-driven and provisional.

[purse-first#113]: https://github.com/amarbel-llc/purse-first/pull/113

## Problem

Two observability surfaces exist today; neither carries plan/progress/log:

| Path | Plugins | What the orchestrator sees during the run |
|---|---|---|
| **EntryV1** (`CaptureRoot` → `capture_sink.Sink`) | fs | per-entry `Entry`/`Notice`/`Failure`/`StoreGroupReceipt`, then `Finalize` |
| **RFC 0002 protocol** (`CaptureProtocol`) | git, ytdlp | nothing until `{ReceiptDigest, ObjectCount}` returns — silent |

So `restore` is silent until its tally; `diff` is silent until the end;
protocol captures are silent for their whole duration. On a 50 GiB tree
the EntryV1 path is the opposite failure — a wall of per-entry `ok` lines
that scrolls past faster than a human can read (#28). There is no plan /
size estimate anywhere, so no progress-bar denominator and no
large-capture confirm-gate (#10). The TAP sink has no color (#26).

## Context: the live protocol work this builds on

Read from the open PRs, not assumed:

- **#51** rebuilds the subprocess transport as a **persistent JSON-RPC
  2.0 session** over an `AF_UNIX SOCK_SEQPACKET` socketpair, with blob
  bytes passed out of band via `SCM_RIGHTS` (`blob.begin`/`blob.finish`),
  schema `capture-plugin/v1`→`v2`. **But the shipping code in that PR is
  still v1** (`chrest capture-batch` stdin/stdout + per-blob
  `__write-blob`); the JSON-RPC transport is a *proposed spec*, not yet
  implemented. The in-process `Writer` / `WriteReceipt` path is real and
  exported (`pkgs/capture_plugin`).
- **#50** adds scheme-keyed base-`Plugin` resolution with
  capability-precedence dispatch, and establishes the repo pattern for
  **optional plugin capabilities probed by type assertion** (the
  `SourceValidator` interface: "when present the orchestrator MUST call
  it; when absent, skip — not an error"). The `Reporter` adopts this
  ethos.
- **#51** also shows the request-struct extension pattern (it added
  `StoreName` to `ProtocolCaptureRequest`/`ProtocolDiffRequest`); the
  `Reporter` field follows the same shape.
- **purse-first FDR 0010 (Operation Viewport)** already names
  *cutting-garden capture* as a motivating caller and defines the
  `operation_viewport` message vocabulary + two-tier API. It is proposed,
  not yet in dewey. clown's `tent_loader.go` and tap's TTY-viewport FDR
  are the existing WET precedents.

**Consequence for sequencing:** subprocess progress is *double-gated* — it
needs the v2 JSON-RPC transport *implemented* **and** the external
capturer (chrest) to opt in. In-process plugins (fs, git, ytdlp —
everything cutting-garden itself owns) carry full progress via the Go
`Reporter` immediately, independent of any transport work.

## Architecture

Three layers, each with one owner:

```
┌─ Layer 1: SOURCE — RFC 0006 ─────────────────────────────────────────┐
│  plugin → orchestrator: plan / progress / log                        │
│   • subprocess (v2): JSON-RPC notifications on the session            │
│       capture.plan · capture.progress · capture.log   (no-id)        │
│   • in-process: Go Reporter{Plan, Progress, Log} in the request      │
│  Non-identity · optional · byte-equivalence-preserved.               │
└──────────────────────────────────────────────────────────────────────┘
                                  │
┌─ Layer 2: ADAPTER — FDR 0009 (cg-side) ───────────────────────────────┐
│  orchestrator translates Reporter/notification events                │
│  → operation_viewport messages. Owns cg's UX decisions.              │
│  (cg's analogue of tap's "TAP-aware controller".)                    │
└──────────────────────────────────────────────────────────────────────┘
                                  │
┌─ Layer 3: RENDER — FDR 0009 (cg-local WET) ───────────────────────────┐
│  TTY  → cg-local viewport Model: spinner + rolling tail +            │
│         bar(when known), collapse-on-done, dump-on-fail.             │
│  pipe → TAP (colored, #26) / NDJSON, wire format unchanged.          │
│  flag: -progress auto|always|never   (NO_COLOR honored).             │
└──────────────────────────────────────────────────────────────────────┘
```

## RFC 0006 — message kinds, `Reporter`, wire notifications

### In-process `Reporter` (ships first; the 1:1 analogue)

```go
// Reporter carries non-identity observability from a plugin to the
// orchestrator. Opt-in per #50's capability ethos: a nil Reporter is a
// valid no-op, and a plugin MAY omit any or all calls.
type Reporter interface {
    Plan(ReportPlan)                  // ≤1×, before work; absent ⇒ spinner
    Progress(ReportProgress)          // N×, as work proceeds
    Log(format string, args ...any)   // freeform tail lines
}

type ReportPlan struct {
    Items int64  // est. total ops; 0 = unknown (indeterminate bar)
    Bytes int64  // est. total bytes; 0 = unknown
    Label string // e.g. "walking ./src"
}
type ReportProgress struct {
    Item  string // current item, e.g. "src/main.go"
    Items int64  // items done so far (numerator); SHOULD be monotonic
    Bytes int64  // bytes done so far
}
```

Added as a `Reporter` field on `CaptureRootRequest`,
`ProtocolCaptureRequest`, `RestoreRequest`, and `DiffScanRequest` — the
same struct-extension pattern #51 used for `StoreName`.

### Wire notifications (v2-gated; the normative forward spec)

JSON-RPC 2.0 *notifications* (no `id`, fire-and-forget), plugin →
orchestrator, interleaved with `blob.begin`/`blob.finish` on the same v2
session:

| Method | Params | Maps to |
|---|---|---|
| `capture.plan` | `{items?, bytes?, label?}` | `Reporter.Plan` |
| `capture.progress` | `{item?, items?, bytes?}` | `Reporter.Progress` |
| `capture.log` | `{text, level?}` | `Reporter.Log` |

Rules:

- `capture.plan` (if sent) MUST precede any `capture.progress`.
- `items` SHOULD be monotonic non-decreasing.
- All three are notifications; the orchestrator MAY coalesce or drop them
  (advisory). Receiving none is valid.
- **Semantics, not identity:** they never touch blob bytes, receipt
  shape, or `BatchOutput`. A conformant plugin produces byte-identical
  receipts whether or not it emits any. Explicitly outside RFC 0002's
  byte-equivalence conformance.

### `Notice` vs `Log`

The existing `Sink.Notice` (store-switch / shadow warnings) stays on the
durable EntryV1 record stream as today. `Reporter.Log` is the ephemeral
human tail. The viewport adapter MAY mirror a `Notice` into the tail;
`Notice` is not removed.

### Per-direction scope

- **capture** → `Reporter` (in-process) + notifications (v2 subprocess).
- **restore** → `Reporter` only (in-process; routed by receipt kind). Its
  bar denominator is free — the receipt already lists its entry count.
- **diff** → `Reporter` only (in-process `ScanForDiff` / `DiffProtocol`).

## FDR 0009 — the cutting-garden consumer

### WET viewport scope is *smaller* than FDR 0010

FDR 0010's `Run`/`RunBatch` exist to spawn a child, allocate a **PTY**,
and line-scan its output. cutting-garden's plugins are in-process (they
emit `Reporter` calls directly), and the future subprocess path delivers
*structured* JSON-RPC notifications — **neither needs PTY scanning**. So
we copy only FDR 0010's raw **`Model`** tier (spinner + rolling tail +
bar, driven by `p.Send(msg)`) and its message vocabulary, not the
child-process half. Lands as `internal/capture_viewport` (name TBD);
upstreaming to `dewey/pkgs/operation_viewport` is a tracked follow-up.

### Adapter (Layer 2)

A `Reporter` implementation that translates to viewport messages:
`Plan.Items` → bar denominator; `Progress` → bar advance + current-item
line; `Log` → tail line. Multi-root / multi-capture boundaries →
`OperationStarted`/`OperationDone`; command end → `BatchDone`. We mirror
FDR 0010's message *names* for upstream-compat, but our bar binds directly
to `Plan`/`Progress` items (the quantity cg actually has) rather than
0010's batch index.

### TTY behavior: collapse, don't coexist

On a TTY with `-progress` active, the per-entry Sink stream is
**collapsed into the viewport** (tail shows recent entries; bar shows
overall; the receipt summary and any failures print after). On a pipe,
**no viewport** — the full TAP/NDJSON stream prints byte-identically to
today. TTY and pipe are thus *alternates* (tap's collapse-on-TTY /
pass-through-on-pipe model), which is the direct fix for #28's wall of
per-entry lines. *(Tuning lever — see below.)*

### Flag surface

- `-progress auto|always|never` — viewport toggle; `auto` = stdout is a
  TTY ∧ `NO_COLOR` unset. Single-dash, matching cg's `-color`/`-store`/
  `-format`. Added to `capture`, `restore`, `diff`.
- `-color auto|always|never` — extended from `diff` to `capture` (#26).
  The TAP Sink renderer gains a termenv profile lifted from
  `internal/diff/render.go`: `ok`→green, `not ok`→red, `# log`→dim.
  Orthogonal to the viewport — it polishes the *pipe* path.
- `NO_COLOR` forces both off; non-TTY auto-disables both.

### Confirm-gate (#10)

Orchestrator-side, fires *after* a determinate `Plan` and *before* work,
via `huh` (already in the dependency closure). Only plugins that emit a
`Plan` with a real count/bytes can gate — fs does a cheap `Lstat`-only
pre-walk to produce it; streaming plugins emit no plan → no gate. Bypass:
`-yes` / `-no-confirm` flag, an env var, and auto-skip on non-TTY.

## Rollback strategy

Dual-architecture here is **structural and permanent**, not a temporary
window: the verbose TAP/NDJSON Sink stream is the existing architecture
and remains the non-TTY / `-progress never` path forever; the viewport is
a purely additive TTY overlay. The protocol/`Reporter` additions are inert
without a consumer — a nil `Reporter` is a no-op, RFC 0006 notifications
are `MAY`, and a plugin emitting them while nothing listens is harmless.

- **Rollback procedure:** single flag — `-progress never` reverts any
  invocation to today's exact behavior. Repo-wide: flip the `auto` default
  to `never` (one line), or revert the FDR's viewport-wiring commit; the
  RFC/`Reporter` plumbing stays inert.
- **Promotion criteria:** viewport renders capture/restore/diff on TTY
  across fs+git+ytdlp; non-TTY output byte-identical to pre-FDR (golden
  test); ~7 days with no user fallback to `-progress never`.

## Tuning levers

The end UX decides these; prototyping (below) is how they get set.

| Lever | Current | Revisit signal |
|---|---|---|
| Collapse vs coexist on TTY | collapse | prototype shows users want the record lines too |
| Tail height | 5 lines (FDR 0010 default) | too cramped / too tall in real captures |
| Repaint / coalesce rate | TBD (cap Hz; coalesce notifications) | flicker or CPU on fast walks; socket spam |
| Confirm-gate threshold | ~100k entries *or* ~256 MB (basis TBD) | fires on routine captures, or never on huge ones |
| Bar binding | whole-capture `Plan.Items` | multi-root captures read confusingly |

## Testing

- **Adapter unit tests:** `Reporter` calls → expected viewport messages.
- **No-op `Reporter` conformance test:** plugin output byte-identical with
  and without a `Reporter` — enforces the non-identity guarantee.
- **Non-TTY golden:** `capture`/`restore`/`diff` piped output
  byte-identical pre/post-FDR — the rollback safety net.
- **TAP color:** byte-exact SGR assertions mirroring
  `internal/diff/render_test.go` (Ascii passthrough; ANSI wraps).
- **Viewport `Model`:** `teatest` golden-frames, treated as
  prototype-validated, not locked.
- **bats:** non-TTY pass-through is the durable lane (TTY rendering is
  hard to assert in bats).

## Sequencing

RFC 0006 (notifications) + FDR 0009 (cg UX). RFC numbering is settled (see
the [#51 comment][rfc-alloc]): **0005** = #50 (protocol-only resolution),
**0006** = this design, **0007** = #51 (JSON-RPC transport). Pending #51
actually renumbering its file from 0005 to 0007.

[rfc-alloc]: https://github.com/amarbel-llc/cutting-garden/pull/51#issuecomment-4633090796

1. `Reporter` interface + request-struct fields + no-op default.
2. **Prototype the WET viewport `Model` + adapter** — the UX spike, the
   first concrete deliverable, and the arbiter of every rendering detail
   above.
3. Wire fs/git/ytdlp to emit `Reporter` calls.
4. Flags (`-progress`, `-color` on capture) + confirm-gate + TAP color.
5. Wire JSON-RPC notifications once the v2 transport (#51) lands and
   chrest opts in.

## Open questions

- **Viewport package name + home.** `internal/capture_viewport` vs a more
  generic `internal/operation_viewport`; bearing on how cleanly it
  upstreams to dewey.
- **`-progress` vs `--ui`.** cg uses single-dash flags; tap uses
  `--ui=auto|always|never` for the same concept. Cosmetic, but worth
  aligning the ecosystem vocabulary.
- **Confirm-gate threshold basis** — entry count, bytes, or both — and
  the default value (a tuning lever; prototype against real trees).

## References

- `docs/rfcs/0002-capture-plugin-protocol.md` — the receipt/merkle model
  and conformance these events sit outside of.
- amarbel-llc/cutting-garden#51 — JSON-RPC + FD-passed blob transport
  (the v2 session our notifications ride).
- amarbel-llc/cutting-garden#50 — protocol-only plugin resolution (the
  optional-capability pattern `Reporter` follows).
- amarbel-llc/purse-first `docs/features/0010-operation-viewport.md` — the
  viewport message vocabulary + two-tier API we WET and mirror.
- amarbel-llc/tap `docs/features/0001-tty-viewport.md` — the sibling
  TAP-aware controller; the `--ui` flag and collapse/hold precedent.
- amarbel-llc/clown `cmd/clown/tent_loader.go` — the original hand-rolled
  spinner + tail.
- amarbel-llc/purse-first#113 — "semantics not identity" framing.
- Issues: #28 (TTY progress), #26 (TAP color), #10 (pre-walk estimate +
  confirm-gate).
