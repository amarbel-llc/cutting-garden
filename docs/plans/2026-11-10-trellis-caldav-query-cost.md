# Trellis query cost over caldav (cutting-garden#398)

Status: steps 1, 2 and 3 implemented (step 2 by operator decision,
2026-11-10); step 4 proposed.

The report: `list_nodes` with the query `->> [summary~="..."]` over one real
task calendar ran for more than two minutes and returned nothing; a second
try failed fetching the credential from the agent. This note finds where the
requests go, using stand-ins only. Neither the real calendar nor the
operator's card was used. Each claim is labelled **observed** (measured on
a stand-in), **read** (read in the code) or **inferred**.

## Where the requests go

1. **The walk lists every leaf.** (read, observed) A forward closure
   (`->>`, and `->`, `<-`, `<<-`, `[->]`) asked every node it reached for
   its children, leaves included. On a calendar, each task got a
   `ListEnriched` call (caldav declines it for a non-calendar) and then a
   `ListRoots` fallback (which answers "no children"). The stand-in
   `internal/trellis_eval/listing_cost_test.go` has 300 tasks and counted
   **301 `ListEnriched` + 300 `ListRoots`** calls for
   `->> summary~="task 04."`.
2. **Each caldav call is a PROPFIND plus a credential lookup.** (read,
   observed) Every `ListEnriched`/`ListRoots` builds a new client through
   `connectionFromArg`, which resolves the account password, then runs
   `discoverCalendars` (a Depth:1 PROPFIND). Counted against the
   in-package fake CalDAV server (300 VTODOs):

   | call | requests |
   |---|---|
   | `ListEnriched(calendar)` | 1 PROPFIND + 4 REPORT |
   | `ListEnriched(task)` | 1 PROPFIND (declines) |
   | `ListRoots(task)` | 1 PROPFIND (no children) |

   For 300 tasks, the query before the fix: **605 HTTP requests and 601
   password resolutions.**
3. **A `piggy` password source decrypts on every call.** (read)
   `config_common.Account.Password` runs `piggy pass show <key>` each time
   it is called; nothing caches the result. (inferred) If the operator's
   Fastmail account uses `password_source = "piggy"`, the query made
   about 600 card decrypts. That would explain both the minutes of
   runtime and the agent failure on the second try. The operator's
   config has not been checked.
4. **The predicate itself is cheap.** (read, observed) `summary` is a
   listing field that `ListEnriched` carries inline, so
   `matchLeafField` matches it without a leaf fetch. The stand-in query
   matched its 10 expected tasks. Predicates are evaluated client-side,
   but over data the calendar REPORT has already fetched, so that is not
   the cost. (not reproduced) Why the real query returned nothing is
   unexplained; the stand-in does not reproduce it.

## Fix

### Step 1 (done): do not list declared leaves

`NodeType.Container == false` means "a leaf, with no children" (its doc
comment), and the facet fold (`FoldFacets`) already skips such nodes. The
evaluator now does the same. `childrenOf` / `mayHaveChildren` answer
"none" for a node of a declared non-container type, without calling the
plugin. An undeclared type is still listed.

On the stand-in, after the fix: **1 `ListEnriched`, 0 `ListRoots`**
(observed). Against caldav that is 1 PROPFIND + 4 REPORT and one password
resolution per query, whatever the size of the calendar (read and
observed). The "few seconds" acceptance against the real server has not
been measured.

One caveat: the `examples/nixstore` skeleton declares its narinfo type
`Container: false` but says its children are the References. That
contradicts the `Container` contract. A trellis walk will not descend it,
and the facet fold already does not.

### Step 2 (done): resolve a credential once per process

The operator chose this trade-off (2026-11-10): the secret stays in the
plugin process's memory for the life of the process instead of for one
request, and is never written to disk or logs. `Account.Password` now
remembers a `piggy` entry's decrypted password per entry name, so a
long-lived `cg mcp` decrypts once per entry instead of once per call
(observed against a stub `piggy` that counts its runs: 3 calls, 1 run; 8
concurrent first calls, 1 run). The `env` and `password_env` sources stay
live reads: they cost nothing, and a changed variable still applies. A
failed decrypt is not remembered. Each entry has its own lock across its
decrypt, so concurrent first calls decrypt once and one entry's slow decrypt
does not block another's.

config_common is a dagnabit copy (`pkgs/config_common`), so each copy has
its own memo. Every caller of `Password` is a plugin, and plugins import the
`pkgs/` copy, so a binary runs one memo (read). A wire plugin is its own
process with its own memo.

### Step 3 (done): one discovery per host call

At a calendar-home, `ListEnriched` declines after its PROPFIND and the
evaluator's `ListRoots` fallback repeats it. `discoverCalendarsThisCall`
now shares the one `discoverCalendars` result for the rest of the host call
(the `WithCallScope` confirmation pattern, cutting-garden#311). An entry is
served only when this call fetched it, so a new call discovers again, and
outside a call scope nothing is shared. Only `ListRoots` and `ListEnriched`
use it: `FacetVersion` keeps discovering directly so its ctag is always fresh.
`plugins/caldav/discovery_scope_test.go` counts the PROPFINDs for a
calendar-home listing: 2 per call before, 1 after; 2 outside a scope
(observed). The saving is one PROPFIND per calendar-home listing per call,
plus any repeat listing of the same node in one call; it is small next to
step 1.

### Step 4 (later): server-side text-match and cross-call reuse

- A `summary*=` predicate could be sent to the server as an RFC 4791
  §9.7.5 `text-match` in the calendar-query. Two limits: the default
  collation (`i;ascii-casemap`) is case-insensitive, unlike trellis
  `*=`, and `~=` cannot be pushed down at all. It only saves bytes, not
  requests, once step 1 has landed.
- Reusing the facet snapshot token (the collection ctag, `FacetVersion`)
  to skip refetching across calls only matters for repeated queries.
