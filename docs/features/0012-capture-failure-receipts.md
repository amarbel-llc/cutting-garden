---
status: experimental
date: 2026-06-07
promotion-criteria: a real-world failed capture triaged via `cg failures`
  without consulting the event stream; no tuning-lever adjustment needed
  for 2 weeks
---

# Capture failure receipts

## Problem Statement

A capture with failed entries reported only a count (`failed: 2`) on the
ephemeral event stream and exited 2 — nothing durable recorded *which*
entries failed or *why*. After a long walk (or an interrupted run
against a slow remote store), triage meant re-running the capture and
watching. The per-failure detail existed at failure time but was
discarded with the stream.

## Interface

Whenever a capture finishes with failed entries, or a signal aborts it,
each store group with failures writes a **failure receipt** — a
content-addressed blob of type `cutting_garden-capture_failures-v1` in
the same store as the success receipt — and prints its id alongside the
receipt line:

    receipt store=(default) id=blake2b256-… count=6018
    failures store=(default) id=blake2b256-… count=2

The blob carries a metadata block — `ts`, `outcome` (`failures` |
`aborted`; aborted wins when both apply), `signal` (the signal name,
aborts only), `receipt` (the paired success receipt's id), `root` lines
(the group's root args), `captured`, `failed` — and one NDJSON line per
failure: `{"root","path","op","error"}` with `op` ∈ `walk` | `stat` |
`readlink` | `blob-write` | `receipt-write` | `plugin`.

The captures.log entry for the group gains two optional fields:
`outcome` and `failure_receipt_id` (absent on clean captures, so
existing lines and readers are unaffected). A group whose success
receipt never landed journals a receipt-less line so the log still
leads to the failure receipt.

**Spill fallback:** when the failure-receipt blob write itself fails
(a flaky store is often *why* entries failed), the same bytes land in
`$XDG_STATE_HOME/cutting-garden/failures/<ts>.ndjson` and the line
reports `spill=<path>` instead of `id=`. Spilled receipts journal
`outcome` only.

The reader:

    cg failures [-store STORE_ID] [-format text|json] FAILURE_RECEIPT_ID

text (default) prints the metadata then one `<op>\t<root>\t<path>\t<error>`
line per failure; `-format json` re-emits the raw NDJSON body. Exit 0;
unreadable receipt → 2; bad flags/args → 64.

The failure-receipt write never alters the run's exit code — failCount
alone drives it, exactly as before.

## Examples

Triage a partially-failed capture:

    $ cg capture .work ./media
    …
    ✗ walk ./media
      entries: 6018
      failed: 2
    ✓ receipt store=.work
    failures store=.work id=blake2b256-f00… count=2
    $ cg failures blake2b256-f00…
    outcome: failures
    receipt: blake2b256-abc…
    roots: ./media
    captured: 6018  failed: 2
    blob-write	./media	class/17935/ts/video.ts	read: permission denied
    blob-write	./media	class/17938/ts/audio.ts	read: permission denied

Find the last failed capture from the log:

    $ jq -r 'select(.failure_receipt_id) | .failure_receipt_id' \
        "${XDG_STATE_HOME:-$HOME/.local/state}/cutting-garden/captures.log" | tail -n1

Machine consumption:

    $ cg failures -format json blake2b256-f00… | jq -r .path

## Limitations

- **No retry yet.** The format is deliberately retry-ready (full root
  list, per-failure root/path/op, abort marker + paired receipt id for
  resume), but `capture --retry <failure-receipt>` is a separate
  feature (tracked in the followup issue).
- Failure receipts are written per store group; orchestrator-level
  classify failures (bad args) attach to the first group's receipt.
- `Meta.Failed` is `len(Failures)`; a plugin reporting identity-less
  failures (FailCount > len(Failures)) undercounts it.
- The spill path is reported on stderr only — captures.log records the
  outcome but not the spill location.

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| failure-entry cap | uncapped | typical failure counts are small | a failure receipt >10 MB in practice (pathological walk failing every entry) |
| error-string truncation | 1 KiB/line | keeps lines greppable while preserving wrapped-error chains | triage repeatedly needing truncated context |

## More Information

- Design doc: `docs/plans/2026-06-07-capture-failure-receipt-design.md`
- Implementation plan: `docs/plans/2026-06-07-capture-failure-receipt.md`
- Motivating report: amarbel-llc/cutting-garden#74 (the silent
  upload phase that triggered the interrupt)
- Wire-format siblings: RFC 0001 (fs-v1 receipt rules), FDR 0001
  (restore), `internal/capture_failures/` (coder)
