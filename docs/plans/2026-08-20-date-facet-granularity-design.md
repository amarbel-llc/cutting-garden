# Dates as groupable facets: one prefix-granular dimension per date property

**Date:** 2026-08-20 · **Issue:** cutting-garden#230 · **Parent:** FDR 0025
(unified field-codec model), RFC 0012 (facet contract), FDR 0023 (organize)

Approved in the 2026-08-20 design session (cutting-garden/green-chestnut).
Decisions below were made explicitly by the user; alternatives considered are
recorded in the #230 issue comment (2026-08-19).

## Problem

Dates are the last split surface after FDR 0025 Option B: the inline
`date_start`/`date_due` atoms are day-precise codec fields, while grouping goes
through the separate coarse `year`/`month` dimensions — three overlapping
declarations of one underlying value, and no way to group by exact day
(`cg organize --group-by date_start`) or filter one (`list --filter
date_start=2026-08-15`).

## Decisions

1. **One dimension per date property, parameterized granularity** — not a new
   dimension beside `year`/`month`, and no primary-date fallback. The date
   codecs' existing `date_start` (DTSTART, all components) and `date_due` (DUE,
   VTODO only) presentation fields become `Groupable`. A dimension IS a codec
   field; it reads and writes ITS OWN property only.
2. **`year`/`month` are REPLACED IMMEDIATELY** (user's explicit call over a
   dual-period migration): deleted from the declarations, `facetsFromView`, and
   the bats in the same slice. `caldavRescheduleCodec` dissolves into the date
   codecs.
3. **Granularity spelling:** `--group-by dim:granularity` with
   `year|month|day`, legal only on a date-kind dimension; a bare spelling falls
   back to the config default, then the built-in default **day**.
4. **Config:** a new `[organize] date_granularity` key in the RFC 0007
   config.toml (tommy-codegen'd struct), the default for bare date group-bys.
5. **Filtering: prefix match by value shape.** `date_start=2026` matches the
   year, `=2026-08` the month, `=2026-08-15` the day — validated shapes
   (YYYY / YYYY-MM / YYYY-MM-DD); anything else rejects loudly at Validate
   time. Uniform across `list --filter`, the mcp read_facets filter, and
   trellis facet predicates.
6. **Summaries lift at fixed MONTH granularity.** FacetCounts folds date-kind
   dimensions coarsened to `YYYY-MM` (the volume today's `month` histogram
   has, now keyed under `date_start`/`date_due`); day buckets never enter a
   summary; year rollups are month prefixes. Per-node facet values stay
   day-precise, so day grouping/filtering still works.

## Model

- New `FacetKind` value **`date`** (`FacetDate`): bucket keys are ISO days
  (`YYYY-MM-DD`), chronologically ordered (Order `YYYYMMDD`), and
  **prefix-coarsenable** — `2026` ⊃ `2026-08` ⊃ `2026-08-15`. The FDR 0025
  derivation maps `FieldDate` → `FacetDate` (replacing today's numeric-bucket
  mapping).
- The framework owns **prefix truncation only** — a pure string operation
  (year=4, month=7, day=10 chars), no calendar arithmetic — so any plugin
  declaring a date-kind dimension (fastmail, nebulous later) gets granularity
  grouping and prefix filtering for free. The plugin owns the values and the
  writes.

## Behavior

- **organize** `--group-by date_start:month`: per-node day values coarsen by
  prefix into the heading ladder; headings render the coarse key
  (`## =2026-09`). A granularity suffix on a non-date dimension, or an invalid
  granularity name, is EX_USAGE naming the valid options.
- **Writes:** a move under a coarse heading passes the coarse bucket through
  the existing unified derivation (`ParseUnifiedBucketMove` → the date codec's
  `Parse`), which now dispatches on value shape: `YYYY` → year splice,
  `YYYY-MM` → month splice (day clamped to the target month), `YYYY-MM-DD` →
  full-date splice — all preserving clock time and TZID (the existing
  `splicePeriod`/`spliceDateTime` machinery). The write targets the
  dimension's own property; moving a DTSTART-less task under a `date_start`
  heading rejects loudly ("no current value") — group tasks by `date_due`.
  This is a deliberate behavior change from the old DTSTART-then-DUE fallback:
  a task carrying both properties reschedules exactly the property you grouped
  by.
- **Atom edits** share the shape dispatch: hand-typing `date_start=2026-09` in
  a box month-splices (was: rejected as not-YYYY-MM-DD) — the same declared
  leniency the priority band edit has.
- `due_band`, `component`, `timezone`, `status`, `priority` are untouched.

## Rollback

No dual-architecture period (decided): the slice lands as one merge; rollback
is `git revert` of that merge commit followed by a normal re-merge. Acceptable
because every known `year`/`month` consumer is in-repo (the organize bats,
rewritten in the same slice) or the user's own workflows; there are no
external API consumers pinned to the old dimension names.

## Testing

- SDK units: `FieldDate`→`FacetDate` mapping, prefix truncation, filter shape
  validation + prefix matching, granularity parsing/rejection.
- caldav units: date codec `Parse` shape dispatch (year/month/day splices),
  `facetsFromView` day-precise emission per property, FacetCounts month lift.
- bats: `organize_month.bats` rewritten onto `--group-by date_due:month` (and
  a `date_start:month` event lane); a new day-granularity lane; a
  config-default lane; `read_facets` month-lift counts re-pinned.

## Tuning levers

- **Built-in default granularity = `day`** (the identity — no silent
  coarsening). Signal to change: bare date group-bys in practice always
  followed by `:month`.
- **Summary lift granularity = fixed `month`.** Signal: year-scale calendars
  where month histograms are too wide → make the lift granularity a per-
  dimension declaration.
- **Granularity set = `year/month/day`.** Signal: demand for ISO `week`
  buckets.

## Out of scope

- Tags/CATEGORIES (#231/#232), the N-way merge, migrating other plugins.
- `time_*` components as facets (grouping by clock is noise; decided against).
- Zone-correct derived ends / shared instant parser (#238).
