# Facet filters: what the host-side residual costs, and when plugin-side predicates (B) pay

cutting-garden#388, operator ruling 2026-11-15 (q65): start with A — `filter=`
is one trellis step, positive single-value equality is pushed down, the rest is
a host-side residual (RFC 0012 §6) — and examine B (plugins evaluate OR /
negation / missing themselves) for the cost of the residual's extra listing.
This note is that comparison. Its figures come from reading the code at
origin/master 225b5fd, not from measurement; each is cited, and "unknown" means
the code was not read far enough to say.

## What A adds

A residual-free filter costs exactly what it cost before. A residual changes
three surfaces (RFC 0012 §6):

- `list_nodes`: no new requests. The pushdown listing is fetched as before and
  the residual is matched in memory.
- `read_facets`: the one-shot `FacetCounts` call is replaced by a fold that
  reads each container's enriched listing under the pushdown. The cost is the
  listing instead of the count, per container.
- `mutate_nodes` sweep: no new requests (it already lists through
  `ListEnriched`, `internal/cutting_garden_plugins/bulk_dispatch.go:258-272`);
  the host re-checks the step before writing.

So the question is per plugin: how much dearer is a listing than a count?

## Per plugin

| plugin | count today | listing under the pushdown | residual summary vs count |
|---|---|---|---|
| ynab | 1 `GET …/transactions` per date window, 60 s cache (`plugins/ynab/client.go:279-305`, `cache.go:11-19`) | the same request and cache key (`listing.go:69`, `client.go:287-291`) | no extra request inside 60 s |
| fastmail | ~4 JMAP method calls per mailbox, 200-thread cap (`plugins/fastmail/threads.go:8-13, 56-100`); the count is memoized by a FacetVersion call (`facet.go:189`) | the same calls (`listing.go:84`), no memo | about the same requests, but the memo no longer applies; the listing does not report the 200-thread cap (`listing.go:113`), so a residual count may be silently partial — needs a fix under A |
| org | a Depth-1 PROPFIND per folder, no GETs when warm (`plugins/org/facet.go:184`, `cache.go:140-157`) | no `ListEnriched`; `ListRoots` of a file adds a Depth-0 PROPFIND confirmation per file per call (`traversal.go:48-79`) | +1 PROPFIND per file folded over |
| caldav | 1 PROPFIND + 1 full-data REPORT per component per calendar, memoized by ctag (`plugins/caldav/facet.go:156, 219-258, 273-274`) | windowed VEVENT expand REPORT + an hrefs-only REPORT + full VTODO/VJOURNAL REPORTs (`listing.go:147-219`, `expand.go:62-112`); declines at a calendar-home (`listing.go:168`) | about +1 REPORT per calendar and the ctag memo lost; at a calendar-home a fan-out over every calendar; VEVENT coverage shrinks from all events to the listing's -1d/+30d window, so the residual count is partial where the plain count was complete |
| jira | ceil(N/100) search pages per project, filter applied in memory (`plugins/jira/facet.go:133-157`, `client.go:22-25`) | the same paging with the filter pushed into JQL and a larger field set (`listing.go:62, 87`) | at worst the count's own cost, with bigger pages |
| file | a local walk | local | no remote cost |
| git, ytdlp | unknown (not read) | unknown | unknown |
| wire plugins | whatever the peer does (`internal/traversal_serve/adapter.go:803`) | likewise | unknown per peer |

## Reading it

- **A costs nothing measurable** for ynab and file, and little for fastmail and
  jira.
- **A is dearest for caldav**, and not only in requests: a residual count over
  events covers a date window where the plain count covers everything. A
  caller who writes `^status=cancelled` gets a partial count where
  `status=confirmed` gets a complete one. RFC 0012 §6 makes A report it
  partial rather than wrong, but the asymmetry is visible.
- **org** pays one confirmation PROPFIND per file. That is cheap on the
  operator's Orgzly folder, but it grows with the number of files.

## B, where it would go first

B is the reserved pushdown extension of RFC 0012 §6: an OPTIONAL capability
by which a plugin declares the predicate shapes it can evaluate itself.
Nothing in A forecloses it. `FacetFilter` stays the positive-equality pushdown,
and the host lowers more only to a plugin that declares more.

1. **caldav**, the case that motivates B. Its count already fetches every
   object of the component (`facet.go:273-280`) and filters in memory, so
   negation, alternatives and missing would be free there. That restores the
   complete count and the ctag memo.
2. **jira**: JQL can express value lists, negation and emptiness natively (`IN`,
   `NOT IN`, `IS EMPTY`). This is general knowledge of JQL, not verified against
   this plugin's query builder (`listing.go:62`). A JQL pushdown would shrink
   the pages too.
3. **org**: its count already holds every headline's facets in memory, so the
   same in-memory evaluation applies.
4. **ynab and fastmail** gain nothing from B.

Cost of B per plugin: a predicate evaluator over its in-memory facets (shared
host code can supply it, since `FacetFilter`'s matcher already exists),
plus conformance vectors proving it agrees with the host's residual on the
same nodes, including no-value aliases and their implications (#413).

## Recommendation

Ship A as ruled. Then file B as the next slice for caldav first, and decide
jira and org on demand. Fix fastmail's listing so it reports its 200-thread cap
as partial, either now or when its residual is first used.
