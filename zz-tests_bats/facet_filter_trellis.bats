#! /usr/bin/env bats

# cutting-garden#388 (#251's first slice, RFC 0012 §6): the `filter=` of
# read_facets, list_nodes and the mutate_nodes sweep (and `list --facets
# --filter`) is ONE trellis step. Its positive equality conjunction is lowered
# to the FacetFilter pushdown; the rest is a host-side residual, never
# dropped.
#
# Fixture: the testserver's Personal calendar holds task1.ics and task2.ics
# with no STATUS (caldav's no-value alias `none`, implying needs-action,
# #413); setup PUTs done.ics (STATUS:COMPLETED) and open.ics
# (STATUS:NEEDS-ACTION).

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_caldav_server
  local http="${CALDAV_SOURCE#caldav:}"
  OBJ_URL="${http%/dav/}${CALDAV_CAL_PATH}"
  put_task "done" COMPLETED
  put_task open NEEDS-ACTION
}

teardown() {
  stop_caldav_server
}

# put_task ID STATUS PUTs a VTODO with that STATUS into the Personal calendar.
put_task() {
  printf 'BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//bats//EN\r\nBEGIN:VTODO\r\nUID:%s@bats\r\nSUMMARY:%s\r\nSTATUS:%s\r\nEND:VTODO\r\nEND:VCALENDAR\r\n' \
    "$1" "$1" "$2" |
    curl -fsS -X PUT -H 'Content-Type: text/calendar' --data-binary @- "${OBJ_URL}$1.ics" >/dev/null ||
    fail "seeding $1.ics failed"
}

# matching_ids FILTER lists the Personal calendar through list_nodes filter=
# and echoes the matched object ids, sorted, as a JSON array.
matching_ids() {
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$(caldav_cal_uri)" --arg f "$1" '{uri:$u,filter:$f}')")"
  mcp_result_text "$output" 3 | jq -c '[.nodes[].uri | split("/") | last] | sort'
}

# bats file_tags=filter_trellis

# OR over one dimension is a trellis value list. It is residual (not lowered),
# so it is matched host-side, honoring the alias: the status-less tasks
# match needs-action by implication.
function filter_trellis_or_over_values { # @test
  assert_equal "$(matching_ids 'status=[needs-action, completed]')" \
    '["done.ics","open.ics","task1.ics","task2.ics"]'
}

# Negation excludes the completed task and keeps the status-less ones.
function filter_trellis_negation_keeps_missing { # @test
  assert_equal "$(matching_ids '^status=completed')" '["open.ics","task1.ics","task2.ics"]'
}

# Missing field: on a dimension with a no-value alias the valueless tasks
# hold the alias, so "missing" is `status=none` (operator ruling q67 b2); a
# field without an alias spells it `^field~="."`.
function filter_trellis_missing_field { # @test
  assert_equal "$(matching_ids 'status=none')" '["task1.ics","task2.ics"]'
  assert_equal "$(matching_ids '^location~="."')" '["done.ics","open.ics","task1.ics","task2.ics"]'
}

# A filter that is not one step is a usage error, never an unfiltered result.
function filter_trellis_rejects_a_path { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$(caldav_cal_uri)" '{uri:$u,filter:"status=completed -> !caldav-object-vtodo-v1"}')")"
  assert_output --partial 'one trellis step'
}

# The legacy comma form still parses for one release, AND as before, and the
# CLI says it is deprecated.
function filter_trellis_legacy_comma_form { # @test
  assert_equal "$(matching_ids 'status=completed,component=VTODO')" '["done.ics"]'
  run_cg list --facets --filter 'status=completed,component=VTODO' "$(caldav_cal_uri)"
  assert_success
  assert_output --partial 'deprecated'
}

# A filter with a residual is counted host-side, and the count equals the
# size of list_nodes' matching set for the same filter.
function filter_trellis_residual_count_matches_listing { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_facets \
    "$(jq -nc --arg u "$(caldav_cal_uri)" '{uri:$u,filter:"^status=completed"}')")"
  assert_equal "$(mcp_result_text "$output" 3 | jq -c '.facets.component.VTODO')" '3'
}

# A sweep with a negated filter patches exactly the non-completed tasks.
function filter_trellis_sweep_negation { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 mutate_nodes \
    "$(jq -nc --arg a "$(caldav_cal_uri)" \
      '{groups:[{anchor:$a, sweep:{filter:"^status=completed", op:{kind:"patch", body:"{\"location\":\"swept\"}"}}}]}')")"
  local id
  for id in open task1 task2; do
    run curl -fsS "${OBJ_URL}${id}.ics"
    assert_success
    assert_line --regexp '^LOCATION:swept'
  done
  run curl -fsS "${OBJ_URL}done.ics"
  assert_success
  refute_line --regexp '^LOCATION'
}
