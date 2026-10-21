---
status: experimental
date: 2026-10-20
promotion-criteria: |
  PROMOTED to `experimental` 2026-10-20: slice 1 landed (cutting-garden
  5be3473) and the first live read-only check passed on krone's MCP origin
  (see Slicing, "Live check").
  Criterion, as written: promote to `experimental` once slice 1 lands: `plugins/ynab/` reads a real
  YNAB budget over the REST API, declares the node types below, and
  `cg list`/`cg mcp` traverse `account -> budget -> accounts / categories /
  transactions` with the read-only facets rolling up and drilling down, pinned
  by an in-memory testserver and a bats lane. Promote to `testing` once a
  live-budget dry run matches the synthetic fixture.
---

# YNAB plugin

Tracks cutting-garden#328 (read path), #331 (categorize/memo/cleared writes)
and #333 (payees: a `payees/` section, rename, and repointing a transaction's
payee).

## Problem Statement

A YNAB (You Need A Budget) budget is a small relational tree (budgets,
accounts, category groups, categories, transactions) that cutting-garden
cannot browse. The plugin makes a YNAB account a `list` / `mcp` /
trellis substrate (read-only apart from a transaction's category, cleared state,
memo and payee, and a payee's name; see Writes), so questions like "what did I spend per category last
month" or "which transactions are uncleared" run through the same facet and
query surfaces as caldav, fastmail and jira.

## Interface

### Scope and API

The plugin speaks YNAB's REST API (`https://api.ynab.com/v1`) with a personal
access token as a bearer credential. Slice 1 was **read-only**; slice 2 (Writes)
adds three transaction fields, and #333 a fourth (payee) plus the payee rename.
There is no capture/restore/diff. Wire facts, checked 2026-10-20 against api.ynab.com and
the published OpenAPI spec (ynab-sdk-js `open_api_spec.yaml`):

- YNAB's current docs rename budgets to **plans** (`/plans/{plan_id}`), but the
  older `/v1/budgets/{id}/...` paths were observed working live on 2026-10-20
  (a month of one real budget, 246 transactions, came back in a single
  response; `last-used` is accepted as the id). The client keeps the path
  prefix in ONE constant, defaulting to `/budgets` (live-verified), so a
  future removal is a one-line change. The node type keeps the name `budget`
  (the app's user-facing term).
- Observed transaction keys (live, 2026-10-20): `id`, `date`, `amount`
  (signed integer milliunits), `amount_formatted`, `amount_currency`, `memo`,
  `cleared` (`cleared` | `uncleared` | `reconciled`), `approved`, `flag_color`
  (null | blue | green | purple | red), `flag_name`, `account_id`,
  `account_name`, `payee_id`, `payee_name`, `category_id`, `category_name`,
  `transfer_account_id`, `transfer_transaction_id` (set on about 19% of one
  month's rows: transfers), `matched_transaction_id`, `import_id`,
  `import_payee_name`, `import_payee_name_original`, `debt_transaction_type`
  (null | `payment` | `balanceAdjustment`), `deleted`, `subtransactions` (an
  array on every row). Which `|null` fields can truly be null is unverified.
  The response is `{"data": {"server_knowledge", "transactions": [...]}}`.
- A split's `subtransactions` element (one real split observed; the OpenAPI
  spec agrees that a split's `category_name` is the literal `Split`): `id`,
  `transaction_id`, `amount`, `amount_formatted`, `amount_currency`, `memo`,
  `payee_id`, `payee_name`, `category_id`, `category_name`,
  `transfer_account_id`, `transfer_transaction_id`, `deleted`. Whether a split
  leg can be a transfer is unconfirmed.
- `GET /budgets?include_accounts=true` (one request) returns `data.budgets[]`
  plus `data.default_budget`; a budget has `id`, `name`, `last_modified_on`,
  `first_month`, `last_month`, `date_format` (`format`), `currency_format`
  (`iso_code`, `example_format`, `decimal_digits`, `decimal_separator`,
  `symbol_first`, `group_separator`, `currency_symbol`, `display_symbol`) and,
  with the flag, `accounts[]`. Nine budgets came back for the observed
  account, at least one untouched for over a year (archived budgets are
  ordinary entries, so the listing shows them). An account has `id`, `name`,
  `type`, `on_budget`, `closed`, `note`, `balance`/`cleared_balance`/
  `uncleared_balance` (each with `_formatted` and `_currency`),
  `transfer_payee_id`, `direct_import_linked`, `direct_import_in_error`,
  `last_reconciled_at`, `debt_*` objects and `deleted`. Accounts therefore
  ride on the budgets request and need no extra call.
- Amounts are signed integer *milliunits* (1000 = one currency unit); responses
  also carry `..._formatted` and `..._currency` fields (since API v1.82.0).
- Rate limit: **200 requests per hour per token**, a rolling window; over it the
  API answers `429`.
- **There is no page/limit/cursor parameter anywhere.** The transaction
  endpoints (`/plans/{id}/transactions`, and the per-account, per-category,
  per-payee and per-month variants) page only by date: `since_date` (default
  *one year ago* when omitted) and `until_date`, plus `type=uncategorized|unapproved`.
  Pending transactions are excluded.
- Delta requests (`last_knowledge_of_server` in, `server_knowledge` out) exist
  on plan, accounts, categories, months, payees, scheduled transactions and
  transactions.

### Scheme, credentials, addressing

The plugin claims the single `ynab` scheme. As with fastmail the URL "host"
slot names a **config account**, not a server (the API host is fixed). Accounts
are `[[ynab.accounts]]` entries; the token resolves through the shared
`config_common.Account.Password()` (env var, piggy `pass`, ...), so the secret
never lives in the config file. An account MAY set `session_url` to replace the
API base, which is how the bats lane points `ynab://test/` at an in-memory
testserver.

    ynab:                                         all configured accounts' roots
    ynab://personal/                              the account's budgets
    ynab://personal/<budget>/                     one budget
    ynab://personal/<budget>/accounts/<account>   one account
    ynab://personal/<budget>/categories/<group>/<category>
    ynab://personal/<budget>/transactions/<id>    one transaction (leaf, JSON)
    ynab://personal/<budget>/payees/<id>          one payee (leaf, JSON); payees/ lists them

`<budget>`, `<account>`, `<group>` and `<category>` are name segments; a name
that is not unique under its parent is disambiguated by the YNAB id as a
`?id=` query (the fastmail precedent: classification stays network-free). A
payee is addressed by its **id**, not its name: payee names are free text, often
repeated, and a rename changes them, so the URI of a payee just renamed must
stay valid. Its node name is the payee's name.

### Node types

Hyphenated and horizontally versioned (FDR 0018):

| Tag (`cutting_garden-ynab-...`) | Kind | Holds |
|---|---|---|
| `budget-v1` | container | budget attributes (id, name, currency, last_modified) + `accounts/`, `categories/`, `transactions/`, `payees/` |
| `account-v1` | container | type, balance, cleared balance, closed; its transactions |
| `category-group-v1` | container | group attributes; its categories |
| `category-v1` | container | budgeted, activity, balance for the current month; its transactions |
| `transaction-v1` | leaf | the transaction as JSON (amount in milliunits and formatted, payee, memo, cleared, approved, flag) |
| `payee-v1` | leaf | the payee as JSON (id, name, `transfer_account_id` for a transfer payee, deleted); renamable |

A payee node carries no facets or listing fields: a listing is a flat list of
names, and the payee dimension on transactions is what groups by payee.

### Facets (`category`, `cleared` and `payee` writable, see Writes)

On `transaction-v1`, declared once through the unified field-codec model
(FDR 0025, `unified.go`) and derived into the facet surface: `date` (the ISO
day, so `date=(month)` coarsens by prefix), `account`, `category` (the name;
`Split` for a split parent, `uncategorized` for none), `payee` (open and
high-cardinality, capped at the top 20), `cleared` (closed: cleared /
uncleared / reconciled), `approved` (yes/no), `flag` (the colour, absent when
unflagged), `transfer` (yes/no, from `transfer_account_id`, so transfers can be
excluded from spending queries) and `amount_band` (closed numeric bucket of the
ABSOLUTE amount in whole units: `under_10`, `10_to_50`, `50_to_200`,
`200_to_1000`, `1000_and_over`). Listing fields: date, account, category,
payee, cleared, amount (formatted) and memo. Facets are declared and counted
only on the three transaction-bearing nodes (the budget-wide section, one
account, one category); a budget, section, group or leaf reports ok=false and
the framework falls back.

### Paging: a date window, pushed down from the filter

YNAB has no paging, only date bounds, so the plugin's "pages" are **date
windows**. A transaction listing defaults to the last 30 days
(`since_date = today - 30d`, the API's own default being a year, which is too
heavy for a first listing). A caller pages by naming a window, not a cursor:
a `date` filter at year, month or day grain (`--filter date=2026-07`, or the
trellis equivalent; several date predicates intersect) is pushed down to
`since_date`/`until_date`, so each month is one
request and older history is reachable page by page. A summary computed over
the default window reports `complete: false` and says which window it covers;
a month filter reports `complete: true` for that month. The per-account,
per-category and per-month endpoints serve a drill-down (`accounts/<a>`,
`categories/<g>/<c>`) in one request instead of filtering the whole plan.
Because each window is a separate request against a 200/hour budget, windows
are cached per process (below) and a window fetch is never repeated inside a
session unless revalidated.

A node URI can carry the window itself: `?date=2026`, `?date=2026-07` or
`?date=2026-07-15` on a transactions, account or category URI. It is the
counterpart of the `date` filter for consumers that select by URI and cannot
push a filter down: `organize`'s anchor (`ynab://acct/Budget/transactions/?date=2026-07`)
is one, because a `date=` query predicate on an organize selection is not
pushed to the plugin. It intersects any `date` filter. A malformed value, or one
on a node with no transactions, is a 400.

### Writes (slice 2, cutting-garden#331)

`PatchNode` on a transaction accepts exactly `category`, `cleared` and `memo`
and sends one bulk `PATCH /budgets/{id}/transactions` (partial update by id).

- `category` is a category id, `Group/Category`, or a bare name that is unique
  across the budget; an unknown or ambiguous name is refused with the
  candidates, never guessed. `null`, empty and non-string values are refused.
- `cleared` is `cleared` or `uncleared`. Setting `reconciled`, or changing a
  reconciled transaction, is refused: reconciliation is a deliberate act in YNAB.
- `memo` is at most 200 characters; `null` clears it.
- Split parents (detected by their legs; their categories live there) and
  transfers are refused. Transfers to off-budget accounts, which YNAB does
  categorize, are refused too (conservative).
- Hidden and deleted categories are never a write target. A bare name shared by
  two groups is refused as ambiguous; an organize heading carries only the bare
  name, so such categories need a direct patch with `Group/Category` or the id.
- The transaction is read fresh from the server before the write (the guards
  must not see a cached copy); the memo cap of 200 characters is checked locally.
- A patch that changes nothing sends no request and applies nothing; an unknown
  key costs no request. The response is checked against what was asked, and the
  budget's cached transactions are invalidated after a write.

Organize drives the same path through the derived facet writes: moving a line
under another `category=` heading re-categorizes the transaction, a `cleared=`
heading changes its cleared state, and the `uncategorized` bucket is not a
valid target. Pinned by `write_test.go` and, through the real binary,
`zz-tests_bats/ynab.bats` (a patch, two refusals, an organize round trip).

**Live check (2026-10-21, krone, cutting-garden d63ffa2).** One memo, one
category and one cleared-state patch were each applied to a real, unreconciled
transaction, read back, and restored to the original value (the restored
transaction's raw bytes matched the originals). That settles that a personal
access token accepts writes.

### Payees (#333)

Payees are the second thing the plugin can write, and the first design point is
what YNAB's API does and does not offer (read from the changelog and ynab-sdk-js
2026-10-21): payees can be **listed, read, created and renamed**; a transaction
update takes a `payee_id`. There is **no merge**, and it is not documented what a
rename onto an existing payee's name does. So the plugin renames and repoints,
and never merges or creates.

**The `payees/` section.** A budget's live payees, each a `payee-v1` leaf
addressed by id. Transfer payees (the stand-ins YNAB keeps for "the other
account" of a transfer, `transfer_account_id` set) are listed and readable but
read-only.

**Renaming a payee.** `PatchNode` on a payee node accepts exactly `name` and
sends one `PATCH /budgets/{id}/payees/{payee_id}` (`{"payee": {"name": …}}`).

- An empty or blank name, a non-string, or a transfer payee is refused.
- A name another live payee already holds (compared case-insensitively and
  trimmed) is refused with that payee named, and the refusal says merging is
  done in YNAB. Since a rename onto a held name might or might not merge, the
  plugin does not ask. A deleted payee does not hold its name; a case-only
  change to a payee's OWN name is allowed.
- The payee list is read fresh before the write (the guards must see names as
  they are now), so a rename costs two requests: the list and the write. The
  response is checked for the new name. A rename that changes nothing sends
  nothing. The budget's cached payees AND transactions are invalidated after it,
  because transactions carry their payee's name.

**Repointing a transaction's payee.** `PatchNode` on a transaction also accepts
`payee`, the name or id of an EXISTING payee, sent as `payee_id`.

- It is never sent as a `payee_name`: for a name that matches nothing, YNAB
  creates a payee, so a typo in a bulk edit would fork one. An unknown name is
  refused ("this plugin does not create payees"); a name two payees share is
  refused with their ids.
- A transfer payee is refused as a target, and a transfer transaction's payee is
  not changed: its payee is the other account, and repointing it would break the
  transfer. A transaction cannot be left without a payee, so `null`, empty and
  non-string values are refused.
- The target is resolved from a payee list read fresh (a cleanup runs alongside
  YNAB's own UI, where merges happen, so a payee merged away or made a moment ago
  must not be resolved from a cached list). A repoint therefore costs three
  requests: the transaction, the payee list and the write.
- It rides the same single bulk request as the other fields, and a reconciled
  transaction's payee is not locked (only its cleared state is).

`payee` is also a writable facet: moving a line under another `payee=` heading
repoints the transaction, which makes "fold the redundant payee into its twin" one
organize pass. The twin's heading has to be written into the document when it has
no transactions in the selection. An empty bucket is not a valid target.

### Bulk writes (#334)

The plugin is a best-effort `BulkMutator` (RFC 0017), so a `mutate_nodes`
changeset of N transaction patches costs a handful of requests, where N single
patches cost about three EACH: one fresh listing that serves the targets, the
category tree when an op names a category, one payee list when any op names a
payee, and ONE bulk `PATCH`. It is the same write as `PatchNode`, not a looser
one: every op is parsed and planned by the single-patch machinery against a
FRESH read of its transaction, so each keeps its guards (reconciled, split,
transfer, unknown or ambiguous category or payee) and its own outcome. A refused
op is a `failed` entry and the others still go ahead; a patch that changes
nothing is `patched-nothing`; a patch whose effect the saved transaction does not
show is a failure.

- The targets are read fresh with ONE budget-wide listing, not one request each,
  when there are at least three of them or two are in a still-fresh cached view.
  The window is the default 30 days, widened back to the oldest cached target's
  date but never past a year (YNAB's own default); the cached dates only choose
  the window, and the rows that feed the guards are the listing's, never the
  cached ones. A target the listing does not return (older, or absent) is read by
  id, so the cost is one request plus one per straggler. With fewer than three
  uncached targets they are simply read by id.
- Atomic requests are rejected (`ErrBulkAtomicUnsupported`): YNAB documents no
  all-or-nothing guarantee for the bulk update.
- Several patches of one transaction merge into one entry, as sequential patches
  would; two that set the same field to different values conflict and the later
  one fails (the bulk update has no defined order for one id). The same
  transaction reached through two spellings of its budget (a name and an id) is
  caught after the budget is resolved, so it can never be written by two
  requests. A payee rename is one request each in the API and goes through
  `PatchNode` inside the changeset. Create, put and delete are refused per op.
- A request that fails as a whole fails every op of that budget's batch.
- A sweep resolves its matches with the plugin's own `ListEnriched` and batches
  the template over them, and is refused (never widened) where the enriched
  listing declines.

This helps `mutate_nodes` callers only. `organize` applies each move with its own
`PatchNode` and does not use `BulkMutate` (cutting-garden#339), so an organize
pass still pays the per-patch cost until that lands.

### Caching and rate limit

As built (`cache.go`): the budget list, a budget's accounts and category tree,
and each transaction window are served from memory for a **60-second
freshness window**, keyed by config account plus API base. A transaction that
appears in any fresh cached listing of its budget is read from it, so reading
what a listing just showed costs **no** request even when the budget is
addressed by name; one the listing did not cover costs one fetch, then none.

This is a heuristic with a staleness cost: data edited in the YNAB app is
invisible for up to a minute. The right boundary is one host call, which needs
the SDK convention proposed in cutting-garden#311 (the org plugin has the same
trade-off); this plugin moves to it when it exists.

The first design revalidated with `last_knowledge_of_server` and published the
`server_knowledge` value as a `FacetVersioner` token. It was dropped: a delta
request still spends a rate-limit unit per revalidation, which is the cost the
cache exists to avoid, and YNAB offers no cheaper change token. The plugin
therefore does not implement `FacetVersioner`, and the framework's TTL applies.

## Limitations

- Writes are limited to four fields on a plain transaction (category, cleared,
  memo, payee) and a payee's name (see Writes and Payees). Creating, replacing
  and deleting nodes are refused. `mutate_nodes` batches transaction patches
  through the plugin's `BulkMutator` (see Bulk writes), but an organize that
  moves N lines still sends N transaction `PATCH` requests, each preceded by a
  fresh read of its transaction (and, for a payee move, a fresh payee list),
  against the 200/hour limit, because organize does not use `BulkMutate`
  (cutting-garden#339).
- Payees cannot be merged, created or deleted from here (YNAB's API has no
  merge). Renaming a payee onto a name another holds is refused.
- Scheduled transactions, months and goals are not nodes.
- Split transactions surface as one node with a `subtransactions` array; the
  parent's category is `Split`, so category facets count the parent once under
  `Split`, not the legs.
- Amounts are the account's currency; the plugin does not convert.

## Slicing

- **Slice 1a (landed with this record):** config section, URI classification
  and minting, `Roots`/`RootLabels`/`RootNames`; no network. The plugin is
  registered but does not yet satisfy the SDK's root interfaces (they embed
  `RootLister`), so it does not surface in no-argument roots.
- **Slice 1b (landed):** the REST client (`client.go`), an in-memory
  testserver (`plugins/ynab/ynabtestserver`; a `cmd/` binary for a bats lane
  is slice 1c), `ListRoots` traversal for every node kind, name/id resolution
  with `?id=` tiebreaks, and the transaction leaf read. Pinned by Go tests that
  also assert the request economy: an account-root listing costs one request,
  a budget's section listing none, and a budget id or `last-used` skips the
  name lookup. The plugin now satisfies the SDK root interfaces and surfaces
  in no-argument roots. Every listing re-fetches (no cache yet); the
  `server_knowledge` delta cache is slice 1c.
- **Slice 1b review fixes (`/code-review high`, 2026-10-20):** a split's leg
  on the per-category endpoint (a *hybrid* row with its own id and a
  `parent_transaction_id`) now addresses the readable parent transaction;
  a duplicate category-group name is minted as the group's id *segment* (its
  `?id=` slot belongs to the category below it); a budget listing confirms the
  budget exists (one request for a name, none for an id or alias); a budget
  named like an alias (`default`, `last-used`) or an id is minted by id;
  `session_url` over plaintext http is refused unless the host is loopback
  (the bearer token goes to it on every request); error excerpts cut on a rune
  boundary and response bodies are capped at 64 MiB.
- **Slice 1c-1 (landed): the cache.** Reading one transaction under a named
  budget used to cost two requests and a named account drill-down three, so a
  consumer reading each listed transaction spent `2N` of the 200-per-hour
  budget. A leaf read after a listing now costs none; pinned by Go tests for
  repeat listings, listed and unlisted leaf reads, expiry at the window edge,
  and stale-then-fresh behaviour.
- **Slice 1c-2 (landed): facets and the date-window pushdown.** The unified
  field declaration, `DescribeFacets`, listing fields, `ListEnriched` and
  `FacetCounts`, with a `date` predicate pushed down to
  `since_date`/`until_date` (`window.go`). A default-window summary reports
  complete=false; an explicit window is complete for that span unless the
  `payee` cap dropped values. Two legs of one split in the same category list
  as one node (their shared parent) but count twice.
- **Slice 1c-3 (landed): the harness.** `cmd/cutting-garden-ynab-testserver`
  (the fixture in `ynabtestserver/fixture.go`, fixed 2026 dates so vectors never
  drift with the clock), `zz-tests_bats/lib/ynab.bash` and
  `zz-tests_bats/ynab.bats` (13 cases over the real binary then: roots,
  sections, drill-downs, the month and year pushdown, completeness, a rejected
  token; 26 now, with the write and payee lanes), and the `flake.nix` wiring
  (`CG_TEST_YNAB`).
- **Slice 1c review fixes (`/code-review high`, 2026-10-20):** the listing and
  the counting path now share ONE set (`visibleTransactions`), so a summary
  always totals the nodes the listing shows: rows are re-checked against the
  window locally (the date predicate is dropped once pushed down, so a server
  that ignored a bound could not make a month's summary cover later months while
  claiming completeness), a split's legs in one category are one node in both,
  and a hybrid leg with no parent id is skipped (its own id is unreadable). A
  contradictory window (`date=2026` plus `date=2025-12`) is a complete, empty
  answer with no request. A budget chosen by `?id=` mints its sections under the
  id. The `category` listing field says `uncategorized` like the facet; an
  amount without `amount_formatted` is shown as plain units, never raw
  milliunits. The cache evicts expired entries on each put and finds a leaf by
  a by-id index; its scope is passed to `newClient` in one place.
- **Live check (2026-10-20, krone's MCP origin, cutting-garden 5be3473):**
  `ynab://budget/` listed 9 budgets (live and archived) and one month of the
  last-used budget summarized 147 transactions in a single request; the
  per-dimension totals agreed with each other (account, cleared, approved and
  transfer each sum to 147). Two API requests in all. The summary reported
  `complete: false` solely because the open `payee` dimension was capped at 20,
  which a busy month always hits; the other dimensions were exact. The
  contract cannot say "one dimension is partial" (cutting-garden#171), so the
  plugin keeps the honest whole-summary flag. This closes the FDR's
  "reads a real YNAB budget" criterion; "testing" still wants a live dry run
  compared against the synthetic fixture.
- **Not built:** `FacetVersioner` (above); a live check against a real budget;
  facet counts that roll up across a budget (each count is per
  transaction-bearing node); the subtransaction legs as nodes.

## More Information

- FDR 0024 (fastmail) and FDR 0026 (org): the API-token and account-config
  precedent; FDR 0019 (jira): the REST-client precedent.
- RFC 0007: `[[ynab.accounts]]` config section and credential resolution.
- RFC 0012 / FDR 0021: the facet contract.
