# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

A filesystem-tree capture/restore CLI atop
[madder](https://code.linenisgreat.com/madder), grown from a port of
dodder's command-dispatch framework. Eleven user-facing subcommands —
`capture`, `restore`, `diff`, `serve`, `failures`, `health`, `list`,
`mcp`, `organize`, `fmt-organize` (regenerate an organize document in
place from its envelope; refuses on unapplied edits — native tags design
G4 v1, edit-preserving v2 is #252), `version` — plus three hidden ones
(`complete` for shell completion,
`__write-blob`, the RFC 0002 writer-protocol sink a config-declared
capture plugin's v1 FALLBACK pipes node blobs into: `internal/capture_wire`
always attempts the RFC 0008 persistent JSON-RPC/SCM_RIGHTS transport
first (`<binary> capture-serve` via `internal/capture_serve`, exported at
`pkgs/capture_serve` for the plugin side; `capture_serve.IsFallbackSignal`
gates the drop to v1's `capture-batch` + `__write-blob`) — and `hook`,
the clown-plugin PreToolUse sink — inert until the MCP server grows
write tools, cutting-garden#102) are registered in
`internal/cgapp.Build()`, the single factory
shared by the `cutting-garden` binary, its `cg` alias, and the
manpage/completion generator `cutting-garden-gen`. Hidden subcommands
implement `command.CommandHidden` so they stay dispatchable but are
filtered out of usage, manpages, and completion. Capture/restore/diff
backends are URI-scheme-keyed plugins (file, git, yt-dlp, caldav,
optical, gphotos, jira; plus fastmail, a traversal-only plugin — JMAP mail
traversal + facets, and writable thread tag sets for `organize` inbox
triage, but no capture/restore/diff, FDR 0024),
each living in `plugins/<scheme>/` and consuming the
public plugin SDK (`pkgs/`, RFC 0009) exactly as an out-of-tree plugin
would — none import `internal/` (the no-inversion guard,
`internal/sdklayering`, enforces this). The in-repo binaries opt into the
standard set via `plugins/all`, which their `cmd/` mains blank-import;
`cgapp.Build()` is plugin-bare. `web` (the `web:<http(s)-url>` scheme,
via chrest) is NOT one of these in-tree plugins any more — it retired
(cutting-garden#146) in favor of a **config-declared capture plugin**:
a `[[plugins]]` stanza (RFC 0013 §Host integration, generalized) naming
`chrest` as the plugin binary and `protocols = ["capture"]`, resolved
through `internal/capture_wire` (RFC 0005's protocol-only resolution
path) rather than compiled-in plugin code. `serve` (`internal/serve/`) is a
long-lived LocalSend receiver bound to the host's Tailscale address:
each incoming transfer lands as a normal fs-v1 capture receipt
(FDR 0011). The original extraction design
lives in `amarbel-llc/madder` →
`docs/plans/2026-05-10-extract-cutting-garden-design.md`; newer design
docs live in this repo under `docs/{rfcs,features,plans}/`.

`list` and `mcp` are the read-only consumers of the plugin **traversal**
primitive (`RootLister`, FDR 0014): `list` prints a node's child nodes,
`mcp` serves them over the Model Context Protocol (FDR 0015). A plugin MAY
also declare **facets** (RFC 0012, FDR 0021) — grouped-count summaries the
framework computes over a node type's children, surfaced by `list --facets`,
the `mcp` container read's `facets` block, the `mcp` `read_facets` tool (the
tools-only-client path resources/read cannot reach, cutting-garden#151), and
`describe_node_types`; caldav is the reference (`FacetDescriber` + a one-shot
`FacetCounter`). Both, with
no URI, aggregate every plugin's **roots** (the `RootProvider` capability)
from the **config subsystem** (RFC 0007): a tommy-codegen'd
`$XDG_CONFIG_HOME/cutting-garden/config.toml` of per-plugin named accounts
(caldav) plus intrinsic roots (the file plugin's working directory). The
config types' source lives in `internal/config_common` (shared
`Root`/`Account` base), exposed publicly at `pkgs/config_common` via
dagnabit **copy mode** (`export -copy`, a real source copy, not an alias)
so the relocated `plugins/caldav` consumes a non-`internal/` definition —
tommy resolves a config field's type to its *defining* package, so an
alias facade would make caldav's generated codec import `internal/`
(RFC 0009 §5). The framework aggregator is `internal/cgconfig`
(`ConfigV0`), which holds ONLY the framework sections (`organize`, `tags`,
`plugins`, `traversal_plugins`) and imports **no plugin**: each
account-bearing plugin claims its own top-level table by name at `init()`
through the SDK's config-section registry
(`cutting_garden_plugins.MustRegisterConfigSection`), and
`command_components.LoadConfig` decomposes the file once, runs
`DecodeConfigV0Into` (the model-taking entry point — plain
`DecodeConfigV0` takes bytes), then `DecodeRegisteredConfigSections` over
the same model. Consequences: a table no registered decoder claims is an ordinary
unknown-key **warning**, not an error (a `[caldav]` table in a binary that
never linked caldav is inert); a failing section decoder is EX_USAGE naming
file, section and entry; and **decoding a section injects it**, so a command
must load the config once and thread the value rather than re-calling
`LoadConfig`. Every plugin's section decoder shares ONE tommy schema,
`config_common.AccountsSection` (`{Accounts []Account}`) — tommy blanks a
package's own generated output while type-checking it, so a plugin cannot
call a `Decode…Into` generated in its own package; a plugin needing extra
section fields must put its schema in an imported leaf. `internal/sdklayering`
pins that no `internal/` package imports `plugins/`, in production code or in
tests. `*_tommy.go`
files are generated — run `just codemod-generate` (`go generate -run
tommy` through godyn-go) after editing a `//go:generate tommy generate`
struct; `just`'s `validate-generate` gate (`checks.tommy-codegen`) fails on
drift. tommy is a flake-bridged dep (generator binary + Go library at one
rev; the library arrives inherited through madder's bridge).

Traversal plugins need not be linked Go code: RFC 0013 defines an
**out-of-process wire transport** (JSON-RPC 2.0, one message per NDJSON
line, over an `AF_UNIX` stream socket with the RFC 0008-style
cookie+announce launch). A `[[traversal_plugins]]` config stanza
(name/command/schemes/config_section) registers a lazily-spawned
`traversal_serve.WirePlugin` beside the linked plugins; the named config
section crosses `initialize` wrapper-stripped (`SectionTOML`), and the
plugin's capabilities (roots, leaf-read, facets, mutate) are advertised
in its `initialize` result — consumers cannot distinguish a wire plugin
from a linked one (the RFC's conformance bar, pinned by the
indistinguishability e2e in `internal/traversal_serve_testpeer` and the
`zz-tests_bats/traversal_serve.bats` lane, whose `portable`-tagged cases
any non-Go implementation runs via `CG_TEST_TRAVERSAL_SERVE`
substitution). Go peers implement the plugin side via
`pkgs/traversal_serve`; the first external consumer is forgejo-cli's
`fj-cg` (Rust, cutting-garden#140).

`organize` (RFC 0015, FDR 0023) speaks ONE grammar with trellis (native
tags design, `docs/plans/2026-08-30-native-tags-design.md`): `--group-by`,
the `_group-by` envelope field, and the dimension heading share the
spelling `(tags)` (whole tag set, buckets `# <tag>`), `<ns>` (tag
namespace rollup), `dim=` (field), `dim=(granularity)` (date field at
granularity) — a bare term is always a tag, a field is only ever addressed
with an operator, and a `(…)` parenthetical is a meta qualifier (reserved
in query position). `internal/trellis/literal.go` (`ParseLiteral` /
`WriteLiteral`) owns organize's box interiors and heading terms; heading
depth is normalized (shallowest level = root) and an empty heading resets
context. Since native tags slice 2, an object's tag set renders as
key-free bare/quoted atoms in its box (SortKey-ordered), governed by the
`_tag-atoms = leading|trailing|none` and `_tag-strip = placement|none`
envelope levers (`[organize]` config defaults; a tag-grouped document
strips each appearance's placement Via tag by default), and box tag
edits apply as MEMBERSHIP writes through the tag interpreter's exact
`Complete` (RFC 0019 §6.2); `list -format json` and the mcp enriched
listing carry a top-level `tags` array, and `describe_node_types`
reports each tag-declaring type's `tag_set`. A box's object id is the node's
host+path relative to the anchor (`node_view.RelativeID`) unless the plugin
implements the optional `NodeIDer` capability (fastmail: thread ids, which
ride in the URI query); organize and `list -format espalier` resolve every id
through `node_view.RelativeIDFor`, and a `NodeIDer` plugin whose ids collide
under one anchor is refused at generate. The tag/espalier/enriched-listing
presentation helpers live in `internal/node_view`, imported only by `list`,
`mcp` and `organize` — `command_components` stays the composition layer
(config, roots, store resolution, receipts) and MUST NOT import `node_view`,
so organize/list rendering churn no longer re-derives
capture/restore/diff/serve/failures/blob_writer. `list -format espalier`
(slice 4, G8) renders one organize object line per node through the
shared writer + the `node_view` espalier-view helpers, and
`list -format text` is a dewey mesa table (TAB-separated on a pipe)
with a TAGS column when the plugin declares a tag dimension. The organize bats lanes are
whole-document vectors
(`assert_output - <<-EOM`) against the caldav testserver on a pinned port
per lane (`CG_TEST_CALDAV_PORT`; the port table lives in
`zz-tests_bats/lib/caldav.bash`), indexed G# → test in
`docs/plans/2026-08-30-native-tags-vectors.md`. The nvim tree-sitter
grammar (`zz-nvim/grammars/organize`) is the dialect's conformance
corpus: `just test-grammar-corpus` (= `checks.grammar-corpus`) runs it,
`just codemod-generate-tree-sitter` regenerates the committed parser, and
the devShell carries the flake-pinned `tree-sitter` + `nodejs` toolchain.

Comments and TODOs frequently reference upstream dodder issues (#161, #183,
…) and madder issues — check those before "fixing" what looks like a bug; some
divergences from dodder are intentional carry-forwards.

## Build & test

- `nix build` — produces `result/bin/cutting-garden`. Every Go build goes
  through igloo's `buildGoAuto` (the `buildCuttingGardenGo` helper in
  `flake.nix`): **godyn** (per-package, package graph derived at eval time —
  nothing committed, igloo FDR 0007/0008) on x86_64-linux, and
  `buildGoApplication` elsewhere. Both backends stay reachable as
  `passthru.native` / `passthru.bga`; the main binary's bga build is also
  `.#cutting-garden-build_go_application`. godyn's per-package `go test`
  lane is `checks.cutting-garden-godyn-tests` (`just test-go-godyn`; a skip
  stub off x86_64-linux), gated by `nix flake check`; vet, godyn-lint and the
  three dewey analyzers are `checks.{vet,lint,dewey-*}` (`just lint-go`,
  `just lint-go-analyzers`). See godyn(7).
  **Dependencies live in `go.nix`** (igloo FDR 0008): there is no go.mod,
  go.sum or gomod2nix.toml in the checkout — igloo renders them inside nix.
  `flakeInputs` bridges madder, dewey and go-mcp (purse-first) onto their
  flakes' `go-pkgs` (RFC 0001); hyphence, tap, crap, tommy and piggy arrive
  as bridges inherited through madder's passthru (they still appear as
  versioned `require` entries, which the bridge overrides). `require` holds
  every third-party module with its vendor hash and Go version.
  cutting-garden also **produces** `go-pkgs` / `go-pkgs-test` flake outputs
  (RFC 0009 §2, the out-of-tree-consumer surface; `mkGoPkgs` renders go.nix
  into them): a plugin in its own repo bridges
  `code.linenisgreat.com/cutting-garden` onto `go-pkgs` to import the
  `pkgs/` facades. Regenerate the facades with `just codemod-generate-dagnabit`
  (through godyn-go; the pure drift gate is `checks.dagnabit-codegen`).
- **No ambient go toolchain.** go commands run inside nix through
  `godyn-go -- <go command>` (e.g. `just update-go-get <mod>@<ver>`,
  `just codemod-generate`), which applies the result back to the checkout
  and rewrites go.nix. One package's tests: `just debug-test-pkg
  internal/command TestUtility_Run_DispatchesToRegisteredCmd` (godyn-test; a
  NEW file needs `git add -N` first). gopls and dlv are unsupported.
- `just codemod-fmt` formats the tree via `conformist` (goimports→gofumpt,
  nixfmt, shfmt) + lints (shellcheck) + the eng-convention linters. conformist
  is consumed as a **nix module** (`conformist.lib.evalModule`): the config is
  defined in `conformist.nix` + `conformist.lib.presets.eng` and *generated*
  (no hand-written `conformist.toml`). `just lint-fmt` is the read-only gate
  (wired into `test`); `just build-nix-check` (= `nix flake check`,
  `checks.formatting`) is the sandboxed PURE lane; `just lint-worktree` runs
  the IMPURE git-state eng checks (`presets.eng-impure`: git-remotes,
  agents-md, gomod2nix, …) against the working tree (both wired into `test`).
  `nix fmt` runs the same formatter. See `eng-design_patterns-conformist`(7),
  `conformist-nix`(7).

The flake's `devShells.default` provides `godyn-go` and `godyn-test` — no
`go`, `gopls` or `gomod2nix`.

### When dependencies change

Two cases:

1. **A bridged dep (madder, hyphence, tap, crap, tommy, piggy, dewey, go-mcp)**
   — bump the flake input:
   ```sh
   just update-nix-input madder   # or hyphence, tap, crap, tommy, piggy, purse-first
   ```
   `flake.lock` is the source of truth; go.nix records no version for it.

2. **A third-party dep** — through godyn's escape hatch, which runs `go get`
   / `go mod tidy` inside nix and rewrites go.nix with the new hashes:
   ```sh
   just update-go-get github.com/google/go-cmp@v0.7.0
   ```

Either way, new source files must be `git add`'d (or `git add -N`) before
`nix build`, godyn-go or godyn-test sees them — a dirty-tree flake build
only includes git-tracked files.

## Architecture

Everything user-facing lives behind one type: `command.Utility` in
`internal/command/`. The dispatch loop is small enough to read end-to-end
in `utility.go`:

```
main → cgapp.Build() [MakeUtility + RegisterComplete + AddCmd…] → u.Run(os.Args)
```

`Utility.Run` →
1. Builds a cancelable `errors.Context` (SIGTERM/SIGINT/SIGHUP).
2. `MakeCmdAndFlagSet` looks up `args[1]`, parses flags via
   `dewey/pkgs/flags`. Subcommands implement
   `interfaces.CommandComponentWriter.SetFlagDefinitions` to bind flags.
3. `MakeRequest` wraps parsed positional args into `Request{ input *CommandLineInput }`.
4. `cmd.Run(req)` dispatches to user code.

`Run` deliberately **does not call `os.Exit`** — keeping it side-effect-light
for tests. `cmd/cutting-garden/main.go` does `os.Exit(utility.Run(os.Args))`
to propagate the code. Exit semantics mirror `diff(1)` / `git --exit-code`:

- `0` — success
- `1` — clean mismatch (a `*command.MismatchError` is in the error chain;
  e.g. `diff` found drift)
- `2` — trouble (any other error — the command did not run to completion)
- `64` — EX_USAGE (`errors.Is400BadRequest`)

Commands that want the mismatch / trouble distinction return
`command.Mismatchf(...)` instead of a plain error. Otherwise anything
nonzero from `cmd.Run` becomes `2`.

### Opt-in command interfaces

A `Cmd` is just `Run(Request)`. Everything else is opt-in via narrow
interfaces — implement only what your command needs:

| Interface | File | Surfaces |
|---|---|---|
| `CommandWithDescription` | `cmd.go` | `complete` listing, manpage NAME/DESCRIPTION |
| `CommandWithArgs` | `arg.go` | manpage ARGUMENTS section |
| `CommandWithEnvVars` / `CommandWithFiles` / `CommandWithExamples` / `CommandWithSeeAlso` / `CommandWithManpageFiles` | `manpage.go` | corresponding manpage sections |
| `CommandWithMCPAnnotations` | `arg.go` | future MCP wiring (still inert) |
| `interfaces.CommandComponentWriter` (`SetFlagDefinitions`) | dewey | flag binding during parse and during completion |
| `Completer` (`Complete(Request, any, CommandLineInput)`) | `completion.go` | tab-completion candidates |
| `SupportsCompletion` | `completion.go` | marker only — not yet dispatched on |

`Completer.Complete`'s second arg is `any` for portability — dodder types it
as `env_local.Env`. Cutting-garden commands that need env will type-assert
at the call site.

### Completion & manpages

`RegisterComplete(&utility)` adds a hidden `complete` subcommand that the
shell stubs invoke. `Utility.GenerateCompletions(outDir)` and
`GenerateManpages(outDir)` write installable artifacts under
`<outDir>/share/...`. The flake's `postInstall` runs
`cutting-garden-gen $out` to install both, then deletes the gen binary
so release artifacts don't ship it (pinned by
`zz-tests_bats/install_artifacts.bats`). For eyeballing a page after
editing command metadata, use `just debug-manpage <page>`.

The bash/fish/zsh stubs hold no per-command knowledge; they shell out to
`<binary> complete --bash-style --in-progress=<cur> -- <words>`. The
running binary owns the grammar.

### Request semantics — important divergences

`Request.LastArg` is **destructive**: it consumes every remaining
positional arg and returns the last one. Use `PeekArgs()` for a
non-destructive look. This deliberately diverges from dodder HEAD, which
panics here (tracked at dodder#183 — see the comment on
`Request.LastArg`).

`CommandLineInput.CompleteArgs()` returns the fully-typed args
(FlagsOrArgs with the trailing in-progress token dropped when
`InProgress != ""`). `LastCompleteArg()` is its single-element
convenience wrapper. This **diverges from dodder/madder**, both of
which still carry a buggy `LastCompleteArg` returning the unmodified
`Last()` after decrementing for `InProgress`. Both upstreams open-code
the correct logic in their `complete.go`; we reformulated instead of
parity-fixing. See dodder#182 and the cg #1 resolution.

## External dependencies

The framework leans heavily on
`code.linenisgreat.com/purse-first/libs/dewey`. All exported
surface is under `pkgs/` (dagnabit-generated facades over `internal/`):

- `pkgs/errors` — context-based error propagation
  (`ContextCancelWithError`, `BadRequestf`, `Is400BadRequest`,
  `MakeContextDefault`).
- `pkgs/flags` — flag parsing (drop-in for `flag.FlagSet` with
  `interfaces.CLIFlagDefinitions` shape).
- `pkgs/config_cli` — `Config` interface plumbing.
- `pkgs/collections_slice` — slice wrappers used by `CommandLineInput`.
- `pkgs/interfaces` — `ActiveContext`, `CommandComponentWriter`,
  `FlagValue`, `Seq2`, etc.

(Older code in this repo and its parents used `dewey/0/`, `dewey/bravo/`,
`dewey/charlie/`, etc. paths; those were collapsed into `pkgs/` upstream
and rewritten here as part of the Phase 6 cutover.)

When in doubt about a dewey symbol, read the source under the module
cache rather than guessing — interfaces are small but their semantics
matter.

## Worktree & merge flow

Sweatfile pins `pre-merge = "just"`. The justfile follows
`eng-design_patterns-justfile`(7) — verb-noun leaves under aggregate
targets (`build`, `test`, `update`), and `default: build test` is
the gate `spinclass merge-this-session` runs. Inspect with
`just --list`; aggregates have no body, so add new work as a leaf
recipe and wire it into the right aggregate.

The `set output-format := "tap"` line from the design pattern is
intentionally omitted — the system `just` is upstream 1.49, not
`just-us`, and rejects that setting.
