# Native tags Slice 1 — one grammar Implementation Plan

**Design:** `2026-08-30-native-tags-design.md` (G9, G10, G13, G15, G16) ·
**Prereq (parallel):** Slice 0 — hyphence reserves `()` (`hyphence-content.peg`
`Reserved`, `go/hyphence/content.go` `contentReservedRunes`,
`rust/hyphence/src/content.rs` `RESERVED_RUNES`, + vectors); cutting-garden's
`internal/trellis` lexer is hand-rolled and independent, so this slice does not
BLOCK on the hyphence merge — it bumps the `hyphence` flake input when it lands.

Execute subagent-driven (fresh implementer per task, spec review then quality
review, fixes re-verified) as slices 2/3 were. Every task's vectors are
whole-document `assert_output - <<-EOM` heredocs (G16); `_base` digests stay
verbatim.

## Status (2026-08-30) — all seven tasks landed

| task | commits (green-chestnut) |
|---|---|
| T1 whole-document vectors + pinned testserver port | `69c2f4c`, `1497160`, `0a34c5f` (pre-rebase `b913fbd`, `e425f5a`, `ad205b8`; merged to master) |
| T2 `(…)` qualifier term | `861689d`, `2aa10c8` (merged to master) |
| T3 trellis owns box/heading parsing | `746f8d1`, `3f5a27f` (merged to master) |
| T4 converged `--group-by` spelling | `1ee35a9`, `088a32c` (merged to master) |
| T5 depth normalization + resets | `1b9e49b`, `17ee7de` |
| T6 nvim grammar + corpus | `8f49f67`, `4315ec6` |
| T7 vectors index + doc-drift | the commit carrying this table |

Followups filed during the slice: cutting-garden#247, #248, #249, #250, #251,
#252, #253, #254 (the vectors-regeneration recipe below is among them).

## Task 1: whole-document vector conversion (behaviour-neutral)

Convert every organize lane from awk/partial asserts to whole-document vectors
BEFORE any dialect change, so later tasks show up as reviewable vector diffs.

- **Determinism first.** The caldav testserver binds a random port, which lands
  in `_anchor`, the `% generated:` provenance line, and therefore `_base`. Add
  a `CG_TEST_CALDAV_PORT` env to `cmd/cutting-garden-caldav-testserver` (and
  `caldavtestserver.Start`) so the bats lanes pin a fixed port (the nix bats
  sandbox is network-isolated per build; verify no collision across the lanes —
  they run sequentially per file, one server per test). If a fixed port proves
  impossible, fall back to a `normalize_doc` helper masking ONLY the port; never
  mask `_base`.
- Convert `organize.bats`, `organize_tags.bats`, `organize_ns.bats`,
  `organize_date.bats`, `organize_priority.bats`, `organize_fields.bats`: each
  generate assertion becomes the full expected document; each apply assertion
  keeps its write check but ALSO asserts the full post-apply `organize` render
  (the "after" document). Delete `lines_under_bucket` / `lines_ungrouped` awk
  helpers once nothing uses them.
- `list_facets_*` tests are untouched (`--filter` is out of scope, G5).
- Vectors: every existing test, unchanged names. This task changes NO product
  behaviour; the diff is bats + testserver env only.

## Task 2: the `(…)` qualifier term in trellis

- `docs/rfcs/0014-trellis.peg`: `Reserved` is imported from hyphence (Slice 0
  adds `()`); add `Qualifier <- '(' Ident ')'` and admit it (a) as a `Value`
  alternative — `k=(month)` is a `FieldPred` whose value is a qualifier, a
  "value hole with a meta qualifier" — and (b) as a `BasicTerm` on its own —
  `(tags)`. Add conformance vectors for both, plus `"c(1)"` (a quoted tag
  containing parens).
- `internal/trellis`: `reservedRunes` gains `()`; AST `Qualifier{Name}`;
  `FieldPred.Value` may be a `QualifierValue`; new `QualifierBasicTerm`. Parser
  + `String()` round-trip tests. `trellis_eval/validate.go` REJECTS both forms
  in query position for now (reserved: "qualifier terms are not evaluable yet")
  — organize is their only consumer this slice.
- Vectors: parser unit tables (Go); one bats vector proving `list --query
  'status=(x)'` is a loud bad request (G10 reserved-in-query).

## Task 3: trellis owns box interiors, headings, `_base`, `_type`

