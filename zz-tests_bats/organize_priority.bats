#! /usr/bin/env bats

# The organize priority-reorganization lane (FDR 0023 / cutting-garden#221, #77):
# grouping a task calendar by the `priority` facet buckets every VTODO into one of
# four urgency-first bands (0_must / 1_should / 2_nice / 3_unspecified), and moving
# a task's box line between `## =<band>` buckets REWRITES its RFC 5545 PRIORITY —
# the FacetWriteApplier completes the band to its canonical value (must->1,
# should->5, nice->9, unspecified->0, which the serializer OMITS, clearing the
# property). A bucket->value completion like the month reschedule, distinct from
# status's verbatim passthrough (organize.bats). Since native tags slice 1.5 D the
# box atom ALSO presents the band — the same derived value the buckets use — so a
# `--group-by priority=` document strips the redundant atom (#229) and a
# band-valued atom EDIT completes identically (the field-edit lanes below). This
# is the e2e regression net the FDR 0025 codec migration is measured
# behaviour-neutral against.
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

# generate_priority runs `organize -group-by priority=` (the field spelling,
# design G10) and asserts the document in full: the four bands pre-rendered
# urgency-first, each task under its own band. The priority atom presents the
# BAND (native tags slice 1.5 D), so under its own `## =<band>` heading it is
# pure redundancy and STRIPPED (#229) — boxes carry only location/status where
# present.
generate_priority() {
  run_cg organize -group-by priority= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-fyfaty53qlcyjpu0y6fquexdtefjl0nl757tugwfgggh7srrsjhs74luk9
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
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
	EOM
}

# Grouping by priority pre-renders the four bands (urgency-first) and files each
# task under its own band; the grouped priority atom is elided from every box
# (#229 — the band atom equals its bucket key), leaving location/status only.
function organize_priority_generate_buckets { # @test
  generate_priority
}

# The core tracer: move field2 from 1_should to 0_must and commit. The box
# carries no priority atom (stripped, #229), so the reorganize is a pure move;
# the band completes to its canonical PRIORITY value (must->1) on the live
# object, and the re-rendered document shows field2 under 0_must.
function organize_priority_band_move_rewrites { # @test
  generate_priority
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-fyfaty53qlcyjpu0y6fquexdtefjl0nl757tugwfgggh7srrsjhs74luk9
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=needs-action] Pay rent
	- [field2.ics] Read book

	## =1_should

	## =2_nice

	- [field3.ics] Water plants

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
	- _base = @blake2b256-lf0td5kvhtkhtsakdp643e0cfhewepsh3a2k0gs56p2jqhzwfppsa2y4sk
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	- [field1.ics location=Bank status=needs-action] Pay rent
	- [field2.ics] Read book

	## =1_should

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field4.ics] Someday idea
	EOM
}

# Moving into 3_unspecified completes to PRIORITY 0, which the serializer OMITS:
# the property is CLEARED, not written as literal 0. The task's LOCATION/STATUS
# are untouched — only the banded field changes — so the re-rendered box keeps
# its location/status atoms.
function organize_priority_unspecified_clears { # @test
  generate_priority
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by priority= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-fyfaty53qlcyjpu0y6fquexdtefjl0nl757tugwfgggh7srrsjhs74luk9
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	## =1_should

	- [field2.ics] Read book

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field1.ics location=Bank status=needs-action] Pay rent
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
	- _base = @blake2b256-l5vuyk2fp4zamvayztt2d0khf8xljamjan667x45zaj56veehxqsmj4jpa
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# priority=

	## =0_must

	## =1_should

	- [field2.ics] Read book

	## =2_nice

	- [field3.ics] Water plants

	## =3_unspecified

	- [field1.ics location=Bank status=needs-action] Pay rent
	- [field4.ics] Someday idea
	EOM
}

# generate_status_fields runs `organize -group-by status=` on the same calendar —
# a grouping ORTHOGONAL to priority, so every box shows its band atom (nothing
# strips): the status-less field2/field3/field4 sit ungrouped above the heading,
# field1 under =needs-action (its grouped status atom elided instead).
generate_status_fields() {
  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-3m5m48ukpehpcahvufllcrvr4fe8fsfext48fsvj29j0fud364cshzwd7n
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
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

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# A band-valued atom EDIT behaves like the bucket move (native tags slice 1.5 D):
# editing field3's priority=2_nice to priority=0_must in place — grouped by
# status=, so this is a FIELD edit, never a move — completes to the must band's
# canonical RFC 5545 PRIORITY (1) on the live object, curl-verified, and the
# re-rendered atom reads 0_must.
function organize_priority_field_edit_band_completes { # @test
  generate_status_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-3m5m48ukpehpcahvufllcrvr4fe8fsfext48fsvj29j0fud364cshzwd7n
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=0_must] Water plants
	- [field4.ics] Someday idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =in-process

	## =completed

	## =cancelled
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field3.ics  priority=[-2_nice-]{+0_must+}]  Water plants

organize: wrote 1 change(s)
EOF

  # The band edit wrote the must-band canonical PRIORITY (1) into the live object.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field3.ics"
  assert_success
  assert_output --partial 'PRIORITY:1'

  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-0xmp6w0494w6xtq29frp87k7l26wtz0z0v0tj4garxn6307na75s6lmehs
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=0_must] Water plants
	- [field4.ics] Someday idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# The pinned asymmetry (native tags slice 1.5 D): the band presentation is LOSSY,
# so an explicit raw-integer edit (priority=7) still writes the exact RFC 5545
# value verbatim — the power-user path to an intra-band value the band spelling
# cannot express — curl-verified, and the re-rendered atom presents 7's band
# (2_nice).
function organize_priority_field_edit_raw_int_writes_verbatim { # @test
  generate_status_fields
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-3m5m48ukpehpcahvufllcrvr4fe8fsfext48fsvj29j0fud364cshzwd7n
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=7] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =in-process

	## =completed

	## =cancelled
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field2.ics  priority=[-1_should-]{+7+}]  Read book

organize: wrote 1 change(s)
EOF

  # The raw int landed verbatim — not band-completed.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field2.ics"
  assert_success
  assert_output --partial 'PRIORITY:7'

  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43105/dav/fields/`
	- _base = @blake2b256-wy3v0r7yk5srvv7wqewy055jptxcm3epslgmd38kur9zv8aaqjwskpqlq7
	- _anchor = caldav:http://127.0.0.1:43105/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=2_nice] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea

	# status=

	## =needs-action

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =in-process

	## =completed

	## =cancelled
	EOM
}
