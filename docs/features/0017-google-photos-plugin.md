---
status: proposed
date: 2026-06-06
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture/diff round-trip against a real Google Photos
  share URL is documented. Promote to `accepted` after a milestone ships
  with the plugin in the default binary and gallery-dl in the runtime
  closure.
---

# Google Photos plugin

## Problem Statement

Cutting-garden captures filesystem trees and, via the yt-dlp and git
plugins, two classes of URL-addressable source. Google Photos albums —
the most common place non-technical users accumulate irreplaceable
photos and videos — sit outside that surface. The only workaround is to
download an album manually (`gallery-dl`, the Takeout flow, etc.) into a
directory and then `cutting-garden capture` that directory, which mixes
archival intent into ad-hoc filesystem layout and loses the share URL as
an organizing key.

The plugin registry was sized for exactly this extension: a non-fs
capture source registers under its own URI scheme, returns the same
`capture_receipt.EntryV1` shape the file plugin returns, and slots into
the existing `capture` and `diff` dispatch loops without command-side
changes. This FDR specifies that plugin: `cutting-garden capture` and
`cutting-garden diff` learn to route `gphotos:` arguments through
`gallery-dl`, which downloads the album media plus metadata sidecars into
the configured blob store under the filesystem receipt shape.

## Interface

### Accepted argument forms

The plugin claims the single `gphotos` URI scheme and accepts two
argument shapes:

1. `gphotos:<share-url>` — opaque form. The inner URL may be
   fully-qualified (`gphotos:https://photos.app.goo.gl/X`) or a bare-host
   shorthand (`gphotos:photos.app.goo.gl/X`, in which case https is
   assumed). Preferred form.
2. `gphotos://<host>/<path>` — hierarchical form. Reconstructed as
   `https://<host>/<path>` before being handed to gallery-dl.

The resolved host MUST be in `googlePhotosHosts` (`url.go`):
`photos.app.goo.gl`, `photos.google.com`. Any other host is refused with
a hint listing the accepted hosts. The resolved URL is also guarded
against a leading `-` so it cannot be misread as a gallery-dl option.

Unlike the yt-dlp plugin, this plugin does **not** claim the bare `https`
scheme. yt-dlp already owns `https` exclusively (a second registration
panics at `init()` via `MustRegisterCapture`), and a Google Photos
capture should be explicit anyway — so the `gphotos:` prefix is always
required. The host allowlist is therefore not about avoiding silent
grabs of arbitrary `https:` arguments; it is a guard against pointing the
Google Photos backend at a URL gallery-dl could not extract.

### Capture

```
cutting-garden capture gphotos:https://photos.app.goo.gl/AbCdEf123
```

For each `gphotos` argument, the plugin:

1. Creates a per-invocation tempdir.
2. Runs `gallery-dl --quiet --write-metadata --directory <tempdir> --
   <source-url>`. `--directory` pins a flat output location so the
   artifact walk does not depend on gallery-dl's per-extractor directory
   templates; `--write-metadata` emits a `<file>.json` sidecar beside
   each item.
3. Walks the tempdir (recursing through any album subdirectories),
   streams every regular file into the destination blob store, and emits
   one `capture_receipt.EntryV1{Type: TypeFile, Root: <canonical source
   url>, Path: <relative filename>, BlobId: <markl>, Size: …, Mode}` per
   artifact.
4. Removes the tempdir.

A non-zero gallery-dl exit collapses into a single `sink.Failure(rawArg,
err)` carrying the last 4 KiB of gallery-dl's stderr; the surrounding
capture command's failure counter increments by 1 and the rest of the
capture continues per the existing `internal/capture/capture.go`
contract.

### Diff

```
cutting-garden diff <receipt-id> gphotos:https://photos.app.goo.gl/AbCdEf123
```

The plugin always does a **full re-scan**: re-download into a fresh
tempdir, hash every artifact through the receipt's source-store hash
family (discard-store wrapper, the same trick the file plugin's
`ScanForDiff` uses), and return fresh entries for the diff comparator.

This is deliberately simpler than the yt-dlp plugin's two-step freshness
probe. yt-dlp fronts a cheap `--skip-download --write-info-json` probe
because a single video maps to a single canonical metadata sidecar whose
content fingerprint disambiguates the version. A Google Photos album has
no single canonical sidecar — a freshness probe would have to enumerate
every item's per-photo metadata anyway, which is most of the cost of the
download. The lighter probe is **deferred** until album sizes make the
re-download cost material; the receipt shape needs no change to add it
later (per-item metadata blob-ids are already in the receipt).

