---
status: research
date: 2026-09-21
---

# godyn invalidation cones: the `cgconfig → plugins/*` edge, and a more cache-friendly package tree

Research only. Nothing here is decided; every "Proposal" is labeled as such.
All paths are relative to the cutting-garden module root unless stated.

## 0. Summary

- **Root cause confirmed.** `internal/cgconfig` imports `plugins/caldav`,
  `plugins/fastmail`, `plugins/jira` (`internal/cgconfig/config.go:16-18`,
  `inject.go:4-6`, generated `config_tommy.go:10-12`). `cgconfig` is imported
  by `internal/command_components` (`config.go:11`, `roots.go:12`),
  `internal/mcp` (`mcp.go:294`) and `internal/organize` (`document.go:81-94`),
  and `command_components` is imported by nine of the eleven command packages
  plus `blob_writer`. Under godyn (one content-addressed derivation per
  package, dependents keyed on their imports' archives — godyn(7) § DESCRIPTION)
  any edit to any of those three plugins re-derives compile, vet, the four
  lint/analyzer lanes, test-binary and test-run for ~14 framework packages and
  re-links the three binaries. `internal/command`, `internal/trellis*`,
  `internal/cutting_garden_plugins` and the `capture_*` leaves are NOT in the
  cone — the cascade enters purely through `cgconfig`.
- **Recommended inversion (Part A):** replace static tommy delegation with a
  name-keyed decoder registry in the SDK. Each plugin registers, at `init()`,
  `name → func(*cst.Value) error` (calling its own generated
  `Decode<X>Into`, which is exactly what `config_tommy.go:46/52/58` calls
  today) plus the inject step; `cgconfig` keeps only the framework sections
  (`organize`, `tags`, `plugins`, `traversal_plugins`) and dispatches every
  other top-level table to the registry. After this, a plugin edit invalidates
  `plugins/all` and the three `cmd/` mains only. RFC 0007 § Package Layering
  and § Loading and Validation need a revision; RFC 0009 §2 gains one SDK
  symbol; the `sdklayering` guard is unchanged (it can even be tightened).
- **Top tree restructurings (Part B):** (1) the Part A inversion; (2) split
  `command_components` into a config/roots layer and a presentation layer
  (`tag_view`, `espalier_view`, `enriched_listing` — the churny half) so
  organize/list UI work stops re-deriving capture/restore/diff/serve;
  (3) move the test-only `plugins/*` blank-imports out of framework package
  tests (`internal/health`, `internal/capture`, `internal/diff`,
  `internal/restore`, `internal/command_components`) into an external-test or
  bats lane so the test derivations of those packages stop depending on plugin
  sources.
- **Surprises:** `internal/config_common` has zero importers (it exists only as
  the dagnabit copy-mode source for `pkgs/config_common`); every plugin
  `[caldav]`/`[fastmail]`/`[jira]` section is decoded and validated even by an
  SDK-built binary that does not link that plugin; and `pkgs/` facade headers
  are restamped on every dagnabit/tommy bump (23 of the last 100 master commits
  touch `pkgs/`), which is cheap under godyn only because content-addressed
  archives early-cut — but it still re-derives every plugin's compile.

## 1. Part A — the `cgconfig → plugins` edge

### 1.1 Current dependency picture

Edges below are production imports only (`_test.go` files excluded), derived
from every `"code.linenisgreat.com/cutting-garden/…"` import line in the tree.

**`internal/` and `cmd/` packages that import `plugins/*`:**

| Importer | Imports | Where |
|---|---|---|
| `internal/cgconfig` | `plugins/caldav`, `plugins/fastmail`, `plugins/jira` | `config.go:16-18`, `inject.go:4-6`, `config_tommy.go:10-12` |
| `cmd/cutting-garden`, `cmd/cg`, `cmd/cutting-garden-gen` | `plugins/all` (blank) | `main.go:14`, `main.go:17`, `main.go:36` |
| `cmd/cutting-garden-caldav-testserver` | `plugins/caldav/caldavtestserver` | `main.go:29` |
| `cmd/cutting-garden-test-git-sshd` | `plugins/git/gittestssh` | `main.go:23` |

Test-only (still matter under godyn `tests = true`, `flake.nix:603`, because
each tested package's test variant depends on its test imports):

| Tested package | Blank-imports |
|---|---|
| `internal/health` (`health_test.go:15-20`) | caldav, file, git, googlephotos, optical, ytdlp |
| `internal/capture` (`plan_test.go:9`), `internal/diff` (`main_test.go:14`), `internal/restore` (`restore_test.go:14`), `internal/command_components` (`receipt_test.go:16`) | file |

**Which `pkgs/` facades each plugin consumes:**

| Plugin | `pkgs/` imports |
|---|---|
| caldav | capture_events, capture_failures, capture_plugin, capture_receipt, config_common, cutting_garden_plugins, plugin_blob_io |
| fastmail | config_common, cutting_garden_plugins |
| jira | capture_events, capture_failures, capture_plugin, capture_receipt, config_common, cutting_garden_plugins, plugin_blob_io |
| file, optical, ytdlp | capture_events, capture_failures, capture_receipt, cutting_garden_plugins, plugin_blob_io |
| git | capture_events, capture_plugin, capture_receipt, cutting_garden_plugins |
| googlephotos | capture_failures, capture_receipt, cutting_garden_plugins, plugin_blob_io |

No plugin imports `internal/` (enforced by
`internal/sdklayering/layering_test.go:76-88`), and no `internal/` package
imports `pkgs/` (`layering_test.go:58-68`).

**Framework spine (importers → imported, production only):**

```mermaid
graph TD
  fastmail[plugins/fastmail] --> cgconfig[internal/cgconfig]
  caldav[plugins/caldav] --> cgconfig
  jira[plugins/jira] --> cgconfig
  cgconfig --> cc[internal/command_components]
  cgconfig --> mcp[internal/mcp]
  cgconfig --> organize[internal/organize]
  cc --> capture[internal/capture]
  cc --> diff[internal/diff]
  cc --> restore[internal/restore]
  cc --> serve[internal/serve]
  cc --> failures[internal/failures]
  cc --> list[internal/list]
  cc --> mcp
  cc --> organize
  cc --> blob_writer[internal/blob_writer]
  capture --> cgapp[internal/cgapp]
  diff --> cgapp
  restore --> cgapp
  serve --> cgapp
  failures --> cgapp
  list --> cgapp
  mcp --> cgapp
  organize --> cgapp
  blob_writer --> cgapp
  cgapp --> cmds["cmd/cutting-garden, cmd/cg, cmd/cutting-garden-gen"]
  all[plugins/all] --> cmds
  fastmail --> all
```

(Arrows point from dependency to dependent, i.e. the direction an
invalidation propagates.)

### 1.2 Invalidation cone for one plugin edit today

An edit to `plugins/fastmail` (a non-test `.go` file) invalidates, in order:

1. `plugins/fastmail` — compile, vet, lint ×4 lanes (`flake.nix:1052-1057`),
   test bin + run.
2. `internal/cgconfig` — same set.
3. `internal/command_components`, `internal/mcp`, `internal/organize`.
4. `internal/capture`, `internal/diff`, `internal/restore`, `internal/serve`,
   `internal/failures`, `internal/list`, `internal/blob_writer`.
5. `internal/cgapp`.
6. `plugins/all`; the three mains; the install derivation (`postInstall`
   runs `cutting-garden-gen`, godyn(7) § buildGodynModule *postInstall*).
7. Every `checkAll`/`vetAll`/`lintAll` manifest, plus the godyn "recompiled
   against test variant" nodes for tests that import a package above
   (godyn(7) § TESTS).

That is 14 framework packages × (compile + 5 analysis lanes + 2 test
derivations) ≈ 110 package-level derivations before links, manifests and
recompiled test variants, which is consistent with the ~250 observed.

**Not in the cone** (these do not import `cgconfig` transitively):
`internal/command` (13 importers), `internal/cutting_garden_plugins` (11),
`internal/capture_receipt` (13), `internal/trellis`, `internal/trellis_eval`,
`internal/traversal_serve`, `internal/capture_wire`, every `capture_*` leaf,
`internal/health`, `internal/hook`, `internal/version`. The cascade is
entirely a `cgconfig` phenomenon, which is why the inversion alone buys most of
the win.

### 1.3 Why `ConfigV0` names the plugin types today

RFC 0007 (`docs/rfcs/0007-config-subsystem.md`):

- § Top-Level Structure (l.229-251): `ConfigV0` "aggregating per-plugin
  sections as **delegated** fields owned by the plugin packages", one typed
  field per plugin.
- § Package Layering (l.387-403): "`cgconfig` imports the plugin packages to
  embed their sections as delegated fields; this is the only direction." The
  composition root "loads the config once, and injects each plugin's section
  into that plugin" (realized by `cgconfig.Inject`, `inject.go:19-23`, called
  from `command_components.LoadAndInjectConfig`, `roots.go:44`).
- § Loading and Validation (l.376-385): the generated `DecodeConfigV0` invokes
  each section's `Validate`; unknown keys are reported via `Undecoded()`.

tommy's mechanism (`~/eng/repos/tommy/README.md:159-183`, "Cross-Package
Structs"): a field whose type is defined in another package is decoded by
**delegation** — the generated code calls that package's
`Decode<X>Into(data *X, sub *cst.Value) error` / `Encode<X>From(...)`, so the
generated file must import the *defining* package. That is what
`config_tommy.go:44-61` and `:268-286` do. tommy has no notion of "decode a
table into a type looked up by name at runtime" and no open/catch-all section
kind (the README's supported-type table, l.144-157, is closed; embedded
cross-package structs promote fields rather than delegate —
`tommy/docs/plans/2026-03-25-cross-package-delegation.md:765`).

The same resolution rule is why `config_common` is exported via dagnabit
**copy mode** (`pkgs/config_common/export.go:5-15`, dagnabit(1) `--copy`):
caldav's generated codec for `[]config_common.Account` must import the
package that *defines* `Account`, and an alias facade would resolve to
`internal/config_common`, tripping the no-inversion guard (RFC 0009 §4/§5).
Corollary from the edge list: `internal/config_common` now has **zero**
production importers — it is a codegen input only.

So today the plugin import is structural to the design, not incidental: a
typed `ConfigV0` field of type `fastmail.AccountsConfig` *is* an import of
`plugins/fastmail`.

There is also a second, smaller coupling worth naming: `cgconfig.Inject`
(`inject.go:19-23`) is the "single place that maps config sections to
plugins", so adding an account-bearing plugin is a one-line edit — but that
one line sits in the most-imported config package.

### 1.4 Precedent already in the tree: name-keyed raw sections

RFC 0013's `[[traversal_plugins]]` / `[[plugins]]` stanzas
(`internal/traversal_serve/config.go:57-72`) name a `config_section`; the
section is *not* decoded by `ConfigV0` — it is extracted from the raw bytes,
wrapper-stripped, by `traversal_serve.SectionTOML` (`section.go:24-83`) and
handed to the wire plugin at `initialize`. `command_components.loadConfigWithRaw`
(`config.go:51-79`) keeps the raw bytes for exactly this, and
`withoutStanzaClaimedKeys` (`config.go:84-106`) suppresses the unknown-key
warning for claimed sections. `ConfigV0`'s own doc comment says so
(`cgconfig/config.go:29`: "the sections THOSE name are consumed raw by
SectionTOML, not decoded here").

So the repo already has *two* section-ownership models: static typed
delegation for linked plugins, dynamic name-keyed raw text for wire plugins.
Part A's proposals collapse them toward the second.

### 1.5 Proposals

#### Proposal A1 — SDK config-section registry, `*cst.Value` decoder (recommended)

Add to `internal/cutting_garden_plugins` (and therefore to the
`pkgs/cutting_garden_plugins` facade):

```go
// ConfigSectionDecoder decodes one top-level config table (RFC 0007
// § Plugin-Owned Sections). sub is the table's tommy CST value; the
// decoder MUST mark what it consumes (the generated Decode<X>Into does)
// and MUST validate. It runs once per process, from the composition
// step, after every plugin init() has registered.
type ConfigSectionDecoder func(sub *cst.Value) error

func MustRegisterConfigSection(name string, d ConfigSectionDecoder) // init()-time; panics on dup
func RegisteredConfigSections() []string                              // for unknown-key filtering / describe
func decodeConfigSections(model *cst.Value) error                    // called by cgconfig
```

Plugin side (fastmail example, `plugins/fastmail/config.go`):

```go
func init() {
    cg.MustRegisterConfigSection("fastmail", func(sub *cst.Value) error {
        var c AccountsConfig
        if err := DecodeAccountsConfigInto(&c, sub); err != nil { return err }
        SetConfiguredAccounts(c.Accounts)
        return nil
    })
}
```

`DecodeAccountsConfigInto` already exists in every plugin's `config_tommy.go`
(fastmail `:110`) — it is the function cgconfig's generated code calls today
(`config_tommy.go:52`), and it calls `Validate` (tommy README l.185-189). The
registry therefore replaces *static* delegation with *dynamic* delegation
over the identical generated entrypoint.

Framework side:

- `internal/cgconfig/config.go`: drop the three plugin fields and their
  `Validate` lines; keep `Organize`, `Tags`, `Plugins`, `TraversalPlugins`.
  `ConfigV0` no longer imports `plugins/*`; `inject.go` is deleted.
- `internal/command_components/config.go::loadConfigWithRaw`: after
  `DecodeConfigV0(raw)`, obtain the CST model (tommy's `cst.Decompose` of the
  parsed document — `config_tommy.go:30-34` shows the two calls) and for each
  registered section name with a `VTable` value, call its decoder; wrap the
  error as `errors.BadRequestf("%s: %s: %s", path, name, err)` to preserve the
  RFC 0007 § Loading and Validation EX_USAGE-naming-the-entry guarantee.
  `Undecoded()` then works unchanged because the delegated decoders mark
  consumption on the *same* model — no claimed-keys hack needed for linked
  plugins (the stanza hack stays for wire plugins).
- Ordering: `LoadAndInjectConfig` runs at command time, after all `init()`s,
  so the registry is complete before dispatch; dispatch must happen before
  `registerPlugins` (wire) and before `AggregateRoots`, exactly where
  `cgconfig.Inject` sits today (`roots.go:44`).
- A section present in the file with no registered decoder falls out as an
  unknown key (warning, not error) — today a `[caldav]` table is decoded and
  validated even in a binary that never linked caldav, which is a latent
  divergence from RFC 0009's "inherits only the plugins it explicitly links".

Resulting cone for a `plugins/fastmail` edit: `plugins/fastmail`,
`plugins/all`, the three `cmd/` mains' links, the install derivation. Zero
`internal/` packages.

Effect on wire plugins: none functionally; the two paths converge on "a
top-level table owned by a name". A follow-up could route wire plugins through
the same registry (their decoder being "serialize this `*cst.Value` back to
wrapper-stripped TOML"), retiring the line-based `SectionTOML` header scanner
(`section.go:104-146`) — out of scope here.

Codegen: `just codemod-generate` regenerates `cgconfig/config_tommy.go` without
the plugin imports; `checks.tommy-codegen` (`flake.nix:1062`) still gates
drift. The plugins' own `//go:generate tommy generate` is untouched.

Risks / costs:

- `pkgs/cutting_garden_plugins` gains a dependency on
  `code.linenisgreat.com/tommy/pkg/cst` (a bridged dep, `go.nix:42`). Plugins
  already import tommy via their generated codecs, so the plugin-side closure
  does not grow; the SDK's public surface does name a third-party type.
  Variant A1' (below) avoids that at the cost of Undecoded fidelity.
- `ConfigV0Document.Encode` loses the plugin sections. Its only consumer is
  `cgconfig/config_test.go:37`; there is no config-writing command. If one is
  ever wanted, the registry entry can carry an optional encoder.
- `command_components/config_test.go` (and any test seeding
  `[[caldav.accounts]]` through `LoadConfig`) must either register a fake
  section in-test or move to the caldav plugin's own tests; today it works
  only because cgconfig links caldav.
- Init-order hazard: none new — registration is `init()`-time like
  `MustRegisterScheme` (`scheme_registry.go:56-60`); dispatch is
  command-time.
- The `describe`/manpage surfaces do not generate config schema from
  `ConfigV0` — `internal/list/manpage.go` and `internal/mcp/manpage.go`
  mention `config.toml` in hand-written prose, and tommy's schema export
  (tommy RFC 0001) is not wired in this repo — so nothing there regresses.
  A future schema tool would need `RegisteredConfigSections()` plus a
  per-plugin schema hook; that is the same shape the registry already has.

#### Proposal A1' — same registry, decoder takes wrapper-stripped TOML text

Identical shape, but the decoder signature is `func(sectionTOML []byte) error`
and the plugin calls its generated top-level `DecodeAccountsConfig(input
[]byte)` (fastmail `config_tommy.go:26`). The framework feeds it
`traversal_serve.SectionTOML(raw, name)` — the exact RFC 0013 § initialize
contract, so linked and wire plugins receive byte-identical section text.
Pro: no tommy type in the SDK signature. Con: unknown-key tracking for the
section is lost to the parent document (the plugin's own document tracks it,
so the plugin would have to surface `Undecoded()` itself), the section is
parsed twice, and it inherits `SectionTOML`'s restrictions (a top-level
`[[name]]` array is rejected, `section.go:56-65`). Acceptable fallback.

#### Proposal A2 — keep typed `ConfigV0`, move it out of `internal/`

Move the typed aggregator to a non-`internal/` leaf (`plugins/config` or
`plugins/all`) so only the composition root depends on plugins. Problems:
`internal/mcp` and `internal/organize` need framework sections (`Tags`,
`Organize`) from the loaded config, and `internal/` may import `plugins/…`
(no guard forbids it) but doing so re-creates the cone. The split would have
to be: `cgconfig` keeps framework sections; a second struct in `plugins/config`
holds plugin sections; `command_components` decodes framework sections and the
composition root decodes plugin sections from the same bytes — two decoders
over one document with merged `Undecoded` sets, and `Inject` living in
`plugins/config`. tommy cannot embed one into the other without changing the
TOML layout (embedded cross-package structs promote rather than delegate).
Cone after: `plugins/fastmail` → `plugins/config` → `plugins/all` → mains.
Same win as A1 but with a bespoke two-decoder loader and a second place that
enumerates plugins. Not recommended.

#### Proposal A3 — tommy feature: open sections

Teach tommy a catch-all field (`Sections map[string]tommy.RawTable
`toml:"*"`` or similar) so `ConfigV0`'s generated decoder captures every
unclaimed top-level table as a `*cst.Value` and cgconfig dispatches by name.
This is A1 with the model-walking moved into tommy. It would make
`Undecoded()` and the dispatch loop generated rather than hand-written, and
give wire plugins a typed handle too. Cost: an upstream tommy feature and a
release cycle before cutting-garden can use it; tommy's decode-normalization
ADR (`tommy/docs/decisions/2026-06-07-decode-normalization.md`) enumerates a
kinds × spellings × position matrix that a wildcard kind would extend.
Reasonable *later* refinement of A1, not a prerequisite.

### 1.6 Recommendation and migration sketch (A1)

1. `internal/cutting_garden_plugins/config_section.go`: registry + dispatch
   (mirroring `scheme_registry.go`). Regenerate the facade
   (`just codemod-generate-dagnabit`).
2. Each of caldav/fastmail/jira: add the `init()` registration next to
   `SetConfiguredAccounts` (`plugins/caldav/config.go:108`,
   `fastmail/config.go:80`, `jira/config.go:109`); unit-test the decoder in the
   plugin's own package (move the `[[<scheme>.accounts]]` fixtures there).
3. `internal/command_components/config.go`: dispatch registered sections
   after `DecodeConfigV0`; delete the `cgconfig.Inject` call in `roots.go:44`.
4. `internal/cgconfig`: drop the plugin fields, `inject.go`, and the three
   `Validate` calls; `just codemod-generate`. Verify `checks.tommy-codegen`
   and `checks.dagnabit-codegen`.
5. Tighten `internal/sdklayering`: add a third test asserting no
   `internal/` package imports `plugins/` in production code (the only
   current offender is cgconfig). Optionally scope it to production imports so
   the test-only blank-imports of §2.4 are handled separately.
6. Docs: RFC 0007 § Top-Level Structure (delegated fields → framework fields
   only), § Package Layering (replace "cgconfig imports the plugin packages"
   with "plugins register a section decoder; cgconfig imports no plugin"),
   § Loading and Validation (dispatch step; unknown-section semantics for an
   unlinked plugin); RFC 0009 §2 (new SDK symbols) and §6 (an account-bearing
   plugin registers its section in `init()`); `AGENTS.md` l.63-72 and the
   `cgconfig` package doc (`config.go:1-10`).

Each step is independently mergeable; steps 1-3 can land with cgconfig still
importing the plugins (dual path), and step 4 is the cut-over.

## 2. Part B — a more godyn/dagnabit-ergonomic tree

### 2.1 How dodder and madder are laid out

**dodder** (`~/eng/repos/dodder`, `go/CLAUDE.md:87-102, 319-328`):
`go/lib/{0,alfa,…,delta}` (domain-agnostic, 62 packages) and
`go/internal/{0,alfa,…,uniform}` (domain), both NATO-tiered so a package may
only import lower tiers; the composition root is at the top tier
(`internal/uniform/commands_dodder`, `go/cmd/dodder/main.go:6,18`). No `pkgs/`
directory; dodder consumes madder and dewey through *their* `pkgs/` facades.
Codegen (`*_tommy.go`) is regenerated in tier order (`go/CLAUDE.md:28-31`).
Builds are still `buildGoApplication` (igloo FDR 0007 § Problem Statement).

**madder** (`~/eng/repos/madder`, `AGENTS.md`, `README.md:216-225`):
`go/internal/{0,alfa,…,juliett}` NATO tiers plus `internal/futility` (the
command framework), `go/pkgs/*` as dagnabit alias facades
(`go/pkgs/blob_store_env/main.go:1-5`: `type BlobStoreEnv =
internal.BlobStoreEnv`), and the composition root at
`internal/india/commands` (`go/cmd/madder/main.go:7,21`). `pkgs/` is the
documented external-consumer substrate; breaking changes are coordinated with
cutting-garden. dagnabit's default subcommand is the tool that *computes* the
NATO tier from dependency depth and moves packages (dagnabit(1) § reposition),
so the tiering is mechanically maintained rather than hand-kept.

Both siblings therefore encode the layering *in the path* (tier = dependency
depth), keep the composition root at the top tier, and expose a facade tree.
Neither has an in-tree plugin layer that must consume the facade, which is the
extra constraint cutting-garden carries (RFC 0009 §4).

### 2.2 cutting-garden today: fan-in, fan-out, churn

Fan-in (production importers within the module):

| Package | Importers | Notes |
|---|---|---|
| `internal/capture_receipt` | 13 | leaf; stable |
| `internal/command` | 13 | leaf; stable (2 touches / last 100) |
| `internal/cutting_garden_plugins` | 11 | SDK core; 11 touches / last 100 |
| `internal/command_components` | 9 | the hub; imports cgconfig, capture_wire, traversal_serve, trellis, cutting_garden_plugins, capture_receipt |
| `pkgs/cutting_garden_plugins` | 8 (all plugins) | facade of the above |
| `pkgs/capture_receipt` | 7 | |
| `internal/capture_plugin`, `internal/buildinfo` | 6 | |
| `internal/trellis` | 5 | 13 touches / last 100 |
| `internal/cgconfig` | 3 (command_components, mcp, organize) | the cascade entry; 10 touches / last 100 |

Fan-out (imports of other module packages): `internal/capture` 13,
`internal/cgapp` 13, `internal/mcp` 9, `internal/command_components` 6,
`internal/organize` 6, `plugins/caldav` and `plugins/jira` 7 facades each.

Churn, counted as commits among the last 100 on `master` that touch the path
(`git log master --oneline -100 -- <path>`, intersected with the top-100 set):

| Path | Commits |
|---|---|
| `internal/organize` | 34 |
| `plugins/caldav` | 25 |
| `pkgs/` | 23 (mostly dagnabit/tommy stamp restamps on flake bumps) |
| `internal/trellis` + `trellis_eval` | 13 |
| `internal/cutting_garden_plugins` | 11 |
| `internal/cgconfig` | 10 |
| `internal/command_components` | 6 |
| `internal/mcp` + `internal/list` | 6 |
| `internal/command` + `internal/cgapp` | 2 |

Diffstat over the same window (`git diff --stat master~100..master`): the
largest churn is `internal/organize/*` (~6.5k lines), `plugins/caldav/*`,
`zz-tests_bats/organize_*`, and the tree-sitter grammar; framework spine files
(`command_components/{tag_view,espalier_view,enriched_listing,tag_interpreter}.go`)
account for ~750 added lines — all presentation helpers for organize/list,
all living in the 9-importer hub.

Observations:

- The hot packages (`organize`, `caldav`, `trellis`) are *leaves or near-leaves*
  in the invalidation sense, except that `caldav` cascades through `cgconfig`
  (Part A) and `trellis` cascades through `command_components` (5 importers
  including the hub).
- `command_components` mixes two things with different churn: the config/roots
  composition layer (`config.go`, `roots.go`, `env_blob_store.go`,
  `store_resolve.go`, `receipt.go` — stable) and organize/list presentation
  (`tag_view.go`, `espalier_view.go`, `enriched_listing.go`,
  `tag_interpreter.go` — 2026-09 churn). Every edit to the latter re-derives
  `capture`, `restore`, `diff`, `serve`, `failures`, `blob_writer`, which never
  use it.
- `pkgs/` restamps: a dagnabit or tommy version bump rewrites the `// Code
  generated by …` header of every facade (`0436793`, `3047a92`, `e2e1d29`).
  godyn's content-addressed archives early-cut (godyn(7) § DESCRIPTION), so
  dependents of an unchanged archive do not rebuild, but every facade and
  every plugin still re-derives compile/vet/lint. Copy-mode facades
  (`pkgs/config_common`, `pkgs/trellis` — `pkgs/trellis/literal.go` is a 403-line
  copy) are full re-compiles, not header-only.

### 2.3 Restructuring candidates

| # | Move | Edits that stop cascading | Cost | Guard / RFC impact |
|---|---|---|---|---|
| B1 | **Part A inversion** (`cgconfig` stops importing `plugins/*`) | any plugin edit no longer touches `internal/` | 1 SDK symbol, 3 plugin `init()`s, cgconfig regen | RFC 0007 §Layering/§Loading, RFC 0009 §2/§6; guard can tighten |
| B2 | **Split `command_components`**: keep `config.go`/`roots.go`/`store_resolve.go`/`receipt.go`/`env_blob_store.go` as the composition layer; move `tag_view.go`, `espalier_view.go`, `enriched_listing.go`, `tag_interpreter.go` to a new `internal/node_view` (or `listing_view`) imported only by `list`, `mcp`, `organize` | organize/list presentation churn (the busiest 2026-09 stream) stops re-deriving capture/restore/diff/serve/failures/blob_writer/cgapp — 6 packages × all lanes | mechanical move (dagnabit `move` handles qualified-reference rewrites, dagnabit(1) § move); no RFC text names these helpers | none |
| B3 | **Test-only plugin imports out of framework tests**: `internal/health/health_test.go:15-20`, `capture/plan_test.go:9`, `diff/main_test.go:14`, `restore/restore_test.go:14`, `command_components/receipt_test.go:16` blank-import real plugins. Replace with an in-package fake plugin registered in the test (the `traversal_registration_test.go` pattern) or move those cases to bats/the plugin's own tests | a `plugins/file` edit stops re-running the test lanes of 5 framework packages (their production archives are already unaffected) | small; `health`'s capability report test genuinely wants real plugins — keep one such test in `zz-tests_bats/` | none; optionally make the guard's third clause cover test imports too |
| B4 | **Framework config out of `cgconfig` into consumers** — `organize` uses only the lever constants (`document.go:81-94`); `mcp` only `cfg.Tags`. After B1, `cgconfig` is small and stable (framework sections + stanzas), so its 3 importers are fine; do *not* split further | — | — | — |
| B5 | **Keep `pkgs/` as the only cross-boundary surface, and prefer alias mode**: the copy-mode facades (`config_common`, `trellis`) turn a facade restamp or an `internal/trellis` edit into full plugin recompiles. `trellis` is copied because plugins need its *types* for tommy? — no: `pkgs/trellis` is consumed by no plugin today (edge list), only external consumers. Consider whether `pkgs/trellis` needs copy mode at all; `config_common` must stay copy mode for the tommy resolution reason (§1.3) | trellis edits stop re-deriving `pkgs/trellis` copies | check external consumers (fj-cg) before changing | RFC 0009 §1 (alias-identity) already prefers alias mode |
| B6 | **SDK read/write split** (`cutting_garden_plugins` → read-only traversal contract vs mutation/bulk/facet-write/tag-interpreter helpers) | `tag_interpreter.go`/`facet_*`/`bulk_*` churn (11 touches) would stop re-deriving read-only consumers (`health`, `traversal_conformance`, `traversal_serve`) | high: every plugin imports the one facade; RFC 0009 §2 lists the exported set and the alias-identity guarantee means a split must keep interface identity; `RegisteredPlugins()` unions registries that would straddle the split | RFC 0009 §2 rewrite; guard unchanged. Defer — the payoff is small because all plugins import both halves anyway |
| B7 | **NATO tiering of `internal/`** via `dagnabit --initial internal` (dagnabit(1) § Reposition options `--initial`) | none directly — tiering makes depth visible in paths (as madder/dodder) and lets `dagnabit rename` police it, but godyn keys on the import graph, not on paths | a tree-wide rename (import-path churn in every file, all facades regenerated, every open worktree conflicts) | RFC/AGENTS path references throughout. Not worth it for cache behavior alone |

### 2.4 Recommended sequence

1. **B1 (Part A, Proposal A1).** Largest win, smallest diff, RFC text change
   is a clarification of an already-existing precedent (§1.4).
2. **B2 (split `command_components`).** Second-largest: the current
   organize/list work is the busiest stream and it lands in the hub. Do it as
   one `dagnabit move` per file group; no RFC touches.
3. **B3 (test-only plugin imports).** Cheap; complements B1 so that the
   *test* lanes also stop crossing the plugin boundary.
4. **Guard tightening**: a `sdklayering` test that no `internal/` production
   package imports `plugins/`, and (after B3) none of their tests do either
   except an explicitly listed set.
5. **B5 audit** (copy-mode facades) opportunistically; **B6/B7** not now.

## 3. Facts checked while researching (for future readers)

- godyn is the default only on x86_64-linux in this repo
  (`flake.nix:405-410`, `godynSystem`); tests are derived (`tests = true`,
  `:603`) with `testFiles."internal/trellis"` (`:607`); five analysis lanes
  (`:1052-1057`); codegen drift lanes for tommy and dagnabit (`:1062-1074`).
- godyn(7): one derivation per package; dependents keyed on imports' archives;
  test variant recompiles in-graph importers of the tested package; comment-only
  edits early-cut through content addressing; vet/lint facts chain along the
  same edges (§ VET, § LINT).
- tommy's generated delegation entrypoint per struct is
  `Decode<X>Into(*X, *cst.Value) error` and `Encode<X>From(*X,
  *document.Document, *cst.Node) error` (`plugins/fastmail/config_tommy.go:110,132`),
  called by cgconfig's generated codec (`config_tommy.go:46-58`).
- `ConfigV0Document.Encode` has no non-test consumer.
- No config schema or manpage section is generated from `ConfigV0`.
- `internal/config_common` has no production importers; `pkgs/config_common`
  (copy mode) has three (caldav, fastmail, jira).
- `internal/sdklayering` runs only under `just test-go` (it needs `go list`;
  `layering_test.go:25-29`), not in the godyn per-package test lane.

## Addendum (2026-09-22): what actually shipped

B1–B3 shipped as `docs/plans/2026-09-21-invalidation-cone-moves.md` Units A–C.
The research above is left as written; this section corrects where
implementation diverged from it.

**§1.5's plugin-side sketch and §1.6's "the plugins' own `//go:generate tommy
generate` is untouched" are WRONG.** tommy's codegen bootstrap blanks every
`*_tommy.go` in a directive's directory while type-checking it (tommy#93), so
hand-written code in a package can never call a `Decode<X>Into` generated in
that same package. The sketch — each plugin's decoder calling its own
`DecodeAccountsConfigInto` — therefore does not compile.

What shipped instead: the three per-plugin tommy codegen sites were DELETED
and replaced by ONE shared schema, `internal/config_common.AccountsSection`
(`{Accounts []Account}`), copy-mode-facaded at `pkgs/config_common`. Each
plugin's registered decoder is now
`config_common.DecodeAccountsSectionInto` → `AccountsConfig{Accounts}.Validate()`
→ `SetConfiguredAccounts`. Consequence worth knowing before the next
account-bearing plugin: a plugin needing section fields BEYOND `{accounts}`
cannot reuse `AccountsSection` and must put its own tommy schema in a leaf
package it imports.

Everything else in §1.5/§1.6 landed as described: the `*cst.Value` decoder
signature (Proposal A1, not A1'), `MustRegisterConfigSection` panicking on a
duplicate/empty name/nil decoder, `RegisteredConfigSections()`, dispatch in
`command_components.loadConfigWithRaw` right after `DecodeConfigV0Into` over the
same decomposed model, `cgconfig.Inject` and `inject.go` deleted, and the
unknown-table-is-a-warning semantics of §1.5's last bullet. The dispatch
entry point is EXPORTED as `DecodeRegisteredConfigSections` (§1.5 sketched it
unexported as `decodeConfigSections` "called by cgconfig") because the loader
lives in `command_components`, outside the SDK package.

Two smaller deltas: plugin `Validate` errors lost tommy's `validation failed:`
infix, since the decoder now calls `Validate` itself rather than the generated
codec doing it; and `LoadConfig`/`LoadDefaultConfig` now INJECT as a side
effect of decoding, so a command must load once and thread the value
(`internal/list` and `internal/organize` were fixed to do so).

B2 landed as `internal/node_view` (§2.3's parenthetical alternative
`listing_view` was not used), also taking `tag_interpreter.go`. B3's guard is
two `sdklayering` clauses, not one: `TestInternalDoesNotImportPlugins`
(production imports) and `TestInternalTestsDoNotImportPlugins` (test imports,
with an allow-list that is empty). B3 also needed more than §2.3's
"replace with an in-package fake" implies: `internal/capture`'s end-to-end
`Run` tests need a plugin that really stores blobs and reports a blob-write
failure, so its fake is a minimal tree walker rather than a stub, and
`internal/health`'s real-capabilities assertion moved to
`zz-tests_bats/health.bats` as planned.

**Measurement (2026-09-22).** §0's "~14 framework packages / ~250 derivations /
92-minute gate" is the BEFORE figure. The first fastmail-only merge after
Units A–C (fastmail slice Tasks 3+4, `plugins/fastmail` only) took two gate
runs, so the AFTER figure comes from both logs:

- **Invalidation set: confirmed confined.** The first run
  (`merge-43210475`) logged `godyn-compile-…` builds for exactly
  `plugins-fastmail`, `plugins-all`, `cmd-cg`, `cmd-cutting-garden` and
  `cmd-cutting-garden-gen` — no `internal-*` compile derivations. Its
  vet/lint/test builds covered the same five packages plus
  `plugins-fastmail-fastmailtestserver`: about 45 godyn-lane derivations
  logged, against ~250 before.
- **Wall time: not cleanly measured.** That first run's producer died inside
  the `cutting-garden-godyn-tests` step (ringmaster: liveness `gone`, no
  further spool output, then `interrupted: producer died before honoring
  cancel`) after ~34 minutes of logged progress. The re-run
  (`merge-024cdcaf`) passed in 55m17s but found every godyn derivation
  already built — its only logged build was the `bats-all` lane — so its
  time is almost entirely flake evaluation plus bats, not a cold
  fastmail-only gate. Neither log carries per-step timestamps, so the split
  between evaluation and building is unknown. The re-run's 55 minutes with
  near-zero Go building suggests flake evaluation, not the invalidation cone,
  is now the dominant gate cost; that is an inference from one run, not a
  measurement.
