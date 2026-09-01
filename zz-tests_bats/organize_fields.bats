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
# never a move. Since native tags slice 1.5 D the priority atom presents its BAND
# and thus equals its bucket key, so it is stripped from every box here (#229).
#
# field5 "Waiting idea" carries NO STATUS property at all (and no priority/
# location/categories): the missing-STATUS coverage object (native tags slice
# 1.5 C). Under `--group-by status=` it sits UNGROUPED above the first heading
# with no status atom in its box; moving it under a `## =<value>` bucket
# ASSIGNS the property, while an un-bucketed placement stays a NO-OP across an
# apply (the RFC 0015 write:one rule for an exclusive field — absence writes
# nothing).
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
  start_caldav_server 43106
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_fields runs `organize -group-by priority=` (the field spelling,
# design G10) and asserts the document in full; its field1 box surfaces the
# editable location/status atoms + SUMMARY trailer (the grouped priority atom
# is stripped, #229).
generate_fields() {
  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-pg34g6682ss3dxfet4gs8asvvqxdlatv670y43rm3qsmncf2pa8s5ghcgg
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=needs-action] Pay rent

	## =1_should

	- [field2.ics] Read book

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea
	EOM
}

# write_field1_edited writes the generated document to $2 with field1's box line
# replaced by $1 — a field edit against the generated `_base`, never a move.
write_field1_edited() {
  local box="$1" out="$2"
  cat >"$out" <<-EOM
	---
	% generated: \`cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/\`
	- _base = @blake2b256-pg34g6682ss3dxfet4gs8asvvqxdlatv670y43rm3qsmncf2pa8s5ghcgg
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	$box

	## =1_should

	- [field2.ics] Read book

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea
	EOM
}

# assert_field1_office re-renders the calendar and asserts the "after" document
# of the location edit in full: field1's box now reads location=Office in its
# unchanged 0_must bucket, and the `_base` pin moved with the content.
assert_field1_office() {
  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-su2pf2rznltena8297gf433dwym4vm6erpa5lzanaxpu5efv2kdsdpgt4h
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Office status=needs-action] Pay rent

	## =1_should

	- [field2.ics] Read book

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea
	EOM
}

# Editing a plain atom value (location Bank -> Office) writes the property through
# FieldWriteApplier — the box stays in its bucket, so this is a field edit, not a
# move. The unchanged status atom produces no edit.
function organize_fields_location_edit_writes { # @test
  generate_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_field1_edited '- [field1.ics location=Office status=needs-action] Pay rent' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field1.ics  location=[-Bank-]{+Office+}]  Pay rent

organize: wrote 1 change(s)
EOF

  # Exact full line (the rewritten body is CRLF-serialized with a volatile
  # DTSTAMP, so the whole body cannot be pinned).
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field1.ics"
  assert_success
  assert_line --regexp $'^LOCATION:Office\r?$'

  assert_field1_office
}

# Editing the SUMMARY trailer writes it back to the same field it was rendered from
# (the declared trailer field), and the diff shows it as a word-level diff, not a
# whole-value atom diff.
function organize_fields_summary_trailer_edit_writes { # @test
  generate_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_field1_edited '- [field1.ics location=Bank status=needs-action] Pay rent now' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field1.ics]  Pay rent {+now+}

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field1.ics"
  assert_success
  assert_line --regexp $'^SUMMARY:Pay rent now\r?$'

  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-qxnt8kxzm59ejqsgr5nueww60cx64xp9ra5wgzecp6akyald25nszyx99t
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=needs-action] Pay rent now

	## =1_should

	- [field2.ics] Read book

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea
	EOM
}

# A pinned base whose live field has drifted is a conflict, not a silent clobber:
# commit one location edit, then apply a second built against the ORIGINAL base
# (still location=Bank) — the merge must reject because the live value is Office,
# and the live document still shows only the first edit.
function organize_fields_conflict_rejects { # @test
  generate_fields
  local edited_a="$BATS_TEST_TMPDIR/edited_a.txt"
  write_field1_edited '- [field1.ics location=Office status=needs-action] Pay rent' "$edited_a"
  run_cg organize -apply "$edited_a" -commit
  assert_success

  local edited_b="$BATS_TEST_TMPDIR/edited_b.txt"
  write_field1_edited '- [field1.ics location=Warehouse status=needs-action] Pay rent' "$edited_b"
  run_cg organize -apply "$edited_b" -commit
  assert_failure 2
  # The whole rejection: the exact refused field edit with base/live/edited
  # values named.
  assert_output - <<-'EOM'
	cutting-garden: organize --apply: 1 field conflict(s) — the live state drifted from the pinned base; regenerate and re-edit:
	  field1.ics.location: base="Bank" live="Office" (your edit set "Warehouse")
	EOM

  assert_field1_office
}

# generate_status_doc runs `organize -group-by status=` and asserts the
# missing-STATUS review surface in full (native tags slice 1.5 C): every
# status-less object — field2/field3/field4 and the designated STATUS-free
# field5 — sits UNGROUPED above the `# status=` heading with NO status atom in
# its box; field1 files under =needs-action (its grouped atom elided, #229).
generate_status_doc() {
  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-c42kx0gd96e59vnu3nq7s3zpgkf6hyr78dsgqxrknk28yhsdye2qxtgdk3
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# A VTODO missing its STATUS property entirely is presented OUTSIDE the groups:
# field5 lands in the ungrouped set above the first heading, with no status
# atom in its box — the whole generated document is the review surface.
function organize_fields_missing_status_ungrouped { # @test
  generate_status_doc
}

# Moving the STATUS-free field5 under `## =needs-action` ASSIGNS the property —
# the RFC 0015 write:one "move writes" half. The stored property is canonical
# RFC 5545 UPPERCASE (Task E: lowercase is presentation-only, never persisted),
# and the after-document files field5 under the bucket with its grouped status
# atom stripped (#229) — it reads exactly as a freshly generated document.
function organize_fields_missing_status_move_in_writes { # @test
  generate_status_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-c42kx0gd96e59vnu3nq7s3zpgkf6hyr78dsgqxrknk28yhsdye2qxtgdk3
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent
	- [field5.ics] Waiting idea

	## =in-process

	## =completed

	## =cancelled
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field5.ics  status={+needs-action+}]  Waiting idea

organize: wrote 1 change(s)
EOF

  # Exact full line (the rewritten body is CRLF-serialized with a volatile
  # DTSTAMP, so the whole body cannot be pinned).
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field5.ics"
  assert_success
  assert_line --regexp $'^STATUS:NEEDS-ACTION\r?$'
  refute_output --partial 'STATUS:needs-action'

  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-j8n6k5t4azyny4n3v9enu39ezcsk3rtdq05cc58w40eaw2wfm5lsdw7ljt
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent
	- [field5.ics] Waiting idea

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# The write:one rule's other half (RFC 0015): for an EXCLUSIVE field, ABSENCE
# is a NO-OP — leaving field5 un-bucketed (ungrouped) across an apply writes
# NOTHING to it, even while the same apply commits an unrelated move (field1
# =needs-action -> =in-process). The live object is asserted byte-for-byte:
# still the seeded fixture body, no STATUS property ever appeared.
function organize_fields_missing_status_absence_is_noop { # @test
  generate_status_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-c42kx0gd96e59vnu3nq7s3zpgkf6hyr78dsgqxrknk28yhsdye2qxtgdk3
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# status=

	## =needs-action

	## =in-process

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =completed

	## =cancelled
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field1.ics  status=[-needs-action-]{+in-process+}]  Pay rent

organize: wrote 1 change(s)
EOF

  # field5 was untouched: the live object is byte-identical to the seeded
  # fixture (a write would have rewritten it with PRODID/DTSTAMP) — and above
  # all, no STATUS.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field5.ics"
  assert_success
  assert_output - <<'EOF'
BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VTODO
UID:field5
SUMMARY:Waiting idea
END:VTODO
END:VCALENDAR
EOF

  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43106/dav/fields/`
	- _base = @blake2b256-zyjfuu55dqypep95zjq9mpzcaykjdavp4444uvcj35nfw64tlmdqfqtuj6
	- _anchor = caldav:http://127.0.0.1:43106/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# status=

	## =needs-action

	## =in-process

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =completed

	## =cancelled
	EOM
}
