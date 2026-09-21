# Invalidation-cone moves — B1 config-section registry, B2 `command_components` split, B3 test-only plugin imports

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan unit-by-unit, two-stage review per unit (spec compliance, then code quality). Three merge units, not seven: the merge gate is the cost this plan exists to cut, so each unit is one implementer + one merge.

**Goal:** Make a plugin edit invalidate only `plugins/<scheme>`, `plugins/all` and the `cmd/` mains under godyn; make organize/list presentation churn stop re-deriving capture/restore/diff/serve/failures/blob_writer; make framework test lanes stop depending on plugin sources. Measured against the research in `docs/plans/2026-09-21-godyn-invalidation-cone-research.md` (committed with Unit C): today a `plugins/fastmail` edit re-derives ~14 framework packages (~250 derivations, a 92-minute gate).

**Architecture:** (B1) replace RFC 0007's static tommy delegation — `cgconfig.ConfigV0` naming each plugin's `AccountsConfig` type — with a name-keyed config-section registry in the SDK: a plugin registers at `init()` a decoder over the tommy CST value of its top-level table, the decoder being the plugin's own generated `Decode<X>Into` plus its inject step; `cgconfig` keeps only framework sections and the loader dispatches every registered section after `DecodeConfigV0`. This is the in-tree RFC 0013 `config_section` precedent generalized. (B2) move the presentation helpers out of the 9-importer `command_components` hub into a new `internal/node_view` imported only by list/mcp/organize. (B3) framework package tests stop blank-importing real plugins; a fake plugin registered in-test replaces them, the one genuine "real plugins are linked" check moves to bats.

**Tech Stack:** Go; `internal/cutting_garden_plugins` (+ `pkgs/` facade regen via dagnabit), `internal/cgconfig` (+ tommy regen), `internal/command_components`, `plugins/{caldav,fastmail,jira}`, `internal/sdklayering`, `internal/node_view` (new), framework tests, RFC 0007/0009 text. No behavior change for users: `config.toml` is byte-for-byte the same file with the same validation errors.

**Rollback:** each unit is one merge, `git revert`-able independently; B1 lands in dual-path order inside its unit (registry + registrations first, cgconfig cut-over last) so the unit's intermediate commits also build.

**References:** research doc above (§1.5 Proposal A1, §1.6 migration sketch, §2.3 B1–B3, §2.4 sequence); RFC 0007 (`docs/rfcs/0007-config-subsystem.md` § Top-Level Structure, § Package Layering, § Loading and Validation); RFC 0009 §2/§6; RFC 0013 § Host integration (`config_section`); tommy README "Cross-Package Structs"; `internal/cutting_garden_plugins/scheme_registry.go` (the registry pattern to mirror); `internal/traversal_serve/section.go` (`SectionTOML`); `internal/sdklayering/layering_test.go`; dagnabit(1) § move.

**Prerequisite reading for the implementer:** `internal/cgconfig/{config.go,inject.go,config_tommy.go}`, `internal/command_components/{config.go,roots.go}` (`loadConfigWithRaw`, `withoutStanzaClaimedKeys`, `LoadAndInjectConfig`), `plugins/fastmail/config.go` + `config_tommy.go` (`DecodeAccountsConfigInto`, `SetConfiguredAccounts`), same for caldav and jira, `internal/cutting_garden_plugins/scheme_registry.go`, `internal/sdklayering/layering_test.go`, `internal/command_components/{tag_view,espalier_view,enriched_listing,tag_interpreter}.go` and their importers, the five test files listed in Unit C.

---

## Decisions settled going in

