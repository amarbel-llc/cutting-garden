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

## Roots as nodes

The root aggregate (`cg list` with no URI; RFC 0007 + FDR 0014) is the
**default anchor**, and each configured root is a **typed node** (tag
settled at implementation, e.g. `root-v1`) with fields (`scheme`,
`account`) and config-declared tags. Root selection is then ordinary
predicate machinery — no root/URI syntax in the language:

    !root-v1 scheme=caldav -> !caldav-object-vevent-v1 dtstart^="20260718"
        # today's events across ALL caldav accounts

    work -> !caldav-object-vevent-v1
        # ... across roots tagged `work` in config.toml

    caldav:fastmail -> component=VEVENT
        # one specific root, named by its URI as an opaque identifier
        # (strict sigil rule, RFC 0014)

Requires: root nodes in the traversal surface (SDK change), and an
optional `tags = [...]` key on RFC 0007 account stanzas (filed as a
config-subsystem amendment). URIs remain host-layer sugar — compressed
ground containment paths (RFC 0014 "Isometry").

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

## Relation to GraphQL, and two future directions (2026-07-19)

Comparing trellis to GraphQL sharpens the boundary above and surfaced two
future directions. The core contrast: **GraphQL is a response-projection
language over a mandatory typed schema; trellis is a set-selection +
graph-traversal language that deliberately decouples selection from
projection** (boundary #4). trellis's traversal (reverse edges, typed
transitive closures) is graph-query territory GraphQL has no operators for;
its query/result **isometry** (a query is a data shape with holes, the
result is the ground-filled espalier, and espalier parses as trellis) has no
GraphQL analog; and trellis carries a first-class version/history dimension
(sigils) GraphQL models only as schema fields. What GraphQL has that trellis
deliberately punts — client-specified projection and a validated typed
schema — maps onto exactly two future directions, each the trellis-shaped
answer (gradual, isometric) rather than the GraphQL-shaped one:

