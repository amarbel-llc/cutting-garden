# Native tags — vector index (G# → bats test)

Review checklist for `2026-08-30-native-tags-design.md`. Every row is a
whole-document vector (`assert_output - <<-EOM`) unless marked golden. Test
names are filled in as each slice lands; `—` = not yet written. Bats names
are the `function <name> { # @test` form; Go names are `go test` functions.
Slice 1 rows verified against the tree 2026-08-30 (T7). Slice 2 rows (G1, G2,
G3, G6, G7, G12, and G8's JSON row, pulled forward) verified against the tree
2026-09-03 (slice 2 T5) — the slice-2 set is COMPLETE. Slice 3 (the G4
`fmt-organize` rows) landed 2026-09-06 with the command; slice 4 (the G8
espalier/mesa rows) landed 2026-09-20 with `list -format espalier` and the
mesa text table — every row is now filled.

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
| G2 | tag-grouped: strip only `Via`, siblings stay | 2 | `organize_tagatoms.bats:organize_tagatoms_strip_placement_keeps_sibling`, `organize_tagatoms.bats:organize_tagatoms_ns_root_strip` (G10a root), `organize_tagatoms.bats:organize_tagatoms_ns_strip_all_contributors` (both same-bucket tags strip), `organize_tagatoms.bats:organize_tagatoms_whole_dim_move_applies` (bucket-to-bucket move with sibling atoms untouched; + `organize_tags.bats`, `organize_ns.bats`, `organize_headings.bats` re-pointed; Go `TestTagRenderFill_*`) |
| G2 | `_tag-strip = none` | 2 | `organize_tagatoms.bats:organize_tagatoms_strip_none` (+ Go `TestTagRenderFill_StripNoneKeepsVia`) |
| G3 | levers omitted at default; config default; doc wins | 2 | `organize_tagatoms.bats:organize_tagatoms_leading_default` (omitted), `organize_tagatoms.bats:organize_tagatoms_trailing_config` / `organize_tagatoms_none_config` / `organize_tagatoms_strip_none` (config default + `_base`-addressed echo), `organize_tagatoms.bats:organize_tagatoms_doc_wins` (+ Go `TestEffectiveTagLevers` — the doc-wins resolution itself; full doc-wins apply semantics land with G7) |
| G4 | `fmt-organize` regenerates + rewrites `_base` | 3 | `fmt_organize.bats:fmt_organize_regenerates_and_rewrites_base` (+ `fmt_organize.bats:fmt_organize_unchanged_is_byte_identical` — unchanged live data → byte-identical file, "unchanged" summary) |
| G4 | `fmt-organize` refuses on unapplied edits | 3 | `fmt_organize.bats:fmt_organize_refuses_unapplied_edits` (exit 64; file byte-untouched) |
| G4 | `fmt-organize` never emits reset headings | 3 | `fmt_organize.bats:fmt_organize_never_emits_reset_heading` (+ every whole-document AFTER assert in the lane) |
| G4 | `fmt-organize` lever round-trip: `_tag-atoms = trailing` kept and re-rendered trailing, doc-authoritative (the `[organize]` config is REMOVED before fmt) | 3 | `fmt_organize.bats:fmt_organize_keeps_trailing_lever` |
| G6 | `categoriesCodec.Format` produces the tag set (Go unit) | 2 | Go `plugins/caldav` `TestCategoriesCodec_FormatProducesTagSet`, `TestCategoriesCodec_FormatAgreesWithFacetValues` (Format ↔ facet-value agreement); `internal/cutting_garden_plugins` `TestPresentUnifiedTags_PicksDesignatedField`, `TestPresentUnifiedTags_EmptyWhenNoFieldTag`, `TestValidateUnifiedFieldSets_OneFieldTagPerType` (one `FieldTag` per type enforced) |
| G7 | box atom added → membership add (exact) | 2 | `organize_tagatoms.bats:organize_tagatoms_add_writes_membership`, `organize_literal.bats:organize_literal_bare_token_is_tag` (dry-run preview) (+ Go `TestPlanTagAtomDeltas_AddAndRemove`, `TestPlanMemberships_AtomDeltasFoldAfterBuckets`, `TestPlanAtomMembershipEdits_FoldsExact`) |
| G7 | box atom removed → membership remove | 2 | `organize_tagatoms.bats:organize_tagatoms_remove_writes_membership` (+ Go `TestPlanTagAtomDeltas_AddAndRemove`, `TestPlanTagAtomDeltas_NamespaceMoveKeepsSiblings`) |
| G7 | cross-appearance tag disagreement → conflict; placement-vs-box → conflict; `_tag-strip = none` move-is-not-an-edit | 2 | `organize_tagatoms.bats:organize_tagatoms_cross_appearance_disagreement_conflicts`, `organize_tagatoms.bats:organize_tagatoms_placement_vs_box_conflicts`, `organize_tagatoms.bats:organize_tagatoms_strip_none_move_is_not_an_edit` (+ Go `TestPlanTagAtomDeltas_CrossAppearanceDisagreement`, `TestPlanTagAtomDeltas_PlacementVsBox`, `TestPlanTagAtomDeltas_StripNoneMoveIsNotAnEdit`, `TestPlanTagAtomDeltas_MigratedToPlacementIsNotARemove`) |
| G7 | stale atom RE-ASSERTS its tag (bucket line deleted, sibling box atom retained → adds=[tag]; composed fold nets ZERO edits — never a silent removal) | 2 | Go `internal/organize` `TestPlanTagAtomDeltas_StaleAtomReAsserts` (+ `organize_headings.bats` reset vector's bare-box comment attests the inverse spelling) |
| G7 | `%`-atom edit rejected (parse-level: `%` is reserved, no box production) | 2 | Go `internal/organize` `TestParseObjectLine_PercentMarkedTermRejects` |
| G8 | `list -format espalier` == organize boxes | 4 | `list_espalier.bats:list_espalier_matches_organize_object_lines` (the isometry pin: organize's object lines are grep-derived from a live render and asserted byte-equal, then pinned literally), `list_espalier.bats:list_espalier_query_filters_and_renders_boxes`, `list_espalier.bats:list_espalier_multi_type_inlines_type` (the multi-type `!type` inline rule, via `DistinctTypes`) (+ Go `internal/list` `TestRun_EspalierBoxes`, `TestRun_EspalierNoTagPlugin`, `TestRunFacets_EspalierRejects`) |
| G8 | JSON `tags` array | 2 (landed early, with G12) | `organize_ns.bats:organize_ns_list_json_carries_tags` |
| G8 | mesa table (golden) | 4 | `list_espalier.bats:list_text_mesa_table_carries_tags_column` (TAB-separated pipe form + TAGS column), `list_espalier.bats:list_text_no_tag_plugin_keeps_three_columns` (+ Go `TestRun_TextTable`, `TestRun_JSONCarriesTags`'s text half; the `organize.bats` / `organize_date.bats` / `organize_priority.bats` `list -query` asserts re-pointed to the TAB form) |
| G9 | bare token in box is a tag, even if it names a field | 1 | `organize_literal.bats:organize_literal_bare_token_is_tag` (+ Go `internal/trellis` `TestLiteral_RoundTrip`) |
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
| G11 | nvim corpus mirrors the slice-1 + slice-2 vectors (tag atoms in every dialect; `_tag-atoms`/`_tag-strip` envelope fields; audited byte-for-byte against the bats twins 2026-09-03) | 1 / 2 | `zz-nvim/grammars/organize/test/corpus/organize.txt` (`just test-grammar-corpus`) |
| G12 | `describe_node_types` reports `tag_set` `{field, interpreter}` | 2 | `mcp.bats:mcp_describe_node_types` (+ Go `internal/mcp` `TestCollectSchema_ReportsTagSet`, `internal/node_view` `TestTypeTagSets`) |
| G12 | `list -format json` node views carry SortKey-ordered `tags` | 2 | `organize_ns.bats:organize_ns_list_json_carries_tags` (+ Go `internal/list` `TestRun_JSONCarriesTags`, `internal/node_view` `TestNodeTagsPresenter`) |
| G12 | mcp enriched listing entries carry `tags` | 2 | `mcp.bats:mcp_list_nodes_enriched_entry_carries_tags` (+ Go `internal/mcp` `TestReadResource_EnrichedEntriesCarryTags`) |
| G13 | hand-written bare token round-trips through parse→write | 1 | `organize_literal.bats:organize_literal_bare_token_is_tag` (+ Go `internal/organize` `TestObjectLineTagRoundTrip`) |
| G16 | existing lanes converted to whole-document vectors | 1 | all `organize*.bats` |

## UAT feedback (slice 1.5)

| item | test (file:function) |
|---|---|
| D: priority atoms present the band (stripped under their `## =<band>` heading per #229); a band FIELD edit completes to the RFC 5545 int, a raw-int edit writes verbatim | `organize_priority.bats:organize_priority_field_edit_band_completes`, `organize_priority.bats:organize_priority_field_edit_raw_int_writes_verbatim` (+ Go `plugins/caldav` `TestBuildFieldWritePatch_PriorityBandAndRawInt`) |
| E: case-fold status — present lowercase everywhere (atoms, `## =needs-action` buckets, facet values); stored stays canonical UPPERCASE (`STATUS:COMPLETED` curl-verified); `status=completed` AND `status=COMPLETED` both match (`FoldCase` folds both sides); out-of-enum still presents and round-trips to ITS uppercase (Go unit) | `organize.bats:organize_apply_status_move_commits` (+ every status-carrying organize lane re-pointed lowercase; Go `plugins/caldav` `TestCaseFoldCodec_PresentsLowercaseWritesUppercase`, `TestFacetFilter_FoldCaseMatchesBothSpellings`, `TestStatusDimensionFoldCase`) |
| B: whole-output asserts on the residual partials — `list -query` checks are full URI/NAME/TYPE tables, error checks the full `cutting-garden: …` line(s), conflict rejections the whole two-line message, seeded curl bodies byte-for-byte; written curl bodies stay exact-full-LINE asserts (CRLF + volatile DTSTAMP, documented in-file), and the wall-clock `due_band` facet row is the one facet line left unpinned | every `organize*.bats` + `trellis_qualifier.bats` (the sweep; each file's residue carries its justification comment) |

## Fastmail tags slice 1

Decisions D1–D6 and Task 5b of `2026-09-21-fastmail-tags-slice1.md`, pinned
against the fastmail testserver's triage fixture (port 43113,
`plugins/fastmail/fastmailtestserver/triage_fixture.go`). Every file below is
`organize_fastmail.bats`; each test runs against a fresh server. Per-message
keyword read-backs go through `cg mcp` `read_node` on the member email nodes
(the structured JMAP Email body) — observable cg output, no server backdoor.

| decision | test (organize_fastmail.bats:function) |
|---|---|
| D1 label→tag join from the nearest bare ancestor; `_` root never renders; thread tags are the member union | `organize_fastmail_list_json_smoke`, `organize_fastmail_inbox_grouped_render` (+ Go `plugins/fastmail` `TestTagOf`) |
| D2 state tags `_inbox`/`_unread`/`_flagged`/`_trash` present and sort first | `organize_fastmail_inbox_grouped_render`, `organize_fastmail_inbox_ungrouped_render` (`_inbox` unstripped; organize has no ungrouped mode, so the vector groups by the read-only `has_attachment=`) |
| D2 state atoms write: `_unread` removed → `$seen`, `_flagged` added → `$flagged` | `organize_fastmail_state_atoms` |
| D2 `_trash` is a soft mutation (trash mailbox added, labels + inbox kept) | `organize_fastmail_trash_atom` |
| D2 reserved `_` tag (`_sent`) refused by name, nothing written | `organize_fastmail_reserved_state_tag_rejected` (exit 64 — D2's bad request is EX_USAGE, not the plan's "exit 2"; same `_base` on re-render) |
| D3 fan-out to EVERY member (incl. a Sent-only member) | `organize_fastmail_state_atoms` (T2's three members read back via `cg mcp`) (+ Go `TestPlanThreadPatch`) |
| D3 removing `_inbox` from a labelled thread keeps it out of Archive | `organize_fastmail_archive_by_move` |
| D3 archive invariant: a member left with no mailbox lands in Archive | `organize_fastmail_archive_unlabeled_lands_in_archive` |
| D4a new tag with an existing proper prefix → `-`-continuation child | `organize_fastmail_create_continuation_tag` (`payee-acme` → `payee/-acme`) (+ Go `TestPlaceNewTag`) |
| D4b new tag sharing a prefix → bare sibling; candidates ranked by shared `-` segments, so a one-segment prefix (`zz-archive/proj`'s `proj`) loses to a three-segment sibling | `organize_fastmail_create_sibling_tag` (`proj-trips-26-10-hike` → a bare child of `area/-travel`, beside `proj-trips-26-09-yoga`) (+ Go `TestPlaceNewTag`) |
| D4c new tag with no shared prefix → root mailbox | `organize_fastmail_create_root_tag` (`misc-thing`) |
| D5 `--group-by _inbox` = G10a root heading; `_inbox` placement-stripped; moving a line above the heading removes exactly `_inbox` | `organize_fastmail_inbox_grouped_render`, `organize_fastmail_archive_by_move` |
| D6 box shape `[<threadId> <tags…> from=…] <subject>`; `tags` is the designated field (`describe_node_types` `tag_set` `{tags, dodder-hyphen}`) | `organize_fastmail_inbox_grouped_render`, `organize_fastmail_list_json_carries_tags` (via `cg mcp`, the `mcp.bats` precedent) |
| D6 `year` retired → `date` FieldDate, `--group-by date=(month)` | `organize_fastmail_group_by_date_month` |
| Task 5b plugin-declared box ids (NodeIDer) + trailing-slash mailbox URIs | every whole-document vector above (`[T1 …]` ids, `_anchor = fastmail://test/Inbox/`) (+ Go `plugins/fastmail` `TestRelativeNodeID`, `TestURIMinting_TrailingSlash`; `internal/node_view` `TestRelativeIDFor`, `TestBoxIDs_BindsListerAndAnchor`; `internal/organize` `TestBuildDocument_BoxIDsResolveThroughNodeIDer`, `TestBuildDocument_RejectsAmbiguousBoxIDs`) |

Apply summaries are the object's document box line with per-atom word-diff
markers — each removed tag `[-t-]`, each added `{+t+}` (#260/#270; the plan's
`tags=[-_inbox-]` shorthand predates that form).
