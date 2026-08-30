---
status: testing
date: 2026-08-13
promotion-criteria: |
  Promote to `experimental` once Slice 1 lands: the unified field descriptor
  + codec type exists in the SDK, caldav is migrated onto it, and the existing
  facet/listing-field/box-atom surfaces are re-expressed through it with no
  behavior change (the current organize bats stay green after the rename).
  Promote to `testing` once the model delivers a NET-NEW capability end to end —
  the first of: lowercase status/component (case-fold codec), `--group-by
  date_start` (#230), or `--group-by categories` with the N-way merge — against
  the caldav testserver. MET 2026-08-20: #230 delivered end to end
  (prefix-granular date grouping/filtering; see the dated status note). Promote to `accepted` once every in-tree plugin
  (nebulous, jira, fastmail, …) is migrated off the legacy facet/listing-field
  interfaces and those interfaces are deleted, and the N-way merge has gone two
  weeks without a correctness lever moving.
---

# Unified field-codec model

## Problem Statement

cutting-garden today carries **two parallel concepts** for a plugin's data:

- **Facets** (`FacetDimension`/`FacetValue`, `FacetDescriber` + `FacetWriteApplier`,
  RFC 0012) — the *groupable* surface: `## =value` buckets, `-group-by`,
  `list -query`.
- **Listing fields / box atoms** (`ListingField`/`BoxAtom`, `FieldPresenter` +
  `FieldWriteApplier`, FDR 0023) — the *inline* surface: box interiors and
  field edits.

The two overlap badly. A dimension can be *both* (caldav `status` is, after the
field-editable-status change) and then renders as **both a grouping heading and
a box atom at once** — the cutting-garden#229 redundancy, which also opens a
contradictory-edit footgun. The date "codec" (split `DTSTART` into
`date_start`/`time_start`, recombine on write) lives ad-hoc on the atom side
only. There are **two separate write appliers** and two merge paths
(`planMoves` for facet moves, `planFieldEdits` for field edits). And a run of
requested features each straddle *both* surfaces at once:

- **lowercase status/component** (presentation only) must apply to bucket
  headings *and* box atoms, and reverse on both writes;
- **dates as groupable facets** (#230) — `date_start` should be a grouping
  dimension, not just an atom;
- **tags** (caldav `CATEGORIES`) must be readable, writable, *and* groupable —
  and multi-valued;
- the **cancel-remap** (move to `cancelled` → write `STATUS:COMPLETED` + append a
  category) is a facet move that writes *two* stored properties.

Writing each of those against the split model means writing the transform
twice (facet side + atom side) and keeping two write paths in sync. This record
proposes collapsing the two into **one field abstraction with a codec**, so the
renderer — not the plugin, and not two duplicated declarations — decides how a
field is presented.

## Model

### Two layers, bridged by codecs

A plugin declares **two layers**:

1. **Stored fields** — the raw property slots (`DTSTART`, `STATUS`,
   `CATEGORIES`). This is the unit of **persistence** and of **three-way
   conflict detection** (the pinned base and re-queried live are compared here).
2. **Presentation fields** — the codec-produced components (`date_start`,
   `time_start`, `status`, each individual tag). Each carries the presentation
   **metadata**: `key`, `label`, `groupable`, `inline` (presentable as an atom),
   `writable`, `kind` (categorical / date / numeric-bucket / tag), declared
   `values`, `terminal-values` (#214), `order`. The presentation field is the
   unit the **renderer places**, that `-group-by` / `list -query` **name**, and
   that a **field edit targets**.

**Codecs** bridge the two. A codec is a declared, **reversible, M↔N** transform:
`format(stored…) → presented…` and `parse(presented…) → stored…`. Cardinalities
fall out of one mechanism:

| codec | stored ↔ presented |
|---|---|
| case-fold | `STATUS` ↔ `status` (1↔1; `COMPLETED`↔`completed`) |
| date-split | `DTSTART` ↔ `date_start` + `time_start` (1↔2) |
| tags | `CATEGORIES` ↔ each tag (1↔N) |
| *future many→one* | `DTSTART` + `TZID` ↔ one zoned datetime; `RRULE`+`EXDATE` ↔ one "repeats" |

The **metadata lives on the presented component, not the stored field** — a
component sourced from several stored fields has no single stored field to hang
`groupable`/`label` on, and one stored field feeding two components needs two
metadata sets. This generalizes today's `BoxAtom{Name, Value, Field}` (the
`Field` back-reference) into a first-class codec.

### Renderer placement (dissolves #229)

A presentation field declares whether it *can* be a grouping heading
(`groupable`) and/or an inline atom (`inline`). The **renderer** decides, given
the current group-by:

- the field that **is** the current group-by → rendered as the **heading ladder
  only** (never also an atom);
- every other `inline` field → a box atom;
- coarse/fine relatives (`month` heading + `date_start` atom) are **distinct
  fields**, so they coexist — only the *same* field is suppressed.

Nothing is ever both a heading and an atom at once. That is the #229 dissolution.

### Multi-valued grouping + N-way merge

A multi-valued groupable field (tags) forces the general case. An object
**appears under every heading it matches** (several presentation points); the
grouping dimension is carried **purely by placement** (never a box atom); the
box shows only the *other* fields.

- **Membership = placement.** An object's grouping value(s) = the set of
  headings its line sits under. Single-valued → exactly one (a move is a
  *replace*); multi-valued → N (adding/removing a line *adds/removes* a member,
  never a whole-set replace).
- **N-way merge.** An object may appear at N presentation points; its final
  state is reconciled — membership from the placement **set-diff**, each atom
  from **all** its appearances (agree → apply, disagree → conflict) — then still
  3-way against the pinned base and the re-queried live. Conflict fires on live
  drift (as today) *or* on two appearances of the same object edited to
  different values of the same atom. Single-valued grouping is the **N=1
  degenerate**, so this subsumes today's `planMoves` + `planFieldEdits`.

## Worked example (`-group-by categories`)

Live: **T1** (categories `proj-a`,`proj-b`), **T2** (`proj-a`), **T3** (none).

```
# categories=
## =proj-a
  - [T1.ics  status=needs-action]  Ship release
  - [T2.ics  status=needs-action]  Write docs
## =proj-b
  - [T1.ics  status=needs-action]  Ship release
```

T1 appears under both categories; the grouping dimension is factored out at each
point (no `categories=` atom — placement carries it); T3 (no categories) lands
ungrouped. Editing: delete T1's line under `## =proj-a` → **remove** `proj-a`;
add T2's line under `## =proj-b` → **add** `proj-b`; edit a `status` atom at any
point → reconciled across T1's appearances. Result: T1 → `{proj-b}` (+ any atom
edit), T2 → `{proj-a, proj-b}`.

## Codecs in this model

- **Case-fold** (lowercase status/component) is a 1↔1 codec: present lowercase,
  write **canonical RFC 5545 uppercase** (never persist lowercase). Presentation
  normalization only; timezone is a future codec (same mechanism).
- **Date-split** (existing) becomes a declared codec rather than ad-hoc
  `present.go` logic; dates additionally become **groupable** fields (#230) —
  delivered as ONE prefix-granular dimension per date property
  (`dim:year|month|day`), not distinct coarse fields.
- **Tags** — `CATEGORIES` is a codec-carrying, multi-valued, groupable,
  read+write field, governed by a pluggable **tag-interpreter plugin**
  (cutting-garden#231; `naive` + `dodder-hyphen` builtin, config-linkable). The
  interpreter owns segment hierarchy, `namespace → bucket` expansion, bare-tag
  query matching, and the write-back (RFC 0019; `_` is literal — no lift, per
  the 2026-08-25 UAT resolution). The same interpreter surface applies across
  caldav categories, carddav groups, and fastmail labels. Its algebra is
  deferred to #231's own grill/FDR; this model only needs a tag field to *name*
  an interpreter.
- **Cancel-remap** is a codec/write-rule: a move to the `cancelled` status
  heading desugars to `STATUS:COMPLETED` + append the `zz-archive-task-cancelled`
  category — one placement, a **multi-stored write**. (Motivated by Tasks.org
  ignoring `STATUS:CANCELLED`; see the interop note in #229's neighbourhood.)

## Bare-tag → trellis native tag

A bare-identifier trellis term (`proj-cutting_garden`, `project`) matches an
object's tag set — **un-deferring bare-identifier terms in the evaluator**
(currently rejected in `internal/trellis_eval/validate.go`) and routing them,
via the field's tag-interpreter, to the node's designated tag-set field. General
evaluator feature: any plugin that declares a tag field participates.

## Relationship to existing records

- **RFC 0012 (facet contract)** and **FDR 0023 (organize)** are the two models
  this unifies; their `FacetDimension`/`FacetWriteApplier` and
  `ListingField`/`FieldWriteApplier`/`BoxAtom` become the *legacy* surface,
  re-expressed as unified fields + codecs and eventually deleted.
- **cutting-garden#229** (heading/atom redundancy) is **subsumed** — renderer
  placement dissolves it by construction.
- **#230** (dates as facets), **#231** (tag-interpreter plugin), **#232**
  (tags-as-editable-objects, dodder `:e`) are linked sub-designs.
- **RFC 0014 (trellis)** gained bare-tag evaluation (previously a deferred form;
  un-deferred in tags slice 3); the tag match semantics come from the
  interpreter, not the grammar.

## Staging / migration

This replaces four SDK surfaces (`FacetDimension`, `ListingField`, `BoxAtom`,
and the two write appliers) plus the merge, so it lands **incrementally**, never
big-bang:

1. **Introduce** the unified field descriptor + codec type in the SDK, with the
   N-way merge, *alongside* the legacy interfaces (the legacy ones become thin
   adapters onto the unified model).
2. **Migrate caldav** onto it and re-express its existing facets/fields with no
   behavior change (organize bats stay green) — the conformance bar for Slice 1.
3. **Deliver a net-new capability** (case-fold, or `-group-by date_start`, or
   `-group-by categories`) to prove the model earns its keep — DONE 2026-08-20
   (#230, prefix-granular date facets; see the dated status note).
4. **Migrate the remaining plugins** (nebulous, jira, fastmail, …) and **delete**
   the legacy interfaces.

## Slice 1 implementation status (2026-08-19)

Slice 1 is landing in phases (bright-olive branch). What has merged, and the
current legacy↔unified split:

**Merged:**

- **Phase 0** — `zz-tests_bats/organize_priority.bats` + `organize_fields.bats`:
  the behaviour-neutral conformance net (opt-in `/dav/fields/` caldav testserver
  calendar, `CG_TEST_CALDAV_FIELDS`). Closes #77.
- **Phase 1** — the SDK unified types, additive/unconsumed:
  `UnifiedField{…, Source}`, `FieldValue`, `FieldKind`, `Codec`
  (`Fields`/`Format`/`Parse`; `Parse` takes `current` so a partial edit preserves
  the untouched parts), `IdentityCodec`, `UnifiedDescriber`
  (`internal/cutting_garden_plugins/field_unified.go`, `codec.go`).
- **Option A** — caldav's PRESENT + FIELD-WRITE surface migrated: `PresentBoxAtoms`
  and `BuildFieldWritePatch` now DELEGATE to the generic SDK helpers
  `PresentUnifiedAtoms` / `ParseUnifiedFieldEdits` (`field_derive.go`) over
  plugin-local codecs (`caldavDateCodec`, `caldavPriorityCodec`, `IdentityCodec`
  — a component-agnostic union set in `plugins/caldav/unified.go`). The hand-rolled
  `caldavFieldProperty` / `dateTimeAtom` / `fieldInt` are removed.
  **`internal/organize` is untouched** — it consumes only the generic
  `FieldPresenter` / `FieldWriteApplier` interfaces; the codecs are plugin-local
  and the helpers are generic, so no caldav knowledge enters the framework. This
  is a HARD invariant: organize (and the framework generally) MUST stay
  plugin-agnostic — anything caldav-shaped leaking into it is a violation.

**Approach decision:** *plugin-local derivation*, not a generic framework adapter.
caldav keeps implementing the legacy interfaces but each delegates to the shared
SDK helper reading its codecs. The generic adapter, and the N-way merge that
consumes the unified model directly, are later slices. `CaseFold` and a generic
`SplitDateTime` codec are deferred until a second plugin consumes them (build only
what's consumed); caldav's date/priority codecs are plugin-local for now.

### Migration-state matrix

With Option B landed, caldav's whole DECLARATION and WRITE surface derives from
one unified declaration — present + field-edit (Option A) and the facet
dimensions + bucket moves (Option B); no field is described twice. Still legacy:
the hand-written `ListingFieldsDescriber` (stored-field declarations) and the
intentionally plugin-side counting.

**SDK surfaces — legacy ↔ unified:**

| Concern | Legacy surface | Unified equivalent | State after Option A |
|---|---|---|---|
| Inline field decl | `ListingField` / `ListingFieldsDescriber` | `UnifiedField` / `UnifiedDescriber` | legacy — still hand-written (declares *stored* fields) |
| Groupable field decl | `FacetDimension` / `FacetDescriber` | `UnifiedField{Groupable}` + `DeriveFacetDimensions` | **unified-derived** (Option B) |
| Kind + value types | `FacetKind`, `FacetValue` | `FieldKind`, `FieldValue` | both exist (`FieldKind` is the superset) |
| Atom presentation | `BoxAtom` / `FieldPresenter` | `Codec.Format` + `PresentUnifiedAtoms` | **unified-derived** (Option A) |
| Field-edit write | `FieldEdit` / `FieldWriteApplier` | `Codec.Parse` + `ParseUnifiedFieldEdits` | **unified-derived** (Option A) |
| Bucket-move write | `FacetWrite*` / `FacetWriteDescriber` / `FacetWriteApplier` | `DeriveFacetWrites` + `Codec.Parse` via `ParseUnifiedBucketMove` | **unified-derived** (Option B) |
| Counting | `FacetCounter` / `FacetHistogram` / `FacetSummary` | — (stays plugin-side) | legacy — intentional |
| Volatility token | `FacetVersioner` | `UnifiedField.RevalidateAfter` (declaration only; the version token stays `FacetVersioner`) | declaration **unified-derived** (Option B) |
| Opaque-key labels | `FacetLabeler` | `FieldKind=labelled` (not wired) | legacy |
| Filtering | `FacetPredicate` / `FacetFilter` | reuses facets | legacy |

**Field types × surface × model (caldav today)** — ✅ unified codec · ⛔ legacy ·
— n/a · ❌ unmodeled:

| Field kind | Example | Present (atom) | Groupable (heading) | Write |
|---|---|---|---|---|
| categorical | `status` | ✅ IdentityCodec | ✅ same field, `Groupable` | field ✅ · bucket ✅ |
| numeric-bucket (band) | `priority` | ✅ priorityCodec | ✅ same codec, band `Values` | field ✅ · band ✅ (`Parse` completes band→int) |
| date | `dtstart`/`due`/`dtend` | ✅ dateCodec (split) | ✅ same date codec, prefix-granular (`FacetDate`: `date_start`/`date_due`) | field ✅ (splice) · bucket ✅ (`Parse` shape-dispatches year/month/day splices) |
| date (volatile) | `due_band` | — | ✅ facetOnlyCodec + `RevalidateAfter` | — (read-only, declared write:none) |
| text | `location` | ✅ IdentityCodec | — | ✅ |
| text (trailer) | `summary` | ✅ IdentityCodec/Trailer | — | ✅ |
| duration | `duration` (P6D) | ✅ derived end — the dtend codec falls back to DTSTART+DURATION (#233) | — | — (end atoms read-only) |
| tag (multi) | `CATEGORIES` | — | ✅ categoriesCodec, naive (RFC 0019) | ✅ write:many, full-set replace (slice 2, N-way merge) |

### Option B — collapse the facet surface (landed 2026-08-19)

The Groupable/facet-write column is migrated onto the codec model: caldav
declares per-component codec sets ONCE (`unifiedFieldSets` / `DescribeUnified`,
in `plugins/caldav/unified.go`), and `DescribeFacets` / `DescribeFacetWrites` /
`BuildFacetWritePatch` DERIVE from them via the SDK helpers
(`DeriveFacetDimensions` / `DeriveFacetWrites` / `ParseUnifiedBucketMove`, in
`internal/cutting_garden_plugins/facet_derive.go`) — so `status` / `priority` /
dates are no longer double-described. `UnifiedField` grew the write/volatility
metadata the derivation needs (`WriteValues`, `CompletionHint`,
`RevalidateAfter`). The computed facets (`year`/`month`, `priority`-band,
volatile `due_band`) are codec-declared groupable fields whose bucket-move
writes run through their codec's `Parse` (period splice, band→PRIORITY
completion); their bucket VALUES stay computed by the plugin-side counting path
(`facetsFromView`) — **`FacetCounter` stays plugin-side** (counting is a
volatile-count concern, not presentation declaration). The atom/field-edit
union (`unifiedCodecs`) is now derived from the per-component sets, restricted
to inline/trailer codecs, so the two can never drift. #233 (present a
DURATION-event's end) landed as a dtend-codec fallback deriving the end from
DTSTART+DURATION. The `year`/`month` dimensions and `caldavRescheduleCodec`
described here have since retired/dissolved with #230 (below): the date codecs
themselves are the groupable, prefix-granular dimensions.

### Prefix-granular date facets — #230 delivered; promoted `testing` (2026-08-20)

The model's net-new capability landed end to end against the caldav testserver
(organize + `list --filter` + mcp `read_facets`/`list_nodes` filters + the
trellis `=` operator; the bats lanes pin it), meeting the `testing` promotion
gate. A new `FacetKind` **`date`** (`FacetDate`): bucket keys are ISO days
whose year/month buckets are string PREFIXES (`2026` ⊂ `2026-08` ⊂
`2026-08-15`), so the framework's whole share is prefix truncation — no
calendar knowledge — and the `FieldDate` derivation now maps onto it. The
caldav date codecs' `date_start`/`date_due` fields turned `Groupable` (one
dimension per date property, reading and writing ITS OWN property only);
`year`/`month` retired in the same slice, and `caldavRescheduleCodec`
dissolved — bucket moves shape-dispatch in the date codecs' own `Parse`
(`YYYY` → year splice, `YYYY-MM` → month, `YYYY-MM-DD` → day; clock + TZID
preserved). organize spells granularity as `--group-by dim=(granularity)` — a
trellis `(…)` meta qualifier on the field term, native tags design G10 (the
original `dim:granularity` suffix spelling of #230 was retired 2026-08-30 and
now rejects with a hint); a bare `dim=` on a date field resolves the
`[organize] date_granularity` config default, then day, and organize PERSISTS
the resolved spelling in the document's dimension heading (`# date_due=(month)`),
so apply coarsens live day-values identically without consulting config; filters prefix-match by validated value shape (`date_start=2026-08`);
summaries lift date dimensions at fixed month granularity while per-node
values stay day-precise. Known boundary: a `FacetDate` dimension declaring a
CLOSED value domain (`Values` non-nil) is currently out of contract —
`FacetFilter.Validate`'s closed-domain check does exact containment, so coarse
prefix values would be rejected; no in-tree dimension does this, and SDK-level
enforcement is tracked in #237.

### Tags slice 1 — read-only `categories` grouping (2026-08-20)

Slice 1 of the tags design (`docs/plans/2026-08-20-tags-design.md`) landed: the
last unmodeled matrix column now has a read-only, naive-interpreter (RFC 0019)
`categories` grouping dimension on caldav (`categoriesCodec`, multi-valued,
groupable-only — placement carries membership, so it is never a box atom, the
#229 rule), with slices 2 (N-way merge / tag write-back) and 3 (dodder-hyphen +
config-linkable interpreter override) pending.

## More information

- cutting-garden#229 (heading/atom redundancy — subsumed)
- cutting-garden#230 (dates as groupable facets — landed 2026-08-20)
- cutting-garden#231 (tag-interpreter plugin type; dodder-hyphen algebra)
- cutting-garden#232 (tags editable as objects, dodder `:e`)
- RFC 0019 (tag-interpreter contract — the `naive` / `dodder-hyphen` interpreter
  algebra; tags slice 1 uses `naive`)
- RFC 0012 (plugin facet contract), FDR 0023 (organize), RFC 0014 / FDR 0022
  (trellis), FDR 0024 (fastmail — the write:many tag-membership consumer)
