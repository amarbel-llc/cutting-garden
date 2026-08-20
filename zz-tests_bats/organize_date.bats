#! /usr/bin/env bats

# The organize date-granularity lane (FDR 0023 Slice 2b, cutting-garden#230):
# `date_due` is a prefix-granular FacetDate dimension. Grouping a calendar by
# `date_due:month` coarsens the day-precise DUEs into `## =<YYYY-MM>` buckets,
# and moving an object between them RESCHEDULES it — the FacetWriteApplier
# splices the target month into the object's existing DUE, preserving the
# day-of-month, clock time, and TZID. A bare `date_due` group-by resolves the
# `[organize] date_granularity` config default, then the built-in day; the
# document heading always carries the RESOLVED spelling (`date_due:month=`).
# `list --facets --filter` prefix-matches date values by validated shape.
# Replaces the retired `month` dimension's lane (the old organize_month.bats).
#
# The fixture calendar (/dav/sched/) holds two VTODOs with clock- and
# TZID-bearing DUE dates in distinct months (sched1 → 2026-08-15,
# sched2 → 2026-09-10).

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  # Opt this lane's testserver into the dedicated /dav/sched/ calendar (kept out
  # of the shared fixture so it doesn't inflate caldav.bats's home-capture count).
  export CG_TEST_CALDAV_SCHED=1
  # Pin the config dir under the sandboxed $HOME: os.UserConfigDir prefers
  # $XDG_CONFIG_HOME, so an ambient value on a host (non-sandbox) run would leak
  # the developer's real config into the bare-group-by default resolution.
  export XDG_CONFIG_HOME="$HOME/.config"
  start_caldav_server
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/sched/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_grouped runs `organize -group-by $1` and saves the emitted document.
generate_grouped() {
  run_cg organize -group-by "$1" "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/date.txt"
  printf '%s\n' "$output" >"$DOC"
}

# move_to_bucket rewrites the document ($1=box substring, $2=target bucket
# value, $3=out path): the object's box line is pulled from its current bucket
# and re-filed under the `## =<target>` heading.
move_to_bucket() {
  local box="$1" target="$2" out="$3" line
  line="$(grep "$box" "$DOC")"
  [[ -n $line ]] || fail "no $box box in document: $(cat "$DOC")"
  awk -v ln="$line" -v h="## =$target" '
    $0 == ln { next }
    { print }
    $0 == h { print ""; print ln }
  ' "$DOC" >"$out"
}

# Grouping by date_due:month coarsens the day-precise DUEs into their distinct
# month buckets, and the dimension heading persists the FULL resolved spelling
# so a later apply coarsens identically without consulting config (#230).
function organize_date_month_generate_buckets { # @test
  generate_grouped date_due:month
  assert_line '# date_due:month='
  assert_line '## =2026-08'
  assert_line '## =2026-09'
  assert_output --partial '- [sched1.ics date_due=2026-08-15 time_due=14-30]'
  assert_output --partial '- [sched2.ics date_due=2026-09-10 time_due=16-30]'
}

# The read-side field presenter (cutting-garden#47) surfaces each VTODO's DUE as
# structured date_due/time_due atoms inside the box — split so the clock is its
# own editable field (HH-mm), not scraped from the description trailer. Proves
# the FieldPresenter render end to end against the real binary.
function organize_date_surfaces_due_atoms { # @test
  generate_grouped date_due:month
  assert_output --partial '[sched1.ics date_due=2026-08-15 time_due=14-30]'
  # The clock lives in its own atom, never smuggled into the trailer.
  refute_output --partial 'Book flights 14'
}

# The core tracer: move sched1 from 2026-08 to 2026-09 and commit. The object
# leaves the old month bucket and lands in the new one, AND its raw DUE proves
# the reschedule preserved the day (15), clock (14:30:00), and TZID
# (America/Los_Angeles) — only the month changed.
function organize_date_month_reschedule_preserves_datetime { # @test
  generate_grouped date_due:month
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  move_to_bucket 'sched1.ics' '2026-09' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output --partial 'sched1.ics'

  # sched1 moved into 2026-09 (joining sched2) and left 2026-08. The trellis
  # `^=` prefix operator matches the day-precise date_due facet by month.
  run_cg list -query 'date_due^=2026-09' "$CAL"
  assert_success
  assert_output --partial 'sched1.ics'
  assert_output --partial 'sched2.ics'

  run_cg list -query 'date_due^=2026-08' "$CAL"
  assert_success
  refute_output --partial 'sched1.ics'

  # The authoritative check: the object's stored iCalendar shows the spliced DUE
  # with day/clock/TZID intact.
  run curl -fsS "${CALDAV_SOURCE#caldav:}sched/sched1.ics"
  assert_success
  assert_output --partial 'DUE;TZID=America/Los_Angeles:20260915T143000'
}

# A bare `--group-by date_due` with no config resolves the built-in DAY default
# (the identity — no silent coarsening): one bucket heading per distinct day,
# with the resolved day granularity persisted explicitly in the heading.
function organize_date_bare_groups_by_day { # @test
  generate_grouped date_due
  assert_line '# date_due:day='
  assert_line '## =2026-08-15'
  assert_line '## =2026-09-10'
}

# With `[organize] date_granularity = "month"` in config.toml, a bare
# `--group-by date_due` resolves to month at GENERATE time — and the document
# heading carries the resolved spelling, not the bare one, so a later apply
# never re-consults (possibly changed) config.
function organize_date_config_default_month { # @test
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[organize]
	date_granularity = "month"
	EOF

  generate_grouped date_due
  assert_line '# date_due:month='
  assert_line '## =2026-08'
  assert_line '## =2026-09'
  refute_output --partial '## =2026-08-15'
}

# `list --facets --filter` prefix-matches a date dimension by validated value
# shape (#230): a year prefix keeps every 2026 DUE (the summary lifts date
# buckets at fixed month granularity), a month prefix narrows to its one VTODO,
# and a malformed shape is rejected loudly at Validate time — never a silent
# exact-match degradation.
function list_facets_date_filter_prefix_matches { # @test
  run_cg list -facets -filter 'date_due=2026' "$CAL"
  assert_success
  assert_output --partial '2026-08 1'
  assert_output --partial '2026-09 1'

  run_cg list -facets -filter 'date_due=2026-08' "$CAL"
  assert_success
  assert_output --partial '2026-08 1'
  refute_output --partial '2026-09'

  run_cg list -facets -filter 'date_due=aug' "$CAL"
  assert_failure
  assert_output --partial 'not a date bucket'
  assert_output --partial 'YYYY'
}
