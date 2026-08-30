# Native tags: key-free tag atoms and one trellis grammar

**Date:** 2026-08-30 · **Parent:** RFC 0014 (trellis/espalier), RFC 0015
(organize dialect), RFC 0019 (tag interpreter), FDR 0022, FDR 0023, FDR 0025 ·
**Supersedes-in-part:** `2026-08-20-tags-design.md` D3 (group-by spelling) and
the slice-3 hoisted-dialect spellings (`categories/project`, `dim:granularity=`).

Approved in the 2026-08-30 grill (cutting-garden/green-chestnut). Every decision
below was made explicitly by the user; the grill was UX-first — the subjects were
the CLI, trellis, and the espalier/organize document.

## Problem

Tags slices 1–3 made caldav CATEGORIES a groupable, writable, interpreter-governed
dimension — but tags are still carried ONLY by placement under a tag heading. In
a document grouped by anything else an object's tags are invisible; a bare token
inside a box is silently dropped by organize's ad-hoc box parser; `--group-by`
resolves bare names by a token-shape heuristic (field if a field exists, else
tag); the granularity/namespace spellings (`date_due:month=`,
`categories/project`) are organize-private encodings; and `list` shows tags
nowhere. RFC 0014's isometry ("an organize document is a materialized query
result in the query's own syntax") is not enforced by construction: organize,
`_group-by`, `_base`/`_type`, and heading quoting each re-implement a slice of
the term grammar.

The goal: **a plugin-declared tag field surfaces as key-free atoms in
trellis/espalier**, and that ONE representation drives `--group-by`, query
matching, and headings — parsed and rendered by the one trellis grammar.

## Decisions

### G1 — Tags render as bare atoms after id/type

In a box interior, an object's tag set renders as key-free espalier terms,
ordered by the interpreter's `SortKey`, placed after the id/`!type` and before
the `name=value` atoms (doddish box order):

    ## =NEEDS-ACTION
    - [nsA.ics project-client-acme work date_due=2026-09-01] Acme retainer
    - [nsD.ics other] Loose idea
    - [field4.ics] Someday idea

Lever `_tag-atoms = leading | trailing | none` (default `leading`). Rationale for
the default: the planned nvim eliding collapses ids + `name=value` atoms into an
ellipsis, so leading tags + trailer are the resting-state line.

### G2 — When tag-grouped, strip only the placement tag(s)

Under a tag heading, the tag(s) that PRODUCED the placement (the membership's
`Via`) are stripped from the box; every other tag stays (the #229 rule: placement
carries it, never also an atom).

    - _group-by = project
    ## -client
    - [nsA.ics work] Acme retainer          ← project-client-acme stripped; work stays
    ## -cutting_garden
    - [nsC.ics] CG roadmap
    - [nsD.ics other] Loose idea            ← ungrouped: nothing stripped

Lever `_tag-strip = placement | none` (default `placement`). Apply reads it: an
object's tag set per appearance = placement-derived tag(s) ∪ box atoms.

### G3/G14 — Levers are data-plane `-` envelope fields

`_tag-atoms` and `_tag-strip` are `- _<name> = <value>` envelope fields
(hyphen-joined, per `_dry-run` / `_group-by`). They are DATA plane, not `%:`
operational: `_tag-strip` changes what an elided tag MEANS to apply, so it is
document semantics, content-addressed into `_base`. Defaults are omitted on
generate (existing docs stay byte-identical); `[organize]` config may set the
defaults (as `date_granularity` does); the document's explicit field wins.

### G4 — `cg fmt-organize <path>`

