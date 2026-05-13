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
  semantics. **Status:** exported in madder@6ff15af; consumed
  directly since the v0.3.17 bump (cutting-garden#19 closed). Local
  vendor `internal/output_format` deleted 2026-05-13.
- `internal/charlie/arg_resolver` → `pkgs/arg_resolver` —
  `DetectShadow`, `FormatShadowWarning`, `FormatStoreSwitchNotice`.
  The detection logic is store-id-aware but otherwise generic; useful
  for any command that mixes path args with store-id args.
  **Status:** exported in madder@6ff15af; reachable as of v0.3.17.
  Step 6 consumes it directly (no local vendor).
- `internal/charlie/tap_diagnostics` → `pkgs/tap_diagnostics` —
  `FromError`. Wraps an error into a TAP-shaped diagnostics map; the
  only domain-specific bits are special-cases for `markl.ErrNotEqual`
  and `markl.ErrIsNull`, which are already public via `pkgs/markl`.
  **Status:** NOT exported (madder#165's third ask not fulfilled);
  still vendored as `internal/tap_diagnostics` with a
  delete-on-upstream TODO. Pin bump doesn't change this.
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

1. ✅ **Receipt format vendor.** ([`3efa300`](https://github.com/amarbel-llc/cutting-garden/commit/3efa300))
   Copy `capture_receipt` + tests verbatim. No CLI surface. Locked
   the wire-format invariant in one reviewable diff.
2. ✅ **Sink vendor.** ([`7707d91`](https://github.com/amarbel-llc/cutting-garden/commit/7707d91))
   Copy `capture_sink` + tests; also vendored `tap_diagnostics`
   (madder#165 third ask, not fulfilled upstream).
3. ✅ **Plugin registry + file plugin vendor.** ([`52330ae`](https://github.com/amarbel-llc/cutting-garden/commit/52330ae))
   Copy `cutting_garden_plugins` + `cutting_garden_plugin_file`. At
   this point the `file:` plugin can walk a tree and emit `EntryV1`
   values, but no CLI drove it.
4. ✅ **Minimal `capture` cmd.** ([`e7767cc`](https://github.com/amarbel-llc/cutting-garden/commit/e7767cc))
   `[STORE_ID] DIR` positional surface, NDJSON to stdout, default
   store resolved exactly the way madder resolves it. Also: added
   madder as a flake input pinned at v0.3.15, switched nixpkgs to
   amarbel-llc/nixpkgs (resolves cutting-garden#2), added
   `pkgs/markl_registrations` blank import. Partial fix for
   cutting-garden#4 (single-root only).
5. ✅ **`--format` flag + TAP sink wiring.** ([`fc37f9c`](https://github.com/amarbel-llc/cutting-garden/commit/fc37f9c))
   `-format={auto,tap,json}` selects between NDJSON and TAP sinks.
   Originally landed with `internal/output_format` vendored locally
   while pinned at v0.3.15; the local copy was deleted 2026-05-13
   after the v0.3.17 bump made `pkgs/output_format` reachable.
6. ✅ **Multi-root + multi-group + store-switching.** ([`71ac5e4`](https://github.com/amarbel-llc/cutting-garden/commit/71ac5e4))
   Ported `planCapture`, `classifyArg`, `checkRootCollisions` to
   `internal/capture/plan.go`, consuming upstream `pkgs/arg_resolver`
   (`DetectShadow`, `FormatShadowWarning`,
   `FormatStoreSwitchNotice`) directly. `Capture.Run` rewritten as a
   group-loop / root-loop. `classifyArg` cleans `sourceURL.Path` via
   `filepath.Clean` to close cutting-garden#4 (multi-root half of
   the double-slash bug); original arg preserved verbatim on
   `captureRoot.path` for sink labels, shadow detection, and
   collision diagnostics. Table-driven tests in `plan_test.go` cover
   classify/plan/collision paths and the trailing-slash regression.
   End-to-end multi-root coverage deferred to step 9 (bats); tracked
   at cutting-garden#20.
7. ⏳ **Audit log.** Port `capture_log.go` + `appendCaptureLog`. Build
   the cutting-garden-scoped `env_dir` directly from `pkgs/env_dir`
   inside `Capture.Run` (the `MakeEnvDirForScope` call-site
   reimplemented here per the decision above).
8. ⏳ **Store-hint metadata.** Port `computeStoreHint`. Needs
   `pkgs/blob_store_configs.Coder`, already exported.
9. ⏳ **Bats coverage.** Port the bats tests from
   `madder/zz-tests_bats/` that exercise `cutting-garden capture`.
   This is where the byte-identical-receipt cross-test against
   madder's build lives.

## Pin status: resolved at madder v0.3.17 (2026-05-13)

Tracked at **cutting-garden#19** (closed 2026-05-13).

- `go.mod`: `madder/go v0.3.17`
- `flake.nix`: `madder.url = github:amarbel-llc/madder/a2c01c6...`
- Empirical verification: capture against the original repro store
  (`dodder-v8-take3`) succeeds without the `encryption: invalid
  checksum` error from #19; receipt round-trip confirmed via
  `madder has --no-inventory-log dodder-v8-take3 <id>` (scoped, no
  fallback) and `madder cat <id>`.
- Local vendor cleanup after the bump:
  - `internal/output_format` — deleted 2026-05-13; callsites in
    `internal/capture/capture.go` now import
    `madder/go/pkgs/output_format`.
  - `internal/tap_diagnostics` — **still vendored**; no upstream
    counterpart (madder#165's third ask not fulfilled).
- Step 6 (next) does NOT introduce a new local vendor — consumes
  `pkgs/arg_resolver` directly.

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
