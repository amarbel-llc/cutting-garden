---
status: experimental
date: 2026-05-23
revised: 2026-07-18 (channel capture, traversal, and facets landed —
  cutting-garden#145; see the Implementation Note)
promotion-criteria: |
  Promote to `testing` once one manual end-to-end capture/diff
  round-trip against a small real channel is documented and diff's
  two-level freshness probe (still unimplemented — see Implementation
  Note) lands. Promote to `accepted` after a second channel-shape
  consumer (podcast feeds, RSS, etc.) reuses the parent-child
  encoding.
---

# yt-dlp channel capture

## Implementation Note (2026-07-18, cutting-garden#145)

Channel/playlist capture, traversal (FDR 0014), and facets (RFC 0012)
landed together, built the way FDR 0014's caldav reference ended up:
capture, `ListRoots`, and `FacetCounts` all share ONE enumeration
primitive (`probeFlatPlaylist`, `plugins/ytdlp/flatplaylist.go`) rather
than each re-deriving the tree. A few corrections to this document's
original proposal, found while implementing against yt-dlp's actual
`--flat-playlist` behavior (verified directly against yt-dlp's
`YoutubeTabIE._extract_video` source, not just its docs):

- **`upload_date` is NOT populated by default in flat-playlist mode at
  all** — yt-dlp's own `--flat-playlist` help text: "some entry
  metadata may be missing". The implementation always passes
  `--extractor-args youtubetab:approximate_date`, which makes yt-dlp
  parse an APPROXIMATE date out of the listing's relative "N years
  ago" text (yt-dlp's own caveat: "This may cause date-based filters
  to be slightly off"). Non-YouTube extractors ignore that
  youtubetab-namespaced arg, so upload_date stays absent for them —
  handled by simply omitting the year/month facet for those entries,
  never a synthetic value.
- **`duration` IS populated in flat mode** for ordinary videos (parsed
  straight off the listing page's length badge) but is absent/null for
  live streams, premieres, and some Shorts — same graceful-absence
  handling.
- **`--ytdlp-limit` shipped as a URL query parameter
  (`?cg-ytdlp-limit=N`), not a new `capture` CLI flag.**
  `CaptureRootRequest` has no per-plugin options field, and adding one
  is a cross-cutting SDK change out of scope here; a query parameter on
  the source URL is this codebase's existing precedent for a
  plugin-specific knob (the optical plugin's `?mode=`). `N=0` opts
  into unlimited; with no parameter, a channel above
  `defaultChannelCaptureThreshold` (25) refuses with a hint rather than
  silently downloading — the "refuse-with-hint" posture this
  document's Open Questions leaned toward.
- **The `--print "%(id)s"` probe this document proposed is `--dump-json`
  instead**, since the framework needs title/uploader/duration/
  upload_date for traversal and facets, not just ids — FDR 0014 and
  RFC 0012 postdate this document and require richer per-entry data
  than the original one-field probe.
- **Traversal and facets were built in from day one** (not deferred, as
  this document originally implied by predating FDR 0014/RFC 0012): a
  channel/playlist is a `ytdlp-channel-v1` container node; each video is
  a `ytdlp-video-v1` leaf, addressable and independently capturable —
  its `Node.URI` is the flat entry's own canonical `https://` URL,
  which round-trips through the plugin's existing single-video
  `ValidateSource` allowlist unchanged. Facet dimensions: `uploader`
  (categorical), `year`/`month` (open numeric-bucket, only when
  upload_date is present), `duration_band` (closed short/medium/long).
  `FacetCounts` is implemented directly (a one-shot fold over the same
  probe) rather than relying on a framework fold, because no
  framework-fold consumer exists yet (`internal/list/list.go`'s
  `runFacets` requires `FacetCounter` directly) — the same honest
  fallback caldav and jira use.
- **Diff's two-level freshness probe (channel-list + per-video) is
  UNIMPLEMENTED.** `ScanForDiff` is unchanged from FDR 0003 (single
  entry per invocation); diffing a channel receipt is not yet wired to
  the flat-playlist primitive. Tracked as follow-up work before
  promoting past `experimental`.
- **Restore semantics for a channel receipt are unresolved** (this
  document's last Open Question) — restore still routes through the
  file plugin unchanged, which materializes `<dest>/<video-id>/<files…>`
  with no channel-level prefix, exactly as this document's MVP
  proposal.

See `plugins/ytdlp/flatplaylist.go`, `plugins/ytdlp/capture.go`
(`captureChannel`), `plugins/ytdlp/traversal.go`, and
`plugins/ytdlp/facet.go`.

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
- [FDR 0014](0014-plugin-root-traversal.md) — the `RootLister`
  traversal primitive the channel/video tree implements; ytdlp is its
  second implementer after caldav, reusing the "capture and
  ListRoots share one enumeration primitive" resolution FDR 0014
  itself credits to this document's original one-source-to-many
  question.
- [RFC 0012](../rfcs/0012-plugin-facet-contract.md) — the facet
  contract the `uploader`/`year`/`month`/`duration_band` dimensions
  implement.
- `plugins/ytdlp/AGENTS.md` — package README.
