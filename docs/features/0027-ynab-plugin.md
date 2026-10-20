---
status: proposed
date: 2026-10-20
promotion-criteria: |
  Promote to `experimental` once slice 1 lands: `plugins/ynab/` reads a real
  YNAB budget over the REST API, declares the node types below, and
  `cg list`/`cg mcp` traverse `account -> budget -> accounts / categories /
  transactions` with the read-only facets rolling up and drilling down, pinned
  by an in-memory testserver and a bats lane. Promote to `testing` once a
  live-budget dry run matches the synthetic fixture.
---

# YNAB plugin

Tracks cutting-garden#328. Categorize writes are a separate follow-up issue.

## Problem Statement

A YNAB (You Need A Budget) budget is a small relational tree (budgets,
accounts, category groups, categories, transactions) that cutting-garden
cannot browse. The plugin makes a YNAB account a read-only `list` / `mcp` /
trellis substrate, so questions like "what did I spend per category last
month" or "which transactions are uncleared" run through the same facet and
query surfaces as caldav, fastmail and jira.

## Interface

### Scope and API

The plugin speaks YNAB's REST API (`https://api.ynab.com/v1`) with a personal
access token as a bearer credential. Slice 1 is **read-only**: no writes, no
capture/restore/diff. Wire facts, checked 2026-10-20 against api.ynab.com and
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

`<budget>`, `<account>`, `<group>` and `<category>` are name segments; a name
that is not unique under its parent is disambiguated by the YNAB id as a
`?id=` query (the fastmail precedent: classification stays network-free).

### Node types

Hyphenated and horizontally versioned (FDR 0018):

| Tag (`cutting_garden-ynab-...`) | Kind | Holds |
|---|---|---|
| `budget-v1` | container | budget attributes (id, name, currency, last_modified) + `accounts/`, `categories/`, `transactions/` |
| `account-v1` | container | type, balance, cleared balance, closed; its transactions |
| `category-group-v1` | container | group attributes; its categories |
| `category-v1` | container | budgeted, activity, balance for the current month; its transactions |
| `transaction-v1` | leaf | the transaction as JSON (amount in milliunits and formatted, payee, memo, cleared, approved, flag) |

### Facets (all read-only)

On `transaction-v1`: `month` (date, coarsens by prefix), `account`, `category`,
`payee` (labelled, open, top-N capped), `cleared` (closed: cleared / uncleared /
reconciled), `approved` (yes/no), `flag` (the colour), `transfer` (yes/no, from
`transfer_account_id`, so transfers can be excluded from spending queries) and
`amount_band` (numeric bucket of the absolute amount). Counts roll up per budget.

### Paging: a date window, pushed down from the filter

YNAB has no paging, only date bounds, so the plugin's "pages" are **date
windows**. A transaction listing defaults to the last 30 days
(`since_date = today - 30d`, the API's own default being a year, which is too
heavy for a first listing). A caller pages by naming a window, not a cursor:
a `date` or `month` filter (`--filter month=2026-07`, or the trellis
equivalent) is pushed down to `since_date`/`until_date`, so each month is one
request and older history is reachable page by page. A summary computed over
the default window reports `complete: false` and says which window it covers;
a month filter reports `complete: true` for that month. The per-account,
per-category and per-month endpoints serve a drill-down (`accounts/<a>`,
`categories/<g>/<c>`) in one request instead of filtering the whole plan.
Because each window is a separate request against a 200/hour budget, windows
are cached per process (below) and a window fetch is never repeated inside a
session unless revalidated.

### Caching and rate limit

One budget read is cached per process and revalidated with
`last_knowledge_of_server`, so repeated facet counts and drill-downs inside a
session cost one delta request, not one per node. `FacetVersioner` reports the
`server_knowledge` value as the snapshot token.

## Limitations

- Read-only. Re-categorizing transactions is a follow-up issue.
- Scheduled transactions, payees, months and goals are not nodes in slice 1.
- Split transactions surface as one node with a `subtransactions` array; the
  parent's category is `Split`, so category facets count the parent once under
  `Split`, not the legs.

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
- **Slice 1c:** facets and the date-window pushdown, then a bats lane.
  **Request economy is a 1c requirement, not a nicety:** reading one
  transaction under a *named* budget costs two requests (budget list, then the
  transaction) and a named account drill-down three, so a consumer that reads
  each listed transaction spends `2N` of the 200-per-hour budget. The 1c cache
  (the budget list, accounts and category tree, per process, revalidated with
  `server_knowledge`) must make a leaf read after a listing cost one request.
- Amounts are the account's currency; the plugin does not convert.

## More Information

- FDR 0024 (fastmail) and FDR 0026 (org): the API-token and account-config
  precedent; FDR 0019 (jira): the REST-client precedent.
- RFC 0007: `[[ynab.accounts]]` config section and credential resolution.
- RFC 0012 / FDR 0021: the facet contract.
