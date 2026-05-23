---
status: proposed
date: 2026-05-22
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture/diff round-trip against a real YouTube
  URL is documented. Promote to `accepted` after the v0.7.0 milestone
  ships with the plugin in the default binary.
---

# yt-dlp plugin

## Problem Statement

Cutting-garden today captures only filesystem trees. Audio/video
artifacts that live behind URLs — YouTube videos, podcast episodes,
streaming-platform recordings — sit outside that surface, even though
the content-addressable blob model is a natural fit for archiving
them. The only workaround is to download manually with `yt-dlp` into
a directory and then `cutting-garden capture` that directory, which
mixes the user's archival intent into ad-hoc filesystem layout and
loses the source URL as an organizing key.

The plugin registry introduced in Phase 2 was sized for exactly this
extension: a non-fs capture source registers under its own URI
scheme, returns the same `capture_receipt.EntryV1` shape the file
plugin returns, and slots into the existing `capture` and `diff`
dispatch loops without command-side changes.

This FDR specifies that plugin: `cutting-garden capture` and
`cutting-garden diff` learn to route URL arguments through `yt-dlp`,
which downloads (or freshness-probes) the media plus its sidecars
into the configured blob store under the same receipt shape as the
filesystem plugin.

## Interface

### Accepted argument forms

The plugin claims two URI schemes and accepts three argument shapes:

1. `ytdlp:<source-url>` — opaque form. The inner URL is preserved
   verbatim including any `?query` / `#fragment`. Preferred form.
2. `ytdlp://<host>/<path>` — hierarchical form. Reconstructed as
   `https://<host>/<path>` before being handed to yt-dlp.
3. `https://<youtube-host>/...` — direct pass-through. The host
   MUST be one of:
     - `youtu.be`
     - `youtube.com`
     - `www.youtube.com`
     - `m.youtube.com`
     - `music.youtube.com`

   Any other `https` host is refused with a hint pointing at the
   `ytdlp:` prefix form for non-YouTube yt-dlp-supported sites.

### Capture

```
cutting-garden capture ytdlp:https://youtu.be/dQw4w9WgXcQ
```

For each yt-dlp argument, the plugin:

1. Creates a per-invocation tempdir.
2. Runs `yt-dlp --no-progress --no-warnings --write-info-json
   --write-thumbnail --write-subs -o <tempdir>/%(id)s.%(ext)s --
   <source-url>`.
3. Walks the tempdir, streams every regular file into the destination
   blob store, and emits one `capture_receipt.EntryV1{Type:
   TypeFile, Root: <canonical source url>, Path: <relative
   filename>, BlobId: <markl>, Size: …, Mode: 0o644-ish}` per
   artifact.
4. Removes the tempdir.

A non-zero yt-dlp exit collapses into a single `sink.Failure(rawArg,
err)` carrying the last 4 KiB of yt-dlp's stderr; the surrounding
capture command's failure counter increments by 1 and the rest of
the capture continues per the existing
`internal/capture/capture.go` contract.

### Diff (freshness probe)

```
cutting-garden diff <receipt-id> ytdlp:https://youtu.be/dQw4w9WgXcQ
```

Full re-download per diff is wasteful for video URLs; the network
cost dwarfs the few-KiB info.json that already disambiguates a video
version. The plugin therefore uses a two-step strategy:

1. Run `yt-dlp --skip-download --write-info-json …` to fetch only
   the metadata sidecar.
2. Hash the resulting `.info.json` through the receipt's source-store
   hash family (discard-store wrapper, same trick the file plugin's
   `ScanForDiff` uses).
3. Compare against the receipt's `.info.json` entry's blob-id:
     - **Match** → re-emit the receipt's entries verbatim. The
       comparator reports zero differences without any media bytes
       being transferred.
     - **Miss** → fall back to a full yt-dlp invocation (the
       `--skip-download` flag dropped) and return fresh-hashed
       entries; the comparator localizes the difference per
       artifact.

This is the closest match to the ETag intent the registry's HTTP
peers eventually want. yt-dlp itself does not expose HTTP ETags
directly, and the info.json content fingerprint is more reliable
across CDN edge caches anyway.

### Receipt shape

EntryV1 records are byte-identical to filesystem-plugin entries:

- `Type` = `"file"` for every artifact (media, info.json, thumbnail,
  subtitles).
- `Root` = the canonical https URL (post URL-coercion).
- `Path` = the filename relative to the per-invocation tempdir
  (e.g. `dQw4w9WgXcQ.mp4`).
- `BlobId`, `Size`, `Mode` = standard.

The receipt's `TypeTag` stays `cutting_garden-capture_receipt-fs-v1`
— see [the package README](../../internal/cutting_garden_plugin_ytdlp/CLAUDE.md)
for the reuse rationale.

### Single-root collapse interaction