- **D1 — Registry shape (Proposal A1, `*cst.Value`).** `cutting_garden_plugins.MustRegisterConfigSection(name string, d ConfigSectionDecoder)` with `type ConfigSectionDecoder func(sub *cst.Value) error`; `RegisteredConfigSections() []string`; an unexported `decodeConfigSections(model *cst.Value) error` (or an exported `DecodeRegisteredConfigSections` if the loader lives outside the SDK package — implementer's call, documented). Duplicate registration panics at init like `MustRegisterScheme`. The SDK facade gains a dependency on `code.linenisgreat.com/tommy/pkg/cst` — accepted (every account-bearing plugin already imports tommy via its generated codec). A1' (wrapper-stripped TOML text) is the fallback ONLY if `cst.Value` cannot be obtained from the parsed document without re-parsing; the implementer must say which path was taken and why.
- **D2 — Unknown-section semantics.** A top-level table with no registered decoder is an unknown key: the existing RFC 0007 warning, not an error. This intentionally stops decoding/validating `[caldav]` in a binary that never linked caldav (research §1.5). Wire-plugin `config_section` stanzas keep today's `withoutStanzaClaimedKeys` path untouched.
- **D3 — Error contract preserved.** A failing section decoder is reported as `errors.BadRequestf("%s: %s: %s", path, name, err)` — EX_USAGE naming the file, the section, and the entry (RFC 0007 § Loading and Validation). Pin it with a test.
- **D4 — Dispatch point.** Registered sections are decoded inside `command_components.loadConfigWithRaw` immediately after `DecodeConfigV0`, before wire-plugin registration and before `AggregateRoots` — exactly where `cgconfig.Inject` runs today. `cgconfig.Inject` and `inject.go` are deleted.
- **D5 — B2 target package.** `internal/node_view`. Files moved: `tag_view.go`, `espalier_view.go`, `enriched_listing.go`, `tag_interpreter.go` (+ their tests and any helper they alone use). Use `dagnabit move` where it applies; qualified references in `internal/list`, `internal/mcp`, `internal/organize` are rewritten. `command_components` keeps `config.go`, `roots.go`, `env_blob_store.go`, `store_resolve.go`, `receipt.go`, and MUST NOT import `node_view`. If a moved helper turns out to be needed by a non-list/mcp/organize package, STOP and report rather than re-introducing the edge.
- **D6 — B3 fake plugin.** Framework tests that blank-import real plugins (`internal/health/health_test.go`, `internal/capture/plan_test.go`, `internal/diff/main_test.go`, `internal/restore/restore_test.go`, `internal/command_components/receipt_test.go`) register an in-package test plugin instead (the `traversal_registration_test.go` pattern). The one assertion that genuinely needs real plugins linked — health's capability report — moves to `zz-tests_bats/health.bats` (create if absent) against the built binary.
- **D7 — Guard.** `internal/sdklayering` gains a third clause: no `internal/` package imports `plugins/` in production code. After Unit C, a fourth: no `internal/` package's tests import `plugins/` either, except an explicit allow-list (empty after B3). The guard still runs only under `just test-go` (research §3); that gap is noted in the plan's out-of-scope, not fixed here.
- **D8 — Out of scope.** B4–B7 (research §2.3), routing wire plugins through the registry, tommy open sections (A3), the copy-mode facade audit, and NATO tiering.

---

## Unit A (B1): config-section registry + cut-over — ONE merge

Commits inside the unit, in this order (each must build and pass `just debug-test-pkg` for the touched packages):

**A1 — SDK registry.** Create `internal/cutting_garden_plugins/config_section.go` mirroring `scheme_registry.go`; unit tests: register/lookup, duplicate panics, `decodeConfigSections` dispatches only names present in the model, a decoder error surfaces with the section name, unknown tables untouched. `just codemod-generate-dagnabit`; `just validate-generate-dagnabit`. Commit: `feat(sdk): name-keyed config-section registry (invalidation-cone B1, A1)`.

**A2 — plugin registrations (dual path).** In `plugins/caldav/config.go`, `plugins/fastmail/config.go`, `plugins/jira/config.go`: `init()` registers `"<scheme>"` with a decoder that calls the generated `Decode<X>Into` then `SetConfiguredAccounts`. Move the `[[<scheme>.accounts]]` config fixtures/tests that today go through `cgconfig`/`LoadConfig` into each plugin's own tests (decoder → accounts round-trip incl. a `Validate` failure surfacing). cgconfig still imports the plugins at this commit — both paths decode; nothing double-injects because `cgconfig.Inject` is still the only caller of `SetConfiguredAccounts`… NO: at this commit the registry decoder ALSO calls `SetConfiguredAccounts` — so the dispatch in A3 must not run until A4 removes `Inject`. Keep A2's registrations inert by NOT wiring dispatch yet (A3 wires it, A4 cuts over, in one merge). Commit: `feat(plugins): register config sections with the SDK (invalidation-cone B1, A2)`.

**A3 + A4 — loader dispatch and cgconfig cut-over (one commit, since dispatch without cut-over would double-inject).** `command_components.loadConfigWithRaw`: obtain the CST model of the parsed document and call the registry dispatch after `DecodeConfigV0` (D3 error wrapping, D4 placement); delete the `cgconfig.Inject` call in `roots.go`. `internal/cgconfig/config.go`: drop the three plugin fields and their `Validate` lines; delete `inject.go`; `just codemod-generate` (tommy) so `config_tommy.go` loses the plugin imports; `just validate-generate`. Fix `command_components/config_test.go` and `cgconfig/config_test.go` (tests seeding `[[caldav.accounts]]` through `LoadConfig` register a fake section in-test or move to caldav — the plugin tests from A2 cover the real decoders). Pin D2 (unregistered `[nosuchplugin]` table → warning, exit 0) and D3 (decoder error → EX_USAGE naming file+section). Commit: `refactor(cgconfig): dispatch plugin config sections through the SDK registry; ConfigV0 imports no plugin (invalidation-cone B1, A3/A4)`.

**A5 — guard.** `internal/sdklayering/layering_test.go`: third clause per D7. Run `just test-go` for that package (it needs `go list`; note the paved path in the report). Commit: `test(sdklayering): no internal/ package imports plugins/ (invalidation-cone B1, A5)`.

**Acceptance for the unit:** `just test-go` green for cgconfig, command_components, cutting_garden_plugins, sdklayering, the three plugins; `checks.tommy-codegen` and `checks.dagnabit-codegen` clean; the existing caldav bats lanes (config-driven accounts, `[tags] interpreter`) unchanged — they are the behavior pin. Report the new invalidation cone by inspection: `rg 'cutting-garden/plugins/' internal/ cmd/ --glob '!*_test.go'` must list only `cmd/` mains and the two test-server mains.

---

## Unit B (B2): split `command_components` — ONE merge

**B-1.** Create `internal/node_view`; move `tag_view.go`, `espalier_view.go`, `enriched_listing.go`, `tag_interpreter.go` and their tests (`dagnabit move` if it handles the rename; else a mechanical move + import rewrite). Update importers in `internal/list`, `internal/mcp`, `internal/organize`. `command_components` must compile without `node_view`; add a sdklayering-style assertion (or a one-line test in `command_components`) that it does not import `node_view`. Any shared helper both halves need (e.g. `CollapseToSingleLine` if `receipt.go` uses it) stays in `command_components` and `node_view` imports it — NEVER the reverse. Whole-document bats vectors (organize_*, list_espalier, fmt_organize) are the behavior pin; nothing in their output may change. Commit: `refactor(node_view): move organize/list presentation helpers out of command_components (invalidation-cone B2)`.

**Acceptance:** `just test-go` for the moved package and its three importers; the espalier↔organize isometry vector still passes; `internal/capture`, `restore`, `diff`, `serve`, `failures`, `blob_writer` no longer transitively import trellis via `command_components` (report the import list of `command_components` before/after).

---

## Unit C (B3 + docs): test-only plugin imports + RFC/AGENTS refresh + research doc — ONE merge

**C-1 (B3).** Per D6, replace the real-plugin blank imports in the five framework test files with in-package fake plugins; move health's real-capabilities assertion to `zz-tests_bats/health.bats`. Add the fourth sdklayering clause (D7). Commit: `test: framework package tests stop importing real plugins (invalidation-cone B3)`.

**C-2 (docs).** RFC 0007: § Top-Level Structure (framework sections only; plugin sections are registry-owned), § Package Layering (cgconfig imports no plugin; plugins register a decoder at init), § Loading and Validation (dispatch step, D2/D3 semantics); RFC 0009 §2 (new SDK symbols) and §6 (an account-bearing plugin registers its section in `init()`); `AGENTS.md` config paragraph (l.~63-72) and `CLAUDE.md` "Project status" config sentence; `internal/cgconfig` package doc; `cutting-garden-config(5)` manpage source if it describes the delegation. Add `docs/plans/2026-09-21-godyn-invalidation-cone-research.md` to the commit (it is currently untracked). Run `just lint-worktree` (doc-drift). Commit: `docs: config-section registry, node_view split, test-import policy (invalidation-cone B1–B3); commit the godyn research`.

---

## Verification of the win (after Unit A merges, for the record)

The next fastmail-only merge's gate log (ringmaster spool) must show `building '…godyn-compile-…'` lines for `plugins-fastmail`, `plugins-all`, and the `cmd-*` mains only — no `internal-*` compile derivations. Record the before/after derivation counts and wall time in the research doc's §0 as an addendum (a docs-only follow-up commit is fine).

## Out of scope

B4–B7 from the research; routing wire plugins through the registry; tommy A3; running `sdklayering` in the godyn lane (needs `go list` inside the sandbox — file as an issue if wanted); the fastmail tags slice itself, which resumes at its Task 3 after Unit C.
