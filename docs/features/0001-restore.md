---
status: experimental
date: 2026-04-28
ported-from: amarbel-llc/madder docs/features/0001-restore.md
promotion-criteria: |
  Promote to `experimental` once the implementation lands and the bats
  conformance suite for [RFC 0001 §Conformance Testing] passes. Promote
  to `accepted` after the v0.6.0 milestone ships and the feature has
  shipped in at least one tagged cutting-garden release.
---

# restore

## Problem Statement

`cutting-garden capture` writes a content-addressable receipt that records
every entry of a captured directory tree. Today there is no way to
reconstruct that tree from a receipt: a user with a receipt id has no
in-tree command that materializes the captured files, directories, and
symlinks back onto disk. This feature adds the inverse —
`cutting-garden restore <receipt-id> <dest>` — covering the consumer side
of the operational contract specified by [RFC 0001] §Consumer Rules.

The absence of a consumer also leaves [RFC 0001]'s store-hint
mechanism unused: receipts now carry `- store/<id> < <markl-id>`
metadata (per amarbel-llc/cutting-garden#12), but no in-tree code consumes it. `restore` is
the first consumer that needs to resolve the source store, validate
config drift, and surface the diagnostics RFC 0001 §Store-Hint
Resolution specifies.

## Interface

### Synopsis

    cutting-garden restore [-store <id>] <receipt-id> <dest>

### Positional arguments

- `<receipt-id>` — the markl-id of a `cutting_garden-capture_receipt-fs-v1`
  blob. Required.
- `<dest>` — the directory the captured tree will be materialized
  under. MUST NOT exist at invocation time. Required.

### Flags

- `-store <id>` — explicit blob-store-id to resolve `<receipt-id>` and
  every entry's `blob_id` against. Overrides the receipt's store-hint
  resolution. When omitted, the consumer follows
  [RFC 0001] §Store-Hint Resolution.

### Exit codes

- `0` — restore completed; `<dest>` exists with the captured tree
  materialized under it.
- nonzero — restore was refused or aborted; `<dest>` does NOT exist
  on disk (no partial materialization). Diagnostics on stderr.

### Behavior

Materialization happens in three phases:

1. **Resolve store + parse receipt.** Use `-store` if set, else the
   active default store. Open the receipt blob. Parse via
   `capture_receipt.Coder.DecodeFrom` (the dispatcher in amarbel-llc/cutting-garden#9
   step 2). The result is a `*V1` carrying optional `Hint` and
   `Entries`.

2. **Resolve effective store via hint.** Per [RFC 0001] §Store-Hint
   Resolution: see [Store-hint resolution](#store-hint-resolution).

3. **Validate then materialize.** Per [RFC 0001] §Path Sanitization
   and §Per-Type Materialization: see [Sanitization](#sanitization)
   and [Per-type materialization](#per-type-materialization). Refusal
   from validation aborts before any disk write; refusal from
   materialization (e.g. mid-stream blob read failure) leaves a
   partially-written tree only as a last resort and emits a
   diagnostic naming the failed entry.

#### Destination preconditions

Per [RFC 0001] §Consumer Rules §Destination Preconditions:

- `<dest>` MUST NOT exist when `restore` is invoked. The consumer
  creates `<dest>` as part of restore.
- `os.Lstat(dest)` is checked before any other work. If it returns nil
  error (i.e. the path exists), refuse with:

      error: <dest>: destination already exists
      hint: choose a destination that does not exist, or remove this one

  Exit nonzero. No store reads, no blob fetches.

#### Sanitization

Per [RFC 0001] §Consumer Rules §Path Sanitization, for each entry
`e`, the consumer computes:

    materialized := filepath.Clean(filepath.Join(dest, e.root, e.path))

The consumer MUST refuse the entire receipt — without leaving any
partial materialization on disk — if any of the following hold for any
entry:

1. `materialized` is not equal to `filepath.Clean(dest)` and is not
   lexically rooted under `filepath.Clean(dest) + os.PathSeparator`.
2. `e.root` or `e.path` contains a NUL byte (`\x00`).
3. `e.root` is the empty string.

Refusal happens BEFORE `<dest>` is created, before any blob is opened,
and before any byte is written.

Diagnostics use a multi-line shape: a single `error:` summary line,
followed by indented `key: value` payload lines. This is a
deliberate FDR-level convention (see [Limitations](#limitations) for
the rationale and the comparison to `capture`'s single-line
`error:` + `hint:` style). The shape is appropriate here because the
RFC mandates that the diagnostic identify multiple distinct fields
(root, path, materialized, destination) per refusal, and a single
line cannot fit them all readably:

- Path escape:

      error: entry escapes destination
        root: <e.root>
        path: <e.path>
        materialized: <materialized>
        destination: <filepath.Clean(dest)>

- NUL byte:

      error: entry contains NUL byte
        root: <quoted_e.root>
        path: <quoted_e.path>

- Empty root:

      error: entry has empty root
        path: <e.path>

In every refusal case, exit nonzero and DO NOT create `<dest>`.

Newlines (`\n`, `\r`) and other valid-UTF-8 characters in `e.root` and
`e.path` MUST be permitted. Per [RFC 0001] §Consumer Rules, the
consumer MUST NOT reject an entry on the basis of its name containing
newlines or other unusual-but-legal characters. Tested by
`restore_round_trips_unusual_filenames` (deferred — see
[Limitations](#limitations)).

#### Multi-root receipts

Per [RFC 0001] §Multi-Root Receipts, a receipt MAY contain entries
from multiple positional roots when a single store-group capture had
multiple top-level directory arguments. Each distinct value of
`e.root` materializes as a top-level subdirectory under `<dest>`, via
the same `filepath.Clean(filepath.Join(dest, e.root, e.path))`
formula used in [Sanitization](#sanitization).

The consumer MUST NOT re-canonicalize `e.root` beyond what
`filepath.Join` + `filepath.Clean` provides. Producer-side root
collision detection (amarbel-llc/cutting-garden#11) guarantees that no two distinct values of
`e.root` within a single receipt resolve to the same path under
`filepath.Clean`, so consumers MAY rely on root-distinctness as a
structural invariant for reporting and diagnostics — but MUST NOT use
it as a security pre-condition (the sanitization checks defend
against hand-crafted receipts regardless of producer conformance).

#### Per-type materialization

Per [RFC 0001] §Consumer Rules §Per-Type Materialization, for each
entry `e` accepted by [Sanitization](#sanitization):

| `e.type` | Materialization |
|---|---|
| `file` | Resolve `e.blob_id` against the resolved store. Open `materialized` for write (create-only, `os.O_WRONLY|os.O_CREATE|os.O_EXCL`, mode `0o666` modulo umask). Stream the blob via `io.Copy(file, blobReader)` — `BlobReader` provides `io.WriterTo`. On success, `os.Chmod(materialized, e.mode & 0o777)`. |
| `dir` | `os.MkdirAll(materialized, e.mode & 0o777)`. `MkdirAll` is safe because §Sanitization confines `materialized` to under `<dest>`. (Conformant producers also emit a `dir` entry for every ancestor — `filepath.WalkDir` visits every directory it descends into — but the consumer MUST NOT rely on producer conformance for safety per [RFC 0001] §Security Considerations.) |
| `symlink` | `os.Symlink(e.target, materialized)`. The mode field MUST be ignored. The target string is the literal value from `e.target` — NOT resolved, NOT cleaned, NOT validated against the destination's containment. (See [Limitations](#limitations) on symlink-following.) |
| `other` | Skip with a notice: `notice: skipping entry of type "other": <materialized>`. The consumer MUST NOT attempt to recreate devices, FIFOs, sockets, or other special files. |

Streaming for `file`: the consumer MUST NOT buffer file content in
memory. The `BlobReader` returned by `MakeBlobReader` is required to
satisfy `io.WriterTo`; the consumer uses `io.Copy(dst, blobReader)`
which prefers `WriteTo` when available.

The consumer MUST NOT preserve mtime, atime, owner, group, xattrs, or
ACLs.

#### Store-hint resolution

Per [RFC 0001] §Store-Hint Resolution, the consumer determines which
configured blob store to use for resolving entry `blob_id` references
via the following procedure (after the receipt has been parsed):

1. **`-store` flag wins.** If `-store <id>` is set, use that store. No
   warning, no fallback. The flag suppresses every branch below.
2. **Hint present, store configured, config-blob hash matches** — use
   the hinted store, no prompt:

       (no diagnostic; restore proceeds silently against the hinted store)

3. **Hint present, store configured, config-blob hash differs** —
   refuse without `-store` override:

       warning: store <hint.StoreId> has been re-configured since this
       receipt was written
         receipt config-hash: <hint.ConfigMarklId>
         current config-hash: <local-config-markl-id>
       error: pass -store <id> to override and use the current store
       hint: re-running with -store <hint.StoreId> uses the current
       configuration

   Exit nonzero. No materialization.

4. **Hint present, store NOT configured locally** — fall back to the
   active store with a notice:

       notice: receipt names store "<hint.StoreId>" which is not configured locally
       notice: falling back to active store

5. **No hint** — use the active store with a notice:

       notice: receipt carries no store hint
       notice: falling back to active store

The receipt blob ITSELF is fetched in phase 1 via the same store
resolution as the entries' blobs. The current `capture` producer
emits one receipt blob per store-group ([RFC 0001] §Introduction),
which means in practice all entries in a single receipt resolve via
the same store. RFC 0001 does not normatively forbid cross-store
entries within a single receipt, so a future producer or hand-crafted
receipt could legitimately split entries across stores — but the v1
consumer does not handle that case (see [Limitations](#limitations)).

The local config-blob markl-id is computed the same way `capture`
computes the hint at write time (see amarbel-llc/cutting-garden#12's `computeStoreHint`):
re-encode the store's `GetBlobStoreConfig()` through a digesting
writer using the store's `GetDefaultHashType()`. Hash families MUST
match for the comparison; if the receipt's hint uses a hash family the
local store does not support, the consumer falls through to branch 4
(missing-fallback) with a notice naming the hash mismatch.

### MCP and TAP output

`restore` does NOT emit MCP-style structured output in v1. The
consumer's diagnostics are plain stderr lines. A future
`-format json` flag is deferred (see [Limitations](#limitations)).

## Examples

### Round-trip

    $ cutting-garden capture -format json src
    ok 1 - capture src
    ok 2 - receipt blake2b256-7g…

    $ cutting-garden restore blake2b256-7g… restored
    $ ls restored/src
    main.go
    go.mod

The receipt has a hint, the local store matches the hint's
config-markl-id, no diagnostic.

### Refusal: destination exists

    $ mkdir out
    $ cutting-garden restore blake2b256-7g… out
    error: out: destination already exists
    hint: choose a destination that does not exist, or remove this one

Exit nonzero. `out/` is unchanged.

### Refusal: path-escape entry

A hand-crafted receipt with `{"root":"src","path":"../../../etc/passwd",...}`:

    $ cutting-garden restore <receipt-id> out/
    error: entry escapes destination
      root: src
      path: ../../../etc/passwd
      materialized: /etc/passwd
      destination: /home/sasha/projects/foo/out

Exit nonzero. `out/` is NOT created.

### Store-hint mismatch

The receipt was written against `.work` whose config has since been
rotated:

    $ cutting-garden restore <receipt-id> restored
    warning: store .work has been re-configured since this receipt was written
      receipt config-hash: blake2b256-9ft3m74l5t2ppwjrvfg3wp3…
      current config-hash: blake2b256-3wp380jqj2zfrm6zevxqx3…
    error: pass -store <id> to override and use the current store
    hint: re-running with -store .work uses the current configuration

    $ cutting-garden restore -store .work <receipt-id> restored
    (proceeds against the current .work configuration)

### Symlink preservation

A receipt with a `type:"symlink"` entry whose `target` is `../bar`:

    $ cutting-garden restore <receipt-id> restored
    $ readlink restored/src/link
    ../bar

The target is preserved verbatim, NOT resolved.

## Limitations

These are deliberate scope boundaries for v1. Each is filed as a
follow-up issue or deferred to a future schema version.

- **No mtime / atime / owner / group / xattrs / ACLs preservation.** The
  v1 receipt schema does not record these; preserving them is deferred
  to `cutting_garden-capture_receipt-fs-v2` ([RFC 0001] §Producer Rules
  §Body Schema).
- **No overwrite, merge, or partial-restore policy.** `<dest>` MUST NOT
  exist; the consumer creates it. There is no `-force`, no `-merge`,
  no `-resume`. Re-running after a partial failure means deleting the
  partial output and re-invoking.
- **No cross-store fallback for individual entries.** If the resolved
  store is missing a referenced `blob_id`, the restore aborts with a
  diagnostic naming the entry. There is no per-entry probe into other
  configured stores.
- **No `-format json` MCP output.** Deferred to a follow-up. The v1
  output is human-readable diagnostics on stderr only.
- **No symlink-target sanitization.** `e.target` is written literally
  via `os.Symlink`. A receipt with a symlink pointing to `/etc/shadow`
  is restored faithfully. [RFC 0001] §Security Considerations notes
  this is the standard tar-style risk; the consumer relies on the
  caller not following symlinks in the restored tree until reviewed.
- **No `-dry-run` flag.** Validation runs in the same pass as
  materialization (refusing before any write); a separate dry-run mode
  is deferred.
- **No mtime recording for the destination.** `<dest>` and any created
  directories receive the OS-default mtime at creation time, not the
  capture-time mtime.
- **Conformance test for unusual-but-legal filenames is deferred.**
  [RFC 0001] §Path Sanitization permits newlines and other valid-UTF-8
  characters in `e.root` / `e.path`. The bats scenario for this is not
  in v1's matrix; it is filed as a follow-up. The Go-level sanitizer
  permits them by construction (it only refuses NUL bytes, empty
  roots, and parent-escape).
- **No mid-stream rollback.** If a `file` entry's blob read fails
  partway through `io.Copy` (network drop, hash mismatch, etc.), the
  consumer aborts and the destination is left in a partial state.
  Diagnostic shape:

      error: <materialized>: blob read failed
        blob_id: <e.blob_id>
        cause: <wrapped err>
      hint: remove <dest> and re-run

  [RFC 0001] §Consumer Rules does not mandate transactional rollback;
  this FDR explicitly chooses leave-partial-on-mid-stream-failure
  rather than reverse-walk-and-delete. Cleanup is the operator's job.
- **No `other → skip` bats scenario in v1.** [RFC 0001] §Conformance
  Testing lists `restore_skips_type_other_with_notice` as
  required; the v1 matrix omits it because injecting a `type:"other"`
  entry without root privileges (FIFO via `mkfifo` works on POSIX but
  not on macOS in the bats sandcastle environment) is awkward. The
  Go-level switch arm IS implemented and emits the notice; only the
  end-to-end bats coverage is deferred. Filed as a follow-up.
- **Hash-family mismatch in store-hint falls through to
  missing-fallback.** [RFC 0001] §Store-Hint Resolution does not
  specify behavior when the receipt's hint markl-id uses a hash
  family the locally-configured store cannot reproduce (e.g. receipt
  hash is `sha512` but local store is `blake2b256`). This FDR chooses
  to treat that case as branch 4 (missing-fallback) with a notice
  naming the hash-family mismatch:

      notice: receipt store-hint uses hash family <hint-hash> not
      supported by local store <hint.StoreId> (<local-hash>)
      notice: falling back to active store

  This is an FDR-level decision, not an RFC-level one. If the RFC is
  amended to specify the behavior, this Limitations bullet should be
  removed.
- **Multi-line diagnostic style is FDR-introduced.** The standard
  diagnostic style elsewhere in cutting-garden (e.g. `capture`'s
  `checkRootScope` / `checkRootCollisions`) uses a single `error:`
  line plus an optional `hint:` line. This FDR introduces two
  variants:
  - **Multi-line indented payload.** For sanitization refusals
    (path-escape, NUL-byte, empty-root, store-hint mismatch payload),
    the `error:` line is followed by indented `key: value` lines
    naming each distinct field [RFC 0001] requires the diagnostic to
    surface (root, path, materialized, destination, etc.). Single
    lines cannot fit them all readably.
  - **Multi-prefix on store-hint mismatch.** The mismatch case uses
    three severity prefixes — `warning:` (config drift), `error:`
    (refuse without `-store`), `hint:` (override syntax) — to surface
    the diagnosis-then-refuse-then-action triple that [RFC 0001]
    §Store-Hint Resolution mandates.

  These are intentional FDR-level conventions. Future diagnostics
  with similar shapes SHOULD follow them. If the project later
  consolidates on a single diagnostic shape, this FDR's strings
  should be re-examined.

## More Information

- [RFC 0001] — Capture / Restore Operational Rules
  (`docs/rfcs/0001-capture-restore-rules.md`). Normative source
  for every behavioral rule in this FDR.
- [amarbel-llc/cutting-garden#9] — the issue this FDR resolves.
- [amarbel-llc/cutting-garden#11] — producer-side root scoping + collision detection. Closes the
  capture-time half of [RFC 0001] §Producer Rules.
- [amarbel-llc/cutting-garden#12] — store-hint metadata emission. Produces the metadata this
  FDR's §Store-Hint Resolution consumes.
- ADR 0005 — the remote-driven SFTP design (madder upstream;
  motivated the config-hash lock in the store hint).

## Conformance test mapping

For traceability, every normative rule in [RFC 0001] §Consumer Rules
maps to a bats test in `zz-tests_bats/restore.bats`. The matrix
is the v1 acceptance criterion:

| [RFC 0001] § | Test |
|---|---|
| §Destination Preconditions | `restore_refuses_existing_destination` |
| §Path Sanitization (parent-escape) | `restore_refuses_path_escape_no_partial_writes` |
| §Path Sanitization (NUL byte) | `restore_refuses_nul_byte_in_path` |
| §Path Sanitization (empty root) | `restore_refuses_empty_root` |
| §Per-Type Materialization (file) | `restore_round_trips_file` |
| §Per-Type Materialization (dir) | `restore_round_trips_dir` |
| §Per-Type Materialization (symlink) | `restore_round_trips_symlink` |
| §Per-Type Materialization (other → skip) | `restore_skips_type_other_with_notice` (DEFERRED — hard to inject without root) |
| §Store-Hint Resolution §Auto-use | `restore_uses_hint_store_when_config_matches` |
| §Store-Hint Resolution §Mismatch-warn | `restore_warns_on_config_drift` |
| §Store-Hint Resolution §Missing-fallback (hint present, store missing) | `restore_falls_back_to_active_store_on_missing_hint` |
| §Store-Hint Resolution (no hint at all) | `restore_falls_back_to_active_store_on_no_hint` |
| §Store-Hint Resolution (-store override, FDR-added) | `restore_store_flag_overrides_hint` |

The two fallback cases (hint-points-at-missing-store vs.
no-hint-line-at-all) are split into separate tests because, although
the *diagnostic shape* is similar, the *triggering wire-format* is
different (presence-of-hint-line vs. absence) and tying each test to
a single fixture keeps debug failures clearly attributable.

The `-store` override row is FDR-added (not in the [RFC 0001]
§Conformance Testing matrix). Documenting it here so the matrix is
auditable as a superset rather than a divergence.

[RFC 0001]: ../rfcs/0001-capture-restore-rules.md
[amarbel-llc/cutting-garden#9]: https://github.com/amarbel-llc/cutting-garden/issues/9
[amarbel-llc/cutting-garden#11]: https://github.com/amarbel-llc/cutting-garden/issues/11
[amarbel-llc/cutting-garden#12]: https://github.com/amarbel-llc/cutting-garden/issues/12
