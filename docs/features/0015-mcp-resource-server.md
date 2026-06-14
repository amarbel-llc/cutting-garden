---
status: experimental
date: 2026-06-09
promotion-criteria: |
  Promote to `testing` once a second RootProvider plugin (yt-dlp channels,
  FDR 0004, or sftp/webdav #55/#54) surfaces through `resources/list` /
  `resources/read` unchanged, confirming the Node→resource mapping and the
  config-driven root aggregation are not caldav-shaped. Promote to
  `accepted` once a real MCP client (Claude Code, via the bundled clown
  plugin) has driven the server against a live configured endpoint and the
  read-descends-a-container contract has gone two weeks without a lever
  moving — in particular, without leaf body-fetch being folded in (see
  Non-Goals).
---

# MCP resource-traversal server

> **Experimental.** The `mcp` subcommand (`internal/mcp/`) serves the
> capturable trees of cutting-garden's configured and intrinsic roots over
> the Model Context Protocol. It is the fourth consumer of the FDR 0014
> traversal primitive — the one that doc names "MCP resource traversal
> (future)" — and the first consumer of the config subsystem (RFC 0007).
> Leaf body-fetch and the `--split`-style frontier are deliberately out of
> scope (see Non-Goals); this server exposes *structure*, not object bytes.

## Problem Statement

FDR 0014 made a plugin's capturable tree enumerable through one lazy,
hierarchical primitive (`RootLister.ListRoots`) and listed four consumers.
Three landed (the `list` command, `health`, and capture itself). The
fourth — "`Node` → MCP resource, `ListRoots` → `resources/list`" — is the
subject of this FDR: let a Model Context Protocol client discover and
descend what cutting-garden exposes without capturing it, so an agent can
decide *what* to capture by browsing the live tree.

The server is meant to be launched by an MCP client with **no
arguments**, so its roots cannot come from argv — they come from the
config subsystem (RFC 0007): the user's configured CalDAV accounts plus
the file plugin's intrinsic working directory.

## Interface

A top-level subcommand:

    cutting-garden mcp [URI...]

With **no URI**, the server surfaces every plugin's roots, aggregated over
the `RootProvider` capability (RFC 0007): each configured CalDAV account
and the file plugin's working directory. Explicit URIs **override** the
config with exactly those endpoints (each scheme's plugin must support
traversal) — the escape hatch and the shape the old argv-only design used.

The command speaks newline-delimited JSON-RPC over stdin/stdout — the MCP
stdio transport — so it is launched by an MCP client, not run
interactively. It advertises **only resource capabilities** and is backed
by `internal/mcp/Resources`, a `go-mcp` `server.ResourceProvider`:

| MCP method | Mapping |
|---|---|
| `resources/list` | The immediate children of every root — one `ListRoots(root)` call each. The roots are the configured/intrinsic entry points; their children are the discoverable resources. |
| `resources/read <uri>` | `ListRoots(uri)` rendered as a JSON array of node views (`uri`, `name`, `type`, `container`, `mimeType`). A client descends a container lazily by reading successively deeper URIs. |
| `resources/templates/list` | Empty — cutting-garden resources are enumerated, not URI-template parameterized. |

A node's `container` flag and body mimetype are resolved from the
plugin's declared `Types()`, never hardcoded against tag strings (FDR
0014's self-description contract). A container advertises the
`application/json` listing mimetype so a client knows a read yields more
structure; a leaf advertises its declared `NodeType.MimeType` (e.g.
`text/calendar` for a CalDAV object), defaulting to
`application/octet-stream` when the plugin declares none — what the
object's bytes *are*, even though `resources/read` does not fetch them
yet (the leaf body-fetch open question below). A leaf reads as an empty
array.

### Roots come from config, not argv

The set of roots is produced by `command_components.AggregateRoots`, which
walks `cutting_garden_plugins.RegisteredPlugins()`, type-asserts the
`RootProvider` capability, and concatenates each plugin's `Roots()` (RFC
0007). caldav yields its configured accounts; the file plugin yields its
working directory intrinsically (no config needed). A plugin with no roots
contributes nothing. Resolution is fail-fast: an error from any plugin
aborts rather than serving a silently partial set.

Credentials for a CalDAV node resolve per RFC 0007: explicit URI userinfo,
else a configured account matched by host + longest path prefix, else the
global `CALDAV_USERNAME` / `CALDAV_PASSWORD`. Surfaced resource URIs are
credential-free.

### Stateless, read-only

`Resources` holds nothing per node beyond its configured roots. Every read
re-resolves the plugin from the requested URI — mirroring the stateless
RootLister contract — so `resources/read` works for any listable URI a
client has discovered. No blob store is touched and nothing is captured.

### Clown plugin packaging

The repo ships a clown plugin under `plugins/cutting-garden/`
(`.claude-plugin/plugin.json` + `clown.json.in`). The flake's
`cutting-garden-clown-plugin` output substitutes the real binary path into
`clown.json` and stages `share/purse-first/cutting-garden/`, which eng's
`mkCircus` mounts to register the server as `cutting-garden mcp` (no args).
A client loading the plugin gets the configured CalDAV accounts and the
working-directory tree as MCP resources out of the box.

## Examples

    # serve every configured/intrinsic root (the common case)
    $ cutting-garden mcp
    #   resources/list → the working directory's entries + each CalDAV
    #     account's calendars
    #   resources/read caldav://dav.host/dav/me/work/
    #     → [ {"uri":"caldav://dav.host/dav/me/work/event1.ics",
    #          "name":"event1.ics","type":"caldav-object-v1",
    #          "container":false}, ... ]

    # override the config: serve one explicit endpoint
    $ cutting-garden mcp caldav://dav.host/dav/me/

## Limitations / Non-Goals

- **No leaf body-fetch.** Reading a leaf returns an empty child listing,
  not the object's bytes (FDR 0014 separates structure-only enumeration
  from the body-fetch path, which is capture's job). Tracked at #85.
- **No tools.** The server advertises resources only — no
  capture/restore mutation tools. A read-only window onto the tree.
- **No `--split` frontier.** The receipt-fanout selector grammar (FDR
  0014) is a planner concern; it does not surface here.
- **Fail-fast aggregation.** A resolution or traversal error on any root
  fails `resources/list` rather than returning a silently partial set.

## Open Questions

- **Leaf content.** When (and whether) `resources/read` on a leaf should
  fetch the object body and return it as resource content. Tied to FDR
  0014's body-fetch open question. Tracked at #85.
- **Pagination / huge trees.** An endpoint with thousands of children
  returns one enormous `resources/list`. The `go-mcp` V1 cursor exists;
  wiring it is deferred. Tracked at #86.
- **Change notifications.** `notifications/resources/list_changed` is not
  emitted; the tree is re-read per request. Tracked at #87.

## More Information

- [FDR 0014](0014-plugin-root-traversal.md) — the traversal primitive this
  consumes; names this server as its fourth consumer.
- [RFC 0007](../rfcs/0007-config-subsystem.md) — the config subsystem and
  the `RootProvider` capability this server aggregates over.
- [FDR 0013](0013-caldav-plugin.md) — the first `RootLister` implementer,
  the reference endpoint this server is exercised against.
- `github.com/amarbel-llc/purse-first/libs/go-mcp` — the MCP server library
  (`server.ResourceProvider`, stdio transport), bridged into the build like
  dewey (see `gomod.nix`).
