---
status: proposed
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

> **Design-only.** No code exists yet. This FDR records the direction
> agreed in the #78 design pass (2026-06-08); the normative interface is
> ratified as it lands. The node-type versioning it leans on is the
> subject of #79.

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
}
```

The declared list is the plugin's compatibility surface: a format change
adds a `-v2` entry while the `-v1` entry stays readable, so a consumer
built against `-v1` keeps working when `-v2` nodes appear beside it. It
also lets the tree be self-describing — descendability and format
version are looked up in the list, never hardcoded against tag strings.

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

    // ListRoots returns the immediate children of node; a nil node means
    // the endpoint root. Read-only and lazy: descend a container by
    // calling ListRoots again with that container's URI. A leaf has no
    // children.
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
    │   ├── task1.ics  (caldav-object-v1)
    │   └── task2.ics  (caldav-object-v1)
    └── work/      (caldav-calendar-v1)
        └── event1.ics (caldav-object-v1)

`--split '/*'` and `--split '//caldav-calendar-v1'` both yield two
receipts (personal, work); `--split '//caldav-object-v1'` yields three
(one per object); no `--split` yields one (the whole endpoint).

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
- **Where bulk orchestration lives.** Planner-side (walk `ListRoots`,
  feed leaves to `CaptureRoot`) keeps plugins thin but moves traversal
  policy into the planner; a default `CaptureRoot(container)` that
  self-walks keeps it plugin-side. The two must not both re-implement
  the walk.

## More Information

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
