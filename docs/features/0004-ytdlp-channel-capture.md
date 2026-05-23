---
status: proposed
date: 2026-05-23
promotion-criteria: |
  Promote to `experimental` once channel capture lands and one
  manual end-to-end capture/diff round-trip against a small real
  channel is documented. Promote to `accepted` after a second
  channel-shape consumer (podcast feeds, RSS, etc.) reuses the
  parent-child encoding.
---

# yt-dlp channel capture

## Problem Statement

The yt-dlp plugin (FDR 0003) captures one video per invocation. yt-dlp
itself happily treats channel URLs (`https://www.youtube.com/@channel`,
`/channel/UC…`, `/playlist?list=…`) as playlists-of-videos via
`--flat-playlist`, so the data shape is already available; the plugin
just doesn't fold it in. Users who want to archive a whole channel
today have to drive the existing plugin from a shell loop over IDs,
which gives up the receipt-as-unit-of-archival contract that makes
cutting-garden useful for everything else.

A channel is naturally a **parent node of videos**: one URL identifies
many videos, each of which already maps to the EntryV1-bundle the
plugin produces today. The extension is to encode that parent-child
relationship inside a single receipt instead of N separate ones.

## Proposed Approach

### URL acceptance

No new scheme. The existing `ytdlp:` and `https:` (allowlisted)
acceptance paths take channel URLs verbatim; the plugin detects
"this is a channel/playlist" by running a `--flat-playlist
--print "%(id)s"` probe up front. If the probe yields more than one
ID, it's the channel path; one ID is the single-video path we ship
today (unchanged).

### Receipt shape

EntryV1 carries the parent-child as a path prefix:

```
Root  = <canonical channel URL>
Path  = <video-id>/<artifact-filename>
```

No schema change — `Path` is already a slash-relative string, and
nesting under a video-id directory is the natural encoding. Restore
through the file plugin produces `<dest>/<video-id>/<files…>` for
free.

### Capture

```
cutting-garden capture <store> ytdlp:https://www.youtube.com/@channel
```

1. Probe: `yt-dlp --flat-playlist --print "%(id)s"` enumerates
   video IDs.
2. Per video (sequentially for the MVP — parallelism is an open
   question): run the existing per-video capture into a per-video
   tempdir, prefix each EntryV1 path with `<video-id>/`, accumulate.
3. Per-video failures aggregate into the same sink rather than
   aborting the whole channel — one geo-blocked video shouldn't
   torch a 500-video archive.

### Diff

Two-level freshness probe:

1. **Channel-list probe.** `yt-dlp --flat-playlist --print "%(id)s"`
   produces the current video-ID set. Hash that set and compare to
   the receipt's derived set (the `<video-id>` prefixes of its
   `Path` entries). Same set → channel hasn't grown/shrunk → fall
   through to step 2 per video. Different set → "channel-level
   change" — list the added/removed IDs and stop, or recurse only
   into kept-IDs. (See Open Questions.)
2. **Per-video info.json probe.** For each video still in the set,
   reuse FDR 0003's existing freshness-probe logic
   (`--skip-download --write-info-json`, hash the result, compare to
   receipt's `<video-id>/<id>.info.json` entry). Match → re-emit
   that video's entries verbatim. Miss → rescan just that video.

The MVP can ship with step 1 only (set-comparison) and a "for
content changes, do a fresh capture" footnote; step 2 is the
optimization that makes diff cheap on stable channels.

### Cost knob

A `--ytdlp-limit N` flag on `capture` (or a `since=<receipt-id>` to
re-capture only videos newer than the last known set) is **necessary
for shippability**, not optional. Without it, "capture the
veritasium channel" is a hundreds-of-GB operation that quietly
saturates the user's blob store. The MVP MUST default to either a
small N or refuse to proceed on > some-threshold channels with a
hint pointing at `--ytdlp-limit`.

## Open Questions

- **Default limit value.** 0 (unlimited), 10 (newest few), or refuse
  >100 without explicit opt-in? Lean toward "refuse with hint" so a
  fresh user can't faceplant.
- **Set-change diff semantics.** When videos disappear (taken down,
  unlisted, channel moves), should diff report them as
  *Removed* entries or omit them? Removal is what the user usually
  wants to see, but it shadows the "I want to know if my archive is
  still complete" question.
- **Parallel per-video capture.** yt-dlp itself can parallelize via
  `--concurrent-fragments`; spawning multiple yt-dlp processes is
  also an option. MVP stays serial; parallelism is a follow-up FDR
  if anyone hits the wall.
- **Restore semantics for a channel receipt.** The file plugin
  materializes `<video-id>/<filename>` under dest with no further
  ceremony. Is that the right shape, or do users want
  `<dest>/<channel-id>/<video-id>/<filename>` so multiple channels
  restore side-by-side without collision? Probably the latter when
  multi-channel restore actually happens — easy to add via a top-
  level `Root`-prefix at restore time.

## References

- [FDR 0003](0003-ytdlp-plugin.md) — single-video yt-dlp plugin
  that this extends.
- [FDR 0002](0002-diff.md) — diff contract this builds on.
- `internal/cutting_garden_plugin_ytdlp/CLAUDE.md` — package README.
