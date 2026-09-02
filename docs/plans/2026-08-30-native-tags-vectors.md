# Native tags — vector index (G# → bats test)

Review checklist for `2026-08-30-native-tags-design.md`. Every row is a
whole-document vector (`assert_output - <<-EOM`) unless marked golden. Test
names are filled in as each slice lands; `—` = not yet written. Bats names
are the `function <name> { # @test` form; Go names are `go test` functions.
Slice 1 rows verified against the tree 2026-08-30 (T7).

The nvim tree-sitter corpus,
`zz-nvim/grammars/organize/test/corpus/organize.txt`, is the dialect's
conformance vector (G11): each of its cases mirrors one of the bats vectors
below (the case title names the lane), and `just test-grammar-corpus`
(= `checks.grammar-corpus`) is its gate.

| G# | decision | slice | test (file:function) |
|---|---|---|---|
| G1 | bare atoms after id/type; `_tag-atoms = leading` default | 2 | `organize_tagatoms.bats:organize_tagatoms_leading_default` (+ every field-grouped lane re-pointed; Go `TestObjectLineTagRoundTrip`) |
| G1 | `_tag-atoms = trailing` | 2 | `organize_tagatoms.bats:organize_tagatoms_trailing_config` (+ Go `internal/organize` `TestTagLeverEnvelopeRoundTrip`) |
| G1 | `_tag-atoms = none` | 2 | `organize_tagatoms.bats:organize_tagatoms_none_config` |
| G2 | tag-grouped: strip only `Via`, siblings stay | 2 | `organize_tagatoms.bats:organize_tagatoms_strip_placement_keeps_sibling`, `organize_tagatoms.bats:organize_tagatoms_ns_root_strip` (G10a root), `organize_tagatoms.bats:organize_tagatoms_ns_strip_all_contributors` (both same-bucket tags strip), `organize_tagatoms.bats:organize_tagatoms_whole_dim_move_passes_gate` (bucket-to-bucket move through the gate; + `organize_tags.bats`, `organize_ns.bats`, `organize_headings.bats` re-pointed; Go `TestTagRenderFill_*`) |
| G2 | `_tag-strip = none` | 2 | `organize_tagatoms.bats:organize_tagatoms_strip_none` (+ Go `TestTagRenderFill_StripNoneKeepsVia`) |
| G3 | levers omitted at default; config default; doc wins | 2 | `organize_tagatoms.bats:organize_tagatoms_leading_default` (omitted), `organize_tagatoms.bats:organize_tagatoms_trailing_config` / `organize_tagatoms_none_config` / `organize_tagatoms_strip_none` (config default + `_base`-addressed echo), `organize_tagatoms.bats:organize_tagatoms_doc_wins` (+ Go `TestEffectiveTagLevers` — the doc-wins resolution itself; full doc-wins apply semantics land with G7) |
| G4 | `fmt-organize` regenerates + rewrites `_base` | 3 | — |
| G4 | `fmt-organize` refuses on unapplied edits | 3 | — |
| G4 | `fmt-organize` never emits reset headings | 3 | — |
| G6 | `categoriesCodec.Format` produces the tag set (Go unit) | 2 | — |
| G7 | box atom added → membership add (exact) | 2 | — |
| G7 | box atom removed → membership remove | 2 | — |
| G7 | cross-appearance tag disagreement → conflict | 2 | — |
| G7 | `%`-atom edit rejected | 2 | — |
| G8 | `list -format espalier` == organize boxes | 4 | — |
| G8 | JSON `tags` array | 4 | — |
| G8 | mesa table (golden) | 4 | — |
| G9 | bare token in box is a tag, even if it names a field | 1 | `organize_literal.bats:organize_literal_bare_token_is_tag_apply_refuses` (+ Go `internal/trellis` `TestLiteral_RoundTrip`) |
| G9 | non-ground interior is a loud bad request | 1 | `organize_literal.bats:organize_literal_non_ground_interior_rejects` (+ Go `TestLiteral_NotGround`) |
| G9 | quoted tag (`"_ inbox"`) round-trips in box and heading | 1 | `organize_literal.bats:organize_literal_quoted_tag_heading_round_trips`, `organize_literal.bats:organize_literal_quoted_box_token_parses` |
| G10 | `--group-by (tags)` → `# <tag>` buckets at minimal depth, no dim heading | 1 | `organize_groupby.bats:organize_groupby_tags_whole_set`, `organize_headings.bats:organize_headings_tags_buckets_at_minimal_depth` (+ `organize_tags.bats`, `organize_literal.bats` re-pointed; Go `TestParseGroupSpec_Tags`, `TestGenerate_TagBucketsAtMinimalDepth`) |
| G10 | `--group-by project` → `# project` root heading + nested `## -client` rollup; direct root placement = bare tag (G10a) | 1 / 1.5 | `organize_groupby.bats:organize_groupby_namespace_rollup`, `organize_ns.bats:organize_ns_direct_root_placement_writes_bare_tag` (+ `organize_ns.bats` re-pointed; Go `TestGroupNodesByNamespace_RootPlacement`, `TestPlanMemberships_RootBucket*`, `TestNamespaceResetPopsToRootBucket`) |
| G10 | `--group-by status=` → `# status=` / `## =value` | 1 | `organize_groupby.bats:organize_groupby_field` (+ `organize.bats`, `organize_priority.bats`, `organize_fields.bats` re-pointed) |
| G10 | `--group-by date_due=(month)` → `# date_due=(month)`; bare `date_due=` → `(day)` / config default | 1 | `organize_groupby.bats:organize_groupby_date_granularity`, `organize_date.bats:organize_date_bare_groups_by_day`, `organize_date.bats:organize_date_config_default_month` |
| G10 | legacy `date_due:month` / `categories` / `categories/project` rejected with hint | 1 | `organize_groupby.bats:organize_groupby_rejects_legacy_spellings` (+ Go `TestParseGroupTerm_Rejects`, `TestGroupedSpec_RejectsUnknownGranularity` for the heading) |
| G10 | empty-namespace bare name → error suggesting `name=` | 1 | `organize_groupby.bats:organize_groupby_empty_namespace_suggests_field` (+ Go `TestRejectEmptyNamespace`) |
| G10 | query shapes (`status=x`, `(foo)`) are not groupings | 1 | `organize_groupby.bats:organize_groupby_rejects_query_shapes` |
| G10 | qualifier in query position is reserved (bad request) | 1 | `trellis_qualifier.bats:list_query_rejects_qualifier_value_as_reserved`, `trellis_qualifier.bats:list_query_rejects_qualifier_term_as_reserved` |
| G10 | depth normalization (a `##`-rooted document applies identically to the `#` form; generate never emits an empty heading) | 1 | `organize_headings.bats:organize_headings_double_hash_document_applies_identically`, `organize_headings.bats:organize_headings_generate_never_emits_reset` (+ Go `TestParseDepthNormalization_*`, `TestParseFieldDoc_DepthNormalizationPreservesLadder`, `TestGenerateNeverEmitsResetHeading`) |
| G10 | empty-heading reset (`##` pops one; `#` → ungrouped; deeper no-op) | 1 | `organize_headings.bats:organize_headings_reset_pops_to_parent_and_ungrouped`, `organize_headings.bats:organize_headings_reset_deeper_than_current_is_noop` (+ Go `TestParseReset_*`) |
| G11 | nvim corpus mirrors the slice-1 vectors | 1 | `zz-nvim/grammars/organize/test/corpus/organize.txt` (`just test-grammar-corpus`) |
| G12 | `describe_node_types` reports `tag_set` | 4 | — |
| G13 | hand-written bare token round-trips through parse→write | 1 | `organize_literal.bats:organize_literal_bare_token_is_tag_apply_refuses` (+ Go `internal/organize` `TestObjectLineTagRoundTrip`) |
| G16 | existing lanes converted to whole-document vectors | 1 | all `organize*.bats` |

