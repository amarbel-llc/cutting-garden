---
status: proposed
date: 2026-06-20
promotion-criteria: |
  Promote to `experimental` once the facet contract (RFC 0012) lands with
  one plugin declaring facets and `cg list --facets` / the MCP container
  `facets` block rendering a summary. Promote to `testing` once a one-shot
  plugin (chrest tabs, or nebulous from its in-memory store) and an
  incremental plugin folded by the framework (caldav: calendars then events)
  both produce correct summaries, and a multi-filter drill-down narrows both
  the listing and the re-computed summary. Promote to `accepted` once the
  partial-summary marker and the top-N / fold-bound levers have gone two
  weeks without moving and an MCP client has driven the summary → narrow →
  descend loop against a live tree.
---

# Faceted progressive disclosure for plugin trees

## Problem Statement

cutting-garden lets a client walk a plugin's tree one level at a time
(FDR 0014) and serve it over MCP (FDR 0015), but a container only tells you
*that* it has children, never *what* they are. To decide what to capture, or
to browse a calendar, a feed, or a window full of tabs, you have to open
everything and count by hand. nebulous solved this for one app with a
hand-written "facets" summary (stories by year, tag, feed, read/unread), but
that code is per-app, can only summarize the whole corpus (never a filtered
slice), and is disconnected from the tree. This feature makes summaries a
**plugin capability** the framework computes, and lets a leaf's facets **roll
up to its containers**, so reading a container shows the shape of everything
under it — the top-down half of progressive disclosure.

## Interface

A **facet** is a labelled way to group a node's children and count them — "by
status", "by domain", "by year". A plugin opts in by declaring its facets and
attaching facet values to the nodes it lists. The framework does all the
counting and rolling-up; the plugin never writes aggregation code. The exact
Go contract is RFC 0013; this record is the plain-language version.

### Facet values are cheap, and come from enumeration

A facet value is just a small tag on a node: a tab carries `domain=github.com`,
an event carries `status=CONFIRMED` and `category=work`, a story carries
`year=2026` and `tag=rust`. The rule that keeps facets cheap: **a node's facet
values must already be in hand when the plugin lists it** — no extra fetch per
node. A browser returns every tab's attributes in one call; a calendar can
list its events' status in one request; an in-memory story index already holds
every field.

Facet values do **not** have to be the same fields a client sees on a quick
look. caldav happens to facet over the same fields it displays, but nebulous
facets stories by read/unread — fields it does not show in a story preview.
So: facet values must be cheap to produce at list time; they may, but need
not, overlap the display fields.

### Declaring facets

A plugin declares, per node type, its facet dimensions — each with a name, a
**kind**, and whether a node can have more than one value for it:

- **categorical** — a plain label: `status`, `state`.
- **numeric-bucket** — a number put in an ordered bucket: a date's `year`, a
  size band. These sort newest/largest first.
- **labelled** — a stable but opaque key whose human name lives elsewhere: a
  feed id whose title is in a separate index. The name is looked up only when
  displaying, never mixed into the counts (see "Honesty").

A dimension is also either **open** or **closed**:

- **open** — its values are discovered as nodes are listed (tags, domains).
  You can only show values that actually appear.
- **closed** — its full set of values is known up front (`read`/`unread`,
  `pinned` yes/no). A closed dimension can show an informative **zero**:
  "unread: 0" means "you've read everything", which an open dimension could
  never tell you.

The declared facets are reported by the MCP `describe_node_types` tool, so a
client can learn a tree's facet axes before reading anything — the same way
caldav already advertises its writable fields.

### Two ways a summary is produced

This is the core mechanic, and it has **two paths**, picked by the plugin's
shape — not by how big the data is.

1. **One-shot (preferred when available).** A plugin that can summarize a
   whole subtree in a single operation answers directly. This covers two very
   different plugins: one whose listing is **atomic** (chrest returns *all*
   tabs across all windows in one call, so summarizing them is one call and a
   count), and one with an **in-memory index or backend query** (nebulous
   summarizes from its resident story store; a database plugin runs a `GROUP
   BY`). Either way the plugin hands back the finished summary.

