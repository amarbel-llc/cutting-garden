# caldav (plugins/caldav)

The CalDAV capture/restore/diff backend for cutting-garden. Lives outside `internal/` (in `plugins/`), consuming the public plugin SDK (`pkgs/`, RFC 0009) like an out-of-tree plugin would — it imports `pkgs/`, never `internal/` (enforced by the `internal/sdklayering` guard). Registered in
`init()` under the single `"caldav"` URI scheme, in two argument forms
(mirroring the yt-dlp/git opaque-vs-hierarchical split, see `url.go`):

- hierarchical `caldav://[user[:pass]@]host[:port]/path` → `https://…`
  (the common form; TLS assumed).
- opaque       `caldav:<http(s)-url>` → the inner URL verbatim (the only
  way to reach a plain-HTTP server, e.g. a LAN Radicale at
  `caldav:http://10.0.0.2:5232/`).

This is the cutting-garden home of the CalDAV tool that previously lived
as an **MCP server** in [`amarbel-llc/bob`](https://github.com/amarbel-llc/bob)
(`packages/caldav`). Bob's package is untouched; this is a re-homing of the
capability as a native cutting-garden plugin, not a port of the MCP
surface. The 18 live MCP tools (create/update/complete tasks, etc.) did
**not** come across — a capture/restore/diff plugin snapshots and
materializes calendar state, it does not offer interactive mutation.

## Credentials

The endpoint host comes from the URL; credentials resolve by the RFC 0007
precedence (`connectionFromArg`, `config.go`): the URL's userinfo when
present, else a configured account matched by host + longest path prefix
(`[[caldav.accounts]]`, injected via `SetConfiguredAccounts`; password
from the account's `password_env`), else the global `CALDAV_USERNAME` /
`CALDAV_PASSWORD` (the same env vars bob's caldav used). A request with no
resolvable credentials is sent unauthenticated — the server's 401 is
surfaced as the capture/diff/restore error.

`Roots` (`config.go`) exposes the configured accounts' endpoints as
credential-free traversal roots for the `RootProvider` capability
(`list`/`mcp` with no argument). See [RFC 0007](../../docs/rfcs/0007-config-subsystem.md).

## What lives here

- `Plugin.CaptureRoot` (`capture.go`) — PROPFIND the endpoint for
  calendar collections, then REPORT each calendar's `VTODO` and `VEVENT`
  resources and stream every resource's **raw `text/calendar` body** into
  the destination blob store as one file entry. `EntryV1.Path` is the
  resource's server-absolute path (e.g. `dav/user/calendars/personal/abc.ics`)
  and `EntryV1.Root` is the endpoint origin.
- `Plugin.ScanForDiff` (`diff.go`) — the read-only analogue: re-fetch
  every resource, hash it through the caller's discard store, and return
  entries whose keys match what `CaptureRoot` produced so the diff
  comparator localizes added/removed/modified resources.
- `Plugin.Restore` (`restore.go`) — PUT each captured body back to its
  server-absolute path on the destination origin (`<dest-origin>/<path>`),
  unconditionally (create-or-overwrite). Non-file entries are skipped.
- `client.go` — a minimal, context-aware CalDAV HTTP client
  (PROPFIND / REPORT / PUT) with no iCalendar parser: resources are
  opaque content-addressed blobs. Ported and trimmed from bob's
  `internal/caldav/client.go`.
- `url.go` — argument coercion (`baseURLFromArg`, `connectionFromArg`),
  origin extraction, and the server-relative `Path` key.

## No iCalendar parser, no new dependencies

Bob's caldav carried a hand-rolled VTODO/VEVENT parser (for the MCP
tools' structured fields). The plugin needs none of it: each resource is
captured as its verbatim bytes, which is exactly the content-addressed
shape the receipt machinery wants and the most faithful thing to restore.
The package depends only on the stdlib plus the dewey/madder facades the
other plugins already use — no `go-mcp`, no `gomod2nix`/flake change.

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than a `…-caldav-v1`
variant — CalDAV resources are captured as regular file entries,
byte-identical EntryV1 shape to fs captures. A receipt mixing fs and
caldav roots carries one type-tag, and the captured `.ics` blobs restore
cleanly through either this plugin (back to a server) or the filesystem
plugin (to a local directory). Same rationale as the yt-dlp/git plugins.

## Round-trip fidelity

Because `Path` is the resource's server-absolute path and `Restore`
rebuilds the target as `<dest-origin>/<path>`, a capture→restore cycle
reproduces each object at its original path on the destination host.
Restoring to a *different* host re-creates the same path layout there;
the destination's calendar collections must already exist (the plugin
PUTs resources, it does not MKCALENDAR). Restoring the captured `.ics`
files to a local directory (via the filesystem plugin) yields a plain
on-disk tree of calendar objects.

## References

- [FDR 0013: caldav plugin](../../docs/features/0013-caldav-plugin.md) — behavior.
- [FDR 0005: URI-scheme plugins](../../docs/features/0005-uri-scheme-plugins.md)
  — the scheme-keyed plugin model this implements.