A formatter that regenerates a document FROM ITS ENVELOPE (`_anchor`, `_query`,
`_type`, `_group-by`, levers) against live data, re-renders, and rewrites
`_base`. v1 REFUSES when the body differs from its pinned base ("doc has
unapplied edits; apply or discard first"). v2 (future) carries pending edits
forward and re-places object lines. `fmt-organize` never emits empty (reset)
headings.

### G5 — `--query` only; `--filter` out of scope

Bare tags are already query terms (`matchTag`). `--filter` (RFC 0012 §6,
`dim=value,…` with comma = AND — the opposite of trellis's comma = OR) is NOT
extended; it is out of scope and slated for replacement by `--query` on every
surface (`list --facets`, mcp `read_facets` / `list_nodes filter=`). Tracked as
a followup.

### G6 — The tag set is a derived codec field

v1 follows caldav: one presented tag set per node type. Architecturally the tag
set is a CODEC-produced presentation field (kind `FieldTag`), exactly like
`date_start`/`date_end`: the plugin's codec maps stored properties → the
presented tag set (`Format`) and back (`Parse`), so a plugin with several stored
tag-like properties merges/splits them itself. Consequence: `categoriesCodec.
Format` must actually produce the tag set (today it is empty; values come from
the counting path).

### G7 — Box tag atoms are writable membership edits

Adding/removing a bare atom in a box is a membership add/remove through the same
`planMemberships` → interpreter `Complete` path as a heading move. Rules:

- per-appearance tag set = placement-derived tag(s) ∪ box atoms, with
  `_tag-strip` telling apply what was elided;
- N-way: appearances must agree on the non-placement tags; disagreement is a
  conflict naming the appearances;
- a typed atom is EXACT under every interpreter (`Complete` is exact, RFC 0019
  §6.2) — typing `project` adds the literal tag `project`;
- `%`-prefixed atoms are display-only (RFC 0015) for computed/derived tags.

### G8 — `list -format espalier`, JSON `tags`, mesa tables (last slice)

`list -format espalier` emits one box line per node through the SAME literal
writer organize uses (tags leading, atoms, trailer) — so `list --query project
-format espalier` shows exactly the boxes organize would. `list`'s text table
moves onto `code.linenisgreat.com/purse-first/libs/dewey/pkgs/mesa` (purse-first
FDR 0015 / RFC 0003).

### G9 — Bare is always a tag; fields MUST use an operator

- Box interior: first token is the id, `!x` the type, `k OP v` a field atom
  (organize accepts only `=`), anything else is a tag. No field-key shadowing
  check — a tag literally named `status` is the tag `status`.
- Query step: a bare identifier is always a tag term; a field is only ever
  addressed with an operator. Mid-query id selection stays deferred (FDR 0022).
- Quoting: a tag containing whitespace or a reserved rune is emitted as a
  trellis `String` (`"_ inbox"`) and unquoted on parse — same as headings.

### G10 — Parenthetical = meta qualifier; `--group-by` follows G9

`(` and `)` join the reserved runes (hyphence RFC 0002's content grammar first,
then the trellis PEG + `internal/trellis` lexer); a parenthetical is a META
QUALIFIER on a term. One spelling for the `--group-by` flag, the `_group-by`
envelope field, and the dimension heading:

| spelling | meaning |
|---|---|
| `(tags)` | the type's whole tag set (one bucket per tag) |
| `project` | tag namespace `project` (bare = tag) |
| `status=` | field grouping |
| `date_due=(month)` | field grouping at month granularity |

`(tags)` has no dimension heading: buckets are `# <tag>`. Namespace buckets
stay continuations (`## -client`, or `# -client` after normalization). These
replace `categories`, `categories/project`, and `date_due:month=`.

**Heading depth is normalized**: the shallowest heading level present renders as
`#`; the parser stays structure-only (`#`-depth + text), generate/fmt emit
minimal depth.

**Empty headings are resets**: an empty heading at depth N pops the heading
context at N and deeper, leaving subsequent object lines under the depth N−1
context (`#` alone returns to the ungrouped context). A reset deeper than the
current context is a no-op. Re-entering a bucket needs a new non-empty heading.
Generation and `fmt-organize` never emit them.

    # work
    - [a.ics] …            ← under work
    ## -client
    - [b.ics] …            ← under work, -client
    ##
    - [c.ics] …            ← under work only
    #
    - [d.ics] …            ← ungrouped

### G11 — nvim grammar per slice

`zz-nvim/grammars/organize` (tree-sitter) and its corpus are updated in the same
slice as each dialect change — the corpus is the dialect's conformance vector.
The elide-on-hover UX is a separate nvim-side effort.

### G12 — JSON / MCP shape

Node views (`list -format json`, the MCP enriched listing) gain a top-level
`tags` array (the presented tag set, interpreter-sorted). `facets.categories`
stays until `--filter` retires. `describe_node_types` reports the type's tag set
(`tag_set`, with its interpreter) so an MCP client knows which field bare terms
address.

### G13 — One grammar

`internal/trellis` parses and renders every term: box interiors (`Group` →
id / `TypeTerm` / `IdentBasicTerm` tags / `FieldPred` atoms; organize validates
GROUNDNESS — only `=`, no lists/sigils/subpaths — and rejects loudly),
heading terms, `_group-by`, `_base` (`DigestTerm`), `_type` (`TypeTerm`). A
shared trellis literal writer serves organize and `list -format espalier`.

Inventory of hand-parsers to retire (accidental grammar children):

| where | hand-parses | becomes |
|---|---|---|
| `organize/document.go` `parseObjectLine` / `writeObjectLine` | box interior | trellis `Group` parse / literal writer |
| `document.go` `valueName`, heading `strconv.Quote/Unquote` | heading `=value`, quoting | trellis `FieldPred` / `Ident` / `String` |
| `document.go` `groupedSpec`, `groupspec.go` | `dim:granularity=`, `dim/namespace` | `dim=(month)`, `(tags)`, `project` terms |
| `document.go` `_base` / `_type` prefix stripping | `@…`, `!…` | `DigestTerm` / `TypeTerm` |
| `cutting_garden_plugins/facet.go` `ParseFacetFilter` | `dim=v,…` | retiring with `--filter` — untouched |
| `zz-nvim/grammars/organize` | tree-sitter re-implementation | stays; corpus mirrors the PEG vectors |

### G15 — Slicing

0. **hyphence**: `()` reserved in RFC 0002's content PEG + vectors.
1. **One grammar**: vector conversion of the existing organize bats (awk/partial
   → whole-document); trellis-owned box/heading parse + writer; `(…)` term;
   converged `--group-by`/`_group-by`/heading spellings; depth normalization;
   empty-heading resets; nvim corpus.
2. **Tag atoms**: `categoriesCodec.Format`; key-free atoms + `_tag-atoms` /
   `_tag-strip` (+ config defaults); `Via`-strip; atom edits → memberships;
   nvim corpus.
3. **`cg fmt-organize`** v1.
4. **`list`**: `-format espalier`, JSON `tags`, `describe_node_types` `tag_set`,
   mesa tables.

### G16 — Vectors

Every decision above is pinned by a WHOLE-DOCUMENT bats vector (dodder's
`assert_output - <<-EOM` form): the exact invocation, the full expected
document (envelope + body), and for edits the full input document plus the
resulting state re-listed via `list -format espalier`. One `@test` per decision,
named by grill number. `lib/golden.bash` is ported (`assert_golden`,
`CG_UPDATE_GOLDENS=1`, `just test-bats-update-goldens`) for bulk outputs (mesa
tables). Normalization masks only the testserver's random port; `_base` digests
stay verbatim. The existing organize lanes' awk/partial asserts convert to
whole-document vectors FIRST (behaviour-neutral) so the dialect changes show up
as reviewable vector diffs. `…-native-tags-vectors.md` indexes G# → test.

## Followups (filed, not built)

- `--filter` → `--query` on every surface (list, mcp `read_facets` /
  `list_nodes`), retiring `ParseFacetFilter` and `facets.categories`.
- `fmt-organize` v2: edit-preserving reformat (re-places object lines).
- nvim elide-on-hover for ids + `name=value` atoms.
- Per-account interpreter override (tags design A2 deferral) — unchanged.

## Out of scope

Plugin migrations (nebulous/jira/fastmail) off the legacy interfaces; #232
tag-object editing; the interpreter wire transport.
