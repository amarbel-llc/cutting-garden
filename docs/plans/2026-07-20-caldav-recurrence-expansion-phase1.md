# caldav recurrence expansion — Phase 1 investigation (STOPPED, no Phase 2 code)

Date: 2026-07-20. Status: **investigation complete, implementation NOT started**.
Tracking: #176 (`dtstart`-band rejected in favor of window expansion), #177
(port list from Fastmail's calendar API, item 2 = recurrence expansion).

## Outcome

This investigation stopped at Phase 1 by design. No traversal/listing/facet
code was changed. The two governing questions from the brief:

- **(b) capture/restore identity** — clean. Confirmed isolated today, but
  isolation is a property of *where* future code lands, not an intrinsic
  guarantee (see below).
- **(a) server-side `<expand>` support** — **not resolved**. Could not be
  verified empirically in this session (see "What blocked empirical
  verification" below), and available indirect evidence leans toward "not
  reliably honored by Fastmail," which is the plugin's primary real-world
  target. Absent that verification, committing to a client-side RRULE
  expansion engine — even the narrower "hybrid" shape described below — is a
  real, open-ended cost that the task brief explicitly gates behind proving
  server non-cooperation first. That gate is not met either way (neither
  proven to work nor proven not to), so the responsible action is to report
  the cost rather than absorb it speculatively.

## (a) Does the server support expansion?

**Confirmed by direct code reading (this worktree):**

- `plugins/caldav/client.go` sends three hand-rolled REPORT XML templates —
  `calendarQuery` (`client.go:237-248`), `calendarHrefQuery`
  (`client.go:300-310`), `calendarEtagUIDQuery` (`client.go:323-340`). None
  contains `<C:expand>` or `<C:time-range>`. A repo-wide `rg -i expand
  plugins/caldav/` returns zero matches (excluding the unrelated FDR 0014
  "receipt expansion" planner term).
- `plugins/caldav/caldavtestserver/server.go`'s `report` handler
  (`server.go:196-247`) only sniffs the requested component type
  (`VEVENT`/`VTODO`/`VJOURNAL`) from the REPORT body and returns every
  seeded resource of that type verbatim. It does not implement time-range
  filtering or `<expand>` — by design, per its header comment
  (`server.go:1-18`): "only enough of RFC 4791 for the plugin's
  capture/diff/restore round-trip."
- `due_band` (RFC 0012 §11.3, the shipped precedent for a time-relative
  facet) is **100% client-side**: `facet.go:319-329` fetches all VTODOs via
  the plain component-filtered REPORT and `dueBandOf` (`facet.go:369-397`)
  buckets each against `time.Now()` in Go. There is no server-side
  time-range REPORT anywhere in this codebase today, for any facet.
- RFC 0011 (`docs/rfcs/0011-caldav-archive-binding.md`) does not mention
  RRULE, RECURRENCE-ID, DTEND, DURATION, or `time-range`/`expand` anywhere,
  and does not pin an exact REPORT XML shape (only prose describing the
  diff freshness probe's `getetag`+UID-projection REPORT, §Diff lines
  243-275).
- `RECURRENCE-ID` is parsed and round-tripped by `plugins/caldav/ical`
  (`ical/event.go:26,173-174,237-238`) but consumed nowhere else — not in
  facets, not in listing, not in diff identity. Recurring-instance handling
  is greenfield in this codebase.

**What blocked empirical verification against the real target server:**

This session has authenticated read access to a live Fastmail CalDAV
account (via an already-running cutting-garden MCP deployment) sufficient
to confirm that `RRULE: FREQ=WEEKLY` events exist and surface today as a
single node carrying their original (multi-year-old) `DTSTART` — consistent
with "no expansion happens anywhere in the current pipeline." That MCP
surface, however, only exposes the plugin's existing `list_nodes`/
`read_node`/`read_facets` abstractions, not raw REPORT construction — it
cannot be used to test whether `<C:expand>` or `<C:time-range>` changes the
server's response, because the plugin never sends either.

Constructing an independent authenticated HTTP request (to hand-craft a
REPORT with `<expand>` against `caldav.fastmail.com` directly) would have
required reading stored CalDAV credentials from local config. That action
was blocked by the environment's permission classifier, and per its own
guidance the correct response is to stop rather than work around it — this
is exactly the right call for credential material, not merely a technicality
to route around. This investigation reports that gap honestly rather than
using indirect evidence to declare confidence it doesn't have.

**Indirect evidence (not verified by this session, cited as read):** a
`python-caldav` bug report against Fastmail specifically found
`date_search(expand=False)` and `date_search(expand=True)` returning
identical results for a recurring event — the signature of `<expand>` being
silently ignored — and that library's own Fastmail compatibility notes are
currently commented out / unlisted as actively tested. RFC 4791 does not
require `<expand>` support and provides no capability-discovery mechanism,
so a server ignoring it is fully conformant, not a bug on Fastmail's part.

**The "hybrid" alternative, and why it doesn't resolve the gate either:**
RFC 4791 §7.4 separately requires a server to internally expand recurrences
in order to evaluate a bare `<C:time-range>` *filter* correctly — a
different, more load-bearing MUST than §9.6.5's optional `<expand>` *output*
behavior. In principle, a `calendar-query` with `<time-range>` and no
`<expand>` could return the correct set of recurring masters whose
occurrences intersect a window, server-side, even if `<expand>` itself is
ignored — turning client-side work into "expand only the masters the server
already told us are relevant" rather than "expand everything." This is
strictly cheaper than full-calendar client-side expansion, but:

1. It is unverified for Fastmail specifically — same empirical block as
   `<expand>` itself, since this codebase sends no `<time-range>` filter
   today either (see `due_band` note above).
2. It still requires a real, if bounded, client-side RRULE expander
   (`FREQ`/`INTERVAL`/`BYDAY`/`COUNT`/`UNTIL`/`EXDATE` + `RECURRENCE-ID`
   overrides) to turn a matched master into occurrence instants within the
   window. That is exactly the class of work the task brief gates behind
   "Phase 1 proves the server won't cooperate — and if it comes to that,
   STOP and report first so the cost can be weighed." Phase 1 did not prove
   either outcome, so building it now would be committing that cost on a
   guess, not a decision.

## (b) Capture/restore/diff identity — the stop condition

**Clean, with a scoping caveat.** Traced all six top-level capture/diff/
restore entry points end to end:

| Function | File:line | Object enumeration |
|---|---|---|
| `Plugin.CaptureRoot` (fs-v1) | `capture.go:25` | `discoverCalendars` + `listResources` |
| `Plugin.CaptureProtocol` (RFC 0011) | `protocol.go:24` → `storeObjects` (`protocol.go:83`) | `discoverCalendars` + `listResources` |
| `Plugin.ScanForDiff` (fs-v1) | `diff.go:18` | `discoverCalendars` + `listResources` |
| `Plugin.DiffProtocol` (RFC 0011) | `diff_protocol.go:35` | `discoverCalendars` + `listObjectEtags` |
| `Plugin.Restore` (fs-v1) | `restore.go:22` | none — PUTs receipt entries by stored path |
| `Plugin.RestoreProtocol` (RFC 0011) | `restore_protocol.go:29` | `discoverCalendars` (destinations only) + receipt refs |
| `Plugin.ListRoots` (traversal) | `traversal.go:45` → `objectNodes` (`:89`) | `discoverCalendars` + `listObjectHrefs` |
| `Plugin.ListEnriched` (facets) | `listing.go:82` → `enrichedCalendarNodes` (`:120`) | `discoverCalendars` + `listResources` |

None of the six capture/diff/restore functions call `ListRoots`,
`ListEnriched`, `objectNodes`, or `enrichedCalendarNodes` — confirmed by
direct text search across `capture.go`, `protocol.go`, `diff.go`,
`diff_protocol.go`, `restore.go`, `restore_protocol.go`. They independently
re-derive their object sets from `client`'s own low-level HTTP methods and
build their own `resource`/`probedObject`/`capturedObject` types straight
off REPORT/GET responses — they never consume `Node` values at all. This
matches `plugins/caldav/AGENTS.md:100-104`'s own architecture note that the
*only* shared function is `discoverCalendars` (calendar-collection
granularity, never per-object).

**The caveat that must hold for any future Phase 2:** this isolation is
real today, but it is a property of *where* expansion logic is written, not
an architectural wall. `discoverCalendars` and the low-level `client`
methods (`listResources`, `listObjectHrefs`, `listObjectEtags`,
`getResource`) are the shared surface. If a future implementation added
recurrence expansion *inside* those shared methods (rather than confined to
`objectNodes`/`enrichedCalendarNodes` and new facet code in `traversal.go`/
`listing.go`), it would leak synthetic occurrence nodes into capture, diff,
and restore, which would then treat them as real objects with no backing
href/etag/blob — corrupting receipt identity exactly as the brief warns.
**Any Phase 2 implementation must keep expansion strictly inside the
traversal/listing layer and never modify the shared client methods'
existing contracts.**

## (c) Addressing model, if/when Phase 2 proceeds

Today, node URIs *are* the literal resolved server href, with zero
indirection: `caldavURIForAbs` (`traversal.go:123-136`) maps
`https://host/path` → `caldav://host/path`; `read_node`
(`leaf.go:44-81`) and mutation (`mutate.go:303-313`, `clientForNode`)
resolve the URI straight back to that href and GET/PUT/DELETE it directly
— no lookup table, no UID index.

Proposed shape for a derived occurrence, not yet implemented: keep the
**base path as the real master href** (so it always resolves to something
that actually exists on the server) and encode the occurrence as a
suffix, e.g.

```
caldav://host/collection/UID.ics?recurrence-id=<RFC5545-DATE-TIME>
```

- `read_node` on this URI would GET the real master `.ics` (unchanged
  fetch path) and then, in memory, project the specific occurrence — either
  by finding a `RECURRENCE-ID`-matching override component already stored
  as a separate resource, or by locating that instant via the (not yet
  written) RRULE expander.
- Mutation entry points (`mutate.go`'s `CreateNode`/`PutNode`/`PatchNode`/
  `DeleteNode`, all routed through `clientForNode`) must explicitly detect
  the `recurrence-id` suffix and refuse with a clear error
  ("cannot mutate a derived recurrence occurrence — edit the series or the
  override object directly") rather than resolving the suffix away and
  silently PUTting/DELETEing the master. This satisfies the brief's
  "refuse clearly rather than guess" requirement for the out-of-scope
  edit-this-instance-vs-series problem.
- A node-model statement (RFC 0012/FDR 0014-level, per the brief) is
  required before this ships, since it introduces a URI class that does
  not correspond 1:1 with a stored server object — this doc is the
  precursor note, not that statement.

## What was deliberately NOT done

- No RRULE expansion engine (client-side or hybrid) was written.
- No change to `traversal.go`, `listing.go`, `facet.go`, or any other
  plugin file.
- No new URI scheme, no derived-node handling, no mutation refusal guard.
- No live-wire probe against `caldav.fastmail.com` beyond the read-only
  `list_nodes`/`read_node` calls already available through the existing
  plugin surface (no credential access, no raw REPORT construction).

## Recommended next step

Before any Phase 2 work: get a real answer to whether `<C:time-range>`
(with or without `<expand>`) narrows results server-side against Fastmail,
via a channel that isn't blocked by credential-access policy — e.g. the
repo maintainer running a one-off probe with their own credentials outside
an automated session, or a deliberately-scoped, explicitly-approved live
test lane (there is no existing precedent for one in this repo; see
`zz-tests_bats/caldav.bats`, which always uses the in-memory
`cmd/cutting-garden-caldav-testserver` fake, never a real account). If that
comes back negative, the RRULE-engine cost from the brief's "STOP and
report" gate should be scoped and weighed explicitly, not absorbed inside
a follow-on task silently.
