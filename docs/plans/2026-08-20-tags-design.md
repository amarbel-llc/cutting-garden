# Tags: interpreter contract, tag surface, and the N-way merge

**Date:** 2026-08-20 · **Issues:** cutting-garden#231 (tag-interpreter plugin
type), #232 (tag-object editing — future), plus the FDR 0025 tag/CATEGORIES
and N-way-merge model sections this delivers · **Parent:** FDR 0025,
FDR 0023, RFC 0012, RFC 0014, RFC 0015

Approved in the 2026-08-20 design session (cutting-garden/green-chestnut) —
this session is the grill #231 asked for. Decisions below were made
explicitly by the user; two were flagged PROVISIONAL at the user's request and
RESOLVED by 2026-08-25 UAT (D4 → decision A; D5 → no `_` lift).

## Problem

Tags (caldav CATEGORIES today; carddav groups and fastmail labels as future
destinations) are the last unmodeled column of FDR 0025's field matrix:
multi-valued, groupable, writable, and — critically — their match/group
semantics vary per user and destination, so the semantics must be pluggable
(#231). Tags also force organize's general case: multi-membership rendering
and the N-way merge the FDR specifies.

## Decisions

### D1 — Slicing: read-only → writes → algebra

- **Slice 1:** caldav CATEGORIES as a read-only naive tag surface (field,
  facets, multi-membership rendering, exact filtering); moves/edits on it
  loudly rejected.
- **Slice 2:** the N-way merge write path (membership by placement).
- **Slice 3:** the interpreter registry wired end to end — dodder-hyphen,
  tag-term group-by, namespace rollup, bare-tag trellis terms, write-back
  completion.

Each slice merges independently and is manually testable against real
tag-heavy data. The #231 RFC (normative contract) is authored alongside
slice 1, since the contract shapes the field declaration.

### D2 — The interpreter is a wire-shaped contract, bound in-process now

A new SDK plugin type, `TagInterpreter`: pure value types in/out,
batch-oriented — the exact method set an RFC 0013-style transport can carry
later. Two builtin implementations in a name registry (`naive`,
`dodder-hyphen`); the RFC specs the registry, the contract, and a future
`[[tag_interpreters]]` wire stanza as **defined-but-unimplemented** (added
when an external interpreter — e.g. dodder shipping its canonical algebra —
wants in; adding the transport then changes no consumer).

Interpretation always runs **host-side**: the framework (organize grouping,
trellis matching, write-back computation) applies the interpreter to tag
VALUES that data plugins emit — so linked plugins, fastmail, and wire
traversal plugins (fj-cg) are all covered without the interpreter itself
ever crossing a wire. The wire option exists solely for external SEMANTICS
providers. What builtins-only would have cost (and why the contract is
wire-shaped anyway): out-of-tree implementations, and the discipline that
keeps the interface serializable (no callbacks/iterators/rich types).

Contract method set (names indicative; the RFC finalizes):

- `Normalize(tag) → tag` — identity for both builtins (no `_` lift, D5).
- `SortKey(tag) → key` — plain lexical order (D5; `_`/`_ ` sorts high naturally).
- `Buckets(tags []string, namespace string) → []Membership` — namespace
  expansion + rollup (D4); empty namespace = whole-dimension grouping (each
  normalized tag its own bucket). `Membership{Bucket string, Via string}` —
  Via is the full tag that produced the membership (the write path and
  conflict messages need it).
- `Matches(tags []string, term string) → bool` — bare-tag query semantics
  (naive: exact; dodder-hyphen: `project` matches `project-*` transitively).
- `Complete(tags []string, op Add|Remove, bucket string) → []string` — the
  write-back: the new full tag set for a membership edit.

All methods take the full tag set (batch by node); a future wire binding
batches by node-set. No method takes callbacks or context-rich types.

### D3 — Group-by grammar: doddish-convergent term resolution

`--group-by` takes a TERM, converging on doddish/trellis (RFC 0014) and the
document dialect (whose dimension headings already spell `status=`):

- `<name>=` — explicitly a FIELD dimension (forces field-ness when a tag
  shadows a dimension name).
- Bare `<name>` — **type-resolved** (RFC 0014's own rule: semantics from the
  type system, never token shape): a declared facet dimension → field
  grouping (today's spellings, including `date_due:month`, keep working);
  otherwise a TAG TERM evaluated by the type's designated tag field's
  interpreter (`--group-by project`).

The organize document persists the resolved term in its dimension heading,
exactly as #230's granularity round-trip does, so apply re-derives the
semantics from the document, never from config.

### D4 — dodder-hyphen algebra: segment hierarchy, immediate-segment rollup

Hyphen segments form the hierarchy. Grouping by namespace `project` over
tags `project-cutting_garden`, `project-client-acme`, `project-client-baxter`
buckets by the IMMEDIATE next segment — deeper tags roll up:
`-cutting_garden`, `-client` (acme + baxter together). Drill-down is
grouping by the deeper namespace (`--group-by project-client`). The prefix
hierarchy deliberately mirrors #230's date granularity (day→month→year).

**RESOLVED (2026-08-25 UAT — decision A):** bucket headings render as
CONTINUATIONS of the namespace — `## -client`, common prefix elided, **no
`=`** — tags are continuations, not values. The same no-`=` rule applies to
whole-dimension buckets: a flat tag renders bare (`## work`), quoted when it
carries a space (`## "_ inbox"`). This rhymes with doddish dependent-tag
syntax (leading-hyphen names, RFC 0014's rejected-spellings note); the `=`
spelling read awkwardly on real tags in UAT (`## =_ inbox`), which the
decision resolves. Presentation-layer (RFC 0015), not the interpreter contract.

### D5 — No `_` lift; `_` is literal (RESOLVED — 2026-08-25 UAT)

**Decided: no `_`-lift magic.** A leading `_`, a leading `_ ` (underscore +
space — the user's real pin-to-top convention, `_ inbox`), in-word `_`, and
interior-segment `_` are ALL literal characters. Nothing lifts, rewrites, or
aliases them: `_inbox` and `inbox` are distinct tags, `_ inbox` is its own
tag. Pin-to-top falls out of plain lexical sort (a `_`/`_ ` prefix sorts high
in ASCII order), so no special-case key is needed — and dropping the mechanism
sidesteps the hyphence/trellis `_`-reserved-field collision entirely. An
explicit lift/alias can be reconsidered later if a real need appears; out of
contract now.

### D6 — Interpreter selection: field default + config override

The plugin's UnifiedField names its default interpreter (caldav categories →
`naive`); config overrides: `[tags] interpreter = "dodder-hyphen"` globally,
plus an optional per-account key in the account stanza. Unknown names reject
at config load (bad request). A future `[[tag_interpreters]]` stanza
registers wire-backed names into the same namespace.

### D7 — Slice 1: caldav CATEGORIES, read-only naive

- CATEGORIES parsed into `Node.Fields` and per-node facets (one FacetValue
  per normalized tag).
- Unified field `categories`: `FieldTag`, `MultiValued`, **Groupable and NOT
  Inline** — membership is carried purely by placement, never a box atom
  (the FDR's #229 rule; the box shows only the OTHER fields).
- `groupNodes` already renders one line per matching value — multi-membership
  rendering falls out.
- Filtering: exact tag equality (naive) via the existing facet-filter path.
- Writes: a move/edit naming `categories` rejects loudly (Writable false in
  slice 1).
- Summary lift: raw normalized tags — **tuning lever** (see below).

### D8 — Slice 2: the N-way merge SUBSUMES planMoves/planFieldEdits

The FDR's reconciliation, implemented as THE merge (single-valued is the
N=1 degenerate through the same code — user chose immediate subsumption
over a parallel path):

- **Membership = placement set-diff.** Adding an object's line under a
  bucket adds the membership; **deleting a line under a bucket is the
  first-class remove-membership edit** — no gate — PROVIDED the object still
  appears elsewhere in the document (unambiguous reorganization). An
  object's LAST line vanishing remains #215's territory (the
  `%:allow-deletion` gate, unbuilt) and is rejected as today.
- **Atoms reconcile across all appearances**: agree → apply once; disagree →
  conflict naming the appearances.
- Then the existing 3-way against pinned base + re-queried live, unchanged
  in spirit.
- The write completion for a tag membership edit goes through the
  interpreter's `Complete` (slice 2 ships it for naive; dodder-hyphen
  arrives with slice 3).

**Rollback:** no dual period (explicit user decision, matching #230): the
slice lands as one merge; rollback is `git revert` of that merge. The
subsumption means a revert restores the exact prior planMoves/planFieldEdits
code — reason it stays safe: the slice must keep every existing
single-valued organize bats green, so the N=1 degenerate is
conformance-pinned before tags ever use the general case.

### D9 — Slice 3: dodder-hyphen end to end

The registry + dodder-hyphen builtin; tag-term `--group-by` (D3) with
namespace rollup + continuation headings (D4); bare-tag trellis terms
un-deferred in the evaluator (routing through `Matches`, per FDR 0025
§Bare-tag); membership write-back through `Complete` at namespace buckets
(moving a line under `-client` appends... the RFC must pin WHAT tag a
rollup-bucket move appends — the bucket's namespace tag (`project-client`)
is the only unambiguous choice; drilling deeper is a deeper group-by).

### D10 — #232 stays future

Tag-object editing (rename/edit tag definitions, dodder `:e`-style, fan-out
across objects) depends on everything above plus a rename-fan-out semantics
for string-tag substrates; deliberately not designed here.

## Testing

- SDK/RFC: contract unit tests per builtin (normalize/sort/buckets/matches/
  complete tables; the dodder-hyphen rollup and `_`-is-literal cases pinned).
- caldav: CATEGORIES parse + facet emission; declaration derivation.
- organize: multi-membership render; N-way reconciliation units (add,
  remove, last-line rejection, cross-appearance atom conflict, N=1
  degeneracy against the existing single-valued tests); bats lanes per
  slice against the testserver (fixtures gain CATEGORIES), including the
  full single-valued regression suite for the D8 subsumption.
- trellis: bare-tag term evaluation (slice 3).

## Tuning levers

- **Continuation-heading rendering** (`## -client`, no `=`): RESOLVED
  2026-08-25 UAT → decision A (no `=`, continuation; flat tags bare,
  space-bearing tags quoted). See D4.
- **`_` lift**: RESOLVED 2026-08-25 UAT → dropped. `_` is literal for all
  interpreters; pin-to-top falls out of plain lexical sort. See D5.
- **Tag summary-lift policy**: raw tags in slice 1; signal = summary width
  in practice (fastmail's ~529 labels will force a namespace-bucketed or
  suppressed lift before fastmail's tag field lands).
- **Contract batch shapes**: per-node tag sets now; signal = a wire
  implementation's measured latency (unmeasurable until one exists).

## UAT feedback (2026-08-25, slice-1 categories)

Manual UAT of the slice-1 read-only `categories` surface against live caldav
(`caldav:task`) surfaced three slice-3 rendering inputs. Recorded as
verdicts-so-far — they refine D3/D4/D5/D9; none is a slice-1 defect (slice 1
renders `categories` through the generic FacetValue dimension path, which the
tag-kind rendering of D9 supersedes).

- **No dimension heading for a tag-kind group-by (refines D3/D9).** Grouping
  by `categories` today emits the generic `# categories=` dimension heading
  above the buckets. Verdict: a tag-kind dimension is *hoisted* — the tag
  buckets are the headings, with no `<dim>=` parent ("categories are treated
  as tags for free"). The generic dimension-heading spelling stays for value
  dimensions (status, date); tag dimensions skip it.

- **Tag headings MUST quote values with spaces / reserved runes (refines
  D4/D9).** A real category `_ inbox` (embedded space) rendered as a bare
  heading (`## =_ inbox`) — ambiguous and non-round-tripping. Tag values
  containing a space (or any heading-reserved rune) must be quoted. This is a
  rendering-correctness requirement, not a preference, and applies wherever a
  tag renders as a heading.

- **Live tags use a leading `_ ` (underscore-space) pin-to-top convention —
  stresses D5.** The user's tags include `_ inbox`, where the leading `_ `
  is a manual "sort to top" marker. D5 as written lifts `_inbox` (no space) →
  `inbox` and says nothing about the underscore-SPACE form actually in use.
  Open question the RFC must settle before slice 3: either the `_`-lift
  covers the `_ ` spelling too (lift + identity-transparent to `inbox`), or
  it explicitly excludes it and the `_ ` stays literal content. This is the
  concrete collision D5 anticipated — it raises the bar on settling D5.
  **Resolved 2026-08-25: no lift** — `_` and `_ ` stay literal everywhere;
  pin-to-top rides plain lexical sort. See D5.

## Out of scope

Fastmail/carddav tag fields (future destinations — the contract serves
them; their fields land with their plugins' migrations), #232, the wire
transport implementation, dodder importing (revisit if the dodder-hyphen
reimplementation drifts from dodder's behavior).
