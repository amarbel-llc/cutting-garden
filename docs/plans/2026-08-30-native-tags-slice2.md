# Native tags Slice 2 — key-free tag atoms Implementation Plan

**Design:** `2026-08-30-native-tags-design.md` (G1, G2, G3/G14, G6, G7, G16) ·
**Prereq:** Slice 1 merged (one grammar: `trellis.ParseLiteralPrefix` /
`WriteLiteral` own box interiors; `objectLine.Tags` already round-trips a
hand-written bare token, and apply REJECTS it with "tag atoms are not writable
yet (native tags slice 2)" — this slice removes that gate).

Execute subagent-driven (fresh implementer per task, spec review then quality
review, ONE committing task at a time — the worktree index is shared). Every
task's vectors are whole-document heredocs (G16); `_base` digests verbatim.

## Task 1: the tag set is a codec-produced presentation field (G6)

- `categoriesCodec.Format` (plugins/caldav/unified.go) currently returns an
  empty map — the per-tag values come only from the counting path. Make
  `Format` produce the tag set under the `categories` key (one string per tag,
  interpreter-normalized order is the FRAMEWORK's job, not the codec's — the
  codec emits stored order). The counting path (`facetsFromView`) keeps
  computing facet VALUES; the two must agree (pin with a unit test that the
  facet keys equal the Format output as sets).
- SDK: `PresentUnifiedAtoms` (internal/cutting_garden_plugins/field_derive.go)
  gains a sibling `PresentUnifiedTags(codecs, node) []string` that returns the
  values of the type's designated `FieldTag` field (`Kind == FieldTag`; exactly
  one per type — a second FieldTag field is a declaration error surfaced by
  `DescribeUnified` validation; RFC 0009 note). `BoxAtom` stays field-only.
- `FieldPresenter` gains no new method: organize resolves tags through the
  unified describer + codecs directly (`describedTagDims` already exists in
  generate.go). Wire plugins (RFC 0013) carry tags as a listing field named by
  `tag_set` — out of scope here; note it.
- Vectors: Go unit (Format ↔ facets agreement; PresentUnifiedTags picks the
  designated field). No bats change yet (nothing renders tags until Task 2).

## Task 2: render tag atoms + the `_tag-atoms` / `_tag-strip` levers (G1, G2, G3)

- `objectLine` renders `Tags` through `WriteLiteral` (already does); generate
  fills `Tags` from `PresentUnifiedTags`, sorted by the resolved interpreter's
  `SortKey`, quoting via `QuoteIfNeeded`. Position per `_tag-atoms`:
  `leading` (default: after id/`!type`, before `k=v`) | `trailing` (after
  `k=v`) | `none`.
- `_tag-strip` (default `placement`): when the document is tag-grouped (whole
  or namespace), strip from each appearance's box exactly the `Via` tag(s) that
  produced that placement (`interp.Buckets` already returns `Membership{Bucket,
  Via}`); `none` keeps all tags in every box. Field-grouped documents strip
  nothing (no tag placement exists).
- Envelope: `- _tag-atoms = …` / `- _tag-strip = …` are data-plane fields
  (G3/G14), OMITTED at default; `document` parses/renders them; `_base`
  includes them when present. `[organize] tag_atoms` / `tag_strip` config
  defaults (tommy-generated — `just codemod-generate`), validated at load;
  the document's explicit field wins over config.
