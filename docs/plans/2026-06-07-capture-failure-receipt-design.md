# Capture failure receipts — design

Date: 2026-06-07
Status: approved (brainstorm)
Issue: (filed at implementation time)

## Problem

A capture with failed entries reports only a count in the ephemeral
event stream (`failed: 2`) and exits 2. Nothing durable records *which*
entries failed or *why* — the per-failure detail (`stream.Failure(path,
err)` at every failure site) is discarded with the stream. Triage after
a long walk means re-running and watching.

Decisions from brainstorm (2026-06-07):

- Consumers: **human triage** now, **machine retry** later (format must
  be retry-ready; `capture --retry` ships separately).
- **Aborts recorded too**: an interrupted capture (signal) writes a
  failure receipt marking the abort, enabling resume-style retry later.
- Scope this iteration: **record + inspect**. No retry implementation.

## Approach (chosen: A)

A new content-addressed failure-receipt blob in the same store,
journaled in captures.log. Alternatives considered: enriching
captures.log only (not content-addressed, log lines balloon, weak for
retry); failure entries inside the fs-v1 receipt (breaks the locked v1
wire format, couples lifecycles) — rejected.

## Wire format

New hyphence type tag, registered as a sibling of fs-v1 in
`internal/capture_receipt`'s coder registry:

```
cutting_garden-capture_failures-v1
```

Metadata block (hyphence header, like the store-hint line):

- `ts` — RFC3339 UTC
- `outcome` — `failures` | `aborted`. When entries failed AND a signal
  cut the run short, `aborted` wins; the failure list is still present.
- `signal` — signal name, only when aborted
- `receipt` — markl id of the paired success receipt ("" if the success
  receipt write itself failed)
- `roots` — the capture's root args for this store group (retry needs
  the full root list, not just failed paths)
- `counts` — `captured`, `failed`

Body: NDJSON, one object per failure:

```json
{"root":"./","path":"a/b.ts","op":"blob-write","error":"read: permission denied"}
```

`op` ∈ `walk` | `stat` | `readlink` | `blob-write` | `receipt-write` |
`plugin` (protocol-plugin per-arg failure). Each `stream.Failure` call
site knows its op; plumbing becomes `Failure(path, op, err)` or a
failure collector alongside the stream (implementation choice deferred
to the plan).

## Write path

In `capture.Run`, failures collect during the run scoped per store
group (same scoping as the success receipt). After the success-receipt
write, each store group with failures writes one failure receipt.
On abort (ctx cancelled) the existing partial-receipt path runs first;
the failure receipt follows with `outcome: aborted`.

Printed next to the existing receipt line:

```
failures store=<name> id=<markl-id> count=N
```

**Spill fallback**: if the failure-receipt blob write errors (a flaky
store is often why entries failed), write the same bytes to
`$XDG_STATE_HOME/cutting-garden/failures/<ts>.ndjson` and print that
path instead. Triage info must survive the outage that caused it.

## CLI + captures.log

- New subcommand `cg failures <failure-receipt-id>`: resolves the store
  like `restore` (store-hint → `-store` override), pretty-prints the
  metadata then one line per failure. `-format json` re-emits raw
  NDJSON. Exit 0; exit 2 only when the receipt cannot be read.
- captures.log entries gain optional `outcome` and `failure_receipt_id`
  fields. Absent = clean capture. Old lines and old readers unaffected
  (unknown JSON fields ignored).

## Error handling

- The failure-receipt write never masks the run's own exit: the exit
  code stays driven by failCount exactly as today.
- Spill-fallback errors degrade to a `notice:` line.

## Testing

- Coder unit tests: round-trip, unknown-tag rejection (mirrors
  `v1_io_read_test.go`).
- Capture-level test: unreadable-file fixture → success receipt +
  failure receipt + log fields all asserted.
- Abort test: reuse the cancellation-test pattern (and
  `debug-sigint-capture` as the manual probe).
- `cg failures` golden-output test; bats lane addition end-to-end.

## Rollback

Additive everywhere: stop-writing is the rollback. No dual-architecture
period needed — no existing surface changes shape. The coder registry
already rejects unknown type tags; old binaries ignore the new log
fields.

## Tuning levers

- **Failure-entry cap** — current: uncapped. Rationale: typical failure
  counts are small. Change signal: a failure receipt >10 MB in practice
  (pathological walk failing every entry).
- **Error-string truncation** — current: 1 KiB per line. Rationale:
  keeps lines greppable while preserving wrapped-error chains. Change
  signal: triage repeatedly needing truncated context.

## Out of scope (tracked separately)

- `capture --retry <failure-receipt>` — the format above is
  deliberately sufficient (roots + per-failure root/path/op + abort
  marker), but retry semantics (re-walk, fold-into-new-receipt) are
  their own design.
- No-progress-during-upload papercut (the Ctrl-C trigger): progress
  events don't cover the blob write/upload phase against slow remote
  stores. Separate issue.