- New `internal/trellis/literal.go` (or `internal/organize/literal.go` if it
  must stay organize-private — prefer trellis so `list -format espalier` can
  share it): `ParseLiteral(interior) (Literal, error)` over `trellis.Parse` of
  the bracketed `Group`, returning `{ID, Type, Tags []string, Atoms []Atom}`
  with a GROUNDNESS check (only `=` field preds, no lists/sigils/subpaths/
  closures/negation; anything else → bad request naming the offending term);
  `WriteLiteral(b, Literal)` (no options — as landed) renders
  `[id !type tag… k=v…]` with trellis quoting rules (whitespace/reserved
  runes → `String`).
- `internal/organize/document.go`: `parseObjectLine` / `writeObjectLine`
  delegate; `objectLine` gains `Tags []string` (parsed and round-tripped; NOT
  yet rendered from data — that is Slice 2; a bare token in a hand-edited box is
  preserved verbatim through parse→write and, on apply, is a bad request
  "tag atoms are not writable yet (slice 2)" rather than silently dropped).
  Heading terms (`# dim=`, `## =value`, `## tag`, `## "_ inbox"`) parse via
  trellis terms; `strconv.Quote/Unquote` and `valueName` go. `_base` parses as
  `DigestTerm`, `_type` as `TypeTerm`.
- Retire the inventory in design G13 (all rows except `ParseFacetFilter`).
- Vectors: G13 — a hand-written box with a bare token round-trips through
  `organize` (generate → edit → apply refusal message); a non-ground interior
  (`[x.ics status*=y]`) is a loud bad request; quoted-tag heading round-trip.

## Task 4: converged `--group-by` / `_group-by` / heading spelling

- `groupspec.go`: `parseGroupSpec` accepts exactly the G10 table — `(tags)`
  (whole tag set), bare `<name>` (tag namespace; an EMPTY namespace result is a
  loud error suggesting `<name>=` when a field of that name exists), `<dim>=`
  (field), `<dim>=(<granularity>)` (date field at granularity; bare `<dim>=` on
  a date field resolves `[organize] date_granularity` then day). The legacy
  `dim:granularity`, `categories`, `categories/project` spellings are rejected
  with a hint naming the new spelling.
- `_group-by` persists the SAME spelling as the `--group-by` flag, verbatim —
  there is no separate envelope encoding (`- _group-by = (tags)`, `project`,
  `status=`, `date_due=(month)`); the dimension heading spells `# status=` /
  `# date_due=(month)`; `(tags)` has no dimension heading (buckets `# <tag>`).
  `groupedSpec` parses the heading/envelope with the trellis term parser.
- Re-point every affected vector (Task 1 made them whole-document, so the
  diffs are the spelling changes only).
- Docs: RFC 0015 (dialect + `_group-by` encoding), FDR 0025 (#230 granularity
  spelling note), the slice-3 plan's spellings marked superseded.
- Vectors: G10 — one per row of the spelling table, plus the three rejections.

## Task 5: heading depth normalization + empty-heading resets

- Parser: heading depth is structure-only; the shallowest level present is the
  root. Empty heading at depth N pops context at N and deeper (deeper-than-
  current is a no-op). Generator emits minimal depth (`(tags)` → `# <tag>`;
  field grouping → `# dim=` / `## =value`; namespace → `# -client`).
- Vectors: G10 — the design's reset example applied against the ns fixture
  (line under `##` reset lands under `# work` only; `#` reset lands ungrouped),
  and a depth-normalized `(tags)` document.

## Task 6: nvim grammar + corpus

`zz-nvim/grammars/organize`: bare tag atoms (leading), quoted tags, `(…)`
qualifiers in headings/envelope, empty reset headings, `_tag-*` / new
`_group-by` spellings; `highlights.scm` distinguishes tag atoms from ids and
`name=value` atoms (the eliding feature keys on this); corpus mirrors the
Task 3–5 vectors. `just validate-grammar` stays green.

## Task 7: vectors index + doc-drift

`docs/plans/2026-08-30-native-tags-vectors.md` maps each G# to its test name
and slice; AGENTS.md/README/CLAUDE.md mention the new spellings; RFC 0014 gains
the qualifier term + the "bare is always a tag; fields use operators" rule as a
design constraint.

## Out of scope (this slice)

Rendering tags from data (Slice 2), `fmt-organize` (3), `list` changes (4),
`--filter` (followup), golden.bash port (Slice 4, first bulk-output consumer).

- **Vectors regeneration recipe (followup, filed — see Status).** Task 4 added
  `just debug-organize-vectors`, which only PRINTS every organize lane's
  documents; pasting the `_base` digests back into the bats heredocs is
  manual. The gap: a `CG_UPDATE_GOLDENS`-style lane that rewrites the vectors
  in place (the G16 golden.bash port generalized to the whole-document
  heredocs).