- Vectors (bats, whole-document): G1 leading (status-grouped /dav/fields/ doc
  shows `- [field2.ics errand work] Read book`… note SortKey order); G1
  trailing (`_tag-atoms = trailing` via config → envelope field present); G1
  none; G2 strip-Via (tag-grouped /dav/ns/ doc: `# -client` / `- [nsA.ics] …`
  with a sibling tag kept — add a fixture VTODO carrying TWO tags, one inside
  and one outside the namespace, to /dav/ns/ (nsE `project-client-acme,urgent`
  → `- [nsE.ics urgent] …` under `-client`); G2 `_tag-strip = none` (the Via
  tag stays); G3 defaults omitted + config default + doc-wins. Every EXISTING
  vector changes (boxes gain tags) — re-point them; that churn IS the review
  surface. Update RFC 0015 (object-line dialect, the two fields), the nvim
  corpus (boxes with tags in every dialect), `describe_node_types` unaffected.

## Task 3: box tag edits are membership edits (G7)

- Remove `rejectTagAtoms`. Apply computes each appearance's tag set =
  placement-derived tag(s) (per `_tag-strip`: `placement` → the bucket's Via
  reconstruction, as slice 3 B4 does; `none` → nothing derived, the box is
  authoritative) ∪ box atoms; reconciles N-way across appearances (agree →
  apply; disagree on a non-placement tag → conflict naming the appearances);
  then the existing `planMemberships` → `Complete` (exact) → full-set write.
  A typed atom is exact under every interpreter. `%`-prefixed atoms (RFC 0015
  display-only) are parsed and rejected on edit with a clear message (no
  plugin emits them yet; keep the parse path).
- Base comparison: the pinned base carries the rendered tags, so the 3-way
  diff sees atom-level tag edits exactly like field atoms. A tag REMOVED from
  the box under `_tag-strip = placement` while the line still sits under that
  tag's bucket is a conflict ("placement says X, box says not-X") — pin it.
- Vectors: G7 add (type `urgent` into nsA's box under a status-grouped doc →
  CATEGORIES gains urgent, curl-verified, after-render shows it); G7 remove;
  G7 cross-appearance disagreement → conflict (tag-grouped doc, object under
  two buckets, edit a non-placement tag in one box only); G7 placement-vs-box
  conflict; membership diff lines now show the trailer (#247 — fix here since
  this task owns the membership diff path) and quote tag values via
  `QuoteIfNeeded` (#248).
- **T3 decisions (as landed):** the planner is `planTagAtomDeltas`
  (`internal/organize/tagatom_apply.go`, grown from the gate's ledger
  helpers); deltas fold through the interpreter's exact `Complete` AFTER the
  bucket folds in `planMemberships` (tag grouping) or via
  `planAtomMembershipEdits` on the designated tag dimension (facet branch —
  the status-grouped add path). `_tag-strip = none` apply reading, pinned in
  RFC 0015: membership = box tags ∪ the current placements' reconstructed
  bucket tags, diffed as EFFECTIVE sets with the bucket folds skipped
  (`placementFolds=false`) — a moved line's old tag stays until the box is
  edited, deleting a still-placed tag is a no-op, placement alone never
  removes a namespace subtree. Under `placement` strip, a base box tag now
  expressed by an edited placement is a box→placement migration (not a
  removal), and a stale atom whose placement vanished RE-ASSERTS its tag
  (folds to no change). Conflicts are exit-2 trouble, batched like planMoves':
  `N tag conflict(s) — box tag atoms disagree with placement or across
  appearances; re-edit the document`. `%`-marked atoms don't parse (reserved
  rune, no box production) — pinned as a parse-level rejection, no plugin
  emission built. The literal lane's two gate-refusal tests re-pointed to
  dry-run membership previews (`organize_literal_bare_token_is_tag`,
  `organize_literal_quoted_box_token_parses`).

## Task 4: JSON `tags` + `describe_node_types` `tag_set` (G12, half)

- `list -format json` node view and the mcp enriched listing gain a
  top-level `tags` array (PresentUnifiedTags, SortKey order); `describe_node_
  types` reports `tag_set: {field, interpreter}` per type. `facets.categories`
  stays (retires with #251).
- Vectors: bats `list -format json` on /dav/ns/ (NDJSON lines with `tags`);
  mcp.bats `describe_node_types` shows `tag_set`; mcp `list_nodes` enriched
  entry carries `tags`.
- **T4 decisions (as landed):** the shared enrichment lives in
  `internal/command_components/tag_view.go` — organize's `describedTagDims`/
  `firstTagDim`/`interpreterForDimension`/`unifiedTagPresenter` MOVED there
  (exported), composed as `NodeTagsPresenter` (the per-listing presenter both
  node views call) and `TypeTagSets` (describe_node_types' per-type
  `tag_set: {field, interpreter}` — name-resolution only; the consuming
  paths validate the registry lookup). `list -format json` prefers the
  enriched fetch (the moved `ListEnrichedChildren`) only when a tag
  dimension is declared; the text table is untouched. mcp threads the
  `[tags]` override as a `tagsOverride` field on Resources/Tools, set at
  server startup.

## Task 5: nvim corpus + docs + vectors index

- Corpus: every existing corpus doc gains tag atoms where the vectors did;
  `_tag-atoms`/`_tag-strip` envelope fields (generic `field_line` already
  parses them — add one corpus case); highlights unchanged (`@tag`).
- RFC 0015 object-line + envelope sections; FDR 0025 status note (matrix row
  "tag (multi)" Present column becomes ✅ key-free atom); RFC 0019 §6.2 note
  that box edits go through `Complete` exact; vectors index rows G1 ×3, G2
  ×2, G3, G6, G7 ×4, G12 (tag_set) filled; CLAUDE.md sentence.

## Out of scope

`cg fmt-organize` (slice 3); `list -format espalier` + mesa (slice 4);
`--filter` (#251); multiple tag fields per type (G6 v1 = one designated set);
wire-plugin tag sets (RFC 0013 follow-up); #232 tag-object editing.

## Status (2026-09-03) — COMPLETE

| task | commits |
|---|---|
| T1 — tag set as codec presentation (G6) | `f92fd30` |
| T2 — tag atoms + levers (G1/G2/G3) | `c5702bd` + review `5ce7e22` |
| T3 — box tag edits are membership edits (G7) | `c77d787` + review `c397f22` |
| T4 — JSON `tags` + `tag_set` (G12) | `b7ec06a` + review `ec8ad82` |
| T5 — corpus audit + docs sweep + vectors index | the commit carrying this table |

Followups filed this slice: #257 (surface the stale-atom re-assert fold with a
notice instead of a silent "no changes to apply"), #258 (fold
planMemberships/applyMemberships' parameter lists into a plan-params struct).
