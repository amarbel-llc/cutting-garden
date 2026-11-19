# Lazy warm per root: what it would change, what to test first

Date: 2026-11-19. Status: proposal (docs only; nothing built). Numbers are
ranges from measurements on two hosts with different configs; what is theory
is marked.

## What exists today

At startup `mcp` computes and caches the facet summary and the enriched
listing of EVERY configured root, then refreshes them on a timer
(`startFacetMaintenance`, internal/mcp/resources.go). `[mcp]
no_background_refresh = [schemes]` (#393) turns that off per SCHEME: such a
root is not warmed, not refreshed on the timer, computed on its first read, and
recomputed by a read once it is 15 minutes old (`facetTTL`,
internal/mcp/lazy_refresh.go). So the lazy mode already exists as config.

## The numbers (PSS of the whole `mcp` process tree)

- A host with a real config (several account roots), two runs each, medians,
  no client attached:
  A, only one scheme lazy: roughly 44 MB at 30 s and 70 MB at 90 s, still
  growing (the tree grew 15 and 38 MB between 30 s and 90 s in the two runs).
  B, every account-bearing scheme lazy: roughly 36 MB at 30 s and at 90 s,
  flat.
- B still starts the same child processes (cutting-garden and the
  wire-plugin children).
- A host with few or no account roots: single-digit MB for cutting-garden and
  about 2 MB for one wire-plugin child.
- A synthetic check (12 tiny identical caldav roots against the in-memory test
  server): no measurable difference between warm and lazy; the cost on the
  real-config host is therefore the DATA the warm builds, not the number of
  roots.

## What a code change would add over the config (B)

1. Lazy as the default, and per root rather than per scheme. This changes
   nothing for the schemes B already lists: B IS the lazy mode, and it already
   removes the 44 -> 70 MB growth. Value: no config to carry, and a finer
   choice. Saving over B: about 0 MB for the configured schemes.
2. Nothing in cache warming touches B's floor. By code reading (not
   measured): the child processes start because `mcpRoots` asks every plugin
   for its roots at startup (`AggregateRoots`, internal/command_components/
   roots.go), which spawns each wire plugin's child to answer. Only a LAZY
   SPAWN of those children attacks the floor. The split of B's floor between
   cutting-garden and the children on the real-config host is NOT known yet.
3. Cost of a lazy spawn (design, not just code): resources/list and the tool
   list need each plugin's roots and capabilities (for example mutate_nodes is
   advertised only when a root can be written), which the child answers at
   `initialize`. A lazy spawn would need those declared statically or
   advertised permissively. That is a behavior change a client can see.

## What I would test first (red first)

1. Read-only, no code: per-process PSS of B's tree (cutting-garden vs each
   wire-plugin child). It decides whether item 2 is worth a design at all; if
   the children are most of the floor, it is; if cutting-garden itself is,
   lazy spawn buys little.
2. With the in-memory servers (no live account): first-read latency of one
   root, warm versus lazy (`debug-measure-lazy` pattern plus a timed
   list_nodes), so the trade has a number. Expect the lazy first read to cost
   one cold compute (network time for a live account, which an in-memory
   server cannot show).
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
