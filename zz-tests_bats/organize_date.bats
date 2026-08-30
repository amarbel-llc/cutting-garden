#! /usr/bin/env bats

# The organize date-granularity lane (FDR 0023 Slice 2b, cutting-garden#230):
# `date_due` is a prefix-granular FacetDate dimension. Grouping a calendar by
# `date_due=(month)` coarsens the day-precise DUEs into `## =<YYYY-MM>` buckets,
# and moving an object between them RESCHEDULES it — the FacetWriteApplier
# splices the target month into the object's existing DUE, preserving the
# day-of-month, clock time, and TZID. A bare `date_due` group-by resolves the
# `[organize] date_granularity` config default, then the built-in day; the
# document heading always carries the RESOLVED spelling (`date_due=(month)`).
# `list --facets --filter` prefix-matches date values by validated shape.
# Replaces the retired `month` dimension's lane (the old organize_month.bats).
#
# The fixture calendar (/dav/sched/) holds two VTODOs with clock- and
# TZID-bearing DUE dates in distinct months (sched1 → 2026-08-15,
# sched2 → 2026-09-10).
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

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
  start_caldav_server 43104
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/sched/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_month runs `organize -group-by $1` (default `date_due=(month)`; a
# caller passing bare `date_due` under a month config default must land on the
# SAME vector) and asserts the document in full: the dimension heading persists
# the FULL resolved spelling (`# date_due=(month)`, so a later apply coarsens
# identically without consulting config, #230), each VTODO sits in its own month
# bucket, and every box carries the split date_due/time_due atoms (the coarser
# month heading keeps the day-precise date_due atom).
generate_month() {
  run_cg organize -group-by "${1:-date_due=(month)}" "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by date_due=(month) -query "_terminal=no" caldav:http://127.0.0.1:43104/dav/sched/`
	- _base = @blake2b256-kuaxfueta7yl0n5ceqfacpnvt9zpkcrk0t5uwgz9fhp3vejp09fst9zgap
	- _anchor = caldav:http://127.0.0.1:43104/dav/sched/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# date_due=(month)

	## =2026-08

	- [sched1.ics date_due=2026-08-15 time_due=14-30] Book flights

	## =2026-09

	- [sched2.ics date_due=2026-09-10 time_due=16-30] Renew passport
	EOM
}

# Grouping by date_due=(month) coarsens the day-precise DUEs into their distinct
# month buckets, and the dimension heading persists the FULL resolved spelling
# so a later apply coarsens identically without consulting config (#230). The
# same vector pins the read-side field presenter (cutting-garden#47): each
# VTODO's DUE surfaces as structured date_due/time_due atoms inside the box —
# the clock is its own editable field (HH-mm), never smuggled into the
# description trailer (`Book flights` carries no trailing clock).
function organize_date_month_generate_buckets { # @test
  generate_month
}

# The core tracer: move sched1 from 2026-08 to 2026-09 and commit. The object
# leaves the old month bucket and lands in the new one, AND its raw DUE proves
# the reschedule preserved the day (15), clock (14:30:00), and TZID
# (America/Los_Angeles) — only the month changed.
function organize_date_month_reschedule_preserves_datetime { # @test
  generate_month
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by date_due=(month) -query "_terminal=no" caldav:http://127.0.0.1:43104/dav/sched/`
	- _base = @blake2b256-kuaxfueta7yl0n5ceqfacpnvt9zpkcrk0t5uwgz9fhp3vejp09fst9zgap
	- _anchor = caldav:http://127.0.0.1:43104/dav/sched/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# date_due=(month)

	## =2026-08

	## =2026-09

	- [sched1.ics date_due=2026-08-15 time_due=14-30] Book flights
	- [sched2.ics date_due=2026-09-10 time_due=16-30] Renew passport
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [sched1.ics  date_due=[-2026-08-]{+2026-09+}]  Book flights

organize: wrote 1 change(s)
EOF

  # sched1 moved into 2026-09 (joining sched2) and left 2026-08. A trellis `=`
  # on the date-kind date_due dimension prefix-matches the day-precise facet
  # by validated month shape — the same semantics as `list --filter`, pinning
  # the design's uniformity decision end to end (#230).
  run_cg list -query 'date_due=2026-09' "$CAL"
  assert_success
  assert_output --partial 'sched1.ics'
  assert_output --partial 'sched2.ics'

  run_cg list -query 'date_due=2026-08' "$CAL"
  assert_success
  refute_output --partial 'sched1.ics'

  # The authoritative check: the object's stored iCalendar shows the spliced DUE
  # with day/clock/TZID intact.
  run curl -fsS "${CALDAV_SOURCE#caldav:}sched/sched1.ics"
  assert_success
  assert_output --partial 'DUE;TZID=America/Los_Angeles:20260915T143000'

  # The re-rendered document: both tasks under 2026-09, sched1's date_due atom
  # spliced to the 15th of the new month, the emptied 2026-08 bucket gone.
  run_cg organize -group-by 'date_due=(month)' "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by date_due=(month) -query "_terminal=no" caldav:http://127.0.0.1:43104/dav/sched/`
	- _base = @blake2b256-m9syr55ukvvauq6ey2wwl0hcvdkgatx69xluu4fjyk3mz9z00z3qmpeqxu
	- _anchor = caldav:http://127.0.0.1:43104/dav/sched/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# date_due=(month)

	## =2026-09

	- [sched1.ics date_due=2026-09-15 time_due=14-30] Book flights
	- [sched2.ics date_due=2026-09-10 time_due=16-30] Renew passport
	EOM
}

# A bare `--group-by date_due=` with no config resolves the built-in DAY default
# (the identity — no silent coarsening): one bucket heading per distinct day,
# with the resolved day granularity persisted explicitly in the heading.
# cutting-garden#229: at day granularity the heading shows the date in FULL, so
# the redundant date_due box atom is dropped — while its split sibling time_due
# (which the heading does not show) is kept. Contrast the month lane above,
# where the coarser heading keeps the day-precise date_due atom.
function organize_date_bare_groups_by_day { # @test
  run_cg organize -group-by date_due= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by date_due=(day) -query "_terminal=no" caldav:http://127.0.0.1:43104/dav/sched/`
	- _base = @blake2b256-sv5d5z5w5dnwxtn3k9gp78cwgepp0dfsnn0ftajr9gwc5ltr90wqh6jfx6
	- _anchor = caldav:http://127.0.0.1:43104/dav/sched/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# date_due=(day)

	## =2026-08-15

	- [sched1.ics time_due=14-30] Book flights

	## =2026-09-10

	- [sched2.ics time_due=16-30] Renew passport
	EOM
}

# With `[organize] date_granularity = "month"` in config.toml, a bare
# `--group-by date_due=` resolves to month at GENERATE time — and the document
# heading carries the resolved spelling, not the bare one, so a later apply
# never re-consults (possibly changed) config.
function organize_date_config_default_month { # @test
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[organize]
	date_granularity = "month"
	EOF

  # Bare `date_due` under the month default is the SAME vector as an explicit
  # `date_due=(month)` — provenance, heading, and `_base` digest included.
  generate_month date_due=
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
