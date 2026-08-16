#! /usr/bin/env bats

# The organize field-edit write-back lane (FDR 0023 field write-side,
# cutting-garden#218 / #55): editing a box-interior ATOM value (location, status,
# priority) or the box's SUMMARY trailer in place, then applying, writes the change
# through the plugin's FieldWriteApplier as a PatchNode — the field-edit sibling of
# organize.bats's bucket-move lane. Previously this path was covered only by
# in-process unit tests; this is the e2e regression net the FDR 0025 codec
# migration (which rewrites the present/parse/apply field surface) is measured
# behaviour-neutral against.
#
# The fixture calendar (/dav/fields/, opt-in via CG_TEST_CALDAV_FIELDS) holds
# field1 "Pay rent" carrying LOCATION Bank + STATUS NEEDS-ACTION + PRIORITY 1 — the
# richest editable box the fixtures provide. Grouped by priority so the edited
# fields (location, summary) are orthogonal to the grouping dimension: a field edit,
# never a move.

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

# generate_fields runs `organize -group-by priority` and saves the document, whose
# field1 box surfaces the editable location/status/priority atoms + SUMMARY trailer.
generate_fields() {
  run_cg organize -group-by priority "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/fields.txt"
  printf '%s\n' "$output" >"$DOC"
}

# Editing a plain atom value (location Bank -> Office) writes the property through
# FieldWriteApplier — the box stays in its bucket, so this is a field edit, not a
# move. The unchanged status/priority atoms produce no edit.
function organize_fields_location_edit_writes { # @test
  generate_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  sed 's/location=Bank/location=Office/' "$DOC" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field1.ics  location=[-Bank-]{+Office+}]  Pay rent

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field1.ics"
  assert_success
  assert_output --partial 'LOCATION:Office'
}

# Editing the SUMMARY trailer writes it back to the same field it was rendered from
# (the declared trailer field), and the diff shows it as a word-level diff, not a
# whole-value atom diff.
function organize_fields_summary_trailer_edit_writes { # @test
  generate_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  sed 's/\] Pay rent/] Pay rent now/' "$DOC" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field1.ics]  Pay rent {+now+}

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field1.ics"
  assert_success
  assert_output --partial 'SUMMARY:Pay rent now'
}

# A pinned base whose live field has drifted is a conflict, not a silent clobber:
# commit one location edit, then apply a second built against the ORIGINAL base
# (still location=Bank) — the merge must reject because the live value is Office.
function organize_fields_conflict_rejects { # @test
  generate_fields
  local edited_a="$BATS_TEST_TMPDIR/edited_a.txt"
  sed 's/location=Bank/location=Office/' "$DOC" >"$edited_a"
  run_cg organize -apply "$edited_a" -commit
  assert_success

  local edited_b="$BATS_TEST_TMPDIR/edited_b.txt"
  sed 's/location=Bank/location=Warehouse/' "$DOC" >"$edited_b"
  run_cg organize -apply "$edited_b" -commit
  assert_failure
  assert_output --partial 'conflict'
}
