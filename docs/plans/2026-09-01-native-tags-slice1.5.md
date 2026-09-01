# Native tags Slice 1.5 — UAT feedback batch Implementation Plan

**Date:** 2026-09-01 · **Source:** the user's inline TODO comments in the
slice-1 vectors plus feedback.md; decisions taken 2026-09-01 (G10a amendment in
`2026-08-30-native-tags-design.md`). Runs BEFORE slice 2. Subagent-driven, one
committing task at a time, two-stage review per task, whole-document vectors
(G16). The user's TODO comment lines in the bats files are the work orders:
each task REMOVES the comment(s) it resolves.

Order: A → D → E → C → B (dialect first; presentation codecs next — E changes
query/bucket spellings so the assert-tightening B runs last, after outputs
settle; C's fixture churn lands before B for the same reason).

## Task A: namespace root heading (G10a)

`--group-by project` renders `# project` (the namespace root as a TAG heading),
continuations one deeper (`## -client`, `## -cutting_garden`); ungrouped
objects stay above the root heading. Direct placement under `# project` = the
object carries the BARE tag `project` (membership write reconstructs exactly
`project`); continuation placement unchanged (namespace-tag reconstruction).
Resets compose: `##` under a continuation pops to the root (= bare-tag
membership on apply), empty `#` pops to ungrouped. `(tags)` unchanged.
Re-point organize_ns.bats + organize_groupby.bats (ns row) + the nvim corpus ns
test; add a vector for direct-under-root placement (move nsD's line under
`# project` → the reconstructed tag is exactly `project`; out-of-namespace tags
survive, so nsD ends `other,project`); resolve the TODO in
organize_ns.bats; RFC 0015 hoisted-dialect section + example; design G10a
already records the decision.

## Task D: priority atoms present the band

`caldavPriorityCodec.Format` presents the BAND (`0_must`, `1_should`, `2_nice`,
`3_unspecified`) as the box atom value instead of the raw int — the same
derived value the buckets use; `Parse` already completes band→canonical
RFC 5545 int (band edits in the box now work like bucket moves; an explicit
int in an edit stays accepted if it already was — check and pin either way).
Re-point priority/fields/groupby vectors; resolve the TODO in
organize_groupby.bats; FDR 0025 matrix row note.

## Task E: case-fold status (FDR 0025's named codec)

Present STATUS lowercase EVERYWHERE (atoms, bucket headings `## =needs-action`,
pre-rendered WriteValues buckets, facet values, list/query surfaces); stored
stays canonical UPPERCASE (`Parse` folds up on every write path: field edit,
bucket move). Query/filter values fold before matching so `status=completed`
and `status=COMPLETED` both match (validate.go / facet predicate: fold at the
presented layer — pin which layer owns the fold; the codec presents, so
matching against PRESENTED values is the rule; document it in FDR 0025).
TerminalValues/WriteValues comparisons fold. This is a caldav-local codec
change through the unified model (`status(…)` IdentityCodec → a small
caseFoldCodec in plugins/caldav/unified.go; the SDK gains nothing until a
second plugin needs it — build only what's consumed). Re-point every vector
carrying a status value; resolve the TODO in organize.bats; FDR 0025 dated
note (case-fold delivered).

## Task C: missing-STATUS object coverage

A VTODO with no STATUS property lands in the ungrouped set of a
`--group-by status=` document (above the first heading), with no status atom in
its box. Fixture: add `field5` ("Waiting idea", no STATUS, no PRIORITY 0 —
give it PRIORITY 0/none and no CATEGORIES) to `/dav/fields/` — accept the
one-time churn of the fields/priority/tags/groupby vectors (B re-tightens right
after). New vector in organize_fields.bats: generate shows field5 ungrouped;
moving it under `## =needs-action` writes STATUS (the write:one "absence is a
no-op / move writes" rule); resolve the TODO in organize_fields.bats.

## Task B: whole-output asserts on the residual partials

Convert every remaining `--partial` assert on cg OUTPUT in the organize lanes
to whole-output asserts: `list -query` write checks become full URI/NAME/TYPE
tables (`assert_output - <<-EOM`), curl checks assert the full .ics body where
deterministic (UID/SUMMARY/STATUS/CATEGORIES lines — check what the testserver
serializes; if a volatile line exists, assert the full body minus it and say
so). Error-message tests keep `--partial` ONLY where the message embeds a
tmpdir path; assert the full line otherwise. Resolve the two TODOs
(organize.bats, organize_date.bats). Test-only; no product change.

## Not in this batch

- feedback.md items left untracked BY CHOICE (not selected for filing):
  `caldav:tasks` alias resolution error; apply-prompt "edit again" option.
- Already tracked: #247/#248 (diff rendering), #253 (nvim elide), slice-4 mesa
  (`--facets` readability), #215 (last-line deletion UX).
- Slice 2 (key-free tag atoms) starts after this batch merges.
