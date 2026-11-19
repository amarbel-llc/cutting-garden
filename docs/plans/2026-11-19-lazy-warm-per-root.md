# Lazy warm per root: what it would change, what to test first

Date: 2026-11-19. Session: cutting-garden/slim-cypress. Status: proposal for
the operator's ruling c-1 (step 1, docs only; nothing built). Numbers are
cited from measurements by circus/plain-aspen on flic and by me on nikulin;
what is theory is marked.

## What exists today

At startup `mcp` computes and caches the facet summary and the enriched
listing of EVERY configured root, then refreshes them on a timer
(`startFacetMaintenance`, internal/mcp/resources.go). `[mcp]
no_background_refresh = [schemes]` (#393) turns that off per SCHEME: such a
root is not warmed, not refreshed on the timer, computed on its first read, and
recomputed by a read once it is 15 minutes old (`facetTTL`,
internal/mcp/lazy_refresh.go). So the lazy mode already exists as config.

## The numbers (PSS of the whole `mcp` process tree)

- flic, real config, 2 runs each, medians (plain-aspen, no client attached):
  A, only smith lazy: 43.6 MB at 30 s, 69.8 MB at 90 s, still growing (the
  tree grew 14.5 and 37.8 MB between 30 s and 90 s in the two runs).
  B, caldav + fastmail + newsblur + org + smith lazy: 36.3 MB at 30 s and at
  90 s, flat.
- B still starts the same child processes (cutting-garden, nebulous, smith).
- nikulin (few or no account roots): cutting-garden 6.3 MB + nebulous 1.7 MB.
- My synthetic check on nikulin (12 tiny identical caldav roots against the
  in-memory test server): no measurable difference between warm and lazy; the
  cost on flic is therefore the DATA the warm builds, not the number of roots.

## What a code change would add over the config (B)

1. Lazy as the default, and per root rather than per scheme. This changes
   nothing for the schemes B already lists: B IS the lazy mode, and it already
   removes the 43.6 -> 69.8 MB growth. Value: no config to carry, and a
   finer choice. Saving over B: about 0 MB for the configured schemes.
2. Nothing in cache warming touches B's 36.3 MB floor. By code reading (not
   measured): the child processes start because `mcpRoots` asks every plugin
   for its roots at startup (`AggregateRoots`, internal/command_components/
   roots.go), which spawns each wire plugin's child to answer. Only a LAZY
   SPAWN of those children attacks the floor. nikulin's nebulous alone is
   1.7 MB and flic's 8.9 MB; the split of the 36.3 MB between cutting-garden,
   nebulous and smith on flic is NOT known yet.
3. Cost of a lazy spawn (design, not just code): resources/list and the tool
   list need each plugin's roots and capabilities (for example mutate_nodes is
   advertised only when a root can be written), which the child answers at
   `initialize`. A lazy spawn would need those declared statically or
   advertised permissively. That is a behavior change a client can see.

## What I would test first (red first, after your confirmation)

1. Read-only, no code: per-process PSS of B's tree on flic (cutting-garden vs
   nebulous vs smith). It decides whether item 2 is worth a design at all; if
   the children are most of the 36.3 MB, it is; if cutting-garden itself is,
   lazy spawn buys little.
2. On nikulin with the in-memory servers (no live account): first-read
   latency of one root, warm versus lazy (`debug-measure-lazy` pattern plus a
   timed list_nodes), so the trade has a number. Expect the lazy first read to
   cost one cold compute (network on flic, which I cannot measure here).
3. Red test for the code change: a fake wire plugin counting its spawns and a
   fake lister counting calls; start the server with the root lazy and assert
   zero spawns and zero lister calls before the first read, then exactly one
   compute on the first read and none on the second.

## Risks

- First-call latency per root: the first read of each lazy root pays the cold
  compute (a PROPFIND per calendar for caldav, a paged listing for newsblur).
- Freshness: a lazy entry is recomputed only by a read past 15 minutes, never
  in the background, so a long-idle root's first read after the TTL is slow
  again.
- A touched root keeps its cache for the life of the process (roots are
  pinned), so a session that browses everything ends at A's size; lazy only
  saves what a session does not touch.
- Behavior visible to clients if children are spawned lazily (item 3 above).
- With the shared daemon (step A) the cache and the children exist once per
  host, so the per-session value of this change shrinks to the sessions that
  keep the in-process fallback.
