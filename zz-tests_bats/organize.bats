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

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  start_caldav_server
  init_store
  # The Personal calendar holds task1.ics + task2.ics (VTODO); its VEVENT
  # (event1.ics) windows out of the object listing (#176/#177).
  CAL="${CALDAV_SOURCE%/dav/}/dav/cal/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_doc runs `organize -group-by status` and saves the emitted document.
generate_doc() {
  run_cg organize -group-by status "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/gen.txt"
  printf '%s\n' "$output" >"$DOC"
}

# move_task1_under writes an edited document ($1=target status value, $2=out
# path): task1's box line is pulled out of the ungrouped section and re-filed
# under the (pre-rendered) `## =<value>` bucket.
move_task1_under() {
  local value="$1" out="$2" t1
  t1="$(grep 'task1.ics' "$DOC")"
  [[ -n $t1 ]] || fail "no task1 box line in document: $(cat "$DOC")"
  awk -v t1="$t1" -v h="## =$value" '
    $0 == t1 { next }
    { print }
    $0 == h { print ""; print t1 }
  ' "$DOC" >"$out"
}

# Generate emits the hyphence-envelope dialect: the fenced envelope with the
# framework fields + type, the `# status=` dimension heading, its pre-rendered
# `## =VALUE` buckets, and the two VTODOs as bare espalier boxes.
function organize_generate_emits_envelope { # @test
  generate_doc
  assert_line '---'
  assert_line '! organize-base-v1'
  assert_line '- _type = !caldav-object-v1'
  assert_line --partial '- _base = @'
  assert_line --partial '- _anchor = '
  assert_line '# status='
  assert_line '## =COMPLETED'
  assert_output --partial '- [task1.ics]'
  assert_output --partial '- [task2.ics]'
  # The lone VEVENT windows out of the object listing, so it is not organized.
  refute_output --partial 'event1.ics'
}

# The core tracer: generate, move task1 under `## =COMPLETED`, apply with
# --commit, and confirm the status landed on the live object via a facet query.
function organize_apply_status_move_commits { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  move_task1_under COMPLETED "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output --partial 'task1.ics'

  run_cg list -query 'status=COMPLETED' "$CAL"
  assert_success
  assert_output --partial 'task1.ics'
  refute_output --partial 'task2.ics'
}

# The default is a dry-run: apply without --commit prints the intended move but
# does not write, so the live status query still finds nothing.
function organize_apply_dry_run_does_not_write { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  move_task1_under COMPLETED "$edited"

  run_cg organize -apply "$edited"
  assert_success
  assert_output --partial 'would move'

  run_cg list -query 'status=COMPLETED' "$CAL"
  assert_success
  refute_output --partial 'task1.ics'
}

# A pinned base whose live state has drifted is a conflict, not a silent clobber:
# commit one move, then apply a second edit built against the ORIGINAL base — the
# merge must reject.
function organize_apply_conflict_rejects { # @test
  generate_doc

  local edited_a="$BATS_TEST_TMPDIR/edited_a.txt"
  move_task1_under COMPLETED "$edited_a"
  run_cg organize -apply "$edited_a" -commit
  assert_success

  local edited_b="$BATS_TEST_TMPDIR/edited_b.txt"
  move_task1_under CANCELLED "$edited_b"
  run_cg organize -apply "$edited_b" -commit
  assert_failure
  assert_output --partial 'conflict'
}
