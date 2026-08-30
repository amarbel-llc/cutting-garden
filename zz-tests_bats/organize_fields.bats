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
#
# Every organize step is a WHOLE-DOCUMENT vector (native tags design G16) with
# verbatim `_base` digests, so the testserver is pinned to this file's own port
# (43106) and the file's tests are serialized (see lib/caldav.bash).

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server 43106
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_fields runs `organize -group-by priority` and asserts the document in
# full; its field1 box surfaces the editable location/status/priority atoms +
# SUMMARY trailer.
generate_fields() {
  run_cg organize -group-by priority "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-dvhce5xqj6ece5w8m4r4qrqkhfs9fwywjuwytgregd2qjf55t4gspan0z4
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
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

# write_field1_edited writes the generated document to $2 with field1's box line
# replaced by $1 — a field edit against the generated `_base`, never a move.
write_field1_edited() {
  local box="$1" out="$2"
  cat >"$out" <<-EOM
	---
	% generated: \`cg organize -group-by priority -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/\`
	- _base = @blake2b256-dvhce5xqj6ece5w8m4r4qrqkhfs9fwywjuwytgregd2qjf55t4gspan0z4
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	$box

	## =1_should

	- [field2.ics priority=5] Read book

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM
}

# assert_field1_office re-renders the calendar and asserts the "after" document
# of the location edit in full: field1's box now reads location=Office in its
# unchanged 0_must bucket, and the `_base` pin moved with the content.
assert_field1_office() {
  run_cg organize -group-by priority "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-ds4f8w92rpvu69uknkg748xdv0vc0e93xhpksccxederq4e4s7xssznydf
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Office status=NEEDS-ACTION priority=1] Pay rent

	## =1_should

	- [field2.ics priority=5] Read book

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM
}

# Editing a plain atom value (location Bank -> Office) writes the property through
# FieldWriteApplier — the box stays in its bucket, so this is a field edit, not a
# move. The unchanged status/priority atoms produce no edit.
function organize_fields_location_edit_writes { # @test
  generate_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_field1_edited '- [field1.ics location=Office status=NEEDS-ACTION priority=1] Pay rent' "$edited"

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

  assert_field1_office
}

# Editing the SUMMARY trailer writes it back to the same field it was rendered from
# (the declared trailer field), and the diff shows it as a word-level diff, not a
# whole-value atom diff.
function organize_fields_summary_trailer_edit_writes { # @test
  generate_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_field1_edited '- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent now' "$edited"

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

  run_cg organize -group-by priority "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-xknyqykj8dwheke548ydh3mzkqyhgv3hvym9lh9728htpxwxezjqlvk092
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent now

	## =1_should

	- [field2.ics priority=5] Read book

	## =2_nice

	- [field3.ics priority=9] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM
}

# A pinned base whose live field has drifted is a conflict, not a silent clobber:
# commit one location edit, then apply a second built against the ORIGINAL base
# (still location=Bank) — the merge must reject because the live value is Office,
# and the live document still shows only the first edit.
function organize_fields_conflict_rejects { # @test
  generate_fields
  local edited_a="$BATS_TEST_TMPDIR/edited_a.txt"
  write_field1_edited '- [field1.ics location=Office status=NEEDS-ACTION priority=1] Pay rent' "$edited_a"
  run_cg organize -apply "$edited_a" -commit
  assert_success

  local edited_b="$BATS_TEST_TMPDIR/edited_b.txt"
  write_field1_edited '- [field1.ics location=Warehouse status=NEEDS-ACTION priority=1] Pay rent' "$edited_b"
  run_cg organize -apply "$edited_b" -commit
  assert_failure
  assert_output --partial 'conflict'

  assert_field1_office
}
