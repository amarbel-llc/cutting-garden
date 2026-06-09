---
status: proposed
date: 2026-06-09
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture against a real disc (one data disc via
  ddrescue, one audio CD via cdparanoia) is documented. Promote to
  `accepted` after a release ships with the plugin in the default
  binary.
---

# optical-media plugin

## Problem Statement

Cutting-garden captures filesystem trees, URLs (yt-dlp), git
repositories, and CalDAV calendars. Physical optical media — data
CDs/DVDs/Blu-rays and audio CDs — sits outside that surface. The only
workaround is to rip a disc manually with `ddrescue` or `cdparanoia`
into a directory and then `cutting-garden capture` that directory,
which mixes archival intent into ad-hoc filesystem layout and loses
the source device as an organizing key.

The plugin registry sizes for exactly this extension: a non-fs capture
source registers under its own URI scheme, returns the same
`capture_receipt.EntryV1` shape the file plugin returns, and slots
into the existing `capture` dispatch loop without command-side
changes.

This FDR specifies that plugin: `cutting-garden capture` learns to
route `optical:` arguments through `ddrescue` (data discs) or
`cdparanoia` (audio CDs), which image/rip the disc into the configured
blob store under the same receipt shape as the filesystem plugin.

## Interface

### Accepted argument forms

The plugin claims the single `optical` URI scheme:

1. `optical:/dev/sr0` — image the device with ddrescue (default
   `mode=image`).
2. `optical:/dev/sr0?mode=image` — explicit ddrescue imaging.
3. `optical:/dev/sr0?mode=audio` — rip audio-CD tracks with cdparanoia.
4. `optical:///dev/sr0` — host-empty triple-slash form, equivalent to
   form 1.

The device path MUST be absolute. A bare-host form (`optical://dev/sr0`)
is refused with a hint — the device is a path, not a host. An unknown
`mode` is refused with a hint listing the two valid values.

`ValidateSource` parses the URL only; it does NOT touch the drive. A
missing/unreadable device or an empty drive surfaces at capture time
as the ripping tool's own error, with its stderr tail attached.

### Capture

For each `optical:` root the plugin:

1. Parses the device path and mode.
2. Runs the mode's tool into a tempdir (`cmd.Dir`):
   - **image** — `ddrescue -b 2048 -r 3 <device> disc.iso disc.iso.map`.
     2048-byte blocks match the optical data-sector size; three retry
     passes recover marginal sectors. The `disc.iso.map` rescue map is
     captured alongside the image as provenance — it records which
     sectors were recovered vs. still bad.
   - **audio** — `cdparanoia -d <device> -B`. Batch mode writes one
     `trackNN.cdda.wav` per track using cdparanoia's jitter/error
     correction.
3. Streams every produced regular file into the blob store as one
   `EntryV1` (two-pass walk, identical to the yt-dlp plugin: pass 1
   pre-sums sizes, pass 2 streams each file reporting cumulative
   byte-progress).

A non-zero tool exit collapses into a single root-level
`OpPlugin` failure on the argument, carrying the tool's stderr tail.

### TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than an `…-optical-v1`
variant — the same decision the yt-dlp plugin makes. Optical artifacts
are captured as regular file entries (byte-identical `EntryV1` shape to
fs captures), so a receipt mixing fs and optical roots carries one
type-tag and restores cleanly through the file plugin. `EntryV1.Root`
carries the device path, so origin is still recoverable without a
schema change.

## Restore Deferral

Restore is intentionally not implemented. The produced `.iso` / `.map`
/ `.cdda.wav` files are ordinary files; the filesystem plugin
materializes them on `cutting-garden restore`. Writing bytes back to a
physical optical drive (burning) is a different operation with
different failure modes and hardware assumptions, out of scope here.

## Diff Deferral

Diff is intentionally not implemented either. Unlike yt-dlp's cheap
`info.json` freshness probe, the only way to re-derive an optical
disc's content hash is to re-read the entire disc — minutes of I/O,
and the disc may not even be in the drive. `cutting-garden diff
optical:…` therefore reports an unknown-scheme error by design.

## Runtime dependency

The plugin resolves `ddrescue` and `cdparanoia` via `exec.LookPath`.
The Nix flake wraps the installed `cutting-garden`/`cg` binaries with
both tools on PATH (`wrapProgram … --prefix PATH`), and the devshell
carries them so `go run ./cmd/cutting-garden capture optical:…`
behaves identically to a nix-built invocation.
