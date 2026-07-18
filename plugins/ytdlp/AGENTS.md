# cutting_garden_plugin_ytdlp

The yt-dlp capture/diff backend for cutting-garden. Lives outside `internal/` (in `plugins/`), consuming the public plugin SDK (`pkgs/`, RFC 0009) like an out-of-tree plugin would — it imports `pkgs/`, never `internal/` (enforced by the `internal/sdklayering` guard). Registered in
`init()` under the `"ytdlp"` URI scheme (both opaque
`ytdlp:<source-url>` and hierarchical `ytdlp://<host>/<path>` forms)
and under `"https"` for a closed host allowlist (`httpsAllowlist` in
`url.go`; YouTube + Instagram today).

**Restore is intentionally not implemented.** Captured artifacts are
regular files; the filesystem plugin materializes them. See
[FDR 0003](../../docs/features/0003-ytdlp-plugin.md) §Restore
Deferral.

## What lives here

- `probeFlatPlaylist` (`flatplaylist.go`) — the SHARED enumeration
  primitive (`yt-dlp --flat-playlist --dump-json`): capture's
  channel-vs-single-video classification, `ListRoots`, and
  `FacetCounts` all call this ONE function rather than each
  re-deriving the tree (FDR 0014 §"Where bulk orchestration lives").
  One returned entry means the source is a plain video (the FDR 0003
  path, unchanged); more than one means a channel/playlist (FDR 0004).
  Also owns the `cg-ytdlp-limit` query-parameter guardrail
  (`extractChannelLimit`/`applyChannelLimit`) — see FDR 0004's
  Implementation Note for why it's a query param and not a CLI flag.
- `Plugin.CaptureRoot` (`capture.go`) — classifies via
  `probeFlatPlaylist`, then dispatches to `captureSingleVideo` (FDR
  0003's original body, byte-for-byte: `--write-info-json
  --write-thumbnail --write-subs` into a tempdir, streams every
  artifact into the destination blob store) or `captureChannel` (FDR
  0004: fans out one `captureSingleVideo`-shaped download per video,
  then rewrites `EntryV1.Root` to the channel URL and prefixes `Path`
  with `<video-id>/`). Per-video failures aggregate rather than
  aborting the channel.
- `Plugin.Types`/`Plugin.ListRoots` (`traversal.go`) — the `RootLister`
  capability (FDR 0014): a channel/playlist is a `ytdlp-channel-v1`
  container; each video is a `ytdlp-video-v1` leaf whose `Node.URI` is
  the flat entry's own canonical `https://` URL — it round-trips
  through `ValidateSource`'s allowlist unchanged, so `capture
  <node.URI>` captures exactly that video through the existing
  single-video path.
- `Plugin.DescribeFacets`/`Plugin.FacetCounts` (`facet.go`) — the
  facet contract (RFC 0012): `uploader` (categorical), `year`/`month`
  (open numeric-bucket, populated only when yt-dlp's flat-mode
  `upload_date` is present — see FDR 0004's Implementation Note),
  `duration_band` (closed short/medium/long). `FacetCounts` is a
  one-shot fold over `probeFlatPlaylist`'s own output rather than a
  framework fold, because no framework-fold consumer exists yet.
- `Plugin.ScanForDiff` (`diff.go`) — lightweight freshness probe:
  fetch only the `.info.json` via `--skip-download`, hash it, compare
  to the receipt's info.json blob-id. Match → re-emit receipt
  entries verbatim (no diff). Miss → run a full yt-dlp invocation
  and return fresh-hashed entries. Single-video only — channel diff
  (FDR 0004's two-level freshness probe) is not yet implemented.
- `sourceURLFromArg` (`url.go`) — URL coercion across the three
  accepted argument forms. Refuses https hosts outside the YouTube
  allowlist; refuses non-`ytdlp`/non-`https` schemes.
- `runYtdlp` (`exec.go`) — `os/exec` wrapper that honors
  ctx-cancellation and surfaces the last 4 KiB of stderr on
  non-zero exit. Resolves the binary via `exec.LookPath`; the Nix
  flake wraps cutting-garden binaries so yt-dlp is on PATH at
  install time. `probeFlatPlaylist` reuses it with an empty outDir
  since `--dump-json` writes nothing to disk.
- Blob streaming is delegated to
  `pkgs/plugin_blob_io/`'s `WriteFileBlob`, shared with the
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
`httpsAllowlist` in `url.go` is the single source of truth for which
https URLs the plugin accepts; adding a host requires confirming
yt-dlp ships a working extractor for it (see the comment on the map).

A second `https` consumer would collide at init time. The migration
to a host-routing layer above the scheme map is sketched in
[FDR 0003 §Future host-routing layer for
https](../../docs/features/0003-ytdlp-plugin.md#future-host-routing-layer-for-https).
