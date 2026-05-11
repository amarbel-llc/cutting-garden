# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Phase 1 — framework bootstrap. The repo is a port of dodder's command-dispatch
framework, being extracted to back a future filesystem-tree capture/restore
CLI atop [madder](https://github.com/amarbel-llc/madder). No user-facing
subcommands exist yet; `cmd/cutting-garden/main.go` only registers
`complete`. Design context lives in
`amarbel-llc/madder` → `docs/plans/2026-05-10-extract-cutting-garden-design.md`.

Comments and TODOs frequently reference upstream dodder issues (#161, #183,
…) and madder issues — check those before "fixing" what looks like a bug; some
divergences from dodder are intentional carry-forwards.

## Build & test

- `nix build` — produces `result/bin/cutting-garden`. Uses gomod2nix; reads
  module hashes from `gomod2nix.toml`, **not** `go.sum`.
- `go test ./...` — runs the framework test suite (no external deps).
- `go test ./internal/command -run TestUtility_Run_DispatchesToRegisteredCmd`
  — single-test pattern.
- `go build ./...` — compile check inside the devshell.

The flake's `devShells.default` provides `go_1_26`, `gopls`, and `gomod2nix`.
`GOTOOLCHAIN = "local"` is pinned in both the package build and devshell —
never let go fetch a different toolchain.

### When dependencies change

After `go get` or editing `go.mod`, regenerate the nix lock:

```sh
gomod2nix generate
```

Both `gomod2nix.toml` and any newly tracked source files must be
`git add`'d before `nix build` will see them — `nix build` against a dirty
tree includes only git-tracked files.

## Architecture

Everything user-facing lives behind one type: `command.Utility` in
`internal/command/`. The dispatch loop is small enough to read end-to-end
in `utility.go`:

```
main → MakeUtility(name, config) → RegisterComplete(&u) → u.Run(os.Args)
```

`Utility.Run` →
1. Builds a cancelable `errors.Context` (SIGTERM/SIGINT/SIGHUP).
2. `MakeCmdAndFlagSet` looks up `args[1]`, parses flags via
   `dewey/charlie/flags`. Subcommands implement
   `interfaces.CommandComponentWriter.SetFlagDefinitions` to bind flags.
3. `MakeRequest` wraps parsed positional args into `Request{ input *CommandLineInput }`.
4. `cmd.Run(req)` dispatches to user code.

`Run` deliberately **does not call `os.Exit`** — keeping it side-effect-light
for tests. `handleMainErrors` formats the failure to stderr and *would*
return an exit code (64 EX_USAGE for `errors.Is400BadRequest`, else 1) but
the return value is currently ignored. If you wire a production exit-code
path, do it in `main.go`, not by mutating `Run`.

### Opt-in command interfaces

A `Cmd` is just `Run(Request)`. Everything else is opt-in via narrow
interfaces — implement only what your command needs:

| Interface | File | Surfaces |
|---|---|---|
| `CommandWithDescription` | `cmd.go` | `complete` listing, manpage NAME/DESCRIPTION |
| `CommandWithArgs` | `arg.go` | manpage ARGUMENTS section |
| `CommandWithEnvVars` / `CommandWithFiles` / `CommandWithExamples` / `CommandWithSeeAlso` / `CommandWithManpageFiles` | `manpage.go` | corresponding manpage sections |
| `CommandWithMCPAnnotations` | `arg.go` | future MCP wiring (inert in Phase 1) |
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
`<outDir>/share/...`. The flake's `postInstall` is empty — wiring those
generators into the build is a Phase 2 TODO (see `flake.nix:40`).

The bash/fish/zsh stubs hold no per-command knowledge; they shell out to
`<binary> complete --bash-style --in-progress=<cur> -- <words>`. The
running binary owns the grammar.

### Request semantics — important divergences

`Request.LastArg` is **destructive**: it consumes every remaining
positional arg and returns the last one. Use `PeekArgs()` for a
non-destructive look. This deliberately diverges from dodder HEAD, which
panics here (tracked at dodder#183 — see the comment on
`Request.LastArg`).

`CommandLineInput.LastCompleteArg` carries an upstream bug forward
verbatim (the `Last()` call should be `FlagsOrArgs[argc-1]` after the
decrement). Don't "fix" without coordinating — it's pinned to
dodder-parity behavior on purpose.

## External dependencies

The framework leans heavily on
`github.com/amarbel-llc/purse-first/libs/dewey`:

- `bravo/errors` — context-based error propagation
  (`ContextCancelWithError`, `BadRequestf`, `Is400BadRequest`,
  `MakeContextDefault`).
- `charlie/flags` — flag parsing (drop-in for `flag.FlagSet` with
  `interfaces.CLIFlagDefinitions` shape).
- `foxtrot/config_cli` — `Config` interface plumbing.
- `bravo/collections_slice` — slice wrappers used by `CommandLineInput`.
- `0/interfaces` — `ActiveContext`, `CommandComponentWriter`, `FlagValue`,
  `Seq2`, etc.

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