### Receipt shape

EntryV1 records are byte-identical to filesystem-plugin entries:

- `Type` = `"file"` for every artifact (media and metadata sidecars).
- `Root` = the canonical https URL (post URL-coercion).
- `Path` = the filename relative to the per-invocation tempdir.
- `BlobId`, `Size`, `Mode` = standard.

The receipt's `TypeTag` stays `cutting_garden-capture_receipt-fs-v1` —
see [the package README](../../internal/cutting_garden_plugin_googlephotos/CLAUDE.md)
for the reuse rationale.

## Restore Deferral

Restore is intentionally **out of scope** for this FDR.

The captured artifacts are regular files; restoring them with
`cutting-garden restore <receipt-id> <fs-dest>` works today through the
file plugin, which materializes each entry as an ordinary file under
`<fs-dest>`. The user gets a directory containing the album's media and
metadata — exactly what `gallery-dl` would have produced.

What `MustRegisterRestore` for `gphotos` would buy is restoring **into
Google Photos**: re-uploading the album to the source. That requires
authenticated write access to the Google Photos API and is out of scope;
no concrete need has been identified. The plugin's `init()` deliberately
omits the `MustRegisterRestore` call.

## Traversal and config roots: deliberately out

The plugin implements neither `RootLister` (FDR 0014) nor
`RootProvider` (RFC 0007):

- **No `RootLister`.** A share URL is a single album — one capture root.
  An album does have enumerable structure (its photos), but enumerating
  it requires a gallery-dl run, and the traversal contract is lazy
  structure discovery, not a full extractor invocation. FDR 0014 names
  exactly this shape of remote-folder enumeration (its Google Drive
  example) as deferred; an album-listing traversal would ride that
  decision, not precede it.
- **No `RootProvider`.** Share URLs are pasted, not configured; there is
  no account or endpoint to enumerate from `config.toml`. Whole-library
  capture (which WOULD have a configured account) is a separate FDR —
  see Open Questions.

## gallery-dl runtime dependency

The plugin shells out to `gallery-dl` via `os/exec` and locates the
binary through `exec.LookPath`. The Nix flake adds `gallery-dl` to the
cutting-garden derivation's runtime closure and wraps the installed
binaries so it is on PATH for `nix run` / `./result/bin/cutting-garden`
invocations; the devshell carries it the same way. Outside Nix the
binary is the user's responsibility; a clear error is produced on
`exec.LookPath` failure with a hint to enter the devshell.

Auth / share-link validity is gallery-dl's responsibility. A private or
expired album surfaces as gallery-dl's non-zero exit, whose stderr tail
is wrapped into the failure — cutting-garden does not plumb cookies or
login state itself.

## Open Questions

- **Album metadata as a freshness key.** Deferring the diff probe means
  every diff re-downloads. If album sizes make that painful before a
  proper probe lands, an interim option is to hash only the metadata
  sidecars (`--no-download --write-metadata`) and compare the set. Worth
  a follow-up if the friction matters.
- **Host allowlist drift.** Google Photos share-link host surfaces are
  small and stable today (`photos.app.goo.gl`, `photos.google.com`), but
  a new host would need a code change. Config-driven allowlists are
  deferred until we hit one — same posture as the yt-dlp plugin.
- **Library (non-share) capture.** This plugin handles share URLs only.
  Whole-library capture needs OAuth and a different tool (rclone's
  gphotos backend, gphotos-sync); it is a separate FDR if a need appears.

## References

- `internal/cutting_garden_plugin_googlephotos/CLAUDE.md` — package README.
- `internal/cutting_garden_plugins/CLAUDE.md` — registry contract.
- [FDR 0003](0003-ytdlp-plugin.md) — the exec-a-downloader template this
  plugin follows (and why this one does not claim `https`).
- [FDR 0006](0006-git-plugin.md) — the single-scheme, opt-in-prefix
  precedent.
- [RFC 0001](../rfcs/0001-capture-restore-rules.md) — operational rules;
  gphotos roots are exempt from the §Root Scoping PWD-confinement rule
  because they're URLs, not paths.
