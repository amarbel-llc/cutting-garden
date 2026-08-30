#! /usr/bin/env bats

# The organize priority-reorganization lane (FDR 0023 / cutting-garden#221, #77):
# grouping a task calendar by the `priority` facet buckets every VTODO into one of
# four urgency-first bands (0_must / 1_should / 2_nice / 3_unspecified), and moving
# a task's box line between `## =<band>` buckets REWRITES its RFC 5545 PRIORITY —
# the FacetWriteApplier completes the band to its canonical value (must->1,
# should->5, nice->9, unspecified->0, which the serializer OMITS, clearing the
# property). A bucket->value completion like the month reschedule, distinct from
# status's verbatim passthrough (organize.bats). This is the e2e regression net
# the FDR 0025 codec migration is measured behaviour-neutral against.
#
# The fixture calendar (/dav/fields/, opt-in via CG_TEST_CALDAV_FIELDS) holds four
# VTODOs, one per band:
#   field1 Pay rent      PRIORITY 1 (0_must)   LOCATION Bank  STATUS NEEDS-ACTION
#   field2 Read book     PRIORITY 5 (1_should)
#   field3 Water plants  PRIORITY 9 (2_nice)
#   field4 Someday idea  no PRIORITY (3_unspecified)
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server 43105
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_priority runs `organize -group-by priority=` (the field spelling, design G10) and asserts the document
# in full: the four bands pre-rendered urgency-first, each task under its own
# band, every box surfacing its redundant priority atom (the heading/atom overlap
# #229 tracks) plus location/status where present.
generate_priority() {
  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-pvsu4xx8ayy480jscwkmcn5da9haypl98w8jtu4uh67lnk3hrm7qxsvmt0
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent

	## =1_should

	- [field2.ics priority=5] Read book

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM
}

# Grouping by priority pre-renders the four bands (urgency-first) and files each
# task under its own band; the box surfaces the redundant priority atom too (the
# heading/atom overlap #229 tracks), plus location/status where present.
function organize_priority_generate_buckets { # @test
  generate_priority
}

# The core tracer: move field2 from 1_should to 0_must and commit. The box's
# priority atom is carried verbatim (an unchanged atom is no field edit), so the
# reorganize is a pure move; the band completes to its canonical PRIORITY value
# (must->1) on the live object, and the re-rendered document shows field2 under
# 0_must with priority=1.
function organize_priority_band_move_rewrites { # @test
  generate_priority
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-pvsu4xx8ayy480jscwkmcn5da9haypl98w8jtu4uh67lnk3hrm7qxsvmt0
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field2.ics priority=5] Read book

	## =1_should

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field2.ics  priority=[-1_should-]{+0_must+}]  Read book

organize: wrote 1 change(s)
EOF

  # The reorganize wrote the must-band canonical PRIORITY (1) into the live object.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field2.ics"
  assert_success
  assert_output --partial 'PRIORITY:1'

  run_cg list -query 'priority=0_must' "$CAL"
  assert_success
  assert_output --partial 'field2.ics'

  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-f496ypxfpmc2064m0u2jakm4x723zg75m5vlphey05rscr5egk4skdwtr5
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field2.ics priority=1] Read book

	## =1_should

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM
}

# Moving into 3_unspecified completes to PRIORITY 0, which the serializer OMITS:
# the property is CLEARED, not written as literal 0. The task's LOCATION/STATUS
# are untouched — only the banded field changes — so the re-rendered box keeps
# its location/status atoms and drops priority.
function organize_priority_unspecified_clears { # @test
  generate_priority
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-pvsu4xx8ayy480jscwkmcn5da9haypl98w8jtu4uh67lnk3hrm7qxsvmt0
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	## =1_should

	- [field2.ics priority=5] Read book

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field4.ics] Someday idea
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field1.ics  priority=[-0_must-]{+3_unspecified+}]  Pay rent

organize: wrote 1 change(s)
EOF

  # unspecified -> PRIORITY 0 -> the property is dropped, LOCATION/STATUS kept.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field1.ics"
  assert_success
  refute_output --partial 'PRIORITY'
  assert_output --partial 'LOCATION:Bank'
  assert_output --partial 'STATUS:NEEDS-ACTION'

  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-xhs82xfh0yx6g4tty7s8yw80c3wy0gcujz9cqhwwdrnjww845g6q8rn63r
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	## =1_should

	- [field2.ics priority=5] Read book

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field1.ics location=Bank status=NEEDS-ACTION] Pay rent
	- [field4.ics] Someday idea
	EOM
}
