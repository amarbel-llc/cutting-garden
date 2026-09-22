# Fastmail Tags Slice 1 — one writable tag set + organize inbox triage

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task, two-stage review per task (spec compliance, then code quality). Every design decision below is pinned by a WHOLE-DOCUMENT bats vector (dodder `assert_output - <<-EOM` style, design G16).

**Goal:** Make a Fastmail account a first-class `organize` substrate for inbox triage: `cg organize --group-by _inbox fastmail://<acct>/Inbox/` renders every inbox thread as one box line carrying its key-free tag set (leaf label tags + the `_inbox` / `_unread` / `_flagged` / `_trash` state tags), and edits — a line moved out of `# _inbox`, a tag atom typed into or deleted from a box, a not-yet-existing tag — apply through one JMAP request per apply (`Email/set` fan-out to every member message, `Mailbox/set create` for new labels).

**Architecture:** The thread type migrates from the legacy `DescribeFacets`/`DescribeListingFields` declarations onto the FDR 0025 unified field-codec model (`DescribeUnified`), exactly as caldav did: ONE codec declares the designated `FieldTag` dimension `tags` (multi-valued, groupable, writable, interpreter `dodder-hyphen`); `Format` presents the tag set from the thread's stored fields; `Parse` persists a full-set replacement. The legacy facet/listing/write surfaces become derivations (`DeriveNodeTypeFacets`, `DeriveNodeTypeFacetWrites`). Writes ride the existing engine end to end — `planMemberships` → `MembershipWriteApplier.BuildMembershipWritePatch` → `NodeMutator.PatchNode` — so the organize package changes NOT AT ALL; the plugin grows the three write capabilities and a JMAP write client. The label→tag join lives in `mailboxTree`; the state tags are derived from keywords and role mailboxes; the reverse map (tag→mailbox, plus the creation placement rule) is the write side of the same table.

**Tech Stack:** Go; `plugins/fastmail` (unified codecs, JMAP `Email/set` + `Mailbox/set`, `NodeMutator`), `plugins/fastmail/fastmailtestserver` (+ a standalone `cmd/cutting-garden-fastmail-testserver` binary, the caldav-testserver pattern), `zz-tests_bats/organize_fastmail.bats` + `lib/fastmail.bash`, flake wiring for the test binary. No SDK or `internal/organize` changes are expected; if one turns out to be needed it is its own task with its own review.

**Rollback:** one merge; `git revert`. The plugin has no callers outside the binary; the caldav organize lanes are the N=1 conformance pin that the engine is untouched.

**References:** FDR 0024 (`docs/features/0024-fastmail-plugin.md`), FDR 0025 (unified codecs), RFC 0015 (organize dialect: ungrouped/grouped deletion, G10a root bucket, `_tag-strip`), RFC 0019 (§6 dodder-hyphen, §7 `_` literal), RFC 0017 (`bulk-atomic`), FDR 0020 (`NodeMutator`/`PatchNode`), the UX session record `.tmp/…/scratchpad/inbox-ux/` (scratchpad, PII — not committed), issues #259 #260 #261 (follow-ups this slice does NOT do), memory `fastmail-tag-semantics`.

