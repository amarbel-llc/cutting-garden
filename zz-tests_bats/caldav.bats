#! /usr/bin/env bats

# The caldav RFC 0011 protocol lane: capture a CalDAV endpoint into a
# caldav-v1 protocol receipt, assert the merkle tree, and round-trip it
# through restore + diff. Backed by cutting-garden-caldav-testserver (a
# localhost in-memory CalDAV server spawned as a coproc) rather than
# Radicale, which cannot start under the nix sandbox (dodder#117).

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  start_caldav_server
  init_store
}

teardown() {
  stop_caldav_server
}

# bats file_tags=caldav

# Capture emits an RFC 0011 caldav-kind protocol receipt: a merkle tree
# referencing identity, outcome, and the caldav payload node, with the
# payload listing one caldav-object-v1 ref per resource by native identity.
function capture_caldav_emits_rfc0011_tree { # @test
  run_cg capture -format json "$CALDAV_SOURCE"
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id in output: $output"

  run_madder cat "$rid"
  assert_success
  assert_line '! cutting_garden-capture_receipt-caldav-v1'
  assert_output --partial '- identity < @'
  assert_output --partial '- outcome < @'
  assert_output --partial 'jcs-caldav-payload-v1'

  local payload_id
  payload_id="$(echo "$output" | sed -nE 's/.*- payload < @([^ ]+) !.*/\1/p')"
  [[ -n $payload_id ]] || fail "no payload id in receipt: $output"

  run_madder cat "$payload_id"
  assert_success
  assert_line '! jcs-caldav-payload-v1'
  assert_output --partial 'caldav-object-v1'
  # Native-identity aliases <collection>/<component>/<UID>, all three seeded
  # resources, and the count.
  assert_output --partial 'cal/VTODO/task1'
  assert_output --partial 'cal/VTODO/task2'
  assert_output --partial 'cal/VEVENT/event1'
  assert_output --partial '"object_count":3'
}

# Round-trip: capture → restore back to the endpoint → diff is clean. The
# diff exercises the native-identity freshness probe; an unchanged server
# reports no differences.
function capture_restore_diff_round_trips { # @test
  run_cg capture -format json "$CALDAV_SOURCE"
  assert_success
  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id in output: $output"

  # Restore back to the same endpoint (its calendar collection exists);
  # the PUTs are create-or-overwrite, so the server state is unchanged.
  run_cg restore "$rid" "$CALDAV_SOURCE"
  assert_success

  # Diff the receipt against the (unchanged) live source: clean.
  run_cg diff "$rid" "$CALDAV_SOURCE"
  assert_success
  # A clean diff produces no A/D/M lines.
  refute_output --partial 'A cal/'
  refute_output --partial 'D cal/'
  refute_output --partial 'M cal/'
}
