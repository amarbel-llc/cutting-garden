---
status: exploring
date: 2026-07-18
promotion-criteria: |
  Promote to `proposed` when the RFC 0014 walkthrough completes (all six
  demo scenarios reviewed), the boundary taxonomy below is confirmed
  against them, and the normative trellis.peg is committed.
---

# trellis — query evaluation over plugin trees

The cutting-garden feature side of RFC 0014 (the trellis query language):
where the evaluator lives, what hosts must provide, what it costs, and
where the language's edges are. dodder is a later consumer (haustoria
direction, dodder FDR 0013); cutting-garden owns the spec and the
reference implementation.

## Shape

- Parser + evaluator in the plugin SDK (`pkgs/`), consuming the traversal
  surface: `RootLister` children, node fields, facet projections
  (RFC 0012), leaf bodies. Hand-rolled recursive-descent Go parser in v1;
  `trellis.peg` is normative and authored langlang-compatible (`//`
  comments, explicit spacing discipline) so codegen is a later drop-in —
  the evaluated dialect delta is comment syntax plus a whitespace-handling
  stanza (langlang auto-space-skipping vs trellis's semantic whitespace).
  RFC examples double as conformance vectors.
- Hosts: `cg list <uri> --query`, the MCP server (container reads with a
  query param), and eventually dodder commands.

## Host capability contract

- **Sigils**: meanings are fixed by the framework; support is per-host
  capability. `:` (live tree) is universally required. `+` = captured
  revisions/receipts; `.` = local materializations (restore output);
  `?` = plugin-defined hidden (cancelled, archived). A host that does not
  implement a sigil REJECTS the query ("sigil unsupported by host") —
  never silent degradation.
- **Traversal acceleration**: v1 evaluation is walk-based (reverse edges
  by scan-and-invert; closures as bounded BFS with visited sets). Hosts
  may advertise native reverse-edge / reachability answering; the
  evaluator probes by type assertion exactly as facet capabilities are
  probed (RFC 0012).
- **Limits**: depth/result caps are host policy, not query syntax.

## Type-tag ergonomics (walkthrough #3)

Trellis assumes the short-style tag grammar (`caldav-object-v1`) as the
querying norm. The `cutting_garden-*`-prefixed holdouts (jira, file) are
painful to query — doddish prefix matching cannot reach the
discriminating suffix of `cutting_garden-jira-issue-v1` — so this FDR
adds migration pressure to cutting-garden FDR 0018's one-tag-grammar
direction.

## Boundary taxonomy (what trellis deliberately is not)

Confirmed against the six demo scenarios (walkthrough in progress):

1. **Plugin coverage** — a query can be expressible while its root isn't
   (e.g. forgejo issues: `!forgejo-issue-v1 created^="2026-07-17"
   state=closed` parses and means the right thing; no forgejo plugin
   exists yet). The language is not gated on plugin existence.
2. **Relative time** — no `today`/`next week` vocabulary; hosts resolve
   relative dates to canonical values before the query runs. Permanent.
3. **Aggregation** — trellis returns sets of (object, version) pairs,
   never numbers; counting is the facet machinery's job (`list --facets`,
   MCP facets block). "How many captures of example.com" = `+` selects
   the set, the host counts; captures-per-year is a facet computed over
   the query result. This scenario is the motivating example for the
   deferred "facets as named trellis predicates": the moment the ask is
   "captures per domain across all web roots," the facet's bucketing key
   wants to be a trellis expression.
4. **Presentation** — no output-format directives. The pipeline framing
   (walkthrough #5): trellis selects; the result serializes as an
   **espalier stream** (RFC 0014 "Isometry" — the ground fragment of the
   language, parseable by the same grammar); a **formatter** is a program
   from espalier to anything (plantuml gantt, alfred, html). dodder's
   type-formatter machinery (`format-object`, formatter scripts as
   blobs) is the existing slot on the dodder side; MCP clients/agents
   consume the stream on the cutting-garden side. One parser, no bespoke
   intermediate JSON.
5. **Graph algorithms / path-valued results** — results are unordered
   sets of (object, version) pairs; critical-path-style computations
   (ordered chains, weights) are host algorithms, with trellis selecting
   the subgraph. **Result projection is a host surface with two modes**
   (walkthrough #6), chosen by the caller, never by query syntax:
   *flat* (pairs only — what a formatter like the gantt pipeline wants)
   and *nested espalier* (pairs plus matched edges as ground subpaths —
   the subgraph handoff a graph algorithm parses with the same grammar).

   **Known unknown, flagged in hoc (2026-07-18):** the nested espalier
   stream has serialization ambiguities that are likely only identifiable
   against real streams — node deduplication when an object is reachable
   via multiple in-edges, inline-vs-by-reference children, cycle
   representation in a nested text form, and ordering significance.
   First implementation must treat these as a conformance-vector
   checklist, not discover them.
6. **Walk cost** — `_body` matching and closures without indices are
   honest walks; acceleration is a capability, not a language feature.

## Deferred

See RFC 0014 "Deferred"; additionally here: facets as *named* trellis
predicates (a facet as a stored query the framework counts by);
organize-text × trellis (the organize upstreaming sequence owns it);
dodder-side reverse-reference index; edge typing for dodder references
(prerequisite for `-[blocks]->` over dodder stores).

## More information

- RFC 0014 — the language; grill decisions ledger 2026-07-18.
- FDR 0014 (traversal primitive), FDR 0021 / RFC 0012 (facets),
  RFC 0007 (roots/config); dodder FDR 0017/0018, dodder RFC 0003.
