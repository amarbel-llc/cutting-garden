#! /usr/bin/env bats

# The organize reschedule-by-move lane (FDR 0023 Slice 2b): grouping a calendar
# by the `month` facet and moving an object between `## =<YYYY-MM>` buckets
# RESCHEDULES it — the FacetWriteApplier splices the target month into the
# object's existing DUE, preserving the day-of-month, clock time, and TZID (a
# bucket->value completion, distinct from Slice 2a's verbatim status passthrough).
#
# The fixture calendar (/dav/sched/) holds two VTODOs with clock- and
# TZID-bearing DUE dates in distinct months (sched1 → 2026-08, sched2 → 2026-09).

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  # Opt this lane's testserver into the dedicated /dav/sched/ calendar (kept out
  # of the shared fixture so it doesn't inflate caldav.bats's home-capture count).
  export CG_TEST_CALDAV_SCHED=1
  start_caldav_server
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/sched/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_month runs `organize -group-by month` and saves the emitted document.
generate_month() {
  run_cg organize -group-by month "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/month.txt"
  printf '%s\n' "$output" >"$DOC"
}

# move_between_months rewrites the document ($1=box substring, $2=target YYYY-MM,
# $3=out path): the object's box line is pulled from its current bucket and
# re-filed under the `## =<target>` heading.
move_between_months() {
  local box="$1" target="$2" out="$3" line
  line="$(grep "$box" "$DOC")"
  [[ -n $line ]] || fail "no $box box in document: $(cat "$DOC")"
  awk -v ln="$line" -v h="## =$target" '
    $0 == ln { next }
    { print }
    $0 == h { print ""; print ln }
  ' "$DOC" >"$out"
}

# Grouping by month buckets the two seeded VTODOs into their distinct months.
function organize_month_generate_buckets { # @test
  generate_month
  assert_line '# month='
  assert_line '## =2026-08'
  assert_line '## =2026-09'
  assert_output --partial '- [sched1.ics date_due=2026-08-15 time_due=14-30]'
  assert_output --partial '- [sched2.ics date_due=2026-09-10 time_due=16-30]'
}

# The read-side field presenter (cutting-garden#47) surfaces each VTODO's DUE as
# structured date_due/time_due atoms inside the box — split so the clock is its
# own editable field (HH-mm), not scraped from the description trailer. Proves
# the FieldPresenter render end to end against the real binary.
function organize_month_surfaces_due_atoms { # @test
  generate_month
  assert_output --partial '[sched1.ics date_due=2026-08-15 time_due=14-30]'
  # The clock lives in its own atom, never smuggled into the trailer.
  refute_output --partial 'Book flights 14'
}

# The core tracer: move sched1 from 2026-08 to 2026-09 and commit. The object
# leaves the old month bucket and lands in the new one, AND its raw DUE proves
# the reschedule preserved the day (15), clock (14:30:00), and TZID
# (America/Los_Angeles) — only the month changed.
function organize_month_reschedule_preserves_datetime { # @test
  generate_month
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  move_between_months 'sched1.ics' '2026-09' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output --partial 'sched1.ics'

  # sched1 moved into 2026-09 (joining sched2) and left 2026-08.
  run_cg list -query 'month=2026-09' "$CAL"
  assert_success
  assert_output --partial 'sched1.ics'
  assert_output --partial 'sched2.ics'

  run_cg list -query 'month=2026-08' "$CAL"
  assert_success
  refute_output --partial 'sched1.ics'

  # The authoritative check: the object's stored iCalendar shows the spliced DUE
  # with day/clock/TZID intact.
  run curl -fsS "${CALDAV_SOURCE#caldav:}sched/sched1.ics"
  assert_success
  assert_output --partial 'DUE;TZID=America/Los_Angeles:20260915T143000'
}
