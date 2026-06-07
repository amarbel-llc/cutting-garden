---
status: approved
date: 2026-06-06
supersedes-in-part: the Reporter-alongside-Sink split in
  docs/plans/2026-06-05-capture-progress-protocol-design.md (the
  Reporter contract is subsumed into the unified stream below)
---

# Unified capture events: phases as TAP, one stream, three renderers

## Context

The `-progress` viewport landed with an ephemeral `Reporter`
(Plan/Progress/Log) alongside the durable per-entry `capture_sink.Sink`
(TAP text + a custom ad-hoc NDJSON schema). The next UX ask — persistent
`✓` lines per completed phase that push the live viewport down — exposed
that the architecture has no structured notion of a *phase*. Rather than
bolt on a freeform `PhaseDone(label)`, this design unifies capture
observability around **TAP semantics**, mimicking the NDJSON TAP
protocol from `amarbel-llc/tap` (`doc/tap-ndjson.7.scd`;
`go/internal/bravo/ndjson`): phases are test points, entries nest as
subtests, and the viewport implements tap's own TTY-viewport FDR
rendering (collapse-on-`ok`, hold-on-`not ok`).

Decisions made during brainstorm (2026-06-06):

1. **Full unification** — one structured event stream with three
   renderers (tap-text, tap-ndjson, viewport). This deliberately
   reverses the earlier "separate Reporter alongside Sink" call, now
   that the end-state is clear. Blast radius is cutting-garden only
   (receipts/identity untouched; bats + the user's pipelines are the
   only wire consumers) — the protocol is young, so now is the cheapest
   time.
2. **Staged**: Stage A lands the phase layer + viewport checkmarks with
   the pipe wire **byte-identical**; Stage B flips the sinks onto the
   unified stream (the wire change) behind a dual-format window.
