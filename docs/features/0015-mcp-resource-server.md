---
status: experimental
date: 2026-06-08
promotion-criteria: |
  Promote to `testing` once a second RootLister plugin (yt-dlp channels,
  FDR 0004) is reachable through `resources/list`/`resources/read`
  unchanged, confirming the Node→resource mapping is not caldav-shaped.
  Promote to `accepted` once a real MCP client (Claude Code) has driven
  the server against a live endpoint and the read-descends-a-container
  contract has gone two weeks without a lever moving — in particular,
  without leaf body-fetch being folded in (see Non-Goals).
---

# MCP resource-traversal server

> **Experimental.** The `mcp` subcommand (`internal/mcp/`) and its
> `server.ResourceProvider` over the RootLister registry have landed. It
> is the fourth consumer of the FDR 0014 traversal primitive — the one
> that doc names "MCP resource traversal (future)". Leaf body-fetch and
> the `--split`-style frontier are deliberately out of scope (see
> Non-Goals); this server exposes *structure*, not object bytes.

## Problem Statement

FDR 0014 made a plugin's capturable tree enumerable through one lazy,
hierarchical primitive (`RootLister.ListRoots`) and listed four consumers
of that one walk. Three landed (the `list` command, `health`, and capture
itself). The fourth — "`Node` → MCP resource, `ListRoots` →
`resources/list`" — is the subject of this FDR: let a Model Context
Protocol client discover and descend what a cutting-garden plugin exposes
without capturing it, so an agent can decide *what* to capture by
browsing the live tree.

## Interface

A new top-level subcommand:

    cutting-garden mcp URI [URI...]

Each `URI` is a traversable plugin endpoint (its scheme's plugin must
implement `RootLister`; the file plugin does not, and a non-traversable
or unknown scheme is rejected as `EX_USAGE` before the transport opens).
The command speaks newline-delimited JSON-RPC over stdin/stdout — the MCP
stdio transport — so it is launched by an MCP client, not run
interactively. It advertises **only resource capabilities** and is
backed by `internal/mcp/Resources`, a `go-mcp` `server.ResourceProvider`:

| MCP method | Mapping onto FDR 0014 |
|---|---|
| `resources/list` | The immediate children of every configured endpoint — one `ListRoots(endpoint)` call each. The endpoints are the givens; their children are the discoverable resources. |
| `resources/read <uri>` | `ListRoots(uri)` rendered as a JSON array of node views (`uri`, `name`, `type`, `container`). A client descends a container lazily by reading successively deeper URIs. |
| `resources/templates/list` | Empty — cutting-garden resources are enumerated, not URI-template parameterized. |

A node's `container` flag is resolved from the plugin's declared
`Types()`, never hardcoded against tag strings (FDR 0014's
self-description contract): a container advertises the
`application/json` listing mimetype so a client knows a read yields more
structure to descend; a leaf carries none and reads as an empty array.

### Stateless, read-only

`Resources` holds nothing per node beyond its configured roots. Every
read re-resolves the plugin from the requested URI — mirroring the
stateless RootLister contract (a node is addressed by URI, never a
server-held cursor) — so `resources/read` works for any listable URI a
client has discovered, not only the ones returned by the most recent
`resources/list`. No blob store is touched and nothing is captured.

## Examples

    # serve a CalDAV endpoint's calendars as MCP resources
    $ cutting-garden mcp caldav://dav.host/dav/me/

    # a resources/list response then exposes the calendars; reading one
    # descends into its objects:
    #   resources/read caldav://dav.host/dav/me/work/
    #   → [ {"uri":"caldav://dav.host/dav/me/work/event1.ics",
    #        "name":"event1.ics","type":"caldav-object-v1",
    #        "container":false}, ... ]

    # several endpoints in one server
    $ cutting-garden mcp caldav://dav.host/dav/me/ caldav://dav.host/dav/team/

## Limitations / Non-Goals

- **No leaf body-fetch.** Reading a leaf returns an empty child listing,
  not the object's bytes. FDR 0014 separates structure-only enumeration
  from the body-fetch path (which is capture's job and an open question
  there); this server is the traversal consumer, so it stays on the
  structure side. Surfacing object content as resource `text`/`blob` is
  the natural follow-up and is gated on that decision.
- **No tools.** The server advertises resources only — no
  capture/restore mutation tools. It is a read-only window onto the tree.
- **Roots are argv, not discovered.** The server exposes exactly the
  endpoints passed on the command line; it does not enumerate configured
  stores or known endpoints. A client points it at the endpoints it cares
  about.
- **No `--split` frontier.** The receipt-fanout selector grammar (FDR
  0014) is a planner concern; it does not surface here.
- **Fail-fast list.** A resolution or traversal error on any configured
  root fails `resources/list` rather than returning a silently partial
  set.

## Open Questions

- **Leaf content.** When (and whether) `resources/read` on a leaf should
  fetch the object body and return it as resource content, versus keeping
  the server purely structural. Tied to FDR 0014's body-fetch open
  question. Tracked at #85.
- **Pagination / huge trees.** Mirrors FDR 0014's huge-tree guardrail:
  an endpoint with thousands of children would return one enormous
  `resources/list`. The `go-mcp` V1 cursor shape exists; wiring it is
  deferred until a real client hits the wall. Tracked at #86.
- **Change notifications.** `notifications/resources/list_changed` is not
  emitted. The tree is re-read per request; a long-lived client sees
  changes only on its next call. Tracked at #87.

## More Information

- [FDR 0014](0014-plugin-root-traversal.md) — the traversal primitive
  this consumes; names this server as its fourth consumer.
- [FDR 0013](0013-caldav-plugin.md) — the first `RootLister` implementer,
  the reference endpoint this server is exercised against.
- `github.com/amarbel-llc/purse-first/libs/go-mcp` — the MCP server
  library (`server.ResourceProvider`, stdio transport) this builds on,
  bridged into the build like dewey (see `gomod.nix`).
