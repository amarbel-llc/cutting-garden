# MCP tool ergonomics — triage and order of work

Date: 2026-11-05. Session: cutting-garden/rapid-pine.

Sixteen open issues about the MCP tool surface (list_nodes, read_node,
read_facets, mutate_nodes, describe_node_types) as agents use it, ranked by
the value an agent gets per token we spend. The org-plugin issues (#383–#386)
and organize (RFC 0020) are out of scope here.

## Tier 1 — small, self-contained, done in this order

| # | Issue | Change | Notes |
|---|---|---|---|
| 1 | #302 + #188 (host half) | A successful `mutate_nodes` drops the mcp server's own listing and facet cache entries on the anchor's scheme+host, so the next read through the same server recomputes. | Root cause on the cutting-garden side: `listingCache.serve` returns any cached entry with no read-time token check; only the background `maintain` tick revalidates, and a write never invalidates. This is #188's mechanism (4), host-side, for same-server writes only; plugin-internal caches (nebulous, smith#71) and freshness reporting stay open on #188. #302 closes; #188 gets a progress note in the commit, not a close. |
| 2 | #382 (item 1) | **Needs a ruling; moved to tier 3.** Defaulting a statusless VTODO's status facet to `needs-action` was tried and reverted: organize keeps statusless tasks in their own section on purpose (moving one into a bucket WRITES the STATUS, `organize_fields_missing_status_*`), and a defaulted facet makes them claim a status they do not have. The fix must reach them from the filter side without inventing a facet value: a "missing" predicate (#388) or a `none` bucket that organize knows not to write. | Items 2–4 are filed: compact mode under #367, #388 (OR/negation/missing predicates), #387 (set-delta sweeps), #389 (absent fields as null). |
| 3 | #155 | `health` gets a FACETS column and a `facets` JSON field (the declared dimensions). | Self-contained in `internal/health`. |
| 4 | #170 (remaining option 3) | A filtered `list_nodes` that could not apply its filter (`filterMode: none`, typically at an account root whose children are calendars) carries a `hint` naming the two tools that answer it: `read_facets` with the same filter (`byContainer`) and a trellis `query` (`->> …`). | Options 1 and 2 shipped (trellis query, byContainer). A hint rather than an error, so the honest-unfiltered contract (RFC 0012 §6) keeps its meaning. |
| 5 | #175, #181 | Verify-and-close. #175: the read_facets description already says a filter narrows byContainer; add the same sentence and a one-line example to the server instructions string if it is missing. #181: RFC 0018 `uriTemplate` covers (a); check whether the container-mismatch error names an accepting container, and if not, make the create refusal list the types' `uriTemplate`/container hint. | |

## Tier 2 — medium, one commit each, after tier 1

| # | Issue | Change | Notes |
|---|---|---|---|
| 6 | #319 | Best-effort UDP statsd counters in `mutate_nodes` (calls; invalid JSON body; JSON string wrapping JSON; non-object JSON), tagged by scheme, no-op without STATSD_HOST. | Copy dodder's `mcp_dodder/statsd.go` pattern. Whether krone's unit gets STATSD_HOST is a circus question; it goes to the coordinator, not done here. |
| 7 | #323 | mutate_nodes accepts several groups. Proposed rules: groups dispatch in order and independently; temp ids are scoped to their group; `atomicity` is per group (no cross-group atomicity); the call is an error result only when no group applied anything. | Rules proposed here. Proceeding with them unless the operator rules otherwise. |
| 8 | #177 (item 3) | More caldav listing fields (location, url, duration, participants). | Cheap `ListingFieldsDescriber` addition. The relative-date and recurrence halves wait for #176. |

## Tier 3 — needs a design record or a ruling before code

| # | Issue | Why it waits |
|---|---|---|
| #376 | `ignored` per outcome | Wire change to RFC 0013 `node.patch` and RFC 0017 outcomes (optional member). The issue asks for a proposal first, and two semantic questions need a ruling: does a body of only-ignored fields stay `patched_nothing`, and is there a strict mode? I can draft the RFC amendment. |
| #174 | inline facet labels | Changes `FacetHistogram`'s value shape or adds a parallel shape; RFC 0012 §3 mergeability. Needs the operator to pick option 1, 2 or 3. Option 3 (add an inline `labelled` view beside the bare keys) is additive and my recommendation. |
| #321 | temp ids inside bodies | Needs a substitution syntax: an RFC 0017 / FDR 0020 amendment. |
| #322 | temp ids in atomic batches | Needs a plugin able to resolve refs inside a transaction; none is identified. Lowest value per token. |
| #367 | default-slim listing | The issue itself asks for an inventory first; that inventory is a docs task worth doing after tier 1, before any shape change (bats vectors and fj-cg depend on today's shape). |
| #176, #177 (items 1–2) | event time windows, recurrence | A plan exists (`2026-07-20-caldav-recurrence-expansion-phase1.md`): calendar-query with time-range + expand, derived occurrence nodes. Large; identity and capture/restore constraints. Not started in this pass. |

## Shared changes

- #302 and #188 share the cache-invalidation change.
- #170 and #175 both touch tool text and the server instructions string;
  they go in one commit if #175 needs any change.
- #382 item 2 and #367 are the same request (a compact listing); handle them
  under #367.