3. **NDJSON nesting policy**: failed entries ALWAYS nest as subtests
   (failures are few and machine-actionable — tap's split philosophy);
   successful entries nest up to a cap (tuning lever); every phase
   record carries a counts diagnostic.
4. **Schema source**: tap exports `pkgs/ndjson` (the same dagnabit
   facade move as cutting-garden PR #51's `pkgs/capture_plugin`); cg
   imports the canonical structs. Gates Stage B only.

## The unified contract (`capture_events`, replaces Sink + Reporter)

One producer-facing interface; nil-safe no-op default; all events are
**semantics, not identity** (byte-identity tests per plugin remain the
guarantee).

| Event | tap mapping | Notes |
|---|---|---|
| `PhaseStart(description)` | begins a test point; viewport: title + reset tail/bar/bytes | flat phases v1; multi-root context in the description (nesting lever). The reset retires cg#56's bytesDone phase-bleed properly. |
| `PhaseEnd(Verdict)` | `ok/not ok N - desc` → ndjson `test` record | `Verdict{OK bool; Directive *Directive{Kind "skip"\|"todo", Reason}; Diagnostic map[string]any}`. `n` consumer-assigned (tap writers auto-number). git incremental no-change ⇒ `ok # SKIP`. |
| `Entry(EntryV1)` | subtest point under the current phase (Stage B) | successes capped in ndjson |
| `Failure(source, err)` | failing subtest under the current phase | ALWAYS nested in ndjson |
| `Log(format, ...)` | TAP-text `# comment` + viewport tail; **dropped in ndjson** (schema has no comment record — stay faithful) | absorbs the old `Notice` |
| `Plan(estimate)` / `Progress(...)` | NOT TAP — ephemeral viewport bar denominator/numerator | distinct from the TAP plan record |
| `Finalize(err)` | trailing TAP plan (`1..N` = phase count) + ndjson `summary`; plan/abort errors ⇒ `bailout` | summary counts = phases passed/failed/skipped/todo |

Renderers:
- **tap-text** — tap's exported `Writer` (`Ok/NotOk/Skip/Subtest/Comment/Plan`).
- **tap-ndjson** — tap `pkgs/ndjson` records (`plan`/`test`/`bailout`/`summary`); per-phase buffering per the nesting policy.
- **viewport** — `PhaseEnd{OK:true}` → persistent green `✓ <desc>` via `tea.Println` (scrollback; live region pushes down); `{OK:false}` → tail persists + red `✗ <desc>` + rendered diagnostic; skip → dim `↷ <desc> # SKIP <reason>`. Verbatim tap TTY-viewport FDR semantics.

## Stage A — phases + viewport (wire byte-identical)

Status: implemented 2026-06-06 (commits 7620710..42c438f on calm-juniper).

- Introduce `capture_events` (contract + nop + auto-numbering helper).
- Orchestrator adapts: plugins emit on the unified stream; a thin shim
  forwards `Entry`/`Failure` to the legacy `Sink` exactly as today —
  TAP/NDJSON pipe output stays byte-identical (existing rollback test +
  bats untouched).
- Viewport implements the phase rendering above.
- Emissions: ytdlp (`download <source>`, `write N artifacts`); git
  (`clone <remote>`, `store N objects`; incremental no-change = SKIP);
  orchestrator (`receipt store=<s>` per store-group).
- The old `Reporter` is subsumed: Plan/Progress/Log move onto the new
  contract; request structs swap field type (internal API only).

## Stage B — sink unification (the wire change)

- Prereq: tap exports `pkgs/ndjson`; cg bumps tap.
- TAP-text sink: phases as top-level test points; entries via
  `Writer.Subtest()`; `Log` → comments; trailing plan + summary line.
- NDJSON sink: custom `entryRecord`/`summaryRecord` schema retired in
  favor of tap-ndjson records; failures-always/successes-capped
  nesting + counts diagnostic per phase.
- bats wire assertions migrate; the Stage-A shim is deleted.

### Stage B wire notes (window limitations)

Known deltas between the unified formats and the legacy wire during
the dual-format window — accepted, not bugs:

- **No per-entry store stamping on `json`.** The legacy jsonSink
  stamped every entry record with the active `store` id. On tap-ndjson
  the store lives once in the receipt phase's verdict diagnostic
  (`{store, receipt_id, count}`); entry subtests attribute to a store
  by ordering (they precede their group's receipt phase). `json-legacy`
  retains the per-entry stamping for the duration of the window.
- **Store-switch and shadow notices are TAP comments on `tap`,
  dropped on `json`.** Notices route through `Stream.Log`; the
  tap-ndjson schema has no comment record type, so the ndjson renderer
  drops them. `json-legacy` keeps the old stderr routing. Consumers
  needing the shadow warning machine-readably should not exist yet; if
  they appear, that's a schema conversation with tap, not a cg patch.
- **`Line: 0` on every ndjson record.** Capture events carry no
  source-line provenance (there is no TAP text document for lines to
  index into); tap-ndjson requires the field, so it is pinned to 0.

## Rollback

- **Stage A**: purely additive; `-progress=never` and piped output
  byte-identical (pinned by test). Revert = drop viewport wiring; the
  contract is inert without consumers.
- **Stage B**: dual-format window — new shapes default; `-format
  tap-legacy` / `-format json-legacy` preserve today's exact output.
  **Promotion criteria:** all bats migrated + user pipelines confirmed
  + ~2 weeks with no `-legacy` use → remove legacy formats in one
  commit. **Rollback during the window:** a single `-format` flag flip.

## Tuning levers

| Lever | Initial | Change signal |
|---|---|---|
| Success-subtest nesting cap (ndjson) | 1000/phase | memory pressure or consumer need for full listings |
| Checkmark rendering | `✓ <desc>` glyph (literal `ok N - desc` available via sinks) | preference for literal TAP text on the TTY |
| Phase granularity | flat, coarse (2-3 per plugin) | phases feel chunky/noisy in real captures |
| Multi-root structure | flat; root context in descriptions | multi-root captures read confusingly → nest by root |

## Cross-repo dependencies

- **tap**: export `pkgs/ndjson` (dagnabit facade; mirrors cg PR #51's
  capture_plugin export). Stage-B gate only; coordinate via a tap issue
  when Stage A is merged.

## Testing

- Contract conformance: nil-safety; per-plugin byte-identity (events
  never change receipts).
- Viewport: collapse/hold/skip rendering states; phase-boundary reset
  (bar/bytes/tail) pinned.
- Stage A: pipe-output byte-identity (existing rollback test extended).
- Stage B: golden TAP-text and ndjson fixtures mirrored from tap's own
  bats examples (`format_ndjson.bats`) so cg's output validates against
  tap's reference shapes.

## References

- tap: `doc/tap-ndjson.7.scd` (normative record schema);
  `go/internal/bravo/ndjson/ndjson.go` (reference structs);
  `docs/features/0001-tty-viewport.md` (collapse/hold semantics);
  `docs/plans/2026-05-12-tap-format-ndjson-design.md` (split routing).
- cg: `docs/plans/2026-06-05-capture-progress-protocol-design.md` (the
  feature this extends); cg#56 (bytesDone phase-bleed, retired by
  PhaseStart reset); RFC 0006/FDR 0009 numbering (wire notifications
  will carry tap-ndjson records for phases).
