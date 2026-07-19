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

    !root-v1 scheme=caldav -> !caldav-object-v1 dtstart^="20260718"
        # today's events across ALL caldav accounts

    work -> !caldav-object-v1 component=VEVENT
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

## Amendment: whitespace-insignificance via identifier-limiting (2026-07-19)

status: exploring — prototype only. The normative grammar
(`docs/rfcs/0014-trellis.peg`) is UNCHANGED; the prototype lives beside it
at `docs/rfcs/0014-trellis-whitespace-optional.peg` and is validated (see
below) but not adopted.

### The question

"Whitespace is semantic in trellis" (boundary #… ; RFC 0014 principle 4)
was taken as a fixed constraint — and the langlang-compatibility pledge
disables builtin spacing precisely because of it (`validate-grammar` runs
`-disable-builtins -disable-spaces`). This amendment records a walkthrough
of whether that constraint is actually load-bearing, and how much of it can
be removed by GRAMMAR restructuring (not by langlang's auto-spacer, which
stays the wrong tool — it injects an OPTIONAL space at every sequence
boundary, including the one boundary that is intrinsically significant,
below).

### What "whitespace is semantic" actually decomposes into

It is not one property. Pulling it apart:

1. **AND-by-adjacency is already the model, and its space is INTRINSIC.**
   `Step <- Term (SP Term)*` is juxtaposition-AND. Two bare identifiers
   need a separating space *regardless of any whitespace policy*, because
   `IdentRune` excludes `SP1` at the token level: `todo urgent` is two
   terms, `todourgent` is one, always. This is tokenization, not a rule
   that could be relaxed — under exclusion-class opaque identifiers there
   is no other delimiter. Not removable, and not the interesting part.

2. **The load-bearing required space is around COMBINATORS**, and its job
   is to break shared-prefix lexical collisions, not to express AND. The
   canonical case: `a->b` is NOT parser-ambiguous — PEG yields exactly one
   parse, `FieldPred(a-, >, b)`, because `IdentRune` greedily absorbs the
   `-` into `a-` and `>` is a valid `FieldOp`. The traversal reading
   `a -> b` is merely *unreachable* without the space (confirmed against
   langlang: parse tree shows `FieldName "a-"`, `FieldOp ">"`,
   `Value "b"`). So the space is a two-meanings-one-string discriminator,
   not an ambiguity resolver.

3. **The term-final SIGIL SUFFIX is boundary-defined** (the strict sigil
   rule): `todo:` vs `todo:x` differ by the following rune. This cannot be
   made whitespace-free without abolishing colon/dot-bearing opaque
   identifiers (`caldav:fastmail`, `12.7`). Irreducible — but it does not
   touch ground/espalier literals (which carry no explicit term-final
   sigils), so isometry is unaffected.

### The finding: limit the identifier, at TOKEN granularity

The clean move (credit: the walkthrough's second turn) is to push
disambiguation into the identifier class rather than gating operators —
but the operative unit must be the operator TOKEN, not its leading
CHARACTER. "Characters that start operators cannot be unescaped identifier
terminals" is already true for 11 of the 15 operator-leading characters
(they are in `Reserved`); the residue is exactly `-`, `:`, `.`, `?` — the
opaque-reference alphabet. Reserving those *characters* wholesale forces
quoting the bulk of the real corpus (`"caldav-object-v1"`) and turns
espalier serialization into quote-soup — the isometry cost. Reserving
against the operator *token* does not:

    IdentRune <- ('-' !'>' !'[') / '/' / (SigilRune &IdentRune)
               / (!Reserved !'-' !SP1 .)

A `-` is identifier content EXCEPT when it begins `->`/`->>` (`!'>'`) or a
typed edge `-[` (`!'['`). (Because `-` is not in `Reserved`, the exclusion
alternative must also drop it, `!'-'`, so the guarded branch is the only
admission.) With the required `SP` around combinators relaxed to `SP?` in
`Path`, `SubPath`, and `VersionSub`, `a->b` now self-delimits into
`a` / `->` / `b`, and all four spacings (`a->b`, `a-> b`, `a ->b`,
`a -> b`) parse identically as the traversal.

### Isometry survives — this is the decisive result

The earlier claim (walkthrough turn 1) that isometry *forces* significant
whitespace was WRONG. What isometry needs is self-delimiting term SHAPES,
which the ground fragment already has: `!type`, `key=value`, `@digest`,
`[-> …]`, bare id. The only whitespace in a ground espalier literal is
bare-id adjacency (point 1, intrinsic). Under the token-granular
identifier-limiting route the serializer emits *exactly what it emits
today* and it round-trips — no quote-soup. Verified: the espalier vector
`[story-8841 !newsblur-story-v1 year=2026 [-> content-8841 @blake2b256-…]]`
parses unchanged under the prototype. It is the *character*-granular route
that would have broken isometry — which is why token granularity is the
whole game.

### Residue this prototype deliberately leaves

- **Term-AND boundary stays required-`SP`.** Optionality is scoped to
  combinator and subpath-head boundaries. A group or prefixed term
  following another term (`!web-page-v1 [+]`, `!task ^done`) keeps its
  space — both because bare-value adjacency (`year=2026 feed=hn`) must
  stay a loud error rather than silently re-lex as `year=2026feed` +
  `=hn`, and to keep the rule uniform. Consequence: `!web-page-v1[+]` is
  still rejected glued (a conservative, revisitable choice).
- **Backward/comparison collision NOT resolved.** `a<-b` still parses as
  `FieldPred(a, <, -b)`, because `<` is consumed by the field-operator
  layer before `Path`'s combinator loop runs. Forcing `<-`/`<<-` to win
  when glued needs a `FieldOp` gate (`'<' !'-'`) whose price is DELETING
  the "less-than a dash-led bareword value" spelling (`rank<-3` ceasing to
  mean `rank < -3`, requiring `rank<"-3"`). That is a language-level policy
  call left to reviewers, not silently baked in — so forward and backward
  arrows are asymmetric in the prototype, by design.

### Validation

`docs/rfcs/0014-trellis-whitespace-optional.peg` parses under langlang
(`-grammar-ast -disable-builtins -disable-spaces`, same lane as
`validate-grammar`). Every RFC 0014 conformance vector parses with
unchanged meaning; the new glued forms (`a->b`, `!task->!done`,
`->>!task ^done`, `caldav:fastmail->component=VEVENT`, `[->content-8841]`,
`[+state=closed]`) parse; the negative cases (`->>`, `done@`,
`"unterminated`, `[]`) still fail. One CHANGED case:
`!task->!done` — previously a documented syntax error (no `FieldPred`
escape for a `!`-led name) — is now the legal traversal `!task -> !done`,
the intended consequence of glued combinators (would flip
`internal/trellis/parser_test.go`'s corresponding negative case if the
hand-rolled parser adopted this grammar).

### Net

Whitespace in trellis can be made insignificant across the arrow and
bracket surfaces, and throughout the ground/espalier form, via
identifier-limiting (token granularity) + optional combinator/subpath
`SP`, with isometry intact. The irreducible remainder is the explicit
term-final sigil suffix in query position and the intrinsic bare-term
separator — a small, well-contained set, not the pervasive dependency the
"whitespace is semantic" headline implies. Whether to adopt (and whether
to pay the backward-collision cost for symmetry) is a normative decision
for RFC 0014; this amendment and the prototype exist to make that decision
reviewable rather than asserted.

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
