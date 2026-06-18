---
status: proposed
date: 2026-06-08
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture/restore round-trip against a real CalDAV
  server (e.g. a self-hosted Radicale) is documented. Promote to
  `accepted` after the plugin ships in the default binary and a
  capture→restore cycle has been verified to reproduce a calendar's
  objects on a fresh server.
---

# CalDAV plugin

## Problem Statement

The CalDAV tool in [`amarbel-llc/bob`](https://github.com/amarbel-llc/bob)
(`packages/caldav`) is an **MCP server**: ~18 live tools for creating,
updating, completing, and deleting tasks and events against a CalDAV
endpoint. That surface is about *interactive mutation* of a calendar
from inside an assistant session. It has no notion of snapshotting a
calendar's state, content-addressing it, or restoring it later.

Cutting-garden's capture/restore/diff model is exactly that missing
half: a calendar is a tree of `text/calendar` objects behind a URL, and
the content-addressable blob model is a natural fit for archiving and
diffing it. The URI-scheme plugin registry (FDR 0005) was sized for
precisely this extension — a non-fs source registers under its own
scheme, returns the same `capture_receipt.EntryV1` shape the file plugin
returns, and slots into the existing `capture`, `restore`, and `diff`
dispatch loops without command-side changes.

This FDR specifies that plugin: `cutting-garden capture`,
`cutting-garden restore`, and `cutting-garden diff` learn to route
`caldav:` arguments through a CalDAV client that fetches (capture/diff)
or PUTs (restore) every `VTODO`/`VEVENT` resource as its verbatim
`text/calendar` body under the same receipt shape as the filesystem
plugin.

This is a **re-homing of the capability**, not a port of the MCP
surface: the live mutation tools do not come across, and bob's caldav
package is left untouched.

## Interface

### Accepted argument forms

The plugin claims the single `caldav` URI scheme in two shapes:

- hierarchical `caldav://[user[:pass]@]host[:port]/path` — resolved to
  `https://host[:port]/path` (TLS assumed; the common form).
- opaque       `caldav:<http(s)-url>` — the inner URL verbatim, the only
  way to reach a plain-HTTP endpoint (e.g. a LAN Radicale at
  `caldav:http://10.0.0.2:5232/`).

Unlike the yt-dlp plugin it claims **no** bare `https` hosts: a CalDAV
endpoint is indistinguishable from any other https URL by host, so it
must be opted into explicitly with the `caldav` scheme.

### Credentials

The endpoint host is taken from the URL. Credentials are resolved from
the URL's userinfo when present, otherwise from `CALDAV_USERNAME` /
`CALDAV_PASSWORD` (matching bob's caldav). With no resolvable
credentials the request is sent unauthenticated and the server's `401`
surfaces as the operation's error.

### Capture

`CaptureRoot` issues a Depth:1 PROPFIND from the base URL to discover
calendar collections, then a REPORT `calendar-query` per calendar for
each of `VTODO` and `VEVENT`, and streams every returned resource's raw
`calendar-data` into the destination blob store as one file entry:

- `EntryV1.Path` = the resource's **server-absolute path**, leading
  slash stripped (e.g. `dav/user/calendars/personal/abc.ics`).
- `EntryV1.Root` = the endpoint **origin** (`scheme://host[:port]`).
- `EntryV1.Type` = `file`, `Mode` = `0644` (synthetic — remote objects
  have no filesystem mode).

Per-calendar fetch failures and per-resource write failures are reported
on the event stream and counted; calendars that did succeed still
contribute their entries (the capture is not all-or-nothing).

### Diff

`ScanForDiff` re-fetches every resource and hashes it through the
caller's discard store, returning entries whose `Path` keys match what
`CaptureRoot` produced. The diff comparator then localizes added (`A`),
removed (`D`), and modified (`M`) resources exactly as for a filesystem
tree. Per-resource failures aggregate into a single error — diff is
read-only and atomic.

### Restore

`Restore` PUTs each captured body back to `<dest-origin>/<Path>`,
unconditionally (create-or-overwrite, no `If-Match`/`If-None-Match`).
Because `Path` is server-absolute, a capture→restore cycle reproduces
each object at its original path on the destination host; restoring to a
different host re-creates the same path layout there. The plugin PUTs
resources but does **not** MKCALENDAR — the destination's calendar
collections must already exist. Restore aborts on the first failure so a
partial restore surfaces loudly.

## TypeTag

`TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`), not a `…-caldav-v1` variant.
Resources are captured as regular file entries — byte-identical EntryV1
shape to fs captures — so a receipt mixing fs and caldav roots carries
one type-tag, and the captured `.ics` blobs restore cleanly through
either this plugin (to a server) or the filesystem plugin (to a local
directory). Same rationale as FDR 0003 (yt-dlp) and FDR 0006 (git).

## Non-Goals

- ~~**No iCalendar parsing.**~~ **(superseded — see note below.)**
  Originally: resources are opaque content-addressed blobs; bob's
  hand-rolled VTODO/VEVENT parser stays in bob where the MCP tools need
  structured access. **Superseded by the RFC 0011 protocol migration:**
  the parser was re-homed into cutting-garden (`plugins/caldav/ical/`,
  decoupling from bob) because the protocol receipt keys its entries on
  the calendar's **native identity** (UID + component), which requires
  reading those properties. The original rationale was "don't re-home a
  whole structured parser for a snapshot model that stores opaque bytes"
  — that calculus changed once native identity (RFC 0011) and the
  forthcoming CUD tools (FDR 0020) both needed structured access. The
  capture *blob* is still the verbatim `text/calendar` body; parsing is
  used for identity and (future) mutation, not to alter stored bytes.
- **No live mutation tools.** create/update/complete/delete are MCP
  concepts; capture/restore/diff snapshot and materialize state.
- **No MKCALENDAR on restore.** Recreating collections (and their
  metadata: display name, color, component set) is deferred until a
  use case appears; restore targets existing collections.
- **No VJOURNAL / free-busy.** Only `VTODO` and `VEVENT` are captured,
  covering the surface bob's caldav managed.

## Implementation Notes

- The CalDAV client (`client.go`) is a trimmed, context-aware port of
  bob's `internal/caldav/client.go`: PROPFIND, REPORT, and PUT only,
  with no parser and no MCP plumbing. The command's cancelable context
  is threaded into every request so SIGINT/SIGTERM unwinds in-flight
  I/O promptly.
- No new external dependency is introduced; the package uses only the
  stdlib plus the dewey/madder facades the other plugins already
  consume. There is no `gomod2nix.toml` or `flake.lock` change.
- Registration follows the standard peer-leaf pattern: `init()` calls
  `MustRegisterCapture/Restore/Diff`, and `cgapp.Build()` blank-imports
  the package so registration fires at binary startup.
