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
# $CALDAV_SOURCE is a calendar-HOME (not a single calendar) advertising two
# calendars (Personal, Work — see cmd/cutting-garden-caldav-testserver); this
# also asserts capture at the home level aggregates objects from BOTH,
# proving the cutting-garden#162 discovery path feeds capture, not just
# `list`.
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
  # Native-identity aliases <collection>/<component>/<UID>, all four seeded
  # resources across both discovered calendars, and the count.
  assert_output --partial 'cal/VTODO/task1'
  assert_output --partial 'cal/VTODO/task2'
  assert_output --partial 'cal/VEVENT/event1'
  assert_output --partial 'work/VTODO/task3'
  assert_output --partial '"object_count":4'
}

# cutting-garden#162: a caldav account configured at the principal/
# calendar-home level (which is exactly what $CALDAV_SOURCE is — see
# start_caldav_server / cmd/cutting-garden-caldav-testserver) is already
# fully discoverable via the RootLister traversal FDR 0014 specified: `list`
# on the home PROPFINDs it and returns each calendar collection as a child
# container, labeled by its DAV `displayname` prop (not a raw path segment —
# this is also the cutting-garden#120 friendly-label win for accounts
# configured this way). No config schema change was needed for this to
# work; this test is the first end-to-end proof against the real binary
# that N>1 calendars are discovered distinctly, not just N=1.
function list_discovers_multiple_calendars_at_home { # @test
  run_cg list "$CALDAV_SOURCE"
  assert_success
  assert_output --partial 'Personal'
  assert_output --partial 'Work'
  assert_output --partial 'caldav-calendar-v1'

  # Descending the discovered Work calendar (not the home) lists exactly its
  # own object — proving the discovered child is itself a fully addressable,
  # independently-descendable capture root (FDR 0014), not just a label.
  run_cg list "${CALDAV_SOURCE%/dav/}/dav/work/"
  assert_success
  assert_output --partial 'task3.ics'
  refute_output --partial 'task1.ics'
}

# The trellis query evaluator (cutting-garden#164, FDR 0022): `list --query`
# filters the listed nodes against the real binary. A type predicate over the
# home keeps both discovered calendars; a non-matching type empties the
# listing; and a forward-walk step (`-> !caldav-object-v1`) descends BOTH
# calendars to their objects, proving the multi-level containment walk end to
# end.
function list_query_filters_and_walks { # @test
  # Type predicate over the home's calendar children: both match.
  run_cg list -query '!caldav-calendar-v1' "$CALDAV_SOURCE"
  assert_success
  assert_output --partial 'Personal'
  assert_output --partial 'Work'

  # A non-matching type predicate filters everything out.
  run_cg list -query '!no-such-type-v1' "$CALDAV_SOURCE"
  assert_success
  refute_output --partial 'Personal'
  refute_output --partial 'Work'

  # Forward walk: home -> calendars -> their objects. Objects from BOTH the
  # Personal (task1.ics) and Work (task3.ics) calendars appear, so the walk
  # descended every matched calendar, not just one.
  run_cg list -query '!caldav-calendar-v1 -> !caldav-object-v1' "$CALDAV_SOURCE"
  assert_success
  assert_output --partial 'task1.ics'
  assert_output --partial 'task3.ics'
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
