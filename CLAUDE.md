# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

A filesystem-tree capture/restore CLI atop
[madder](https://github.com/amarbel-llc/madder), grown from a port of
dodder's command-dispatch framework. Three user-facing subcommands —
`capture`, `restore`, `diff` — plus the hidden `complete` are
registered in `internal/cgapp.Build()`, the single factory shared by
the `cutting-garden` binary, its `cg` alias, and the
manpage/completion generator `cutting-garden-gen`. Capture/restore/
diff backends are URI-scheme-keyed plugins (file, git, yt-dlp) under
`internal/cutting_garden_plugin_*`. The original extraction design
lives in `amarbel-llc/madder` →
`docs/plans/2026-05-10-extract-cutting-garden-design.md`; newer design
docs live in this repo under `docs/{rfcs,features,plans}/`.

Comments and TODOs frequently reference upstream dodder issues (#161, #183,
…) and madder issues — check those before "fixing" what looks like a bug; some
divergences from dodder are intentional carry-forwards.

## Build & test

- `nix build` — produces `result/bin/cutting-garden`. Module sources come
  from two places:
    - **Flake-input bridge** (`gomod.nix`): madder, tap, and dewey
      (`libs/dewey` within the purse-first workspace) are sourced from
      sibling flakes via `goFlakeInputs` (RFC 0001). Bumping any of them
      is a `flake.lock`-only edit; no `go get` + `gomod2nix generate`
      lockstep.
    - **Organic gomod2nix** (`gomod2nix.toml`): everything else. Read from
      `gomod2nix.toml`, **not** `go.sum`.
- `go test ./...` — runs the test suite (no external deps).
- `go test ./internal/command -run TestUtility_Run_DispatchesToRegisteredCmd`
  — single-test pattern.
- `go build ./...` — compile check inside the devshell.

The flake's `devShells.default` provides `go_1_26`, `gopls`, and `gomod2nix`.
`GOTOOLCHAIN = "local"` is pinned in both the package build and devshell —
never let go fetch a different toolchain.

### When dependencies change

Two cases:

1. **A bridged dep (madder, tap, dewey)** — bump the flake input:
   ```sh
   nix flake update madder   # or tap, or purse-first (dewey lives there)
   ```
   `flake.lock` is the source of truth; `go.mod` keeps its real `require`
   line (the bridge merges over it at eval time). No `gomod2nix generate`
   needed for the bridged module, though `go.mod`'s require line still
   needs a real version so `nix develop --command go build` (which hits
   GOMODCACHE, not the bridge) finds the dep.

2. **A non-bridged dep** — `go get` it, then regenerate the nix lock:
   ```sh
   gomod2nix generate
   ```

Either way, `gomod2nix.toml` and any newly tracked source files must be
`git add`'d before `nix build` sees them — `nix build` against a dirty
tree only includes git-tracked files.

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
`github.com/amarbel-llc/purse-first/libs/dewey`. All exported
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
