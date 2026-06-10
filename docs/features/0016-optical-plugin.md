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
     correction. Audio mode prepends a metadata phase (see below).
3. Streams every produced regular file into the blob store as one
   `EntryV1` (two-pass walk, identical to the yt-dlp plugin: pass 1
   pre-sums sizes, pass 2 streams each file reporting cumulative
   byte-progress).

A non-zero tool exit collapses into a single root-level
`OpPlugin` failure on the argument, carrying the tool's stderr tail.

### Audio metadata: TOC, CDDB, ID3

Before ripping, audio mode runs a "read toc" phase (`audio_meta.go`)
that captures the disc's identity and metadata as sidecar entries in
the same receipt — yt-dlp's `info.json` idiom applied to optical
media:

1. `cdparanoia -Q` reads the table of contents. A TOC failure is
   fatal (an unreadable disc would fail the rip anyway).
2. The classic CDDB/freedb 8-hex-digit disc id is computed locally
   from the TOC — no network needed for disc identity.
3. A best-effort CDDB lookup (freedb HTTP protocol, proto 6/UTF-8)
   resolves album/artist/year/genre and per-track titles. The server
   defaults to gnudb (`http://gnudb.gnudb.org/~cddb/cddb.cgi`) and is
   overridable via `CG_OPTICAL_CDDB_URL`; the literal value `off`
   disables the lookup entirely. Network failure, a no-match, or a
   10-second timeout degrades to TOC-only metadata with a Log line —
   the lookup NEVER fails a capture.
4. Sidecar artifacts land in the rip tempdir, so the post-rip walk
   streams them into the merkle tree as ordinary `EntryV1`s:
   - `disc.toc.json` — device, CDDB disc id, total seconds, per-track
     begin/length sectors + duration, and (when matched) the parsed
     CDDB fields including per-track titles. Compilation-style
     `artist / title` track entries are split per CDDB convention.
   - `disc.cddb` — the raw CDDB read response verbatim, as provenance
     for the parsed fields. Only written on a successful match.
   - `trackNN.id3` — a standalone ID3v2.4 tag blob per track (UTF-8
     text frames: TIT2/TPE1/TALB/TDRC/TCON/TRCK, plus a
     `CDDB_DISC_ID` TXXX frame). With no CDDB match the tag degrades
     to `Track NN` + track position + disc id. The blob is exactly
     the byte shape an encoder prepends to an MP3/FLAC transcode of
     the paired WAV.

CD-Text extraction (disc-resident metadata, no network) is possible
future work; CDDB covers the overwhelming majority of pressed audio
CDs.

### TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than an `…-optical-v1`
variant — the same decision the yt-dlp plugin makes. Optical artifacts
are captured as regular file entries (byte-identical `EntryV1` shape to
fs captures), so a receipt mixing fs and optical roots carries one
type-tag and restores cleanly through the file plugin. `EntryV1.Root`
carries the device path, so origin is still recoverable without a
schema change.

The plugin declares no node types (it does not implement `RootLister`,
see below), so the node-type versioning question FDR 0014 tracks at #79
does not apply here.

## Traversal and config roots: deliberately out

The plugin implements neither `RootLister` (FDR 0014) nor
`RootProvider` (RFC 0007):

- **No `RootLister`.** A device is an atomic, single-rooted capture
  target — unlike a CalDAV endpoint (calendars → objects) there is no
  sub-structure to enumerate without ripping the disc, and the
  traversal contract is lazy structure discovery, not media I/O. Like
  the file plugin, optical simply opts out.
- **No `RootProvider`.** Enumerating drives (`/dev/sr*`) is
  platform-specific discovery, and a drive's interesting state is the
  disc in it, which only capture can see. Configured optical roots
  (e.g. a named drive in `config.toml`) are possible future work if
  `list`/`mcp` users want drives surfaced; until then `optical:` roots
  are CLI-argument-only.

## Restore Deferral

Restore is intentionally not implemented. The produced `.iso` / `.map`
/ `.cdda.wav` files are ordinary files; the filesystem plugin
materializes them on `cutting-garden restore`. Writing bytes back to a
physical optical drive (burning) is a different operation with
different failure modes and hardware assumptions, out of scope here.

This deferral is sound because entries are origin-agnostic: a receipt
mixing fs and optical roots carries the one fs-v1 tag (see TypeTag
reuse), so FDR 0005's scheme/tag match guard restores the whole receipt
through the file plugin without knowing some entries were ripped.

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
