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
  (PROPFIND / REPORT / PUT). The HTTP transport carries no iCalendar
  knowledge; resources cross the wire as verbatim `text/calendar` bytes.
  Ported and trimmed from bob's `internal/caldav/client.go`.
- `ical/` — the re-homed iCalendar parser/serializer (VTODO/VEVENT,
  `ParseVTODO`/`ParseVEVENT`/`*ToIcal`, RFC 5545 line unfolding). Ported
  from bob; stdlib-only. Used to derive a resource's **native identity**
  (UID + component) for the RFC 0011 protocol receipt, and by the
  forthcoming CUD tools (FDR 0020) for structured create/update.
- `url.go` — argument coercion (`baseURLFromArg`, `connectionFromArg`),
  origin extraction, and the server-relative `Path` key.
- `Plugin.CaptureProtocol` (`protocol.go`) — the RFC 0011 protocol path:
  stores each VTODO/VEVENT body as a `caldav-object-v1` leaf and wraps
  them in a receipt merkle tree whose payload references each object by
  **native identity** `<collection>/<component>/<UID>` (parsed via
  `ical/`) with per-resource etag recorded for the diff freshness probe.
  Emits `cutting_garden-capture_receipt-caldav-v1`.
- `Plugin.DiffProtocol` (`diff_protocol.go`) — the RFC 0011 diff path:
  a getetag-only REPORT (`listObjectEtags`) matches each live resource to
  the receipt's `{href, etag}`; unchanged etags transfer no body, only the
  new/moved/removed residue is re-fetched (UID → native id, body → digest)
  and reported as `A`/`D`/`M` by native identity, with a digest gate so a
  moved etag over identical bytes is not spurious drift.
- `protocol_consume.go` — `loadReceiptPayload`: the consume side shared by
  diff (and, next, restore) — validates the caldav kind, verifies the
  FDR-0001 type locks, and decodes the payload `{id, href, etag}` records.
- `types_register.go` — registers the RFC 0011 binding node types into the
  build-time type-signature registry. `caldav-object-v1` /
  `caldav-calendar-v1` are the SAME tags the traversal layer declares
  (`traversal.go`) — unified per FDR 0018.

## iCalendar parsing: identity only, stored bytes stay verbatim

Bob's caldav carried a hand-rolled VTODO/VEVENT parser for its MCP tools'
structured fields. The original FDR 0013 stance was that the snapshot
model needed none of it — resources are captured as opaque content-
addressed bytes. The RFC 0011 protocol migration changed that calculus:
the receipt keys each entry on the calendar's **native identity** (UID +
component), and `UID` lives only inside the resource body. So the parser
was re-homed into `ical/` (decoupling from bob) and is used to read the
identity. The captured *blob* is still the verbatim `text/calendar` body,
byte-for-byte — parsing never rewrites or normalizes stored bytes. The
parser is stdlib-only, so the package still adds no new dependency
(`go-mcp`, `gomod2nix`/flake all unchanged).

## Two capture paths: flat fs-v1 and the RFC 0011 protocol

The plugin currently carries **both** capture paths (the migration lands
the protocol path alongside the flat one; RFC 0010 immutability keeps old
receipts readable):

- **Flat `fs-v1`** (`CaptureRoot`/`ScanForDiff`/`Restore`, EntryV1).
  `Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
  (`cutting_garden-capture_receipt-fs-v1`): resources are captured as
  regular file entries, byte-identical to fs captures, so a mixed fs+caldav
  store-group receipt shares one tag and the `.ics` blobs restore through
  either this plugin or the file plugin.
- **RFC 0011 protocol** (`CaptureProtocol`, `protocol.go`). The orchestrator
  resolves the plugin via the EntryV1 `CapturePlugin` registry and then
  type-asserts `ProtocolCapturePlugin`, so `CaptureRoot` stays registered
  (the vestigial-stub pattern git uses; dropping it is gated on #48). The
  protocol receipt carries its own `cutting_garden-capture_receipt-caldav-v1`
  kind with native identity — NOT the shared fs tag.

Consolidating the two paths post-migration is tracked in #115.

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
