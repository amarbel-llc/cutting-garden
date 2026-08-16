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

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_priority runs `organize -group-by priority` and saves the document.
generate_priority() {
  run_cg organize -group-by priority "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/priority.txt"
  printf '%s\n' "$output" >"$DOC"
}

# move_box refiles a box line ($1=box substring) under the `## =<band>` bucket
# ($2), writing the edited document to $3 — the box's priority atom is carried
# verbatim (an unchanged atom is no field edit), so the reorganize is a pure move.
move_box() {
  local box="$1" band="$2" out="$3" line
  line="$(grep "$box" "$DOC")"
  [[ -n $line ]] || fail "no $box box in document: $(cat "$DOC")"
  awk -v ln="$line" -v h="## =$band" '
    $0 == ln { next }
    { print }
    $0 == h { print ""; print ln }
  ' "$DOC" >"$out"
}

# Grouping by priority pre-renders the four bands (urgency-first) and files each
# task under its own band; the box surfaces the redundant priority atom too (the
# heading/atom overlap #229 tracks), plus location/status where present.
function organize_priority_generate_buckets { # @test
  generate_priority
  assert_line '# priority='
  assert_line '## =0_must'
  assert_line '## =1_should'
  assert_line '## =2_nice'
  assert_line '## =3_unspecified'
  assert_output --partial '- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent'
  assert_output --partial '- [field2.ics priority=5] Read book'
  assert_output --partial '- [field3.ics priority=9] Water plants'
  assert_output --partial '- [field4.ics] Someday idea'
}

# The core tracer: move field2 from 1_should to 0_must and commit. The band
# completes to its canonical PRIORITY value (must->1) on the live object.
function organize_priority_band_move_rewrites { # @test
  generate_priority
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  move_box 'field2.ics' 0_must "$edited"

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
}

# Moving into 3_unspecified completes to PRIORITY 0, which the serializer OMITS:
# the property is CLEARED, not written as literal 0. The task's LOCATION/STATUS
# are untouched — only the banded field changes.
function organize_priority_unspecified_clears { # @test
  generate_priority
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  move_box 'field1.ics' 3_unspecified "$edited"

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
}
