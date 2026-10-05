#! /usr/bin/env bats

# The organize CREATION lane (forge organize F8/F8b/F8c/F9/F10, RFC 0015
# §Creation): a box whose id is a temp id — `+<id>`, `+"<id>"`, or a bare `+` —
# creates an object. Every appearance of one temp id is ONE new object whose
# tags are the union of its placement tags and typed tag atoms and whose
# single-valued fields (atoms, the grouped `=bucket`) must agree; the plugin
# (caldav: VTODO needs a summary, VEVENT a start date) builds the body and
# assigns the identity, reported as `organize: created +x → <id>`. A box id
# that is neither a temp id nor in the pinned base is refused (F8c), as is a
# creation missing a required field or whose appearances disagree — all at
# plan time, before anything is written.
#
# The fixture is the /dav/fields/ calendar (CG_TEST_CALDAV_FIELDS, see
# organize_tags.bats / organize_fields.bats). A created object's UID — and so
# its `<uid>.ics` resource name — is the creation's IDEMPOTENCY KEY
# (`cgk1-` + 32 hex, derived from the document's `_base` and the temp id): it
# is deterministic, but the vectors run through normalize_created (every key
# becomes `<key>`, the key-dependent `_base` pin `<digest>`) so they read as
# shapes, and new objects are always filed under otherwise EMPTY buckets.
#
# Re-applying is guarded twice: the host's creation receipt (keyed by the
# `_base` digest, in the XDG state dir) skips a recorded creation, and caldav
# recognizes a repeated key (a 412 on `<key>.ics` whose UID is the key).
#
# Whole-document vectors (G16): pinned port + serialized tests, see
# lib/caldav.bash. Regenerate with `just test-bats-update-vectors
# organize_create.bats`.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/vectors.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server 24116
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# normalize_created replaces the creation keys (`cgk1-` + 32 hex) in $output
# with `<key>` and the `_base` pin (which content-addresses them) with
# `<digest>`.
normalize_created() {
  output="$(sed -E -e 's/cgk1-[0-9a-f]{32}/<key>/g' \
    -e 's/^- _base = @blake2b256-[a-z0-9]+$/- _base = @<digest>/' <<<"$output")"
}