- **Gradually-typed trellis** ([cutting-garden#156]) — plan-time
  type-checking (field existence, operator/value typing, edge-target typing)
  via a plugin-declared schema probed like facets, *optional* so FDR 0018's
  opaque-identifier flexibility survives. Edge typing is nearly free once
  edges are reference-valued fields (hyphence RFC 0002).
- **Mutation through trellis** ([cutting-garden#157]) — extend the isometry
  from read=match to write=construct: an espalier form is both a query
  result and a write instruction. organize (RFC 0015) is the existing
  document-UX prototype; the open design problem is the `=`
  predicate-vs-assignment mode-disambiguation. Typing (above) is what makes
  it safe.

Both want a dedicated grill before any design lands; neither is scheduled.

[cutting-garden#156]: https://code.linenisgreat.com/cutting-garden/cutting-garden/issues/156
[cutting-garden#157]: https://code.linenisgreat.com/cutting-garden/cutting-garden/issues/157

## Whitespace-insignificance via identifier-limiting (explored 2026-07-19)

Trellis requires whitespace around combinators (`a -> b`, never `a->b`) —
RFC 0014 design constraint #4, "whitespace is semantic." A prototype
(GitHub PR #133, **closed unmerged**; findings preserved here) tested
whether that is load-bearing, by pushing disambiguation into the
identifier class at **token granularity** rather than leaning on
langlang's auto-spacer — which stays the wrong tool, since it injects an
*optional* space at every sequence boundary, including the one boundary
that is intrinsically significant:

    IdentRune <- ('-' !'>' !'[') / … / (!Reserved !'-' !SP1 .)

`-` stays identifier content EXCEPT where it begins a combinator token
(`->`, `->>`, `-[`), which lets the required `SP` around combinators
relax to `SP?` in `Path`/`SubPath`/`VersionSub`. `a->b` then
self-delimits, and all four spacings parse identically as the traversal.

Findings, which stand independent of whether the prototype is adopted:

- **`a->b` is not ambiguous.** PEG yields exactly one parse —
  `FieldPred(a-, >, b)` — so the traversal reading is merely
  *unreachable* without the space, not contested. (Independently
  rediscovered while writing the hand-rolled parser's negative-case
  suite: `a->b` had to be swapped for `!task->!done`, the only form with
  no legal FieldPred reading.)
- **Token granularity is the decisive axis.** Reserving the *character*
  `-` breaks isometry — every hyphenated tag becomes quote-soup
  (`"caldav-object-v1"`). Reserving the *token* does not.
- **Isometry survives** token-granular limiting: ground/espalier literals
  already use self-delimiting term shapes, so the serializer round-trips
  unchanged.
- **Validated** under langlang: every RFC 0014 conformance vector parses
  with unchanged meaning; glued forms (`a->b`, `->>!task ^done`,
  `caldav:fastmail->component=VEVENT`, `[->content-8841]`) parse; and
  negative cases (`->>`, `done@`, `"unterminated`, `[]`) still fail.
- **One intended semantic change:** `!task->!done` becomes the legal
  traversal `!task -> !done` rather than a syntax error — which would
  flip that negative case in `internal/trellis`'s parser suite.

Deliberately unresolved residue: the intrinsic bare-term separator; the
term-final sigil suffix; and the backward/comparison collision (`a<-b`),
whose fix would delete dash-led field values (`rank<-3` ceasing to mean
`rank < -3`).

**Scope — combinator delimitation only.** Combinators are a trellis-only
construct, so this question does not reach the sibling grammars: piggy's
`marklid.peg` is whitespace-FREE by construction (a markl-id has no
whitespace anywhere in its wire form), and hyphence's content grammar has
no combinators — a hyphence line carries ONE term, never a
space-separated run, so it has neither of trellis's load-bearing rules.
(hyphence went further on 2026-07-19: its one *required* space — before a
trailing `%` comment — became optional (`SP?`), admitting **glued
comments**, since `%` is already `Reserved` and therefore
self-delimiting. Its content grammar is now semantically
whitespace-insensitive: whitespace is permitted wherever it appears,
required nowhere, and never meaning-bearing. Note the PEG mechanics —
`SP` had to become OPTIONAL, not be deleted: unlike Go/Rust/C/JS, whose
tokenizers skip inter-token whitespace independently of what introduces a
comment, a PEG must consume every byte with some production, so deleting
`SP` would have forbidden the spaced form rather than permitting the
glued one. Trellis cannot follow regardless: its space genuinely
separates terms and delimits combinators.)

Note this is a question about the LANGUAGE, kept strictly separate from
langlang's space-injector: `-disable-spaces` stops the tool inserting
optional `Spacing` at sequence boundaries, which says nothing about
whether the language's whitespace is significant. Adoption stays an open
normative RFC 0014 decision; the normative grammar is unchanged.

## Deferred

See RFC 0014 "Deferred"; additionally here: facets as *named* trellis
predicates (a facet as a stored query the framework counts by);
full-text search-index acceleration for the `_body*=` / `~=` predicates,
probed as a host capability like reverse-edges/facets (RFC 0012 posture) —
an honest walk otherwise (boundary #6): cutting-garden#153;
organize-text × trellis (the organize upstreaming sequence owns it);
dodder-side reverse-reference index (direction: index inversion over the
type-defined field index once edges are fields — see below); edge typing
for dodder references (prerequisite for `-[blocks]->` over dodder
stores) — **direction settled 2026-07-18**: typed edges arrive as
reference-valued fields with locks (`- blocks=task/other @digest`), the
field name as edge label, `_`-reserved framework edges (`_base`,
`_mother`); specified in hyphence#2 / hyphence RFC 0002 (content grammar
against trellis).

## More information

- RFC 0014 — the language; grill decisions ledger 2026-07-18.
- FDR 0014 (traversal primitive), FDR 0021 / RFC 0012 (facets),
  RFC 0007 (roots/config); dodder FDR 0017/0018, dodder RFC 0003.
