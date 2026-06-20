#! /usr/bin/env bats

# The CUD write-tools end-to-end lane (FDR 0020): drive `cutting-garden mcp`
# over its stdio JSON-RPC transport against the caldav testserver and exercise
# create_node -> update_node -> delete_node end to end, plus the #102 clown
# PreToolUse hook gating those tools as `ask`. This is the e2e exercise the
# per-layer Go unit tests (caldav mutate, mcp tools, claude_hooks) do not
# cover in one path.
#
# Dependent mutations (create -> update -> delete) are driven as SEPARATE mcp
# invocations against the one testserver (started in setup, persistent across
# the test): the go-mcp server dispatches the requests of a single connection
# concurrently, so two ordered mutations piped into one process would race.
# One mutation per `cutting-garden mcp` run serializes them; the testserver
# carries state across the runs.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_caldav_server
}

teardown() {
  stop_caldav_server
}

# bats file_tags=mcp

# tools/list advertises exactly the three CUD write tools, each annotated
# destructive (the hint a client gates on, mirrored by the #102 hook).
function mcp_advertises_destructive_cud_tools { # @test
  mcp_drive "$CALDAV_SOURCE" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

  local names
  names="$(echo "$output" | jq -r 'select(.id==2) | (.result.tools | map(.name) | sort | join(","))')"
  assert_equal "$names" "create_node,delete_node,update_node"

  local all_destructive
  all_destructive="$(echo "$output" |
    jq -r 'select(.id==2) | ([.result.tools[].annotations.destructiveHint] | all)')"
  assert_equal "$all_destructive" "true"
}

# create -> update -> delete round-trips one VEVENT through the server, each
# mutation a separate invocation against the persistent testserver.
function mcp_cud_round_trips { # @test
  local obj v1 v2
  obj="$(caldav_object_uri e2e.ics)"
  v1="$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e2e\nSUMMARY:E2E v1\nEND:VEVENT\nEND:VCALENDAR\n')"
  v2="$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e2e\nSUMMARY:E2E v2\nEND:VEVENT\nEND:VCALENDAR\n')"

  mcp_call create_node "$(jq -nc --arg u "$obj" --arg b "$v1" '{uri:$u,body:$b,type:"caldav-object-v1"}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == created* ]] || fail "create: $(mcp_result_text "$output" 3)"

  mcp_call update_node "$(jq -nc --arg u "$obj" --arg b "$v2" '{uri:$u,body:$b}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == updated* ]] || fail "update: $(mcp_result_text "$output" 3)"

  mcp_call delete_node "$(jq -nc --arg u "$obj" '{uri:$u}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == deleted* ]] || fail "delete: $(mcp_result_text "$output" 3)"
}

# create is strict: a second create on the same object is an error result,
# not a silent overwrite.
function mcp_create_is_strict { # @test
  local obj args
  obj="$(caldav_object_uri dup.ics)"
  args="$(jq -nc --arg u "$obj" \
    --arg b "$(printf 'BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:dup\nSUMMARY:dup\nEND:VEVENT\nEND:VCALENDAR\n')" \
    '{uri:$u,body:$b,type:"caldav-object-v1"}')"

  mcp_call create_node "$args"
  assert_equal "$(mcp_is_error "$output" 3)" "false"

  mcp_call create_node "$args"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
}

# update on a missing object is strict too: an error result, not a create.
function mcp_update_missing_errors { # @test
  local obj args
  obj="$(caldav_object_uri ghost.ics)"
  args="$(jq -nc --arg u "$obj" \
    --arg b "$(printf 'BEGIN:VCALENDAR\nBEGIN:VTODO\nUID:ghost\nSUMMARY:ghost\nEND:VTODO\nEND:VCALENDAR\n')" \
    '{uri:$u,body:$b}')"

  mcp_call update_node "$args"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
}

# The #102 PreToolUse hook (`cutting-garden hook`) classifies a CUD tool as
# `ask`, exercising the binary's hook subcommand end to end (not just the Go
# Run unit). Uses the prefix the hook assumes for cutting-garden's MCP tools.
function hook_gates_cud_tool_ask { # @test
  local eventfile
  eventfile="$BATS_TEST_TMPDIR/hook_event.json"
  echo '{"hook_event_name":"PreToolUse","tool_name":"mcp__plugin_cutting-garden_cutting-garden__create_node"}' >"$eventfile"

  run --separate-stderr timeout --preserve-status 5s "${CG_BIN}" hook <"$eventfile"
  assert_success

  assert_equal "$(echo "$output" | jq -r '.hookSpecificOutput.permissionDecision')" "ask"
}
