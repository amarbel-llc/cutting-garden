# Phase 2 — port `capture`

Tracking issue: [#3](https://github.com/amarbel-llc/cutting-garden/issues/3).

## Goal

Port `madder/go/internal/india/commands_cutting_garden/capture.go` (and
its `capture_log.go`, `globals.go` siblings) into this repo, behind our
`internal/command` framework, producing **byte-identical receipts** for
a fixture tree against madder's existing build. Phase 6 promotion gates
on that byte-identity, so the receipt format is the load-bearing
invariant — everything else (sinks, audit log, plugins) is replaceable.

Out of scope here: `restore` (Phase 3), `diff` (Phase 4), docs port
(Phase 5), and the `flake.nix:40` postInstall TODO (also Phase 5).

## Upstream dependency map

Madder's `capture.go` (602 lines) pulls in 14 madder packages. The
decision rule for this phase, per the issue and user direction:

> *Generic* helpers → request a single dagnabit-generated `pkgs/`
> export against madder. *Cutting-garden-specific* helpers → copy or
> re-implement here, under `internal/`. We do not import madder's
> `internal/` directly.

### Already exported (consume from `madder/go/pkgs/...`)

- `pkgs/blob_store_env` — `BlobStoreEnv`, `MakeBlobStoreEnv*`
- `pkgs/blob_stores` — `BlobStoreInitialized`, copy result enum
- `pkgs/blob_store_id`, `pkgs/blob_store_configs`
- `pkgs/markl`, `pkgs/markl_io`
- `pkgs/hyphence`, `pkgs/inventory_log`
- `pkgs/env_dir`, `pkgs/env_local`, `pkgs/env_ui`, `pkgs/madder_env`
- `pkgs/plugins`, `pkgs/domain_interfaces`

### Need dagnabit export from madder ([madder#165](https://github.com/amarbel-llc/madder/issues/165))

- `internal/charlie/output_format` → `pkgs/output_format` —
  `Format`, `FormatJSON`, `FormatTAP`, `Default`, `Resolve`,
  `FlagDescription`. Pure I/O-format flag value; no cutting-garden
  semantics.
- `internal/charlie/arg_resolver` → `pkgs/arg_resolver` —
  `DetectShadow`, `FormatShadowWarning`, `FormatStoreSwitchNotice`.
  The detection logic is store-id-aware but otherwise generic; useful
  for any command that mixes path args with store-id args.
- `internal/charlie/tap_diagnostics` → `pkgs/tap_diagnostics` —
  `FromError`. Wraps an error into a TAP-shaped diagnostics map; the
  only domain-specific bits are special-cases for `markl.ErrNotEqual`
  and `markl.ErrIsNull`, which are already public via `pkgs/markl`.
  Temporarily vendored as `internal/tap_diagnostics` in this repo
  with a delete-on-upstream TODO (same policy as steps 5/6).
**Not** requested: `internal/golf/command_components`. The mixin
(`EnvBlobStore.MakeEnvBlobStore`, `MakeEnvDirForScope`,
`BlobStoreIds`) is tightly bound to madder's `futility.Request`
shape, and re-exporting it would force a `futility.Request` ↔
`command.Request` adapter into this repo. Instead, we reimplement
the three call-sites locally against `pkgs/blob_store_env` +
`pkgs/env_dir` directly — matches the issue #3 guidance
(*"whether commands consume `pkgs/blob_store_env` directly"* — yes,
they do).

### Copy/re-implement in this repo (under `internal/`)

- `internal/capture_receipt` — `EntryV1`, `WriteV1WithHint`,
  `StoreHint`. The wire format is the Phase 6 promotion invariant;
  copying is a forcing function for the cross-build receipt-identity
  test. RFC 0003 (`docs/rfcs/0003-capture-restore-rules.md` in madder)
  is normative.
- `internal/capture_sink` — `Sink`, `NewNDJSON`, `NewTAP`,
  `Notice`/`Failure`/`SetStore`/`StoreGroupReceipt`/`Finalize`. The
  TAP output shape is user-facing; copy preserves it verbatim.
- `internal/cutting_garden_plugins` — `CapturePlugin`,
  `CaptureRootRequest`, `CaptureRootResult`, `ResolveCapture`,
  `ValidateSource`, `CaptureRoot`. Plugin registry is intrinsically
  cutting-garden-specific.
- `internal/cutting_garden_plugin_file` — default `file:` plugin.
  Registers itself via `init()` against the plugin registry above.

## Framework gap

Madder's `capture.go` uses `futility.CommandWithParams` +
`futility.Param` + `futility.Arg[T]` for declarative arg metadata.
Our `internal/command` has `CommandWithArgs` returning
`[]ArgGroup` with `Arg` fields — overlapping intent, different
shape. No generic `Param`/`BoolFlag`/typed `Arg[T]` exists here yet.

**Decision (deferred):** port `capture` first using whatever
`internal/command` already expresses; revisit when we hit something
the local types cannot model. We accept that capture's manpage output
will not be byte-identical to madder's during Phase 2 — only the
**receipt** is the Phase 6 invariant.

## Cutting-garden-specific copy: file layout

```
internal/
  capture_receipt/       # EntryV1, WriteV1WithHint, StoreHint
    main.go
    main_test.go
  capture_sink/          # Sink + TAP/NDJSON impls
    main.go
    ndjson.go
    tap.go
    main_test.go
  cutting_garden_plugins/  # registry + interfaces
    main.go
    main_test.go
  cutting_garden_plugin_file/  # file: scheme plugin
    main.go
    main_test.go
  command/               # (unchanged from Phase 1)
  capture/               # the Capture cmd itself
    main.go              # Capture struct, Run, Complete, SetFlagDefinitions
    plan.go              # planCapture, classifyArg, checkRootCollisions
    receipt_blob.go      # writeReceiptBlob, computeStoreHint
    capture_log.go       # captureLogEntry, appendCaptureLog
    globals.go           # NoInventoryLog plumbing (if we keep it)
    *_test.go
```

`internal/command` stays the per-binary CLI framework; capture and
its support packages live as siblings. We do **not** introduce a
`futility`-shaped intermediate layer.

## MVP staging (within Phase 2)

Each step is a separate commit. Step N+1 does not start until step N
runs cleanly against `go test ./...` and a hand-fixture
`nix build && ./result/bin/cutting-garden capture ./fixtures/...`.

1. **Receipt format vendor.** Copy `capture_receipt` + tests verbatim.
   No CLI surface. This locks the wire-format invariant in one
   reviewable diff.
2. **Sink vendor.** Copy `capture_sink` + tests.
3. **Plugin registry + file plugin vendor.** Copy
   `cutting_garden_plugins` + `cutting_garden_plugin_file`. At this
   point we have a `file:` plugin able to walk a tree and emit
   `EntryV1` values, but no CLI to drive it.
4. **Minimal `capture` cmd — single root, default store, no plugin
   args, NDJSON only.** Wire `Capture.Run` to: parse one positional
   directory arg, build `BlobStoreEnv` via madder's `pkgs/blob_store_env`,
   walk via the `file:` plugin, write the receipt. Output: one line
   with the receipt id on stdout. No TAP, no `--format`, no audit log,
   no shadow detection.
5. **`--format` flag + TAP sink wiring.** Needs `pkgs/output_format`
   from the umbrella issue. Madder@6ff15af landed the export
   (madder#165 closed), but we're pinned at v0.3.15 because v0.3.16
   introduced a markl-id wire-format change that breaks pre-flip
   on-disk stores. Vendored as `internal/output_format` with a
   delete-on-upstream-bump TODO. ✅ Landed via commit (TODO: link
   after merge).
6. **Multi-root + multi-group + store-switching.** Port `planCapture`,
   `classifyArg`, `checkRootCollisions`. Needs `pkgs/arg_resolver` or
   we vendor `arg_resolver` locally; same delete-on-upstream policy as
   step 5.
7. **Audit log.** Port `capture_log.go` + `appendCaptureLog`. Build
   the cutting-garden-scoped `env_dir` directly from `pkgs/env_dir`
   inside `Capture.Run` (the `MakeEnvDirForScope` call-site
   reimplemented here per the decision above).
8. **Store-hint metadata.** Port `computeStoreHint`. Needs
   `pkgs/blob_store_configs.Coder`, already exported.
9. **Bats coverage.** Port the bats tests from
   `madder/zz-tests_bats/` that exercise `cutting-garden capture`.
   This is where the byte-identical-receipt cross-test against
   madder's build lives.

## Madder umbrella issue

Filed as [madder#165](https://github.com/amarbel-llc/madder/issues/165).

Asks (mirrored from the issue, for offline reference):
- Generate `pkgs/output_format` (whole package).
- Generate `pkgs/arg_resolver` (whole package; or just
  `DetectShadow`, `FormatShadowWarning`, `FormatStoreSwitchNotice` if
  granular).
- Generate `pkgs/tap_diagnostics` (one function: `FromError`).
  Added during MVP step 2 — capture_sink uses it for `Failure` TAP
  diagnostics; vendored locally as `internal/tap_diagnostics` until
  the export lands.

`command_components` is **explicitly not** in scope — see "Need
dagnabit export from madder" above for the reasoning.

Cross-references: this design doc, cutting-garden#3,
[cutting-garden#4](https://github.com/amarbel-llc/cutting-garden/issues/4)
(receipt double-slash, transferred from madder#162; fix lands here
during MVP step 4 where root + entry path are joined).

## Open questions to resolve before MVP step 1

- [x] ~~madder#162 (receipt double-slash): fix in madder first or
  here?~~ Resolved: transferred to
  [cutting-garden#4](https://github.com/amarbel-llc/cutting-garden/issues/4);
  fix only on the new side. The Phase 6 receipt-identity criterion
  therefore acquires an explicit exception for this bug — receipts
  emitted by cutting-garden will be byte-identical to madder's
  *except* for paths that previously contained `//`. Cross-test
  fixtures must avoid trailing-slash capture-roots, or compare with
  `filepath.Clean` normalization on the madder side.
- [ ] cutting-garden#2 (flake forks alignment): blocks bats lanes per
  the parent issue, so blocks MVP step 9 but not steps 1–8.
- [ ] Do we keep the `--no-inventory-log` global flag and
  `Globals.IsInventoryLogDisabled` shape? Probably yes; defer until
  step 7.
- [x] ~~How does the local `command.Request` plug into madder's
  `pkgs/command_components.EnvBlobStore`?~~ Resolved: we do not
  re-export `command_components`. The mixin's call-sites are
  reimplemented locally against `pkgs/blob_store_env` and
  `pkgs/env_dir`. See "Need dagnabit export from madder".

## Non-goals for Phase 2

- Manpage byte-identity with madder.
- Help-text byte-identity with madder.
- Wiring `GenerateManpages` / `GenerateCompletions` into the flake
  (Phase 5).
- `restore`, `diff` (Phases 3, 4).
- Touching `internal/command` to add `Param`/`Arg[T]`/`CommandWithParams`
  (deferred per Framework Gap).
