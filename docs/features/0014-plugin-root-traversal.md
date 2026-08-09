---
status: experimental
date: 2026-06-08
promotion-criteria: |
  Promote to `experimental` once the RootLister interface, a read-only
  `list` consumer, and one implementing plugin (caldav) land with
  CaptureRoot refactored onto ListRoots. Promote to `testing` once
  `--split` ships a multi-receipt capture and a second plugin (yt-dlp
  channels, FDR 0004) reuses the primitive. Promote to `accepted` once
  the `--split` selector grammar has gone two weeks without a lever
  moving and the MCP traversal consumer is prototyped against it.
---

# Plugin root traversal and expansion

> **Experimental.** The `RootLister` interface
> (`internal/cutting_garden_plugins/traversal.go`), the caldav reference
> implementer (`Types()` + `ListRoots()`, with `CaptureRoot` reworked
> onto the shared traversal and #81's failure-receipt fix folded in), the
> read-only `list` command (`internal/list/`), `health` (#80), and the
> MCP resource-traversal server (`internal/mcp/`, the `mcp` subcommand;
> see [FDR 0015](0015-mcp-resource-server.md)) have landed. The `--split`
> planner expansion is the remaining unbuilt half. The node-type
> versioning this leans on is the subject of #79.

## Problem Statement

Plugin capture is one-arg-one-root: `planCapture` / `classifyArg`
(`internal/capture/plan.go`) turn each CLI argument into exactly one
`(plugin, sourceURL)` root, and the plugin walks *within* that root,
folding any internal hierarchy into `EntryV1.Path` prefixes. But several
sources are naturally a **parent of many sub-roots** a user may want to
discover, address, diff, or restore independently — a CalDAV endpoint's
calendars (FDR 0013), a yt-dlp channel's videos (FDR 0004, which
deferred exactly this), a Google Drive folder's files (FDR 0009). Today
that sub-structure is invisible to the planner, unaddressable on its
own, and re-discovered ad hoc inside each plugin's `CaptureRoot`. There
is also no read-only way to *list* what is capturable under an endpoint
without capturing it — the shape a future MCP resource-traversal server
needs.

## Interface

### Declared node types

A traversal-capable plugin declares the node types it can emit. Each
type carries whether nodes of that type are descendable (a container)
or terminal (a leaf — a capturable object), plus a hyphenated,
horizontally-versioned tag in the madder/dodder scheme (#79):

```go
// NodeType is one entry in a plugin's declared type list.
type NodeType struct {
    // Tag is the hyphenated, horizontally-versioned identifier, e.g.
    // "cutting_garden-caldav-calendar-v1".
    Tag string
    // Container is true when nodes of this type can be descended (have
    // children) and false for leaves (capturable objects with none).
    Container bool
    // MimeType is the content type of a leaf's body (e.g.
    // "text/calendar" for a CalDAV object). Empty means unspecified:
    // consumers resolve a leaf's empty MimeType to
    // application/octet-stream (BodyMimeType). Containers have no body
    // of their own, so their MimeType is conventionally empty and the
    // leaf default never applies to them.
    MimeType string
}
```

The declared list is the plugin's compatibility surface: a format change
adds a `-v2` entry while the `-v1` entry stays readable, so a consumer
built against `-v1` keeps working when `-v2` nodes appear beside it. It
also lets the tree be self-describing — descendability, format version,
and body content type are looked up in the list (`NodeTypeFor` +
`BodyMimeType`), never hardcoded against tag strings.

`NodeType` deliberately grows toward dodder's type definitions — the
`!toml-type-v2` blob whose fields include `binary`, `file-extension`,
`mime-type`, and formatters (dodder FDR 0010 "core types") — one field
at a time as a consumer needs it. `MimeType` is the first such field;
`application/octet-stream` plays the role of dodder's null type `!`
(opaque bytes, no schema) at the leaf default.

### The traversal primitive

```go
// Node is one addressable point in a plugin's capturable tree.
type Node struct {
    // URI re-classifies as a capture root: `capture <URI>` captures
    // exactly this node — one object for a leaf, the whole subtree for
    // a container (today's bulk behavior).
    URI *url.URL
    // Name is a short display label (calendar display-name, video
    // title, file name).
    Name string
    // Type is a Tag from this plugin's Types(); resolve it there for
    // descendability and format version.
    Type string
}

// RootLister is the optional traversal capability. A plugin implements
// it when its scheme has meaningful sub-structure; plugins without
// (e.g. the file plugin) omit it and the planner falls back to today's
// one-arg-one-root behavior.
type RootLister interface {
    Plugin

    // Types declares every node type this plugin can emit, leaf and
    // container.
    Types() []NodeType

    // ListRoots returns the immediate children of node — the URI whose
    // children to enumerate. A consumer begins at the user-supplied
    // endpoint URI and descends a container by calling again with its
    // URI. RootLister plugins are stateless, so node always identifies
    // the target (including the top-level endpoint) and MUST be non-nil.
    // Read-only and lazy; a leaf has no children.
    ListRoots(ctx context.Context, node *url.URL) ([]Node, error)
}
```

Hierarchical and lazy (one level per call) is deliberate: it is the
shape MCP `resources/list` consumes, it bounds work on huge trees, and
it lets a consumer stop at whatever depth it cares about.

### CaptureRoot is built on ListRoots

`CaptureRoot` stops doing its own discovery. A plugin's traversal lives
in exactly one place — `ListRoots` — and `CaptureRoot(node)` captures a
single node's objects. "Capture the whole endpoint" becomes a walk of
`ListRoots` to the leaves, capturing each. This removes the standing
risk that a plugin's discovery path and capture path drift: the caldav
plugin today PROPFINDs inside `CaptureRoot`; that logic moves up into
`ListRoots`, and `CaptureRoot` is reduced to "capture this one
calendar/object."

### Four consumers, one walk

| Consumer | What it does with the walk |
|---|---|
| `list` command (read-only) | Walk and print the tree; nothing is captured. |
| MCP resource traversal (future) | `Node` → MCP resource, `ListRoots` → `resources/list`; the hierarchical-URI requirement originates here. |
| One-receipt expansion (default) | Planner expands one arg's leaves into roots within **one** store-group → one receipt, but the roots are now first-class (per-root progress, collision detection, addressing). |
| N-receipt fanout (`--split`) | Planner cuts the tree at a selector frontier; each cut node's subtree becomes its **own** receipt — independently diff/restore-able. |

The plugin only ever learns how to enumerate its tree; one-receipt vs
N-receipt is a **planner policy**, not a plugin concern.

### Derived nodes (no 1:1 stored object)

A `Node.URI` need not name a distinct, individually-stored server object.
A plugin MAY synthesize additional nodes derived from one stored object's
content — e.g. a value implied by, but not literally equal to, what a
plain enumeration of the source would return — when its own semantics
call for it and no other addressing scheme fits.

The first concrete case is caldav's VEVENT recurrence expansion
(cutting-garden#176/#177, `plugins/caldav/expand.go`): a single stored
`VEVENT` resource carrying an `RRULE` can materialize into SEVERAL
`Node`s — one per occurrence within a bounded window — each addressed by
the real, fetchable master href plus a discriminator query parameter
(`?recurrence-id=<value>`) rather than by a distinct stored blob. Reading
such a node (`ReadLeaf`) fetches the real master and projects the
specific occurrence; there is no separate resource on the server at the
derived URI itself.

This does not weaken [RFC 0012](../rfcs/0012-plugin-facet-contract.md)
§12.2's level-scoping requirement (`ListRoots`/`ListEnriched` must report
the SAME children at one URI) — it generalizes what "the same children"
may consist of. A plugin introducing derived nodes MUST:

- document the addressing scheme (what the discriminator means, and how
  to recover the real, fetchable address from it);
- apply the SAME derivation in every capability that lists a container's
  children, so `ListRoots` and `ListEnriched` (and any future sibling)
  never disagree about a URI's child set;
- refuse mutation of a derived node rather than silently resolving it to
  the underlying stored object and mutating the wrong scope (caldav:
  `mutate.go`'s `clientForNode` refuses a `?recurrence-id=` URI outright
  — editing/deleting one occurrence vs. the whole series is a genuinely
  unresolved question, not a detail to guess at).

### `--split <selector>`

`--split` takes an XPath-like selector over the `ListRoots` tree rather
than a fixed cut depth, because the meaningful unit differs per plugin
(caldav → calendars; gdrive → some folder depth; ytdlp → playlist
level). The selector picks a **frontier** of nodes; each matched node's
subtree becomes one independent receipt. Nodes are addressed two ways:

- **structurally** (by depth): `/*` = the endpoint's immediate children,
  `/*/*` = grandchildren.
- **by type** (using the declared `Types()` tags):
  `//cutting_garden-caldav-calendar-v1` = every calendar-typed node,
  wherever it sits in a given server's layout.

The type-matched form is the robust one — it does not care how deep the
target type sits under a particular server's path scheme, which is
exactly why the declared type list pays off.

## Examples

    # discover without capturing
    $ cutting-garden list caldav://dav.host/dav/me/
    caldav://dav.host/dav/me/personal/  Personal  caldav-calendar-v1
    caldav://dav.host/dav/me/work/      Work      caldav-calendar-v1

    # capture one discovered sub-root
    $ cutting-garden capture caldav://dav.host/dav/me/work/

    # whole endpoint, one receipt (default — surface unchanged)
    $ cutting-garden capture caldav://dav.host/dav/me/

    # one receipt per calendar, structurally
    $ cutting-garden capture --split '/*' caldav://dav.host/dav/me/

    # one receipt per calendar, type-matched (survives nesting)
    $ cutting-garden capture --split '//caldav-calendar-v1' caldav://dav.host/dav/me/

Given the tree

    caldav://dav.host/dav/me/        endpoint
    ├── personal/  (caldav-calendar-v1)
    │   ├── task1.ics  (caldav-object-vtodo-v1)
    │   └── task2.ics  (caldav-object-vtodo-v1)
    └── work/      (caldav-calendar-v1)
        └── event1.ics (caldav-object-vevent-v1)

`--split '/*'` and `--split '//caldav-calendar-v1'` both yield two
receipts (personal, work); a `--split` on the per-component object leaf
type (e.g. `//caldav-object-vtodo-v1`) yields one receipt per object of
that component; no `--split` yields one (the whole endpoint). (The object
leaf is typed by component — `caldav-object-<kind>-v1` — since #45.)

## Limitations / Non-Goals

- **Restore does not traverse.** Restore routes by receipt kind, not
  source URI (FDR 0010); it never calls `ListRoots`. A `--split` capture
  simply yields more receipts to restore individually.
- **Diff mirrors capture.** `diff <endpoint>` expands the same way
  capture does; a `--split` diff is out of MVP scope.
- **No cross-plugin tree.** `ListRoots` enumerates within one plugin's
  scheme; it does not compose a unified tree across plugins.
- **Opt-in.** Plugins with no meaningful sub-structure (the file plugin)
  do not implement `RootLister`; the planner keeps one-arg-one-root.
  Protocol (RFC 0002) plugins like git also opt out: they capture one
  receipt via `CaptureProtocol`, not `CaptureRoot`, so `RootLister` is
  an EntryV1-plugin capability.
- **Derived nodes are read-only.** A derived node (see above) can be
  listed and read; it cannot be mutated, created, or deleted as itself —
  a plugin implementing `NodeMutator` MUST refuse a derived-node URI
  rather than silently retargeting the mutation at the underlying stored
  object. `capture`/`ScanForDiff`/`Restore` are similarly unaffected:
  caldav's expansion is confined to `ListRoots`/`ListEnriched`/`ReadLeaf`
  and never reaches the shared client methods those three entry points
  depend on for identity (cutting-garden#176/#177 Phase 1's governing
  constraint).

## Open Questions

- **Selector grammar.** Keep it small and inspection-decidable — child
  `/`, descendant `//`, wildcard `*`, and a type-tag predicate — no full
  XPath or regex (mirrors FDR 0010's "most-specific-wins stays decidable
  by inspection"). Exact predicate syntax for type matching (`//tag`
  vs `//*[type=tag]`) is unsettled.
- **Frontier rule.** The match set must be a non-overlapping frontier: a
  node and one of its ancestors cannot both match, or a receipt's
  contents are ambiguous. Whether that is enforced (error) or resolved
  (outermost wins) is open.
- **Huge-tree guardrails.** Mirroring FDR 0004's `--ytdlp-limit`: a
  server with thousands of objects under one endpoint must not silently
  fan out unbounded. A default cap or refuse-with-hint is likely needed.
- **Where bulk orchestration lives.** Resolved for the caldav reference
  impl by a third option: capture and `ListRoots` share an internal
  traversal primitive (caldav's `discoverCalendars`) rather than one
  calling the other, so neither re-implements the walk and `CaptureRoot`
  keeps its bulk body-fetch (a per-calendar REPORT-with-data) instead of
  regressing to a per-object GET. Open whether the planner-side walk is
  still wanted for `--split` (capturing each frontier node as its own
  receipt) — that is the unbuilt half.
- **Lightweight enumeration vs body-fetch.** `ListRoots` returns
  structure only (URIs + types); for caldav it uses a getetag-only
  REPORT so discovery never transfers object bodies. Capturing a leaf
  still needs its body, which the plugin fetches in whatever batch its
  protocol allows — so "capture built on the traversal" shares the
  *structure* source, not the body-fetch path.

## More Information

- amarbel-llc/cutting-garden#162 — confirmed this FDR's caldav
  `ListRoots`/`discoverCalendars` already covers the "account configured at
  a principal/calendar-home" case the RFC 0007 config subsystem allows
  (no schema change, no new discovery code); #162 closed a *test* gap — no
  prior fixture exercised N>1 discovered calendars — not an implementation
  gap. See `plugins/caldav/AGENTS.md` for the full account-shape note.
- amarbel-llc/cutting-garden#120 — the friendly-label follow-on, now fully
  resolved. A calendar discovered via `ListRoots` is already labeled by
  its DAV `displayname` (`calendarLabel`) whenever an account is
  configured at the home level. The remaining top-level-root case — an
  account configured directly at one calendar, where no PROPFIND
  happened at that level to learn a displayname — is resolved by the new
  `RootLabeler` capability (RFC 0007 § The Root-Labeler Capability):
  `caldav.Plugin.RootLabels` PROPFINDs a calendar-scoped account's own
  endpoint via `discoverCalendars`, the same traversal primitive
  `ListRoots` uses.
- amarbel-llc/cutting-garden#176/#177 — caldav VEVENT recurrence
  expansion, the first Derived-nodes case; `docs/plans/2026-07-20-caldav-
  recurrence-expansion-phase1.md` is the investigation. A caller-supplied
  expansion window is deferred to #178 (coordinated with trellis — RFC
  0014 (trellis), FDR 0022) rather than invented ahead of that design.
- amarbel-llc/cutting-garden#78 — tracking issue.
- amarbel-llc/cutting-garden#79 — hyphenated / horizontal plugin
  versioning that `NodeType.Tag` adopts.
- [FDR 0004](0004-ytdlp-channel-capture.md) — the deferred
  one-source-to-many question this generalizes.
- [FDR 0005](0005-uri-scheme-plugins.md) — the scheme registry this
  extends.
- [FDR 0010](0010-host-bound-plugin-dispatch.md) — restore routes by
  receipt, not source.
- [FDR 0013](0013-caldav-plugin.md) — first `RootLister` implementer;
  its in-`CaptureRoot` discovery moves into `ListRoots`.
- [FDR 0021](0021-faceted-progressive-disclosure.md) — facets ride on this
  traversal's enumeration (`Node`) and hoist over its tree for progressive
  disclosure.
- [RFC 0012](../rfcs/0012-plugin-facet-contract.md) — the facet contract
  built atop `Node` / `NodeType` / `RootLister`, including §12's `Node`
  extension (`Fields`) and the `EnrichedLister` capability
  (cutting-garden#160): `list_nodes` enriched-by-default listings and the
  filter that retrieves matching nodes, not merely counts them.
- amarbel-llc/cutting-garden#160 — `list_nodes` was metadata-blind
  (`{uri,name,type}` only) and unfilterable, forcing a per-node
  `read_node` fan-out; resolved by carrying `Node.Facets`/`Node.Fields`
  through to the listing views (previously computed but never surfaced
  past the framework fold) and adding a `filter` parameter.
