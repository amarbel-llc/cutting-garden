# Manual validation: prefix-granular date facets (#230) + adjacent slices

Checklist for validating the 2026-08-20 merges against a REAL CalDAV account
(the caldav testserver bats cover the same surfaces hermetically; this is the
live-server pass that historically catches what fixtures don't — see #233's
discovery history). Covers #230 (date facets), the FDR 0025 Option B
derivation hardening, and #233 (DURATION-derived ends).

## Setup

- Build fresh: `nix build` → use `result/bin/cutting-garden` (alias `cg` below
  means that binary). Do NOT use the `debug-organize-live*` / GOMODCACHE-paired
  recipes — they are broken by the madder store-config skew (#240), which
  fails at store open before any organize logic runs.
- Config: a `[caldav]` account in `$XDG_CONFIG_HOME/cutting-garden/config.toml`
  (the fastmail aliases; password via `CALDAV_PASSWORD`). `just
  debug-caldav-shell` works if the freshly-built version is installed to the
  profile first.
- **Write-safety:** sections D–E WRITE to the calendar. Point them at a
  scratch calendar (or scratch tasks you create for the purpose), not live
  work items.
- `<acct>` below = your account/calendar URI arg, e.g. `caldav:<alias>` or the
  explicit `caldav:https://…/dav/calendars/user/…/`.

## A. Declarations & summaries (read-only)

- [ ] `cg list --facets <acct>` — the summary shows `date_start` (and
  `date_due` if tasks exist) with **month-granularity** buckets (`2026-08`),
  never day keys; `year`/`month` dimensions are GONE; `due_band`, `priority`,
  `status`, `component`, `timezone` unchanged.
- [ ] `cg mcp` → `describe_node_types` (or via krone): the caldav VTODO type
  declares `date_start`/`date_due` with kind `date`; VEVENT/VJOURNAL declare
  `date_start` only; the write mapping shows them writable through
  `dtstart`/`due` with the reschedule-by-move hint.
- [ ] `read_facets` on the account root with filter `date_due=overdue`-style
  volatile checks still behave (due_band informative zeros when tasks exist).

## B. Grouping (read-only)

- [ ] `cg organize --group-by date_due:month <acct>` (pipe to stdout, don't
  apply): heading `# date_due:month=`, buckets `## =2026-08` style, tasks
  bucketed by DUE only — a DUE-less task with DTSTART is NOT under date_due
  buckets (ungrouped), unlike the old primary-date fallback.
- [ ] `cg organize --group-by date_start:month <acct>` over an event calendar:
  events bucket by DTSTART month.
- [ ] Bare `cg organize --group-by date_due <acct>` with NO config key: day
  buckets (`## =2026-08-15`), heading `# date_due:day=` (the resolved default
  is persisted explicitly).
- [ ] Add `[organize]\ndate_granularity = "month"` to config.toml → bare
  `--group-by date_due` now groups by month; heading says `date_due:month=`.
- [ ] `cg organize --group-by date_due:year <acct>`: year buckets (`## =2026`).
- [ ] Loud rejections (exit 64, helpful message): `--group-by status:month`
  (not a date dimension), `--group-by date_due:week` (lists year, month, day),
  bad config value `date_granularity = "week"` (rejected at config load).

## C. Filtering & queries (read-only)

- [ ] `cg list --facets --filter 'date_due=2026' <acct>`: narrows to
  this year's tasks; summary still month-lifted.
- [ ] `--filter 'date_due=2026-08'`: month narrowing; compare counts against
  the unfiltered summary's `2026-08` bucket — they should agree.
- [ ] `--filter 'date_due=aug'`: loud rejection naming the accepted shapes
  (YYYY, YYYY-MM, YYYY-MM-DD).
- [ ] Trellis: `cg list -query '!caldav-object-vtodo-v1 date_due=2026-08' <acct>`
  prefix-matches day-precise values (was silently empty before the 6b fix);
  `date_due!=2026-08` excludes that month (symmetric); `date_due^=2026-0`
  still raw-prefix-matches (native operator untouched).

## D. Reschedule-by-move (WRITES — scratch calendar)

Generate with `--group-by date_due:month`, move a task's line under another
month bucket, apply, then verify on the server (or via `cg list`):

- [ ] Month move `=2026-08` → `=2026-09`: DUE keeps day-of-month, clock time,
  and TZID; only the month changes. A day-31 task moved into a shorter month
  clamps to the month's last day.
- [ ] Year move (`--group-by date_due:year`, move `=2026` → `=2027`): month,
  day, clock, TZID all preserved.
- [ ] Day-granularity move (`--group-by date_due`, move a line under another
  day bucket): full date changes, clock/TZID preserved.
- [ ] `--group-by date_start:month` over events: the move splices DTSTART (an
  event with DTEND keeps DTEND untouched — note: the END does not shift with
  the start; flag if that surprises you in practice, it's the per-property
  model working as designed).
- [ ] Moving a DTSTART-less task under a `date_start` bucket: loud rejection
  naming the object ("carries no current value" class), nothing written.
- [ ] Round-trip robustness: generate with `date_granularity = "month"` in
  config, DELETE the config key, then apply the earlier document — the apply
  still coarsens correctly (granularity came from the document heading, not
  config).

## E. Field edits & priority (WRITES — scratch calendar)

Organize box-interior edits (any group-by):

- [ ] Edit `date_due=2026-09-03` on a task's atom line: DUE moves to that day,
  clock preserved.
- [ ] Edit `date_due=20260903` (compact iCal spelling): same result (the
  legacy acceptance was deliberately restored after review).
- [ ] Edit `date_due=2026-09` (coarse month value as an ATOM edit): month
  splices, day/clock preserved (the shape-dispatch leniency).
- [ ] Priority: `--group-by priority`, move a task between bands (`=0_must` →
  `=2_nice`): PRIORITY becomes the canonical 9; move to `=3_unspecified`
  clears the property. A hand-typed bucket `## =7` + move under it: loud
  rejection listing the four declared bands (the closed-domain gate).
- [ ] Atom edit `priority=0_must`: completes to PRIORITY:1 (documented
  leniency); `priority=4` writes 4; `priority=bogus` rejects.

## F. DURATION events (#233, read-only)

- [ ] An event carrying `DURATION` instead of `DTEND` (common for multi-day
  all-day events) shows `date_end` (and `time_end` when timed) in its
  organize box — e.g. `DTSTART:20260813` + `DURATION:P6D` → `date_end=2026-08-19`
  (the exclusive end, matching what an all-day DTEND would carry).
- [ ] An event with an explicit DTEND is unchanged (DTEND wins over DURATION).
- [ ] KNOWN LIMIT (skip unless curious): a TZID-anchored timed event whose
  duration crosses a DST boundary derives an end an hour off the server's —
  documented, tracked as #238.

## G. Regression spot-checks (read-only)

- [ ] `--group-by status` generate + a status move on a scratch task: the
  pre-#230 core loop still works end to end.
- [ ] `due_band` triage: `read_facets`/`list --facets` overdue/today counts
  look right against your actual tasks (its DUE-then-DTSTART fallback was
  deliberately preserved).
- [ ] Terminal exclusion: organize still hides COMPLETED/CANCELLED by default;
  `--include-terminal` shows them.

File findings as issues (the #233-style "found in manual testing" reports have
been the highest-value inputs to this FDR).
