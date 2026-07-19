---
status: experimental
date: 2026-06-09
revised: 2026-07-19 (root aggregation is per-plugin fault-isolated, not
  fail-fast — cutting-garden#165)
promotion-criteria: |
  Promote to `testing` once a second RootProvider plugin (yt-dlp channels,
  FDR 0004, or sftp/webdav #55/#54) surfaces through `resources/list` /
  `resources/read` unchanged, confirming the Node→resource mapping and the
  config-driven root aggregation are not caldav-shaped. Promote to
  `accepted` once a real MCP client (Claude Code, via the bundled clown
  plugin) has driven the server against a live configured endpoint and the
  read-descends-a-container contract has gone two weeks without a lever
  moving.
---

# MCP resource-traversal server

> **Experimental.** The `mcp` subcommand (`internal/mcp/`) serves the
> capturable trees of cutting-garden's configured and intrinsic roots over
> the Model Context Protocol. It is the fourth consumer of the FDR 0014
> traversal primitive — the one that doc names "MCP resource traversal
> (future)" — and the first consumer of the config subsystem (RFC 0007).
> It exposes the tree *structure*, and — for a leaf its plugin can fetch —
> the object's parsed fields plus an optional content-addressed
> `madder://blobs/<digest>` link to its verbatim bytes (#85). The
> `--split`-style frontier remains out of scope (see Non-Goals).

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
| `resources/read <uri>` | `ListRoots(uri)` rendered as a JSON array of node views (`uri`, `name`, `type`, `container`, `mimeType`). A client descends a container lazily by reading successively deeper URIs. A childless URI whose plugin implements `LeafReader` instead reads as the object's **structured JSON body** (the parsed fields), not an empty array (#85). |
| `resources/templates/list` | Empty — cutting-garden resources are enumerated, not URI-template parameterized. |

A node's `container` flag and body mimetype are resolved from the
plugin's declared `Types()`, never hardcoded against tag strings (FDR
0014's self-description contract). A container advertises the
`application/json` listing mimetype so a client knows a read yields more
structure; a leaf advertises its declared `NodeType.MimeType` (e.g.
`text/calendar` for a CalDAV object), defaulting to
`application/octet-stream` when the plugin declares none — what the
object's bytes *are*.

When a `resources/read` enumerates no children, the node is a leaf or an
empty container. The resolved plugin is then probed for the optional
`LeafReader` capability (#85): if it can fetch the object, the read
returns the parsed object as a JSON body (caldav surfaces an
`{component, event|task}` view — summary, dtstart, location, status,
categories, …, the rich `ical` types). A plugin without `LeafReader`, or
a node it does not recognize as a leaf, still reads as an empty array.

The verbatim source bytes (the `.ics`) ride along on `LeafContent.Raw`.
When the server has a blob store (below), it writes those bytes and
appends a **second, link-only content entry** —
`{uri: "madder://blobs/<digest>", mimeType: "text/calendar"}` with no
inlined text — so a client fetches the exact source out-of-band by
digest. The structured fields stay the primary content; the link is a
fidelity add-on that is simply absent when no store is configured.

### Roots come from config, not argv

The set of roots is produced by `command_components.AggregateRoots`, which
walks `cutting_garden_plugins.RegisteredPlugins()`, type-asserts the
`RootProvider` capability, and concatenates each plugin's `Roots()` (RFC
0007). caldav yields its configured accounts; the file plugin yields its
working directory intrinsically (no config needed). A plugin with no roots
contributes nothing. Resolution is per-plugin fault-isolated (cutting-garden#165):
an error from one plugin — most commonly a misconfigured or crashed RFC 0013
wire plugin — is contained to that plugin (a warning is logged and its
contribution omitted); every other plugin's roots are still served. Before
this, any plugin's error aborted the whole aggregation, which meant one bad
wire plugin could fail cutting-garden's own MCP `initialize` handshake and
take every scheme down with it.

Credentials for a CalDAV node resolve per RFC 0007: explicit URI userinfo,
else a configured account matched by host + longest path prefix, else the
global `CALDAV_USERNAME` / `CALDAV_PASSWORD`. Surfaced resource URIs are
credential-free.

### Stateless, read-only

`Resources` holds nothing per node beyond its configured roots. Every read
re-resolves the plugin from the requested URI — mirroring the stateless
RootLister contract — so `resources/read` works for any listable URI a
client has discovered. A leaf read fetches the object body live (a single
caldav GET) and returns its parsed fields. The one write is
content-addressed and optional: when a default madder blob store is
configured, the leaf's verbatim bytes are written so they can be linked by
digest; with no store the read is purely structural. Nothing is captured —
no receipt, no tree, just a deduplicating blob put.

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
    #   resources/read caldav://dav.host/dav/me/work/   (a container)
    #     → [ {"uri":"caldav://dav.host/dav/me/work/event1.ics",
    #          "name":"event1.ics","type":"caldav-object-v1",
    #          "container":false}, ... ]
    #   resources/read caldav://dav.host/dav/me/work/event1.ics   (a leaf)
    #     → contents: [
    #         {"uri":"caldav://.../event1.ics","mimeType":"application/json",
    #          "text":"{\"component\":\"VEVENT\",\"event\":{\"summary\":
    #                   \"Standup\",\"dtstart\":\"20260224T150000Z\", ...}}"},
    #         {"uri":"madder://blobs/blake2b256-…",        # raw .ics, by digest
    #          "mimeType":"text/calendar"} ]               # link only, no bytes
    #     (the second entry is present only when a blob store is configured)

    # override the config: serve one explicit endpoint
    $ cutting-garden mcp caldav://dav.host/dav/me/

## Limitations / Non-Goals

- **Raw bytes need a store + a resolver.** The `madder://blobs/<digest>`
  link beside a leaf's parsed fields is only emitted when the server's host
  has a default blob store configured (it is absent-safe otherwise). And
  the link is a *reference*: a client only gets the bytes if it can resolve
  a `madder://` URI against that store — for the claude.ai-over-a-tunnel
  consumer (circus), that resolution is the client/proxy's concern, not
  this server's.
- **Write tools are CUD, not capture/restore.** The server grew
  `create_node`/`put_node`/`patch_node`/`delete_node` write tools (FDR 0020)
  for plugins implementing `NodeMutator` — so this is no longer a read-only
  window. `put_node` is a full replace; `patch_node` is a partial-field update
  (body contains only the fields to change; absent fields left untouched). Both
  mutate one live node directly; there are still no capture/restore/diff
  *receipt* tools (those stay CLI subcommands).
- **No `--split` frontier.** The receipt-fanout selector grammar (FDR
  0014) is a planner concern; it does not surface here.
- **Per-plugin fault-isolated aggregation.** The startup root aggregation
  (`command_components.AggregateRoots`) contains a plugin's `Roots` error to
  that plugin (cutting-garden#165) rather than failing the whole server —
  see "Roots come from config, not argv" above. A resolution or traversal
  error *within* an already-resolved root's `resources/list` /
  `resources/read` call (a live plugin failing mid-request) is unaffected by
  this and still fails that request outright rather than returning a
  silently partial set.

## Open Questions

- **Body-fetch beyond caldav text.** Leaf body-fetch is implemented for
  caldav (text/calendar → parsed `ical` view + raw blob link). Other leaf
  schemes (file `application/octet-stream`, gphotos media) would need their
  own `LeafReader` and a base64 `blob` rather than a `text` rendering;
  deferred. Tracked at #85.
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
- [FDR 0021](0021-faceted-progressive-disclosure.md) — faceted progressive
  disclosure; extends this server's container `resources/read` with a hoisted
  `facets` block and a `dimension=value` narrowing parameter.
- [RFC 0012](../rfcs/0012-plugin-facet-contract.md) — the facet contract
  whose §7 binds to this server's `resources/read` and `describe_node_types`,
  and whose §12 (cutting-garden#160) makes the `list_nodes` tool's listings
  enriched by default (facets + plugin-declared human-readable fields
  inline, with a `bare` opt-out) and adds a `filter` parameter — the direct
  way to retrieve the matching nodes `read_facets` can only count.