# status_document is the `-group-by status=` document for the fixture.
status_document() {
  cat <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:24116/dav/fields/`
	- _base = @blake2b256-fhmdql3ctryqgngawgqhxgucyscza9dx0rfthum6s68me4fxky3qu3nh35
	- _anchor = caldav:http://127.0.0.1:24116/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics errand work priority=1_should] Read book
	- [field3.ics work priority=2_nice] Water plants
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

# assert_status_document_unchanged (re)generates the status document and pins
# it byte for byte to the original — run first, it pins the base blob an apply
# merges against into the store; run after a refusal, it is the "nothing was
# written" check.
assert_status_document_unchanged() {
  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_vector "$(status_document)"
}

# A temp id under two TAG headings is ONE object: its tags are both
# placements, its status atom rides one appearance, and its wrapped trailer
# merges with the other's single-line twin (whitespace-collapsed). The preview
# marks every slot added; the summary maps the temp id to the minted id; the
# regenerated document files the object under both new buckets.
function organize_create_merges_tag_appearances { # @test
  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:24116/dav/fields/`
	- _base = @blake2b256-0khrdlna5epav5eplpzhc2mqz3pdcr60sj7kmud3dmrplns3qnasu8h830
	- _anchor = caldav:http://127.0.0.1:24116/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [field1.ics location=Bank status=needs-action priority=0_must] Pay rent
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# errand

	- [field2.ics work priority=1_should] Read book

	# work

	- [field2.ics errand priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	EOM

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:24116/dav/fields/`
	- _base = @blake2b256-0khrdlna5epav5eplpzhc2mqz3pdcr60sj7kmud3dmrplns3qnasu8h830
	- _anchor = caldav:http://127.0.0.1:24116/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [field1.ics location=Bank status=needs-action priority=0_must] Pay rent
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# errand

	- [field2.ics work priority=1_should] Read book

	# groceries

	- [+milk status=in-process] Buy oat milk

	# shopping

	- [+milk] Buy oat
	  milk

	# work

	- [field2.ics errand priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  normalize_created
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [+milk {+groceries+} {+shopping+} status={+in-process+}] {+Buy oat milk+}

organize: created +milk → <key>.ics
organize: wrote 1 change(s)
EOF

  run_cg list -format json -query 'categories=groceries' "$CAL"
  assert_success
  normalize_created
  assert_output '{"uri":"caldav:http://127.0.0.1:24116/dav/fields/<key>.ics","name":"<key>.ics","type":"caldav-object-vtodo-v1","tags":["groceries","shopping"]}'

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  normalize_created
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:24116/dav/fields/`
	- _base = @<digest>
	- _anchor = caldav:http://127.0.0.1:24116/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [field1.ics location=Bank status=needs-action priority=0_must] Pay rent
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# errand

	- [field2.ics work priority=1_should] Read book

	# groceries

	- [<key>.ics shopping status=in-process] Buy oat milk

	# shopping

	- [<key>.ics groceries status=in-process] Buy oat milk

	# work

	- [field2.ics errand priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	EOM
}

# A VTODO created under a `status=` bucket takes the bucket as its status and
# its box atoms as fields (the priority band completes to PRIORITY:1). A
# headless dry-run previews and writes nothing; -commit creates it.
function organize_create_vtodo_under_status_bucket { # @test
  assert_status_document_unchanged

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  status_document |
    sed 's/^## =in-process$/## =in-process\n\n- [+call priority=0_must] Call the bank/' >"$edited"

  run_cg organize -apply "$edited"
  assert_success
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [+call priority={+0_must+} status={+in-process+}] {+Call the bank+}

organize: dry-run — nothing written
EOF
  assert_status_document_unchanged

  run_cg organize -apply "$edited" -commit
  assert_success
  normalize_created
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [+call priority={+0_must+} status={+in-process+}] {+Call the bank+}

organize: created +call → <key>.ics
organize: wrote 1 change(s)
EOF

  # A verbatim re-apply of the committed document creates nothing: the
  # receipt records +call for this `_base`. The key is deterministic — the
  # document's `_base` and the temp id derive it.
  run_cg organize -apply "$edited" -commit
  assert_success
  assert_vector - <<'EOF'
organize: +call already created → cgk1-67565802aeedc494981e994948824794.ics (skipped)
organize: no changes to apply
EOF
  run_cg list -format json -query 'status=in-process' "$CAL"
  assert_success
  assert_vector - <<'EOF'
{"uri":"caldav:http://127.0.0.1:24116/dav/fields/cgk1-67565802aeedc494981e994948824794.ics","name":"cgk1-67565802aeedc494981e994948824794.ics","type":"caldav-object-vtodo-v1"}
EOF

  run_cg organize -group-by status= "$CAL"
  assert_success
  normalize_created
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:24116/dav/fields/`
	- _base = @<digest>
	- _anchor = caldav:http://127.0.0.1:24116/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics errand work priority=1_should] Read book
	- [field3.ics work priority=2_nice] Water plants
	- [field4.ics] Someday idea
	- [field5.ics] Waiting idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =in-process

	- [<key>.ics priority=0_must] Call the bank

	## =completed

	## =cancelled
	EOM
}

# A VEVENT in a VTODO document needs an explicit `!type` (F9) and a start date
# (its required field); the created event is read back off the server — its
# date is fixed, so a listing would drift out of caldav's expansion window.
function organize_create_vevent_with_a_date { # @test
  assert_status_document_unchanged
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  status_document |
    sed 's/^- \[field5.ics\] Waiting idea$/&\n- [+dentist !caldav-object-vevent-v1 date_start=2026-10-01] Dentist/' >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  local uid
  uid="$(sed -n 's/^organize: created +dentist → \(cgk1-[0-9a-f]\{32\}\)\.ics$/\1/p' <<<"$output")"
  [[ -n $uid ]] || fail "no created line for +dentist in: $output"
  normalize_created
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [+dentist !caldav-object-vevent-v1 date_start={+2026-10-01+}] {+Dentist+}

organize: created +dentist → <key>.ics
organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/$uid.ics"
  assert_success
  assert_line --regexp '^BEGIN:VEVENT[[:space:]]*$'
  assert_line --regexp "^UID:${uid}[[:space:]]*\$"
  assert_line --regexp '^SUMMARY:Dentist[[:space:]]*$'
  assert_line --regexp '^DTSTART;VALUE=DATE:20261001[[:space:]]*$'
  refute_line --regexp '^STATUS:'
}

# A creation missing a required field (a VEVENT's date_start) is refused at
# plan time: exit 64, nothing written.
function organize_create_refuses_a_missing_required_field { # @test
  assert_status_document_unchanged
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  status_document |
    sed 's/^- \[field5.ics\] Waiting idea$/&\n- [+meet !caldav-object-vevent-v1] Meeting/' >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_vector - <<'EOF'
cutting-garden: organize --apply: 1 new object(s) cannot be created:
  +meet (line 5): creating a !caldav-object-vevent-v1 requires date_start
EOF
  assert_status_document_unchanged
}

# F8c: a box id that is neither a temp id nor in the pinned base is refused —
# headless it aborts the whole apply (exit 64, nothing written), naming the
# line and suggesting a temp id; even the document's valid edits are not
# applied.
function organize_create_refuses_an_unknown_id_headless { # @test
  assert_status_document_unchanged
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  status_document |
    sed -e 's/^- \[field5.ics\] Waiting idea$/&\n- [typo.ics] Not a real object/' \
      -e 's/^## =cancelled$/## =cancelled\n\n- [+call] Call the bank/' >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  # shellcheck disable=SC2016  # the backticks are the message's own quoting
  assert_output 'cutting-garden: organize --apply: body line 5: box id typo.ics is not in the pinned base — an existing object cannot be added by typing its id; to CREATE an object, give its box a temp id (`[+typo.ics …]`, or a bare `[+ …]`)'
  assert_status_document_unchanged
}

# F8b: the appearances of one temp id must agree on every single-valued field —
# here the grouped status (two different buckets). Refused, nothing written.
function organize_create_refuses_disagreeing_appearances { # @test
  assert_status_document_unchanged
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  status_document |
    sed -e 's/^- \[field1.ics location=Bank priority=0_must\] Pay rent$/&\n- [+x] Call/' \
      -e 's/^## =in-process$/## =in-process\n\n- [+x] Call/' >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_vector - <<'EOF'
cutting-garden: organize --apply: 1 problem(s) with new object(s) (`+` boxes); re-edit the document:
  +x (lines 11, 15): appearances disagree on status: needs-action (line 11, its heading) vs in-process (line 15, its heading)
EOF
  assert_status_document_unchanged
}

# A write that fails AFTER a creation landed (here field1's priority edit to a
# value caldav refuses at execution) names the landed creation, and -apply
# warns that the file (never rewritten) still names it by temp id.
# Re-applying the fixed document skips the creation via the receipt; with the
# receipt gone, caldav itself recognizes the repeated idempotency key. Either
# way: one object, never a duplicate.
function organize_create_failure_after_creation_is_reported_and_reapply_is_idempotent { # @test
  assert_status_document_unchanged
  local edited="$BATS_TEST_TMPDIR/edited.txt" fixed="$BATS_TEST_TMPDIR/fixed.txt"
  status_document |
    sed -e 's/^- \[field1.ics location=Bank priority=0_must\] Pay rent$/- [field1.ics location=Bank priority=bogus] Pay rent/' \
      -e 's/^## =in-process$/## =in-process\n\n- [+call priority=0_must] Call the bank/' >"$edited"
  sed 's/priority=bogus/priority=0_must/' "$edited" >"$fixed"

  run_cg organize -apply "$edited" -commit
  assert_failure 2
  normalize_created
  output="${output//$edited/<edited>}"
  assert_vector - <<'EOF'
organize: 2 change(s):

  - [+call priority={+0_must+} status={+in-process+}] {+Call the bank+}
  - [field1.ics location=Bank priority=[-0_must-]{+bogus+}] Pay rent

organize: created +call → <key>.ics
organize: WARNING — <edited> still names +call by temp id, but they now exist; re-applying it unedited skips them (the creation ledger), and edits to their boxes are not applied — regenerate to edit them
cutting-garden: organize: apply failed after creating objects — already created: +call → <key>.ics; no further writes were attempted (re-applying this document skips them — the creation ledger): priority "bogus" is neither an integer nor a priority band (0_must, 1_should, 2_nice, 3_unspecified)
EOF

  run_cg organize -apply "$fixed" -commit
  assert_success
  normalize_created
  assert_vector - <<'EOF'
organize: +call already created → <key>.ics (skipped)
organize: no changes to apply
EOF

  # Lose the host receipt: the create is sent again, and caldav answers the
  # repeated key with the existing object.
  find "$HOME" -type d -name organize-creations -prune -exec rm -rf {} +
  run_cg organize -apply "$fixed" -commit
  assert_success
  normalize_created
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [+call priority={+0_must+} status={+in-process+}] {+Call the bank+}

organize: +call already existed → <key>.ics (idempotency key)
organize: wrote 1 change(s)
EOF

  run_cg list -format json -query 'status=in-process' "$CAL"
  assert_success
  normalize_created
  assert_output '{"uri":"caldav:http://127.0.0.1:24116/dav/fields/<key>.ics","name":"<key>.ics","type":"caldav-object-vtodo-v1"}'
}
