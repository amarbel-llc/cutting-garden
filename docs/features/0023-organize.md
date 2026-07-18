---
status: exploring
date: 2026-07-18
promotion-criteria: |
  Promote to `proposed` when: hyphence RFC 0002 (hyphence#2) is merged;
  ContainerCreator (#143) has landed; the write-descriptor extension to
  RFC 0012's facet schema is specified in Go; and a prototype apply
  engine has run the caldav scenario (reschedule-by-move) end-to-end
  against the testserver.
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

## Shape

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

- **hyphence#2 / hyphence RFC 0002** — content grammar (drafted in
  session hyphence/kind-fig; review pending). Load-bearing for `_base`,
  settings fields, and the metadata distribution rule.
- **cutting-garden#143 ContainerCreator** — creation with
  substrate-allocated identity (in flight, sharp-hazel).
- **RFC 0012 write-descriptor extension** — this FDR's own first
  implementation step.
- **dodder alignment** (tracked in the dodder issue filed alongside
  this FDR): drop comma headings, adopt `_base`, migrate
  `% dry-run:true` → `_dry-run=true`, reconcile organize-text(7)'s
  removal-semantics documentation.
- Adjacent: cutting-garden#142 (root tags) — filter-mode
  `cg capture --organize` composes with tagged roots.

## Boundaries

Substrate-native metadata only in v1 — organize can not tag what the
substrate can not hold; dodder-as-overlay is the named deferred
direction, not an open question. No output formatting, no aggregation,
no graph algorithms (FDR 0022's taxonomy applies). Deletion is
triple-gated (settings field, confirmation, commit-directly flag);
filter mode is selection-only.

## Deferral tiers

- **Near**: conflict mergetool (dodder merge-tool precedent).
- **Deferred**: node-id aliasing in documents; combined filter+edit;
  empty-bucket ergonomics; dodder-as-overlay.
- **Far-future/never**: cross-dimension nesting (all-write:one matrix
  noted for completeness; many-interior likely never).

## More information

RFC 0015 (normative dialect); RFC 0014 + FDR 0022 (trellis/espalier);
RFC 0012 + FDR 0021 (facets); FDR 0020 (writes); RFC 0007 + #142
(roots); dodder orgie source findings (bobo research, 2026-07-18:
constructor/reader/refiner/assignment + Subset clone mechanism).
