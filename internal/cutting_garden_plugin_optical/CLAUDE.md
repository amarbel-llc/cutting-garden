# cutting_garden_plugin_optical

The optical-media capture backend for cutting-garden. Peer leaf of
`cutting_garden_plugins/` — not a nested subpackage. Registered in
`init()` under the single `"optical"` URI scheme
(`optical:/dev/sr0`, optionally `?mode=image|audio`).

**Capture only.** Restore and diff are intentionally not registered;
see [FDR 0016](../../docs/features/0016-optical-plugin.md).

## Two modes, one plugin

- `mode=image` (default) — `ddrescue -b 2048 -r 3 <device> disc.iso
  disc.iso.map`. Images any data disc (CD-ROM/DVD/Blu-ray) into
  `disc.iso` plus its rescue `disc.iso.map`, recovering from read
  errors on scratched media.
- `mode=audio` — `cdparanoia -d <device> -B`. Rips each audio-CD track
  to a separate `trackNN.cdda.wav` with jitter/error correction, after
  a metadata phase that also captures `disc.toc.json` (TOC + CDDB disc
  id + parsed CDDB fields), `disc.cddb` (raw CDDB response, when
  matched), and per-track ID3v2.4 tag blobs (`trackNN.id3`).

## What lives here

- `Plugin.CaptureRoot` (`plugin.go`) — parses the source, runs the
  mode's tool into a tempdir, streams every produced file into the
  destination blob store as one `EntryV1`, then removes the tempdir.
  `walkArtifacts` is the two-pass walk mirrored from the ytdlp plugin
  (pass 1 pre-sums sizes, pass 2 streams with byte-progress).
- `parseSource` / `toolInvocation` (`url.go`) — URL → (device, mode),
  and (device, mode) → (binary, argv). The device path must be
  absolute; a bare-host form (`optical://dev/sr0`) and unknown modes
  are refused with hints.
- `writeAudioMetadata` (`audio_meta.go`) — the audio-mode pre-rip
  phase: `cdparanoia -Q` TOC parse, local CDDB disc-id computation,
  best-effort CDDB lookup, and the metadata sidecar writes. TOC
  failure is fatal; CDDB failure/no-match/disable degrades to
  TOC-only metadata with a Log line.
- `cddbLookup` (`cddb.go`) — two-step freedb-over-HTTP client
  (query → read, proto 6/UTF-8, 10 s timeout). Server defaults to
  gnudb; `CG_OPTICAL_CDDB_URL` overrides it, and the literal `off`
  disables the lookup (tests point it at an httptest server).
- `trackID3` (`id3.go`) — dependency-free ID3v2.4 builder (UTF-8 text
  frames + a `CDDB_DISC_ID` TXXX frame). Empty values produce no
  frame; no-CDDB tags carry `Track NN` fallback titles.
- `runExternal` (`exec.go`) — `os/exec` wrapper that honors
  ctx-cancellation, forwards both stdout and stderr to the reporter's
  `Log`, and surfaces the last 4 KiB of stderr on non-zero exit.
  Resolves the binary via `exec.LookPath`; the Nix flake wraps
  cutting-garden so `ddrescue`/`cdparanoia` are on PATH at install
  time (and in the devshell).
- Blob streaming is delegated to `internal/plugin_blob_io`'s
  `WriteFileBlobProgress`, shared with the filesystem and ytdlp
  plugins.

## TypeTag reuse

`Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
(`cutting_garden-capture_receipt-fs-v1`) rather than an
`…-optical-v1` variant — the same rationale as the ytdlp plugin.
Optical artifacts are captured as regular file entries (byte-identical
`EntryV1` shape to fs captures), so a mixed fs+optical group shares one
type-tag and restores cleanly through the file plugin. `EntryV1.Root`
carries the device path, so origin stays recoverable without a schema
change.

## Validation does not touch the drive

`ValidateSource` parses the URL only. A missing/unreadable device or
an empty drive surfaces at capture time as the ripping tool's own
error (with its stderr tail attached), which keeps validation pure and
the unit tests hardware-free — the round-trip tests install fake
`ddrescue`/`cdparanoia` shims on PATH.