## UAT feedback (slice 1.5)

| item | test (file:function) |
|---|---|
| D: priority atoms present the band (stripped under their `## =<band>` heading per #229); a band FIELD edit completes to the RFC 5545 int, a raw-int edit writes verbatim | `organize_priority.bats:organize_priority_field_edit_band_completes`, `organize_priority.bats:organize_priority_field_edit_raw_int_writes_verbatim` (+ Go `plugins/caldav` `TestBuildFieldWritePatch_PriorityBandAndRawInt`) |
| E: case-fold status — present lowercase everywhere (atoms, `## =needs-action` buckets, facet values); stored stays canonical UPPERCASE (`STATUS:COMPLETED` curl-verified); `status=completed` AND `status=COMPLETED` both match (`FoldCase` folds both sides); out-of-enum still presents and round-trips to ITS uppercase (Go unit) | `organize.bats:organize_apply_status_move_commits` (+ every status-carrying organize lane re-pointed lowercase; Go `plugins/caldav` `TestCaseFoldCodec_PresentsLowercaseWritesUppercase`, `TestFacetFilter_FoldCaseMatchesBothSpellings`, `TestStatusDimensionFoldCase`) |
| B: whole-output asserts on the residual partials — `list -query` checks are full URI/NAME/TYPE tables, error checks the full `cutting-garden: …` line(s), conflict rejections the whole two-line message, seeded curl bodies byte-for-byte; written curl bodies stay exact-full-LINE asserts (CRLF + volatile DTSTAMP, documented in-file), and the wall-clock `due_band` facet row is the one facet line left unpinned | every `organize*.bats` + `trellis_qualifier.bats` (the sweep; each file's residue carries its justification comment) |