2. **Framework fold (the fallback).** A plugin that can only walk its tree one
   level at a time just attaches facet values to each node it lists, and the
   framework adds them up — descending containers, counting leaves. This fits
   a genuinely lazy plugin like caldav (list calendars, then list each
   calendar's events).

It is tempting to treat the framework fold as the cheap default and the
one-shot path as a "huge data" escape hatch; that is backwards. Almost every
plugin worth faceting can answer in one shot, and the fold is the minority
case for simple, small, structural trees.

### Facets roll up — including a container's own

Facets on the leaves roll up to every container above them: read a calendar
account and see all its events by status; read a browser and see all its tabs
by domain. **A container's own facets count too.** A window is both a
container (it holds tabs) and a thing with its own attributes (minimized,
focused) — so you can facet a browser's windows by state *and* its tabs by
domain. Rolling up is just adding counts together, so it works in any order
and the framework does it for free.

### Drilling down — several filters at once

Reading a container takes an optional set of filters, AND-ed together:
`tag=rust` and `status=unread`. The filtered read returns the narrowed list
**and** the summary recomputed over just the matches. This is the loop:

1. read a container → see the summary (what's in here);
2. add one or more filters → read again → narrowed list + the summary of
   what's left;
3. repeat, or descend into a child.

This matters because facet axes are not always a tree. caldav narrows by
descending (account → calendar → event). But a story corpus is a flat set with
*independent* axes — year, tag, feed, read/unread — that you mix freely:
"stories that are `tag=rust` **and** `unread`, broken down by year". You can't
get there by descending a tree; you get there by combining filters. Allowing
only one filter at a time would quietly assume the tree shape and could not
express the corpus shape.

Filters are value equality on a declared dimension. There is no query
language — no ranges, no "or", no negation (combine successive reads instead).
Free-text search is **not** a facet; it is a search index, a separate thing.

### Honesty: partial summaries and live data

- **Partial summaries are marked.** Some sources cap what they return —
  browser history hands back at most ~100k entries, so a history summary
  cannot claim to have seen everything. A summary says whether it is complete;
  a capped or bounded one is shown as partial (e.g. "partial — newest 100k"),
  never passed off as the whole picture.
- **Live data moves under you.** A browser's tabs open and close while you
  browse them, so a summary is a snapshot, not a transaction; two reads a
  second apart can differ, and a multi-step drill-down is not atomic. A plugin
  over live state should answer a whole summary from one snapshot so at least
  that summary is internally consistent.

### Surfaces

- `cg list --facets <uri>` — print the rolled-up summary; `--filter k=v`
  (repeatable) narrows it.
- `cg mcp` — a container's `resources/read` carries a `facets` block beside
  the child list; `dimension=value` read parameters (repeatable) narrow both.
- `describe_node_types` — reports each type's declared facets (names, kinds,
  open/closed), so the axes are discoverable from the schema alone.

## Examples

    # CalDAV (framework fold): summarize an account before opening a calendar
    $ cutting-garden list --facets caldav://dav.host/dav/me/
    status:    CONFIRMED 142  TENTATIVE 9  CANCELLED 3
    category:  work 88  personal 51  travel 17  (+4 more)
    year:      2026 96  2025 48  2024 12
    # 154 events across 2 calendars — none opened

    # chrest tabs (one-shot, atomic): one browser call already has every tab
    $ cutting-garden list --facets chrest://firefox-default/
    domain:    github.com 14  localhost 6  news.ycombinator.com 4  (+22 more)
    group:     work 12  reading 8  (none) 17
    # 'domain' is open (discovered). A per-window grouping is intentionally not
    # shown — window ids are not stable keys (see Limitations).

    # closed dimension on one window: informative zeros
    $ cutting-garden list --facets chrest://firefox-default/window-3/
    audible:   false 7   true 0      # nothing is making noise — a real answer
    pinned:    false 5   true 2

    # chrest history (one-shot, but capped at the source): marked partial
    $ cutting-garden list --facets chrest://firefox-default/history/
    year:      2026 41203  2025 38771  2024 20026
    # partial — source returns at most 100000 entries (newest first)

    # nebulous (one-shot from its store): MULTIPLE filters at once — the shape
    # the first draft could not express
    $ cutting-garden list --facets \
        --filter tag=rust --filter status=unread \
        newsblur://account/feed/512/stories/
    year:      2026 28  2025 9
    # = stories in feed 512 that are tagged rust AND unread, by year

    # MCP: a container read carries the summary inline
    $ cutting-garden mcp
    #   resources/read caldav://dav.host/dav/me/work/
    #   → { "nodes": [ {"uri":".../event1.ics","type":"caldav-object-v1",
    #                    "container":false}, ... ],
    #       "facets": { "status": {"CONFIRMED":142,"CANCELLED":3},
    #                   "category": {"work":88,"personal":51},
    #                   "year": {"2026":96,"2025":48},
    #                   "complete": true } }

## Limitations

- **A summary is a snapshot, not a transaction.** Over live data (browser
  tabs) the tree changes between reads; a multi-step drill-down is not atomic
  and counts can shift under you.
- **Partial when the source caps.** A summary marks itself partial when the
  backend bounds what it returns (history) or when an incremental fold hits
  its size cap. It is never silently incomplete.
- **Labels are display-only.** A labelled dimension counts by stable key; the
  human name is looked up only for display and may need a second index. If
  that lookup fails, the value shows as its key — it never breaks the counts.
- **Filtering is equality, AND-ed.** No ranges, "or", or negation; combine
  reads. Free-text search is a search index, not a facet, and stays a
  plugin-local tool.
- **Read-only.** Like the MCP server (FDR 0015), faceting never touches
  capture, restore, diff, or receipts.
- **One plugin's tree.** Facets roll up within a single scheme; there is no
  cross-plugin summary.

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| summary scope | whole subtree of the container read | disclosure wants "everything under here" | clients re-aggregate across reads because subtree scope is wrong |
| fold bound | bounded (FDR 0014's huge-tree guardrail), result marked partial when hit | an incremental fold must not walk unbounded | real lazy trees exceed it and a one-shot path is impractical for that plugin |
| numeric bucket granularity | plugin-supplied, default year | year is the coarse axis most data wants first | users consistently need month/day |
| top-N values per dimension | capped with "(+N more)"; labels resolved only for shown rows | one big dimension (history by domain) must not dwarf output, and label lookups must not run over hidden rows | clients consistently need the full distribution |

## More Information

- RFC 0012 — the normative facet contract this describes.
- FDR 0014 — plugin root traversal; facets ride on its enumeration and roll
  up its tree.
- FDR 0015 — MCP resource server; the container read this extends with a
  `facets` block, and the `describe_node_types` self-description it reuses.
- Prior art: nebulous's `internal/bravo/tools/facets.go` (whole-corpus-only
  by year/tag/feed/status; its "feed label" is actually the newest starred
  story's title, a bug this design's display-only labels prevent);
  cutting-garden's `plugins/caldav/ical` two-tier types (`Event` /
  `EventMetadata`, derived fields, `BodyDescriber`) — the declared,
  self-describing model the facet schema follows.
