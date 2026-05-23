# cutting_garden_plugin_ytdlp

The yt-dlp capture/diff backend for cutting-garden. Peer leaf of
`cutting_garden_plugins/` — not a nested subpackage. Registered in
`init()` under the `"ytdlp"` URI scheme (both opaque
`ytdlp:<source-url>` and hierarchical `ytdlp://<host>/<path>` forms)
and under `"https"` for a closed YouTube host allowlist.

**Restore is intentionally not implemented.** Captured artifacts are
regular files; the filesystem plugin materializes them. See
[FDR 0003](../../docs/features/0003-ytdlp-plugin.md) §Restore
Deferral.

## What lives here

- `Plugin.CaptureRoot` (`capture.go`) — runs yt-dlp with
  `--write-info-json --write-thumbnail --write-subs` into a tempdir,
  streams every produced artifact into the destination blob store as
  one EntryV1 per file, then removes the tempdir.
- `Plugin.ScanForDiff` (`diff.go`) — lightweight freshness probe:
  fetch only the `.info.json` via `--skip-download`, hash it, compare
  to the receipt's info.json blob-id. Match → re-emit receipt
  entries verbatim (no diff). Miss → run a full yt-dlp invocation
  and return fresh-hashed entries.
- `sourceURLFromArg` (`url.go`) — URL coercion across the three
  accepted argument forms. Refuses https hosts outside the YouTube
  allowlist; refuses non-`ytdlp`/non-`https` schemes.
- `runYtdlp` (`exec.go`) — `os/exec` wrapper that honors
  ctx-cancellation and surfaces the last 4 KiB of stderr on
  non-zero exit. Resolves the binary via `exec.LookPath`; the Nix
  flake wraps cutting-garden binaries so yt-dlp is on PATH at
  install time.
- Blob streaming is delegated to
  `internal/plugin_blob_io/`'s `WriteFileBlob`, shared with the
  filesystem plugin. The package also owns `CtxReader`, the
  ctx-cancellation wrapper used by both plugins' copy loops.

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than a `…-ytdlp-v1`
variant. yt-dlp artifacts are captured as regular file entries —
byte-identical EntryV1 shape to fs captures — and `capture.go` folds
all roots into one receipt per store group, so a mixed
fs+ytdlp group must share one type-tag. Restore through the file
plugin works unchanged.

If a future change ever needs to distinguish yt-dlp origin in the
receipt, `EntryV1.Root` already carries the source URL — no schema
change required.

## https scheme footprint

This plugin claims `https` exclusively (panic on duplicate
registration per `cutting_garden_plugins.MustRegisterCapture`). The
YouTube host allowlist in `sourceURLFromArg` is the single source of
truth for which https URLs the plugin accepts.

A second `https` consumer would collide at init time. The migration
to a host-routing layer above the scheme map is sketched in
[FDR 0003 §Future host-routing layer for
https](../../docs/features/0003-ytdlp-plugin.md#future-host-routing-layer-for-https).
