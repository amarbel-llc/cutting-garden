---
status: proposed
date: 2026-07-18
revised: 2026-07-18 (deletion semantics split by grouped-ness; unresolved
  intents as a first-class apply outcome — surfaced by the dodder
  changes.go trace showing today's selection-tag stripping)
---

# The organize document dialect

## Abstract

An organize document is a **base-pinned set transaction rendered as text**:
a trellis query's result set, grouped into facet buckets, serialized as an
espalier dialect, edited by a human or agent, and interpreted as a
structural delta whose writes flow through each substrate's declared
mapping. This RFC specifies the document format and its delta semantics.
The feature built on it — the `cg organize` command, the mapping
capability, the apply engine — is FDR 0023.

Layering: hyphence RFC 0001 (envelope) + hyphence RFC 0002 (content
grammar against trellis); cutting-garden RFC 0014 (the trellis/espalier
grammar — headings and object lines parse under it); RFC 0012 (facet
schema, which this RFC extends with write descriptors); FDR 0020 /
ContainerCreator (#143) as the write surface. Precedent throughout is
dodder's organize (orgie), whose confirmed behaviors this dialect
preserves or deliberately supersedes — each divergence is marked.

## Core principle

**Headings are writable facet buckets.** A heading is a ground trellis
term; every object beneath it satisfies that term; moving an object under
a heading is an instruction to *make the term true* through the
substrate's mapping. Generation (facet bucketing → headings) and
interpretation (heading position → writes) are fully decoupled: the
parser is structure-only (`#`-depth and heading text), exactly as
dodder's reader already behaves — it never knows or cares what grouping
generated the tree.

## Document structure

A hyphence document. **Metadata section** (RFC 0001 envelope, RFC 0002
content grammar), governed by the **distribution rule**:

> Metadata-section terms distribute over the document's objects — except
> `_`-reserved terms, which address the document/operation itself.

- `- <bare-identifier>` / `- key=value` — distributed: applied to every
  object (dodder's document-tags convention, generalized to fields).
- `- _base=@<digest>` — REQUIRED. The generated document's canonical
  ground form, stored as a content-addressed blob typed
  `organize-base-v1`. A document without `_base` is invalid — organize
  documents are ephemeral action, not durable artifacts; there is no
  legacy mode. (Divergence: dodder's live-only fork-overlay is
  superseded; dodder adopts `_base`.)
- `- _allow-deletion=true` — OPTIONAL settings field; see Deletion.
  Settings are `_`-reserved document fields, never comments (dodder's
  `% dry-run:true` is the legacy-to-align spelling; comments are opaque
  per RFC 0001 and MUST NOT carry behavior).
- `% …` comments — genuinely inert (provenance, e.g.
  `% generated: cg organize …`).
- `! <type>` — LAST line (RFC 0001 canonical order): the **type
  anchor**, naming the substrate whose mapping gives every heading its
  meaning, and the default type for created objects (dodder's dual
  reading, preserved).

**Body**: markdown-style headings + object lines.

### Headings

- A heading's text is one or more space-separated **ground trellis
  terms** (conjunction). Comma is NOT valid in headings. (Divergence:
  dodder's conjunctive comma headings are dropped; dodder migrates.)
- Depth (`#` count) expresses nesting only; effective terms for an
  object = the union of its heading path, composed by the laddering
  rules below.
- **Dependent-dimension sugar**: a heading may be a `PartialTerm`
  (`date=` — field + operator, no value); descendant headings may be
  dependent values (`=2026-07-22` — operator + value). Resolution
  composes them (`date="2026-07-22"`). This lives at **runtime
  resolution, deliberately not the grammar**: a grammar-level spelling
  would be context-sensitive and break the PEG, so `PartialTerm`s parse
  context-freely everywhere and validate only in heading position (the
  `~=` / `-[p]->>` pattern). The `=` overload resolves positionally —
  under a pending dimension a leading-operator term (`=2026-07-22`) reads
  as a dependent value; elsewhere `=` keeps its exact-match meaning.
- **Laddering** is per-dimension composition of parent+child heading
  content: hyphen-joined for tags (dodder's `expandedTags`, verbatim),
  path-segment-joined for directories, calendar decomposition for
  dates. Generation-side compression (showing `-q3`, `=22`, `=rfcs`
  under their parents) is the same-flag inverse (dodder's
  prefix-joints, generalized). Adaptive refinement (synthesizing
  intermediate buckets from the data, collapsing redundant ones) is a
  per-dimension generation strategy (dodder's refiner, generalized).
- `%`-prefixed heading terms mark **read-only scopes** (`write: none`
  dimensions): generation derives the mark from the schema; the schema
  stays authoritative; edits under (moves into/out of) a `%` scope are
  validation errors.
- **Ungrouped objects list before the topmost heading** (dodder orgie's
  convention, preserved): rather than a synthetic `Ungrouped` heading,
  objects with no value for the grouped dimension render *above the first
  `#` heading* — that pre-heading position IS the implicit "ungrouped"
  marker. On apply it reads as membership-∅ for the grouped dimension,
  consistent with the deletion-by-grouped-ness rule.

### Object lines

`- ` followed by an espalier literal (box interior) and description
trailer, per RFC 0014's isometry. `%` object-line prefix (virtual /
inferred type) preserved from dodder. `%`-marked atoms inside the box
(computed tags, derived fields) are display-only; editing them is an
error. Object ids are substrate node ids (strict sigil rule; quoted for
reserved runes). Aliased short ids are reserved-but-unspecified
(deferred).

### Bindings

Hyphence bindings (the `<` document-scoped lock table; hyphence
RFC 0003/0004) resolve across **both** layers of an organize document: a
name bound once (`fred < task/other@digest`) is usable as a metadata field
value (`blocked-by=fred` resolves through the table) *and* as a body
reflink (`[fred]: …`, markdown-style), so one binding serves distributed
metadata and typed-body content alike. Bindings are document-scoped, shadow
lexically, and are not themselves queryable (hyphence RFC 0003); a pinned
identifier bypasses the table.

## Write descriptors

RFC 0012's `FacetDimension` gains `write: none | one | many` plus the
mapping to the underlying mutation (which field a bucket value patches,
the completion rule for underdetermined values, whether the write is
identity-affecting, creation requirements). Facet keys and fields are
one namespace (RFC 0014); writability is a declaration, never inferred
from shape (the proof pair: newsblur `user_tag` write:many vs
`story_tag` write:none — identical shapes).

- **`write: one`** — moving between buckets reassigns; an object under
  two sibling buckets of the dimension is a validation error; absence /
  un-bucketed placement is a **no-op** (an exclusive field cannot be
  unset by position); nullable fields declare an explicit empty bucket.
  `closed: true` composes: the bucket set is the value set; novel
  buckets are validation errors.
- **`write: many`** — clone-per-match rendering (dodder's confirmed
  mechanism, incl. exact-match→ungrouped); **membership = the set of
  headings the object appears under in the patch; absence from the
  patch = the empty set** (clearing membership and total line deletion
  are the same statement). Removal from one heading removes that value
  only. On an **open** dimension a freshly-typed heading is legal and
  **creates** that value (`## ="needs-triage"` adds the label) — the
  converse of the closed-set `novel buckets are validation errors` rule
  above, and what makes that rejection meaningful. (Applies to GROUPED
  documents; see "Deletion semantics by grouped-ness" for the ungrouped
  case.)
- **`write: none`** — groupable for viewing; `%`-marked; moves are
  errors. A document whose entire patchable projection is empty is a
  **view**: generation emits it output-only and says so.

Only ONE dimension may be grouped per document (it may drill into
itself). Cross-dimension nesting is out of scope — far-future/never.

## Delta semantics

Three inputs: **base** (dereferenced `_base` — what the user was shown),
**patch** (the edited document), **live** (the substrate now).

- **patch − base = intent.** Moves, membership changes, trailer/field
  edits, creations, adoptions — all computed structurally.
- **live − base = drift.** Drift on fields the patch also touches ⇒
  conflict: v1 rejects loudly; the end state is a mergetool presenting
  the conflict for intent selection (near-deferred, dodder merge-tool
  precedent). Drift on untouched fields merges silently. Convergent
  edits are idempotent no-ops.
- **The patchable projection is pinned**: base and patch carry only what
  the mapping declares patchable/displayable; the digest certifies what
  the user saw.

### The base blob's shape (ruled 2026-07-18, in-hoc)

Surfaced by the first implementation (dodder #374(b)): the base cannot
contain its own digest. The resolution: **the base blob is an
`organize-base-v1` hyphence envelope whose metadata carries only
generation parameters** (`- _group-by="…"` iff grouped; provenance
comment; type line last) **and whose body is the outer document's
canonical text with exactly one line excised: `- _base=@…`** (the
self-reference). Generation renders the full document without `_base`,
writes the blob, obtains the digest, then inserts the `_base` line at
its canonical position. Apply excises the patch's `_base` line
symmetrically — base-body and patch-sans-`_base` are the same document
class, parsed by the same parser. The body carries the outer document's
distributing metadata (document tags, the substrate type anchor)
faithfully, since edits to those are intents the diff must see; the
envelope's `! organize-base-v1` and the body's substrate anchor are
different layers, never in conflict. The `! organize-base-v1` type line
is descriptive text *inside the blob's own bytes* — it MUST NEVER be
materialized as a committed, listed, or queryable object in any substrate
(no dodder type object, no cg node). The blob is pure infrastructure:
content-addressed for durability and `_base` dereferencing, and invisible
to user-facing output in every mode. (Ruled dodder-side 2026-07-18 — the
defect is a real object existing at all for infrastructure, not merely its
creation being printed; cutting-garden inherits the same invariant.)
- Divergent edits to clones of one object: conflict (detectable only
  via the base).

### Deletion semantics by grouped-ness

Whether the document is grouped is the defining factor for what a
deleted line means (ruled 2026-07-18, after tracing dodder's actual
behavior — which strips the invoking query's selection tags on
deletion, a semantics the two rules below supersede):

- **Grouped document**: deletion (= absence) empties the **grouped
  dimension** — membership-∅ for `write:many`, no-op for `write:one`.
  Selection predicates are NEVER written; they are read-only context.
- **Ungrouped document**: deletion means **removal from the
  selection**. Each selection term is evaluated for writability through
  the mapping: writable terms (a `write:many` tag — dodder's
  `organize tag-5` workflow, preserved and now principled) are removed;
  a non-writable or underdetermined selection term (a field predicate
  like `dtstart^="202607"` — "make this false" has no defined write)
  produces an **unresolved intent**.

**Unresolved intents** are a first-class apply outcome — neither
silently dropped nor flatly rejected. Each carries the object, the
impossible/underdetermined write, and its resolution options ("cannot
remove a start date for X without replacing it" → supply replacement /
skip / abort), and identical intent shapes are **batchable** (one
prompt resolving the same question across N objects). In v1 the apply
engine reports them as structured rejections enumerating each intent
with its options (the failure-surface shape of cutting-garden#147);
the mergetool milestone (Deferred, near) makes them interactively
collapsible — its scope explicitly includes intent-underdetermination
alongside base/live conflicts, batch-capable.

### Creation and adoption

| Patch line | Meaning | Action |
|---|---|---|
| No id | new object | create (ContainerCreator): type from anchor/inline, fields from heading path + inline atoms (+ optional `@digest` content where the substrate supports it); **identity allocated by the substrate and reported back** |
| Id in patch, absent from base, known live | adoption | apply the heading path's writes to the existing object |
| Id unknown to base and substrate | error | loud rejection |

Identity-affecting writes (fs `mv`) likewise report the resulting id;
apply treats them as allocation-like. The three-way merge survives a
live-side rename **only because of content-addressing**: for an
identity-affecting substrate where the path *is* the id, base↔live
re-association across a move matches on the box line's `@digest` (the
stable content identity), not the changed id — which is also why the
deferred id-aliasing ergonomic must survive id churn.

### Deletion

Substrate deletion is expressible ONLY when all gates pass: the
document carries `- _allow-deletion=true`; apply computes the deletion
set (in base and substrate, absent from patch, beyond membership-∅
semantics) and requires **explicit post-editor confirmation**; in
`commit-directly` mode a CLI flag is additionally required (double
assertion for the scripted path). Without the settings field, line
absence never deletes.

The gates guard only substrates with a **true-delete** operation (an
object ceasing to exist). A substrate whose removals are all
soft/membership mutations has nothing for them to guard, so
`_allow-deletion` still parses (round-trip portability across substrates)
but gates nothing there. dodder is the reference case (ruled dodder-side
2026-07-19): its `write:many` tag-clears only un-tag a still-
history-queryable object, and it has no hard-delete primitive at all — so
the gates are inert for it, revisitable only if dodder grows a real
delete operation or a shared apply engine needs the interface uniformly.

## Modes

| Mode | Absence means | Writes |
|---|---|---|
| edit (default) | membership ∅ (`many`) / no-op (`one`) | metadata write-through |
| edit + deletion | as above; wholly-absent objects → confirmed deletion | write-through + confirmed deletes |
| filter | **excluded from the operation** | none — the driving operation consumes the survivors |

Filter mode is dodder's `der checkout/add/clean -organize` made
first-class; cutting-garden analogs: `cg capture --organize`,
`cg restore --organize`. Filter-mode documents are selection-only in v1
(combined filter+edit deferred). Invocation modes (interactive /
commit-directly / output-only) are orthogonal and carry over from
dodder.

## Validation (loud-rejection catalog)

Unmapped metadata for the substrate; moves under `%`/`write:none`
scopes; `write:one` multi-bucket placement; closed-set violations;
divergent clone edits; conflicting live drift (until mergetool);
unknown object ids; stale/undereferenceable `_base`; deletion without
its gates; PartialTerms outside heading position; comma in headings;
field-residence collisions — a key authored in BOTH its metadata and
body homes (the distributed `blocked-by=fred` term vs a `!task` body's
own `blocked-by`), a loud bidirectional error since every field key has
one type-declared authored home and the write-through targets that
residence (dodder#377; FDR 0023 §Dependencies).

## Deferred

Mergetool (near). Aliased object ids; combined filter+edit; empty-bucket
declaration ergonomics; dodder-as-overlay for unmappable substrates
(deferred). Cross-dimension nesting (far-future/never). Espalier
nested-stream serialization unknowns: FDR 0022's in-hoc checklist
applies to `organize-base-v1` blobs.

## See also

FDR 0023 (the feature); RFC 0014 + `0014-trellis.peg`; RFC 0012;
hyphence RFC 0001/0002 (hyphence#2); FDR 0020 + cutting-garden#143;
dodder orgie (`constructor.go`, `reader.go`, `refiner.go`,
`assignment.go` — the confirmed behaviors cited throughout);
organize-text(7) (superseded by this dialect for cutting-garden;
dodder-side reconciliation tracked in the dodder alignment issue).
