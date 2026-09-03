---
status: proposed
date: 2026-07-18
promotion-criteria: |
  MET 2026-08-05: the apply engine ran the caldav reschedule-by-move scenario
  end-to-end against the testserver (FacetWriteApplier + caldav month/year date
  splice; zz-tests_bats/organize_month.bats — since rewritten as
  organize_date.bats with prefix-granularity lanes, #230). The other three original criteria
  were already MET: hyphence RFC 0002 (hyphence#2) merged, ContainerCreator
  (#143) landed, and the write-descriptor extension to RFC 0012's facet schema
  specified in Go (RFC 0012 §14) and implemented (FacetWriteDescriber + caldav
  reference, 2026-08). Promoted exploring→proposed.
---

# organize — cross-substrate facet editing

The feature side of RFC 0015 (the organize document dialect): dodder's
organize, upstreamed into cutting-garden and generalized across plugins.
Query → group → edit → write-through, each stage owned by an existing
subsystem: trellis selects (RFC 0014), the facet machinery groups
(RFC 0012 + write descriptors), espalier renders, the apply engine
interprets the delta against a pinned base, and NodeMutator /
ContainerCreator perform the writes.

Designed 2026-07-18 in a grill + five-scenario walkthrough (caldav,
forgejo/jira, newsblur, fs, dodder-as-regression-check); every decision
individually confirmed. RFC 0015 carries the normative rules; this
record carries the shape, the per-substrate findings, and the ledgers.

**2026-08-05 — interactive round-trip (implemented).** A bare
`organize <uri> -group-by <facet>` on a TTY now generates the document
into a temp file, opens `$EDITOR` (→ `$VISUAL` → `vi`), and applies on
save — dodder's interactive default. With stdout piped/redirected it
prints the document (the MCP/scripting path); `-apply <path>` and
`-commit-directly < doc` are the scripted apply forms. The dry-run vs
commit gate is the **`-commit` CLI flag, defaulting to dry-run**: the
`%:dry-run` directive-in-doc gate the grill designed is deferred until
`%:` is promoted to a first-class hyphence envelope construct
(hyphence#14). After a dry-run the edited buffer's path is printed for
re-apply; an interactive commit whose change set exceeds 30 objects gets
a single `huh` confirmation. Line-deletion stays out of scope (cg#215).

**2026-08-05 — reschedule-by-move (implemented, Slice 2b).** The apply
engine builds each move's substrate patch through a new plugin-owned
**`FacetWriteApplier`** capability (`BuildFacetWritePatch`) rather than a
framework-side patch builder — so the framework carries no substrate JSON
shape or domain-transition logic (RFC 0009 no-inversion). caldav's applier
handles the status passthrough AND the `month`/`year` reschedule: it splices
the target period into the object's existing DTSTART (events) or DUE (tasks),
preserving the day-of-month, clock time, and TZID (PatchNode's GET +
re-serialize keeps the zone; only the date value changes), clamping an
out-of-range day to the target month's last. Writability now requires the
applier — a plugin declaring writable facets without one is rejected loudly,
which also closed a latent corruption: month/year were already declared
`FacetWriteOne`, and the old verbatim passthrough would have written a bare
`"2026-09"` into the date.

**2026-08-09 — detail fields as box atoms, read-side (implemented,
cutting-garden#47).** An object's detail fields now render as ground
`name=value` espalier atoms inside the box interior —
`- [dentist.ics date_start=2026-08-15 time_start=09-30 location=HQ] Dentist` —
instead of the clock time being smuggled into the description trailer. A new
plugin-owned **`FieldPresenter`** capability (`PresentBoxAtoms`, render
direction) owns the transform: the framework never parses substrate values, so
caldav splits DTSTART/DTEND/DUE into `date_*`/`time_*` atoms (date `YYYY-MM-DD`,
time `HH-mm` — the hyphen form is grammar-proven a single espalier value token
AND composes for a future hour/minute heading-join; `:` was rejected for the
latter), passes `location` through, and emits only the date atom for an all-day
value. STATUS stays the grouping heading and SUMMARY the trailer, so neither is
an atom. The atoms round-trip through parse. This slice is **read-side only**: a
*changed* atom is surfaced as a non-blocking apply notice, not written back.
The write-side — `ListingField.Writable` as the sole writability source with
`FacetWrite.Field` validated against it (resolving the
`FacetWriteDescriber`/`ListingField` duplication), the presenter's inverse
(atoms → substrate value, TZID-preserving recombine), the field-edit apply path,
move-vs-edit conflict, fail-hard-on-immutable, and the deferred `%`-prefixed
read-only atom grammar — is tracked as **cutting-garden#218**.

**2026-09-03 — key-free tag atoms (implemented, native tags slice 2).** The
box interior described above now ALSO carries the object's tag set — the
type's designated `FieldTag` field — as key-free bare/quoted atoms after the
id/`!type` (`- [nsA.ics work date_due=2026-09-01] Acme retainer`),
SortKey-ordered, governed by the `_tag-atoms` / `_tag-strip` envelope levers,
and a box tag edit applies as a MEMBERSHIP write through the tag
interpreter's exact `Complete` (RFC 0015 §Tag atoms is normative; FDR 0025
carries the delivery notes). The 2026-08-09 note's framing has been
overtaken twice since: field writes landed with FDR 0025's unified-codec
migration (the #218 write-side), and status/priority DO render as atoms
except under their own grouping heading (#229's renderer-placement rule).

- `cg organize <uri> [--query <trellis>] [--group-by <facet-key>]
  [--mode …] [--allow-deletion]` — anchor and query per trellis's
  host-supplied-anchor rule; one grouped dimension (self-drilling).
- **Mapping capability**: the plugin-declared extension of the facet
  schema — `write: none|one|many` per dimension, bucket→field mapping,
  completion rules (e.g. date-bucket moves preserve clock time —
  timezone handling lives here, never in the framework),
  identity-affecting flags, creation requirements. Probed by type
  assertion like every capability (RFC 0012 posture); unmapped
  metadata rejects loudly.
- **Base blobs**: generated ground espalier stored content-addressed
  (madder), typed `organize-base-v1` (self-describing hyphence
  envelope), pinned via `- _base=@digest`. Mandatory — no legacy mode.
- **Apply engine**: three-way structural merge per RFC 0015; writes via
  PatchNode (fields), ContainerCreator (creation, substrate-allocated
  identity), with resulting-id reporting for identity-affecting writes.
  cg has NO concept of domain transitions: everything is a field patch;
  the plugin owns whatever its API requires to make the term true.

## Per-substrate findings (the walkthrough ledger)

| Substrate | Grouped dimension(s) | The finding it contributed |
|---|---|---|
| caldav | `date`/`month` (write:one) | reschedule-by-move; completion rules; dependent-dimension sugar; creation works against today's writable VEVENT body |
| forgejo/jira | `labels` (many), `state` (one, closed) | clone diffing / per-value membership; closed-set validation; adoption as bulk-close; no-domains rule; gh/fj indistinguishable (mapping is per-node-type, not per-service) |
| newsblur | `read` (one, closed bool), `user_tag` (many), `story_tag`/`feed`/`year` (none) | minimal-write toggle; writability-must-be-declared proof; move-vs-other-edit independence; empty-projection ⇒ view |
| fs | `dir` (one, identity-affecting) | containment as projected facet; path laddering; identity-affecting writes; blob-carrying creation; "vidir with a pinned base" |
| dodder | tag prefixes (many) | full collapse to orgie behavior; two divergences ruled: comma headings dropped (dodder migrates), `_base` mandatory (no back-compat — organize is ephemeral action) |

## Dependencies

- **hyphence#2 / hyphence RFC 0002** — content grammar. **MERGED** (closed
  2026-08). Load-bearing for the two metadata line-planes (`- _base` data-plane
  fields, `%:` operational-plane directives; RFC 0015 §Document structure) and
  the distribution rule.
- **cutting-garden#143 ContainerCreator** — creation with substrate-allocated
  identity. **LANDED** (closed).
- **RFC 0012 write-descriptor extension** — **LANDED** as RFC 0012 §14 (Write
  mapping): the `FacetWriteDescriber` capability + `ValidateFacetWrites`
  (`internal/cutting_garden_plugins/facet_write.go`), caldav's reference
  declaration (`plugins/caldav/facet_write.go`), and its `describe_node_types`
  surfacing. This FDR's stated first implementation step — the declarative
  foundation the apply prototype (below) now builds on.
- **dodder alignment** (tracked in the dodder issue filed alongside
  this FDR): drop comma headings, adopt `- _base`, re-spell
  `% dry-run:true` / `_dry-run` / `_allow-deletion` as `%:` operational-plane
  directives (`%:dry-run`, `%:allow-deletion`; RFC 0015 two-plane revision
  2026-07-28), reconcile organize-text(7)'s removal-semantics documentation.
- **Field residence** (dodder design issue, ruled 2026-07-18): every
  field key has one type-declared authored home — body-resident
  (extracted from the typed body) or metadata-resident (hyphence field
  lines; the default for undeclared and `_`-framework keys) — with the
  index unifying both for residence-blind querying and loud
  bidirectional collision errors. The mapping capability's write-through
  targets the residence (body-resident ⇒ the apply engine edits the
  typed body through the type). Node types want the same residence bit
  (FDR 0018 unified namespace).
- Adjacent: cutting-garden#142 (root tags) — filter-mode
  `cg capture --organize` composes with tagged roots.
- Adjacent: cutting-garden#154 — bulk / multi-node mutation (NodeMutator
  successor), specified as RFC 0017. organize's apply engine is the
  motivating consumer; v1 composes single-node writes, but RFC 0017 makes
  **atomic bulk the EXPECTED posture** for transactional substrates (a
  BulkMutator SHOULD advertise `bulk-atomic` and honor atomic mode for
  every request shape its backend can transact) — best-effort is reserved
  for genuinely non-transactional backends (NewsBlur's REST mark-read). So
  organize's atomic-commit direction has a capability contract that
  *expects* atomic from transactional substrates, not merely permits it.
  Layered on FDR 0020 and bright-cherry's PutNode/PatchNode split. Future
  (RFC 0017): a lazy multi-stream of `io.Reader`s orchestrated into one
  atomic operation, so large deltas apply atomically without buffering
  whole (v1's `[]byte` is a materialization, not permanent).

## Boundaries

Substrate-native metadata only in v1 — organize can not tag what the
substrate can not hold; dodder-as-overlay is the named deferred
direction, not an open question. No output formatting, no aggregation,
no graph algorithms (FDR 0022's taxonomy applies). Deletion is
triple-gated (`%:allow-deletion` directive, confirmation, commit-directly flag);
filter mode is selection-only.

## Deferral tiers

- **Near**: the mergetool (dodder merge-tool precedent) — scope per
  RFC 0015's 2026-07-18 revision: base/live conflicts AND unresolved
  intents (deletion underdetermination in ungrouped documents),
  batch-capable; v1 reports both as structured rejections (#147 shape).
- **Deferred**: node-id aliasing in documents; combined filter+edit;
  empty-bucket ergonomics; dodder-as-overlay.
- **Far-future/never**: cross-dimension nesting (all-write:one matrix
  noted for completeness; many-interior likely never).

## More information

RFC 0015 (normative dialect); RFC 0014 + FDR 0022 (trellis/espalier);
RFC 0012 + FDR 0021 (facets); FDR 0020 (writes); RFC 0007 + #142
(roots); dodder orgie source findings (bobo research, 2026-07-18:
constructor/reader/refiner/assignment + Subset clone mechanism).
