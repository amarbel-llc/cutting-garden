---
status: experimental
date: 2026-06-08
promotion-criteria: |
  Promote to `accepted` once real LocalSend transfers from a phone have
  landed as fs-v1 receipts over a sustained period without the receiver
  needing protocol-level fixes, and the home-manager service module
  (#72) question is resolved one way or the other.
---

# FDR 0011 — `serve`: LocalSend receiver bound to Tailscale

> Originally recorded inline as "Status: implemented"; converted to the
> standard FDR frontmatter. Issue: amarbel-llc/cutting-garden#69.

## Summary

A new top-level subcommand, `cutting-garden serve`, runs a long-lived
[LocalSend](https://github.com/localsend/protocol) **receiver**. Every
incoming LocalSend transfer (one or more files, or a whole directory
tree) lands as a normal cutting-garden capture: each file is written as
a content-addressed blob and the session is folded into one
`cutting_garden-capture_receipt-fs-v1` receipt — the exact wire format
`capture` produces, so `restore` and `diff` work against serve-produced
receipts unchanged.

The listener binds to the host's **Tailscale** address by default
(CGNAT `100.64.0.0/10` for IPv4, the `fd7a:115c:a1e0::/48` ULA for
IPv6), so the receiver is reachable over the tailnet and not on the
public internet or the broadcast LAN. `-bind` overrides with an explicit
host for non-Tailscale setups.

## Why a receipt, not a directory

The issue frames LocalSend as "a transport for receiving directory
captures." Rather than inventing a new receipt kind (RFC 0002 protocol
receipt), serve reuses the filesystem receipt (`fs-v1`): a LocalSend
transfer *is* a set of files with relative paths, which is exactly what
`EntryV1` models. Reuse keeps `restore`/`diff` free.

LocalSend sends a folder as a flat file map whose `fileName` carries the
relative path (`docs/sub/a.txt`); it never sends directory entries.
serve synthesizes the intermediate `dir` entries so `restore`'s
`O_CREATE|O_EXCL` file writes find their parents (entries sort by
`(Root, Path)`, so a dir always precedes its children).

## Protocol surface (LocalSend v2)

| Method + path | Behavior |
|---|---|
| `GET  /api/localsend/v2/info` | our device-info JSON |
| `POST /api/localsend/v2/register` | discovery handshake; echoes our info |
| `POST /api/localsend/v2/prepare-upload` | open a session; allocate a per-file token; `204` if no files, `409` if a session is already active |
| `POST /api/localsend/v2/upload` | stream one file's bytes into the blob store; finalize the receipt when the last file lands |
| `POST /api/localsend/v2/cancel` | drop the active session (a partial receipt is still written for files already received) |

UDP multicast discovery (224.0.0.167) is intentionally **out of scope**:
Tailscale has no LAN multicast. Senders reach the receiver through
LocalSend's "favorites"/manual-IP path or the HTTP `register` handshake.

### One session at a time

LocalSend receivers handle a single transfer at a time; serve enforces
this — a `prepare-upload` while a session is live returns `409`. This
also sidesteps cross-session blob-write contention.

### Path safety

`fileName` is attacker-controlled. serve rejects absolute paths, any
`..` segment, and NUL bytes at upload time (`400`), so a hostile sender
cannot write a receipt that escapes the restore destination. `restore`'s
own `ValidateEntries` remains the second line of defense.

## Durability

Mirrors `capture`'s partial-receipt-on-abort contract: a session that is
cancelled (or whose process is signalled) after some files arrived still
writes a receipt for exactly the files received, and appends a line to
`captures.log`. Blobs are content-addressed, so re-sends are free.

A session whose last pending file *fails* (unsafe name, blob-write
error) finalizes the same way: any files that did arrive are folded
into a receipt and the single-session slot is released, so a failed
transfer never wedges the receiver waiting for a cancel the protocol
does not require senders to issue.

## Exit semantics

serve runs until interrupted. SIGINT/SIGTERM/SIGHUP cancel the
`errors.Context`; the HTTP server drains via `Shutdown`. As with every
cg command, a signal-cancelled context surfaces as a non-zero exit (the
framework's `Signal` cause → exit 2) — there is no "clean" exit code for
a daemon stopped by a signal.

## Flags

- `-bind HOST` — explicit listen host; overrides Tailscale auto-detect.
  Use `-bind 0.0.0.0` to deliberately expose on all interfaces.
- `-port N` — listen port (default `53317`, the LocalSend default).
- `-store STORE_ID` — destination blob store (default store if omitted).
- `-alias NAME` — device alias advertised to senders (default: hostname).

## Non-goals

- Sending (serve is receive-only; `download:false`).
- UDP multicast discovery / announce.
- HTTPS/TLS (Tailscale already provides the encrypted transport; the
  LocalSend `protocol` field is advertised as `http`).