**Prerequisite reading for the implementer (exact signatures this plan does NOT reproduce):**
- `internal/cutting_garden_plugins/field_unified.go` (`UnifiedField`, `Codec`, `UnifiedDescriber`), `field_derive.go` (`PresentUnifiedTags`, `ValidateUnifiedFieldSets`), `facet_derive.go` (`DeriveNodeTypeFacets`, `DeriveNodeTypeFacetWrites`, `ParseUnifiedMembershipWrite`), `facet_apply.go` (`MembershipWriteApplier`), `mutate.go` (`NodeMutator.PatchNode`, the `applied` contract).
- `plugins/caldav/unified.go` (`categoriesCodec` — the FieldTag codec to mirror; `codecsForType`; `TestCategoriesCodec_FormatAgreesWithFacetValues`), `plugins/caldav/facet_apply.go` (`BuildMembershipWritePatch`), caldav's `PatchNode`.
- `internal/organize/membership_apply.go` (how `planMemberships` folds the G10a root bucket: leaving `# _inbox` removes exactly the bare tag), `internal/organize/apply.go` (`resolveMembershipWrites`: the three interfaces a plugin must satisfy).
- `internal/node_view/tag_view.go` (`FirstTagDim`, `InterpreterForDimension`, `TypeTagSets` — what `list -format json` and `describe_node_types` read). It lived in `internal/command_components` until the 2026-09-22 invalidation-cone split moved it.
- `plugins/fastmail/{jmap.go,client.go,threads.go,mailboxtree.go,facet.go,listing.go,traversal.go}` and `fastmailtestserver/server.go`.
- `zz-tests_bats/lib/caldav.bash` (port table, coproc handshake), `zz-tests_bats/organize_ns.bats` (the namespace-grouping vectors this lane's `# _inbox` document mirrors), `cmd/cutting-garden-caldav-testserver/main.go`, `flake.nix` (`cuttingGardenCaldavTestServer`, the `CG_TEST_CALDAV` bats input).

---

## Design decisions settled going in (2026-09-21 UX session)

- **D1 — Tag semantics (label → tag).** A label's tag is the join, with `-` continuations, from its NEAREST BARE ancestor: `area/-career/proj-26-08-thxunext/-msft` → the single tag `proj-26-08-thxunext-msft`; `payee/-one_medical` → `payee-one_medical`; `zz-archive/proj/-24-12-thxunext/-10x` → `proj-24-12-thxunext-10x`. Bare ancestors are tags ON the tag (a tag graph), never object atoms; exposing/querying that graph (`zz-archive` selecting archived threads transitively) is OUT of this slice. The root named `_` is ergonomic only: it never renders, its children are plain tags (`_/req-others` → `req-others`).
- **D2 — State tags.** The tag set also carries `_inbox` (any member in the `inbox` role mailbox), `_unread` (any member lacking `$seen`), `_flagged` (any member with `$flagged`), `_trash` (any member in the `trash` role mailbox). All four are writable. `sent` and `junk` are never emitted; `_sent`, `_junk`, `_archive`, and any other `_`-prefixed tag typed into a box is a loud bad request naming the tag. RFC 0019 §7 keeps `_` literal, so these are ordinary tags to the interpreter (they sort first under plain ASCII order).
- **D3 — Thread-level write semantics.** A tag add/remove fans out to EVERY member message of the thread (the union/any-of read side's inverse): `_unread` removed → `$seen: true` on all members; added → `$seen: false` on all; `_flagged` likewise via `$flagged`; `_inbox`/`_trash` add/remove the role mailbox id on all members; a label tag adds/removes its mailbox id on all members. **Archive invariant:** after removing `_inbox` (or any label), a member left with NO mailbox gets the `archive` role mailbox (JMAP requires ≥1; verified against the real account: every Archive message carries no user label). Adding `_inbox` to a member that is in Archive removes it from Archive.
- **D4 — Unknown tags are CREATED.** A tag realizing no mailbox is created by `Mailbox/set` in the SAME request as the `Email/set` (creation-id back-reference), so the apply stays atomic. Placement: (a) if an existing mailbox's tag is a PROPER PREFIX of the new tag at a `-` boundary, the new mailbox is a `-`-continuation child of the LONGEST such mailbox (`payee-charles_tyrwhitt` → `-charles_tyrwhitt` under `payee`); else (b) if some existing mailbox shares a common prefix with the new tag at a `-` boundary, the new mailbox is a BARE sibling under that mailbox's parent (`proj-trips-26-09-yoga_retreat` → bare child of `area/-travel`, beside `proj-trips-26-09-kyle_yoga`); else (c) a bare root mailbox. Every created mailbox is reported in `PatchNode`'s `applied` slice as `mailbox:<full/path>` (the summary surface is #260; this slice does not print it).
- **D5 — Inbox triage is existing engine mechanics.** `--group-by _inbox` is a dodder-hyphen namespace grouping with no continuations: a G10a root heading holding every line; `_tag-strip = placement` strips `_inbox` as the root's Via tag; a line moved above the heading (or deleted) removes exactly the bare `_inbox`. No new dialect, no query-strip extension.
- **D6 — Dimension key and object shape.** The thread type's designated tag field is `tags`. Boxes render `[<threadId> <tags…> from=<addr>] <subject>`: `from` is the only inline atom (read-only), `subject` is the trailer (read-only), `date` (newest `receivedAt`, ISO) is a listing field only, `has_attachment` stays a groupable read-only categorical. The legacy `read`/`flagged`/`folder` dimensions are DELETED (subsumed by the state tags); `year` is DELETED in favor of `date` declared `FieldDate` groupable read-only (`--group-by date=(year|month)` for free via #230's prefix machinery). Object ids are thread ids, one line per thread, newest first (the plugin's listing order).
- **D7 — Not in this slice.** #259 (dormant sigil), #260 (box-form apply summary), #261 (wrapped boxes), the tag graph (D1), `-query _inbox` from the account root (the anchor is the Inbox mailbox container), the recursive facet rollup (FDR 0024 limitation), capture/diff (FDR 0024 slice 3), message-level (per-email) edits, `_terminal`/TerminalValues.

---

## Task 1: Label→tag join + state tags in `mailboxTree` (read side, pure)

**Files:**
- Modify: `plugins/fastmail/mailboxtree.go` (add `tagOf(id) (tag string, ok bool)`, `tagIndex` reverse map `tag → mailbox id`, `roleID(role) (id, ok)`)
- Create: `plugins/fastmail/tags.go` (`stateTags(members []Email, tree) []string`, the `_`-tag constants, `isStateTag`, the reserved-but-refused set)
- Test: `plugins/fastmail/mailboxtree_test.go`, `tags_test.go`

**Approach:** `tagOf` walks the parent chain collecting segments, then joins from the LAST bare segment onward (a segment is bare iff it does not start with `-`); a chain whose last bare segment is the literal `_` root yields no tag when it IS the leaf, and otherwise the join starts after it (D1). Role mailboxes yield no tag. `tagIndex` is built once per tree; a duplicate tag (two mailboxes joining to one name — legal in Fastmail) keeps the FIRST in listing order and is unit-tested as a known limitation. `stateTags` derives D2 from the members' `Keywords` and `MailboxIDs` against `roleID("inbox")`/`roleID("trash")`.

**Steps:** TDD table tests over a synthetic mailbox list: `area/-career/proj-x/-msft` → `proj-x-msft`; `payee/-a-b` → `payee-a-b`; `_/req-others` → `req-others`; `zz-archive/proj/-24-12-t/-10x` → `proj-24-12-t-10x`; role mailbox → none; state tags for {inbox+unseen}, {trash+flagged}, {sent only} → `[]`. Commit: `feat(fastmail): dodder-hyphen label→tag join + state tags (fastmail tags slice 1, D1/D2)`.

---

## Task 2: Unified declaration for thread/mailbox/email (`DescribeUnified`) — legacy surfaces derived

**Files:**
- Create: `plugins/fastmail/unified.go` (`unifiedFieldSets()`, `tagsCodec`, identity codecs for `subject`/`from`/`date`/`has_attachment` and the mailbox `threads`/`emails`, `codecsForType`)
- Modify: `plugins/fastmail/facet.go` (`DescribeFacets` → `DeriveNodeTypeFacets(unifiedFieldSets())`; `threadFacets` emits `tags` (label tags ∪ state tags), `from`, `date`, `has_attachment`; drop `read`/`flagged`/`folder`/`year` and their constants), `listing.go` (`DescribeListingFields` derived; `threadFields` gains `tags []string` and `date`), `plugin.go` (assert `UnifiedDescriber`, `FacetWriteDescriber`; new `facet_write.go` with `DescribeFacetWrites` derived), `facet_test.go`, `listing_test.go`
- After editing: `just codemod-generate-dagnabit` if any `pkgs/`-facaded package changed (none expected — but run the drift gate).

**Approach:** mirror `plugins/caldav/unified.go`. `tagsCodec.Fields()` = `{Key: "tags", Label: "Tags", Kind: FieldTag, Groupable, MultiValued, Writable, Interpreter: "dodder-hyphen"}`; `Format` presents `stored["tags"]` (the `[]string` `threadFields` now carries; tolerate `[]any` after a JSON round-trip like caldav's `stringsOf`); `Parse` returns `{"tags": <full set>}`. `date` is `FieldDate`, groupable, read-only, its facet value the ISO day of the thread's newest `receivedAt` (prefix-coarsenable per #230). Keep `FacetCounts`/`liftFacets` as the counting path. Pin `TestTagsCodec_FormatAgreesWithFacetValues` (Format's set == `threadFacets`'s `tags` values) exactly as caldav pins categories. `validateUnifiedDeclaration` (one FieldTag per type) must pass.

**Steps:** failing tests for Format/Parse and the agreement pin → implement → derive the three legacy describers → delete the retired dimensions and update their tests → commit: `feat(fastmail): unified field codecs — `tags` is the designated FieldTag (fastmail tags slice 1, D6)`.

---

## Task 3: JMAP write client — multi-call requests, `Email/set`, `Mailbox/set`

**Files:**
- Modify: `plugins/fastmail/client.go` (generalize `call` to `callMany(ctx, calls []jmapMethodCall) ([]jmapMethodResponse, error)`; the single-call helper becomes a wrapper), `jmap.go` (`emailSet(ctx, updates map[string]map[string]any)`, `mailboxSet(ctx, creates map[string]MailboxCreate)`, the combined `applyThreadPatch(ctx, req threadPatchRequest)` that emits `[Mailbox/set create…, Email/set update…]` with `#<creationId>` keys in `mailboxIds` patches; `notUpdated`/`notCreated` → `errors.BadRequestf` naming the id and the server's `type`/`description`)
- Modify: `plugins/fastmail/fastmailtestserver/server.go` (serve `Email/set` update patches — `mailboxIds/<id>: true|null`, `keywords/<k>: true|null`, whole-object `mailboxIds`/`keywords`; serve `Mailbox/set` create with creation ids resolvable in a later call of the same request; reject an update that would leave a message with no mailbox (JMAP `invalidProperties`); bump the account `state` on every set)
- Test: `plugins/fastmail/jmap_test.go` against the in-process testserver

**Approach:** JMAP patch-object semantics (RFC 8620 §5.3): a `mailboxIds/<id>` key sets or clears one membership without resending the map. Creation-id references in the same request are `"#c1"` keys. One request per apply-call; the plugin advertises `bulk-atomic` (RFC 0017) later, not here.

**Steps:** failing tests: update two members' `mailboxIds`+`keywords` in one call; create a mailbox and reference it from the same request; a `notUpdated` surfaces as a bad request → implement client + testserver → commit: `feat(fastmail): JMAP Email/set + Mailbox/set client with creation-id back-references (fastmail tags slice 1)`.

---

## Task 4: The write surface — `NodeMutator.PatchNode` + `MembershipWriteApplier`

**Files:**
- Create: `plugins/fastmail/mutate.go` (`PatchNode`, the `{"tags": […]}` body, `planThreadPatch(current threadView, tree, newTags) (threadPatchRequest, error)`: the D2/D3/D4 rules), `plugins/fastmail/facet_apply.go` (`BuildMembershipWritePatch` via `ParseUnifiedMembershipWrite` → `{"tags": newTags}`; `BuildFacetWritePatch` is NOT implemented — no `write:one` dimension exists; `resolveMembershipWrites` only needs `NodeMutator` + `FacetWriteDescriber` + `MembershipWriteApplier`)
- Modify: `plugins/fastmail/plugin.go` (assertions), `mailboxtree.go` (`placeNewTag(tag) (parentID string, name string)` — D4 (a)/(b)/(c))
- Test: `plugins/fastmail/mutate_test.go` (pure `planThreadPatch` table tests), `plugins/fastmail/mutate_e2e_test.go` (against the testserver: `PatchNode` then re-list and compare tags)

**Approach:** `PatchNode` accepts thread URIs only (mailbox/email/raw → bad request "only thread nodes are patchable"); decodes `{"tags": [...]}`; refetches the live thread members and mailbox tree; diffs the requested full set against the current presented set (Task 1's derivation) into: label adds/removes (→ mailbox ids, creating per D4), state adds/removes (→ keywords / role ids), refusing unknown `_` tags (D2); applies the archive invariant per member (D3); issues ONE `applyThreadPatch`; returns `applied = ["tags"]` plus `"mailbox:<path>"` per creation. An empty diff returns `applied = []` and issues nothing. `BuildMembershipWritePatch` ignores `current` (PatchNode refetches).

**Steps:** TDD each rule of `planThreadPatch`: remove `_inbox` on a labeled thread → `mailboxIds/P-F: null` on inbox members only; remove `_inbox` on an unlabeled thread → also `mailboxIds/<archive>: true`; add `_flagged` → `keywords/$flagged: true` on all members; remove `_unread` → `keywords/$seen: true` on all; add `_trash` → trash id on all; add existing label → its id on all; add new continuation tag → `Mailbox/set create` under the prefix parent + `#c1` reference; new sibling-placed tag; new root tag; `_sent` rejected; non-thread URI rejected; then the e2e round-trip. Commit: `feat(fastmail): PatchNode + MembershipWriteApplier — thread tag-set writes fan out over Email/set (fastmail tags slice 1, D3/D4)`.

---

## Task 5: Standalone testserver binary + bats helper + flake wiring

**Files:**
- Create: `cmd/cutting-garden-fastmail-testserver/main.go` (the caldav-testserver pattern: `CG_TEST_FASTMAIL_PORT` pin, one-line handshake `<session-url> <accountId>` on stdout, deterministic fixture — see below; opt-in fixture groups via env like `CG_TEST_CALDAV_LIT`)
- Modify: `plugins/fastmail/fastmailtestserver/server.go` (`StartAt(addr)`; `session.go`'s session-URL resolution must accept a per-account override so the config's `fastmail://<name>/` can point at the testserver — check how `resolveSessionURL` works and add an env/config knob, e.g. `session_url` on the account or `FASTMAIL_SESSION_URL_<NAME>`; document it in `cutting-garden-config(5)`)
- Create: `zz-tests_bats/lib/fastmail.bash` (`start_fastmail_server PORT` / `stop_fastmail_server`, exports `FASTMAIL_ROOT` = `fastmail://test/`, writes the lane's `config.toml` with `[[fastmail.accounts]] name="test"` + the session override + `[tags] interpreter = "dodder-hyphen"`; port table entry **43113 organize_fastmail.bats**, 43114 spare)
- Modify: `flake.nix` (`cuttingGardenFastmailTestServer` derivation; bats input `CG_TEST_FASTMAIL`), `justfile` (`debug-organize-fastmail-fixture`: build the go binary + testserver, render the `--group-by _inbox` document — the dev-loop recipe; and `debug-organize-fastmail-live`: dry-run against the real account with the token from piggy `fastmail-jmap.env`, mirroring `debug-organize-live`)
- `git add` the new cmd dir before any `nix build` / godyn run.

**Fixture (deterministic, PII-free):** account `test`; role mailboxes Inbox/Archive/Sent/Trash; labels `_/req-others`, `area/-career/-resume`, `area/-career/proj-x/-msft`, `area/-travel/proj-trips-26-09-yoga`, `payee/-one_medical`, `zz-archive/proj/-24-t/-10x`; threads: T1 (1 msg, inbox, unseen, `payee-one_medical`), T2 (3 msgs: one inbox+`proj-x-msft`, one sent-only, one `req-others`+`proj-x-msft` — the union case), T3 (1 msg, inbox, flagged, no label — the archive-invariant case), T4 (1 msg, `proj-24-t-10x` + inbox), T5 (1 msg, archived, `area-career-resume` — NOT in the inbox, proves the anchor scope).

**Steps:** binary + helper + flake → `nix build .#cutting-garden-fastmail-testserver` → a smoke bats test that `cg list -format json $FASTMAIL_ROOT/Inbox/` prints the five… four inbox threads with `tags` arrays → commit: `test(fastmail): standalone testserver binary + bats helper (fastmail tags slice 1)`.

---

## Task 6: Whole-document vectors — `zz-tests_bats/organize_fastmail.bats`

**Files:**
- Create: `zz-tests_bats/organize_fastmail.bats` (`# bats file_tags=organize`, serialized, port 43113)
- Modify: `docs/plans/2026-08-30-native-tags-vectors.md` (a new "fastmail tags slice 1" section, rows D1–D6 → test)

**Vectors (each a full `assert_output - <<-EOM` document or full apply summary + a `cg list -format json` read-back):**
1. `organize_fastmail_inbox_grouped_render` — `--group-by _inbox`: envelope (`_group-by = _inbox`, `_type = !cutting_garden-fastmail-thread-v1`), `# _inbox` root heading, four lines newest-first, boxes `[T… <state> <tags> from=…] subject`, `_inbox` stripped, `_` root absent, T2 showing the union tags, `_unread`/`_flagged` sorting first (D1, D2, D5, D6).
2. `organize_fastmail_inbox_ungrouped_render` — no `--group-by`: same lines, `_inbox` present in every box (no placement to strip).
3. `organize_fastmail_archive_by_move` — T1's line moved above `# _inbox`, apply `-commit`: summary pins `tags=[-_inbox-]`; read-back: T1 no longer under `Inbox/`, still under `payee/-one_medical/`, NOT in Archive (D3, D5).
4. `organize_fastmail_archive_unlabeled_lands_in_archive` — T3 moved out: read-back shows it under `Archive/` (D3 invariant).
5. `organize_fastmail_state_atoms` — delete `_unread` from T1's box and add `_flagged` (in place under `# _inbox`): summary `tags=[-_unread-]{+_flagged+}`; read-back `tags` = `["_flagged","_inbox","payee-one_medical"]`; the testserver's keywords show `$seen`+`$flagged` on every member (D2, D3 all-members).
6. `organize_fastmail_trash_atom` — add `_trash` to T4: read-back under `Trash/` AND still `proj-24-t-10x` (soft mutation, D2).
7. `organize_fastmail_create_continuation_tag` — type `payee-acme` into T3's box: created under `payee` as `-acme`; `cg list fastmail://test/payee/` shows it; T3 carries it (D4a).
8. `organize_fastmail_create_sibling_tag` — type `proj-trips-26-10-hike`: created as a bare child of `area/-travel` (D4b).
9. `organize_fastmail_create_root_tag` — type `misc-thing`: created at the root (D4c).
10. `organize_fastmail_reserved_state_tag_rejected` — type `_sent`: exit 2, message names `_sent`, nothing written (same `_base` on re-render) (D2).
11. `organize_fastmail_list_json_carries_tags` — `list -format json $ROOT/Inbox/` NDJSON with `tags` arrays (SortKey order); and `describe_node_types`' `tag_set` for the thread type (via `cg mcp`? — if no bats precedent, a Go test on `TypeTagSets` suffices; note which in the vectors index).
12. `organize_fastmail_group_by_date_month` — `--group-by date=(month)` renders `# date=(month)` / `## =2026-09` buckets (D6 `year` retirement).

Commit: `test(fastmail): organize inbox-triage whole-document vectors (fastmail tags slice 1)`.

---

## Task 7: Docs + doc-drift

**Files:** `docs/features/0024-fastmail-plugin.md` (facets table → the unified declaration; "role mailboxes read-only" → D2/D3; writes section: implemented, `Mailbox/set` creation D4; limitations updated; promotion criteria: Slice 2's round-trip criterion is now met by Task 6 — promote `proposed → testing`), `CLAUDE.md` (the fastmail sentence in "Project status" — no longer "read-only, traversal-only"), `docs/rfcs/0019-tag-interpreter-contract.md` (a one-paragraph note under §7 that plugins MAY emit `_`-prefixed STATE tags; they are literal tags), `cutting-garden-plugins(7)` / `cutting-garden-organize(1)` manpage text if they enumerate writable plugins, `cutting-garden-config(5)` for the session override from Task 5. Run `just lint-worktree` (agents-md / doc-drift eng checks). Commit: `docs(fastmail): FDR 0024 → testing; state tags, writable inbox/trash, tag creation (fastmail tags slice 1)`.

---

## Task 8: Live UAT gate (user-driven, after merge)

Not a code task. The user adds `[[fastmail.accounts]] name = "personal", url = "fastmail://personal/", password_env = "JMAP_TOKEN"` to `~/.config/cutting-garden/config.toml` and runs `just debug-organize-fastmail-live` (dry-run) against the real inbox; the rendered document must match the synthetic one from the UX session line for line (modulo the `_base` digest). Only after that does a real `-commit` triage run happen, on a handful of threads first.

---

## Out of scope (tracked)

#259 dormant/`?` mapping; #260 box-form apply summary (creation reporting stays in `applied`); #261 wrapped boxes; the tag graph and transitive `zz-archive` queries (needs an RFC 0019 extension — file when the read side lands); `-query _inbox` from the account root; recursive facet rollup; per-message edits; capture/diff (FDR 0024 slice 3); `bulk-atomic` advertisement (RFC 0017) — the plugin already transacts per thread, the capability flag lands with the MCP `bulk_mutate` wiring.
