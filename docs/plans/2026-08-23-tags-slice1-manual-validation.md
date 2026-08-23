# Manual validation: tags slice 1 — read-only naive CATEGORIES (#231)

Checklist for validating the tags slice-1 merge (`d7d073b..29b3e3b`) against a
REAL CalDAV account. The caldav testserver bats cover these surfaces
hermetically; this is the live-server pass that historically catches what
fixtures don't. Scope is exactly what shipped: caldav `CATEGORIES` as a
read-only, multi-valued, **groupable** `categories` dimension with **naive
(exact)** semantics (RFC 0019). No writes, no hierarchy, no interpreter
override — those are slices 2–3 (see the "Out of scope" note at the end).

## Setup

- Build fresh: `nix build` → use `result/bin/cutting-garden` (alias `cg` below
  means that binary). Do NOT use the `debug-organize-live*` / GOMODCACHE-paired
  recipes — they are broken by the madder store-config skew (#240), which fails
  at store open before any organize logic runs. (`just debug-caldav-shell`
  works only if the freshly-built version is installed to the profile first —
  it uses the matched installed `cg`+`madder` pair.)
- Config: a `[caldav]` account in `$XDG_CONFIG_HOME/cutting-garden/config.toml`
  (the fastmail aliases; password via `CALDAV_PASSWORD`). Credentials for the
  live recipes come from `piggy pass show fastmail-caldav.env`.
- `<acct>` below = your account/calendar URI arg, e.g. `caldav:<alias>` or the
  explicit `caldav:https://…/dav/calendars/user/…/`.
- **Test data:** slice 1 is exercised by objects that carry `CATEGORIES`. If
  your real tasks don't use CATEGORIES, create 2–3 scratch VTODOs on a scratch
  calendar first: one with `CATEGORIES:work,errand` (two tags), one with
  `CATEGORIES:work` (shares a tag), one untagged — that shape drives every
  multi-membership check below.
- **Write-safety:** this slice is READ-ONLY. Section D confirms a categories
  move is *rejected* and writes nothing; the only server writes you make are
  the scratch fixtures you create by hand.

## A. Declarations & summaries (read-only)

- [ ] `cg list --facets <acct>` — the summary shows a `categories` dimension:
  one bucket per raw tag with its count. A two-tag task counts once under EACH
  of its tags; an untagged task contributes NOTHING (no zero bucket). The
  existing dimensions (`status`, `priority`, `component`, `date_*`, `due_band`,
  `timezone`) are unchanged and their counts undisturbed.
- [ ] `cg mcp` → `describe_node_types` (or via krone): the caldav VTODO/VEVENT/
  VJOURNAL types declare `categories` with kind `tag`, **multi-valued**,
  **groupable**, interpreter `naive`, and **NOT writable** (write mode `none`).
  It is NOT listed as an inline box atom.
- [ ] Leaf read of a tagged object (`cg mcp` `read_node`, or `list` enrichment):
  `Node.Fields["categories"]` is the raw `[]string` tag list; an untagged
  object omits the key entirely.

## B. Grouping (read-only)

- [ ] `cg organize --group-by categories <acct>` (pipe to stdout, don't apply):
  heading `# categories=`, buckets `## =<tag>` (observed tags, sorted
  ascending). A **multi-tag object's line appears under EACH of its tag
  buckets** (multi-membership); a single-tag object under only its one bucket;
  an untagged object is ungrouped.
- [ ] Naive/exact — NO hierarchy: two tags sharing a hyphen prefix (e.g.
  `project-alpha` and `project-beta`) render as TWO SEPARATE `## =project-alpha`
  / `## =project-beta` buckets, NOT rolled up under a `project` namespace. (The
  rollup + continuation headings are slice 3 — confirm they're absent here.)
- [ ] A tag with an interior/leading underscore (e.g. `_inbox`,
  `client_acme`) groups under its LITERAL value — no lift, no reordering to the
  top (naive `SortKey` is identity; the `_`-lift is slice 3).

## C. Filtering & queries (read-only)

- [ ] `cg list --facets --filter 'categories=<tag>' <acct>`: narrows to objects
  carrying that EXACT tag; the categories histogram still shows the per-tag
  counts, and a multi-tag object is counted under each of its tags. Compare the
  filtered object count against the unfiltered summary's `<tag>` bucket — they
  should agree.
- [ ] Exactness (contrast with dates, which prefix-match): `--filter
  'categories=proj'` does NOT match `project-alpha` — naive matching is exact,
  so a partial tag matches nothing (expect an empty/zero result, not a prefix
  hit).
- [ ] Trellis: `cg list -query '!caldav-object-vtodo-v1 categories=<tag>' <acct>`
  matches objects with that exact tag; `categories!=<tag>` excludes them
  (symmetric). A bare partial (`categories=proj`) matches nothing (exact, not
  prefix).

## D. Read-only invariant (the rejection — nothing is written)

Generate with `--group-by categories`, move an object's line to a different
`## =<tag>` bucket, and apply — it MUST be refused with nothing written:

- [ ] `cg organize --group-by categories <acct>` → edit the document to move a
  line between tag buckets → `cg organize --apply <edited>`: **nonzero exit, no
  server write.** The message depends on the document shape:
  - With a multi-tag object present (the normal case), the rendered document
    carries that object's line under two buckets, so the apply is rejected at
    the duplicate-line guard: `object <id> appears twice (buckets …)`.
  - The underlying "categories is read-only / not writable" gate is the deeper
    cause; it's what the unit tests pin. Either message is acceptable — the
    point is **the move is refused and the calendar is unchanged.**
- [ ] Re-fetch the moved object from the server (or `cg list`): its CATEGORIES
  are exactly as before — no PUT happened.

## E. Interop / regression spot-checks (read-only)

- [ ] `categories` coexists cleanly: `--group-by status` / `--group-by priority`
  / `--group-by date_due:month` still work end to end, unaffected by the
  presence of CATEGORIES on the objects.
- [ ] Box atoms unchanged: a tagged object's organize box line
  (`- [<id> priority=… …] <summary>`) shows the SAME atoms as before —
  `categories` is groupable-only and never appears as a `categories=` box atom
  (the #229 placement rule).
- [ ] `cg list --facets` shows `categories` alongside the other dimensions
  without disturbing their counts.

## Out of scope (slices 2–3 — do NOT expect these yet)

- **Writes / membership editing** — moving a line to add/remove a tag: slice 2
  (the N-way merge). Today it's rejected (section D).
- **dodder-hyphen algebra** — namespace rollup (`--group-by project` bucketing
  `project-*`), continuation headings (`## -alpha`), bare-tag transitive
  trellis matching, the `_`-lift: slice 3.
- **Interpreter override** — `[tags] interpreter = "dodder-hyphen"` in config:
  slice 3. Slice 1 always uses `naive`.

## Why this pass matters for slices 2–3

Two design decisions are flagged PROVISIONAL pending your verdict
(`docs/plans/2026-08-20-tags-design.md`, Tuning levers): the dodder-hyphen
continuation-heading rendering (`## -alpha`, no `=`) and the leading-only
`_`-lift rule. Slice 1 can't exercise those directly, but eyeballing naive
grouping against YOUR real CATEGORIES here — how the flat `## =<tag>` buckets
read, whether hierarchy/rollup is something you actually want, how your tags are
shaped — is the input that will confirm or revise those decisions before I plan
slice 2.

File findings as issues (the "found in manual testing" reports have been the
highest-value inputs to this FDR).
