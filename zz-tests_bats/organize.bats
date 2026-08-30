#! /usr/bin/env bats

# The organize pipeline lane (FDR 0023, RFC 0015): generate a hyphence-envelope
# organize document from a caldav calendar, edit it, and apply the edit as a
# substrate write — proving select -> group -> render -> edit -> three-way-merge
# -> PatchNode -> verify end to end against cutting-garden-caldav-testserver.
#
# The document is the RFC 0015 heading-ladder dialect: a `---` hyphence envelope
# (% comment, - _base/_anchor/_type, ! organize-base-v1), then a `# status=`
# dimension heading with pre-rendered `## =VALUE` buckets, and object lines as
# espalier boxes `- [<id>] <desc>` (envelope `_type` spelling). The writable
# dimension exercised is `status` (a passthrough enum, Slice 2a). The seeded
# VTODOs carry no STATUS, so they start ungrouped (above the dimension heading);
# moving task1 under a `## =COMPLETED` bucket ASSIGNS its status through PatchNode.
#
# Every step is a WHOLE-DOCUMENT vector (native tags design G16): the generated
# document, the edited input, and the post-apply re-render are asserted in full,
# `_base` digests verbatim. That needs a stable server URL, so the testserver is
# pinned to this file's own port (43101) and the file's tests are serialized
# (see lib/caldav.bash).

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  start_caldav_server 43101
  init_store
  # The Personal calendar holds task1.ics + task2.ics (VTODO); its VEVENT
  # (event1.ics) windows out of the object listing (#176/#177).
  CAL="${CALDAV_SOURCE%/dav/}/dav/cal/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_doc runs `organize -group-by status` and asserts the emitted document
# in full: the fenced envelope with the framework fields + type, the two VTODOs
# ungrouped (no STATUS yet), then the `# status=` dimension heading with its
# pre-rendered, empty `## =VALUE` buckets. The lone VEVENT windows out of the
# object listing, so it is not organized.
generate_doc() {
  run_cg organize -group-by status "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status -query "_terminal=no" caldav:http://127.0.0.1:43101/dav/cal/`
	- _base = @blake2b256-tctuw2agyz68ny7knvp3s7z7rkq88pzl4w28frf3343v6wt87uwse3ffes
	- _anchor = caldav:http://127.0.0.1:43101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task1.ics] Buy milk
	- [task2.ics] Walk dog

	# status=

	## =NEEDS-ACTION

	## =IN-PROCESS

	## =COMPLETED

	## =CANCELLED
	EOM
}

# write_task1_under writes the edited document to $2: task1's box is pulled out
# of the ungrouped section and re-filed under the `## =<$1>` bucket, against the
# generated `_base`.
write_task1_under() {
  local value="$1" out="$2"
  cat >"$out" <<-EOM
	---
	% generated: \`cg organize -group-by status -query "_terminal=no" caldav:http://127.0.0.1:43101/dav/cal/\`
	- _base = @blake2b256-tctuw2agyz68ny7knvp3s7z7rkq88pzl4w28frf3343v6wt87uwse3ffes
	- _anchor = caldav:http://127.0.0.1:43101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task2.ics] Walk dog

	# status=

	## =NEEDS-ACTION

	## =IN-PROCESS

	## =COMPLETED

	## =CANCELLED
	EOM
  # Re-file task1 under the target bucket (its heading is pre-rendered).
  sed -i "s|^## =$value\$|## =$value\n\n- [task1.ics] Buy milk|" "$out"
}

# assert_task1_completed re-renders the calendar and asserts the "after"
# document in full. organize's default `_terminal=no` selection windows a
# COMPLETED task OUT of the document, so task1 is simply gone: only task2 remains
# (still ungrouped), every bucket is empty, and the `_base` pin moved with the
# content. The live status itself is proven by the `list -query` check.
assert_task1_completed() {
  run_cg organize -group-by status "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status -query "_terminal=no" caldav:http://127.0.0.1:43101/dav/cal/`
	- _base = @blake2b256-chphhva9fu4yqz6uvl43vxedv59t7y9h2tt3g362d0p2emnf5xvsasermd
	- _anchor = caldav:http://127.0.0.1:43101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task2.ics] Walk dog

	# status=

	## =NEEDS-ACTION

	## =IN-PROCESS

	## =COMPLETED

	## =CANCELLED
	EOM
}

# Generate emits the hyphence-envelope dialect: the fenced envelope with the
# framework fields + type, the `# status=` dimension heading, its pre-rendered
# `## =VALUE` buckets, and the two VTODOs as bare espalier boxes.
function organize_generate_emits_envelope { # @test
  generate_doc
}

# The core tracer: generate, move task1 under `## =COMPLETED`, apply with
# --commit, and confirm the status landed on the live object via a facet query
# and the re-rendered document.
function organize_apply_status_move_commits { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_task1_under COMPLETED "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [task1.ics  status={+COMPLETED+}]  Buy milk

organize: wrote 1 change(s)
EOF

  run_cg list -query 'status=COMPLETED' "$CAL"
  assert_success
  assert_output --partial 'task1.ics'
  refute_output --partial 'task2.ics'

  assert_task1_completed
}

# The default is a dry-run: apply without --commit prints the intended move but
# does not write, so the live status query still finds nothing and the document
# re-renders unchanged.
function organize_apply_dry_run_does_not_write { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_task1_under COMPLETED "$edited"

  run_cg organize -apply "$edited"
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [task1.ics  status={+COMPLETED+}]  Buy milk

organize: dry-run — nothing written
EOF

  run_cg list -query 'status=COMPLETED' "$CAL"
  assert_success
  refute_output --partial 'task1.ics'

  generate_doc
}

# -commit-directly reads the edited document from stdin and commits it — the
# scripted re-apply path (dodder's commit-directly mode; the mode itself is the
# commit assertion). Proves stdin ingestion writes through to the live object.
function organize_commit_directly_from_stdin_writes { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_task1_under COMPLETED "$edited"

  run_cg organize -commit-directly <"$edited"
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [task1.ics  status={+COMPLETED+}]  Buy milk

organize: wrote 1 change(s)
EOF

  run_cg list -query 'status=COMPLETED' "$CAL"
  assert_success
  assert_output --partial 'task1.ics'
  refute_output --partial 'task2.ics'

  assert_task1_completed
}

# A pinned base whose live state has drifted is a conflict, not a silent clobber:
# commit one move, then apply a second edit built against the ORIGINAL base — the
# merge must reject, and the live document still shows only the first move.
function organize_apply_conflict_rejects { # @test
  generate_doc

  local edited_a="$BATS_TEST_TMPDIR/edited_a.txt"
  write_task1_under COMPLETED "$edited_a"
  run_cg organize -apply "$edited_a" -commit
  assert_success

  local edited_b="$BATS_TEST_TMPDIR/edited_b.txt"
  write_task1_under CANCELLED "$edited_b"
  run_cg organize -apply "$edited_b" -commit
  assert_failure
  assert_output --partial 'conflict'

  assert_task1_completed
}
