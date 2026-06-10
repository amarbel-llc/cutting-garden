# cutting_garden_plugin_googlephotos

The Google Photos capture/diff backend for cutting-garden. Peer leaf of
`cutting_garden_plugins/` — not a nested subpackage. Registered in
`init()` under the single `"gphotos"` URI scheme, in two argument forms:

- opaque       `gphotos:<share-url>` — `gphotos:https://photos.app.goo.gl/X`
  or the bare-host shorthand `gphotos:photos.app.goo.gl/X` (https assumed).
- hierarchical `gphotos://<host>/<path>` — reconstructed as https.

Unlike the yt-dlp plugin it claims **no** bare transport scheme. yt-dlp
already owns `https` exclusively (a second `https` registration would
panic at `init()` via `MustRegisterCapture`), and a Google Photos capture
should be explicit anyway — so the `gphotos:` prefix is always required.
The resolved host must still be a Google Photos host
(`googlePhotosHosts` in `url.go`: `photos.app.goo.gl`,
`photos.google.com`), which keeps the backend from being pointed at a URL
gallery-dl could not extract.

**Restore is intentionally not implemented.** Captured artifacts are
regular files; the filesystem plugin materializes them. See
[FDR 0017](../../docs/features/0017-google-photos-plugin.md) §Restore
Deferral.

## Exec-a-tool, like yt-dlp

This plugin follows the yt-dlp plugin's exec-a-downloader template rather
than the git plugin's pure-Go approach: it shells out to **`gallery-dl`**,
which must be on PATH (the Nix flake wraps cutting-garden binaries so it
is present at install time; devshells get it the same way). Auth /
share-link validity is gallery-dl's responsibility — a private or expired
album surfaces as gallery-dl's non-zero exit, whose stderr tail is wrapped
into the failure.

## What lives here

- `Plugin.CaptureRoot` (`capture.go`) — runs gallery-dl with
  `--write-metadata --directory <tempdir>` into a tempdir, streams every
  produced artifact into the destination blob store as one EntryV1 per
  file (recursing through any album subdirectories), then removes the
  tempdir.
- `Plugin.ScanForDiff` (`diff.go`) — full re-scan: re-download into a
  fresh tempdir and hash every artifact, returning fresh entries for the
  diff command to compare against the receipt. There is **no** cheap
  freshness probe (a Google Photos album has no single canonical metadata
  sidecar to hash); the optimization is deferred — see FDR 0017 §Diff.
- `sourceURLFromArg` (`url.go`) — URL coercion across the two accepted
  argument forms plus host-allowlist enforcement and a leading-`-` guard.
- `runGalleryDL` (`exec.go`) — `os/exec` wrapper that honors
  ctx-cancellation and surfaces the last 4 KiB of stderr on non-zero
  exit. Resolves the binary via `exec.LookPath`.
- Blob streaming is delegated to `internal/plugin_blob_io`'s
  `WriteFileBlob`, shared with the filesystem and yt-dlp plugins.

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than a `…-gphotos-v1`
variant. Google Photos artifacts are captured as regular file entries —
byte-identical EntryV1 shape to fs captures — so a mixed fs+gphotos
group shares one type-tag and restores cleanly through the file plugin.
Same rationale as the yt-dlp and git plugins.

## Adding a host

`googlePhotosHosts` in `url.go` is the single source of truth for which
hosts the plugin accepts. Add a host only after confirming gallery-dl
ships a working extractor for its URL surface.