`internal/capture/capture.go` collapses every entry's `Root` to `"."`
when its capture group has exactly one root. A single-URL ytdlp
capture is therefore indistinguishable from a single-directory fs
capture at the receipt level, which is fine for restore (the file
plugin handles both) but means the diff side cannot always recover
the original source URL from the receipt alone. The diff plugin
trusts the user-supplied `<dir>` argument as the source URL for
collapsed receipts. Multi-root receipts preserve distinct `Root`
values and are matched directly.

## Restore Deferral

Restore is intentionally **out of scope** for this FDR.

The captured artifacts are regular files; restoring them with
`cutting-garden restore <receipt-id> <fs-dest>` works today through
the file plugin, which materializes each entry as an ordinary file
under `<fs-dest>`. The user gets a directory containing the media,
info.json, thumbnail, and subtitles — exactly what they would get
from running `yt-dlp` manually.

What `MustRegisterRestore` for `ytdlp` would buy is restoring **into
the URL**: re-uploading or re-downloading at the source. Neither
operation is implementable against the public YouTube/etc. surface,
and no other concrete need has been identified.

A future FDR can revisit this — e.g. if a plugin grows that wants
"materialize from receipt into a fresh yt-dlp working tree shape"
semantics distinct from the file plugin's default — but until then,
the plugin's `init()` deliberately omits the
`MustRegisterRestore` call.

## yt-dlp runtime dependency

The plugin shells out to `yt-dlp` via `os/exec` and locates the
binary through `exec.LookPath`. The Nix flake adds `pkgs.yt-dlp` to
the cutting-garden derivation's runtime closure and wraps the
installed binaries so yt-dlp is on PATH for `nix run` /
`./result/bin/cutting-garden` invocations. The devshell carries
yt-dlp the same way.

Outside Nix the binary is the user's responsibility; a clear error
is produced on `exec.LookPath` failure with a hint to enter the
devshell.

## Future host-routing layer for `https`

This plugin claims the `https` scheme outright. Registry registration
panics on duplicate scheme claims
(`cutting_garden_plugins.MustRegisterCapture`), so no other plugin can
also claim `https` while ytdlp is linked into the binary. The YouTube
host allowlist in `sourceURLFromArg` is the single point that decides
which `https://` URLs are accepted; anything outside the allowlist is
refused with a hint to use the explicit `ytdlp:` prefix.

This is sufficient for Phase 2 — yt-dlp is the only non-fs source
we plan to ship — but a second `https`-capable plugin (e.g. a generic
HTTP-archive backend, a Bandcamp-specific scraper, a podcast feed
ingester) would collide at init-time. Resolving that without
sacrificing the strict registry contract requires a layer above the
scheme-keyed plugin map.

The shape we expect to grow into when a second `https` consumer
appears:

1. **Host-router plugin.** A dedicated plugin claims `https` and
   maintains an ordered list of `(host-matcher, downstream-plugin)`
   rules. `Schemes()` returns `["https"]`; `ValidateSource` /
   `CaptureRoot` / `ScanForDiff` dispatch to the matched downstream
   plugin or refuse with a routing error.
2. **Sub-registration interface.** Downstream plugins (yt-dlp, the
   future generic-https, etc.) register their host matchers with
   the host-router instead of claiming `https` directly. The yt-dlp
   plugin's `init()` becomes
   `host_routing.MustRegisterHTTPS(youtubeHostMatcher, p)` rather
   than `cutting_garden_plugins.MustRegisterCapture(p)` for the
   https scheme; the ytdlp-scheme registration stays.
3. **Match-order policy.** Most specific match wins; ties refuse
   with a config error. A trailing wildcard "generic" plugin is
   allowed but logged on startup so it's visible.

Until then the ordering question doesn't exist — there's exactly
one `https` plugin — and the allowlist is a static map. When the
router lands, the YouTube allowlist becomes that plugin's host
matcher and the migration is mechanical.

The host-routing layer is a future FDR; this section is the
placeholder until that lands.

## Open Questions

- **Allowlist drift.** YouTube's host surface is small and stable
  today, but if Shorts/Live/etc. spawn new hostnames the
  allowlist needs an update. Should the allowlist be config-driven
  rather than compiled in? Deferred until we hit one.
- **Sidecar opt-out.** Some users will want only the media file
  (e.g. archive-only, no thumbnails). A `--ytdlp-no-sidecars` flag
  on `capture` would cover that but is out of scope here; the
  default is media+sidecars.
- **`http://` parity.** Not accepted today. yt-dlp will follow
  redirects to https for most sites anyway; explicit http acceptance
  can be added when a user-facing need appears.

## References

- `internal/cutting_garden_plugin_ytdlp/CLAUDE.md` — package README.
- `internal/cutting_garden_plugins/CLAUDE.md` — registry contract.
- `internal/cutting_garden_plugin_file/CLAUDE.md` — filesystem
  reference plugin (mirror of the layout/file shapes used here).
- [FDR 0001](0001-restore.md), [FDR 0002](0002-diff.md) — capture/
  restore/diff feature docs.
- [RFC 0001](../rfcs/0001-capture-restore-rules.md) — operational
  rules; ytdlp roots are exempt from the §Root Scoping
  PWD-confinement rule because they're URLs, not paths.
