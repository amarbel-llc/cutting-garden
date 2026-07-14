#! /usr/bin/env bats

# The CUD write-tools end-to-end lane (FDR 0020): drive `cutting-garden mcp`
# over its stdio JSON-RPC transport against the caldav testserver and exercise
# create_node -> put_node -> patch_node -> delete_node end to end, plus the #102 clown
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

# tools/list advertises the three read tools (describe/list/read) and the
# four CUD write tools (create/put/patch/delete), with the right destructive
# vs read-only annotations (the hint a client gates on, mirrored by the #102 hook).
function mcp_advertises_tools { # @test
  mcp_drive "$CALDAV_SOURCE" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

  local names
  names="$(echo "$output" | jq -r 'select(.id==2) | (.result.tools | map(.name) | sort | join(","))')"
  assert_equal "$names" "create_node,delete_node,describe_node_types,list_nodes,patch_node,put_node,read_node"

  # The CUD write tools carry destructiveHint=true; the read tools don't.
  local destructive
  destructive="$(echo "$output" | jq -r 'select(.id==2) |
    ([.result.tools[] | select(.annotations.destructiveHint==true) | .name] | sort | join(","))')"
  assert_equal "$destructive" "create_node,delete_node,patch_node,put_node"

  local readonly_tools
  readonly_tools="$(echo "$output" | jq -r 'select(.id==2) |
    ([.result.tools[] | select(.annotations.readOnlyHint==true) | .name] | sort | join(","))')"
  assert_equal "$readonly_tools" "describe_node_types,list_nodes,read_node"
}

# describe_node_types reports caldav's node types and which are writable, with
# the body payload for the writable object leaf.
function mcp_describe_node_types { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 describe_node_types '{}')"

  local text
  text="$(mcp_result_text "$output" 3)"

  # The object leaf is writable, with both accepted body formats described.
  echo "$text" | jq -e '
    any(.[].types[]; .tag=="caldav-object-v1"
        and .container==false and .writable==true
        and (.body.accepts | length) >= 2)' >/dev/null ||
    fail "describe missing a writable caldav-object-v1 with body: $text"

  # The calendar container is present and NOT writable.
  echo "$text" | jq -e '
    any(.[].types[]; .tag=="caldav-calendar-v1"
        and .container==true and .writable==false)' >/dev/null ||
    fail "describe missing a non-writable caldav-calendar-v1 container: $text"
}

# list_nodes (browse) surfaces the configured ROOTS as the entry points — a
# root is a container you descend into, NOT its flattened children (that is
# resources/list; a per-calendar root would otherwise dump every event into
# the entry-point listing, circus#29). Descending the root yields its calendars,
# then read_node returns a seeded object's parsed fields — the read half of
# the claude.ai-UI surface (circus#29), wrapping the RootLister traversal as
# tools.
function mcp_browse_and_read { # @test
  # No uri: the configured root itself, as a single container entry point
  # (mirrors `list` with no arg) — not the calendar/objects one level down.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local roots rooturi
  roots="$(mcp_result_text "$output" 3)"
  echo "$roots" | jq -e \
    'length==1 and .[0].container==true and (.[0].uri | contains("/dav/"))' >/dev/null ||
    fail "list_nodes() root entry-point wrong: $roots"

  # Descend the root (using the uri it reported) → its calendars: the
  # testserver's one calendar, Personal.
  rooturi="$(echo "$roots" | jq -r '.[0].uri')"
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes "$(jq -nc --arg u "$rooturi" '{uri:$u}')")"
  local cals
  cals="$(mcp_result_text "$output" 3)"
  echo "$cals" | jq -e 'any(.[]; .name=="Personal")' >/dev/null ||
    fail "list_nodes(root) missing the Personal calendar: $cals"

  # read_node a seeded VTODO → its parsed task fields. grep (not jq) since a
  # leaf read may append a raw-bytes link line after the JSON.
  local obj read
  obj="$(caldav_object_uri task1.ics)"
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_node "$(jq -nc --arg u "$obj" '{uri:$u}')")"
  read="$(mcp_result_text "$output" 3)"
  # MarshalIndent emits "key": "value" (space after the colon); grep the
  # line so a trailing raw-bytes link (when a store is configured) is fine.
  echo "$read" | grep -q '"component": "VTODO"' ||
    fail "read_node(task1) component: $read"
  echo "$read" | grep -q '"summary": "Buy milk"' ||
    fail "read_node(task1) summary: $read"
}

# A read-only cache root must not crash the server at startup (#121). The
# Phase-B blob writer eagerly inits the madder store, which mkdir's
# <cache>/tmp-<pid>; on an unwritable cache that mkdir fails and madder
# Cancel-panics. Acquisition is isolated to its own context, so the failure
# degrades to structured-only reads — the server still comes up and serves.
function mcp_starts_on_readonly_cache { # @test
  local rocache="$BATS_TEST_TMPDIR/ro-cache"
  mkdir -p "$rocache"
  chmod 0500 "$rocache"

  # Force standard XDG-from-env (disable the cwd walk-up override) so the
  # cache resolves under our read-only XDG_CACHE_HOME, reproducing the
  # unwritable-cache mkdir the krone systemd unit hit.
  export XDG_CACHE_HOME="$rocache"
  export MADDER_XDG_USER_LOCATION_ONLY=1

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"

  # Restore write so bats can clean the tmpdir.
  chmod -R u+w "$rocache"

  # The server served list_nodes (the configured root entry-point) rather
  # than crashing at startup. Post-0bec098, no-uri list_nodes returns the
  # root container, not its flattened calendars — asserting it came back at
  # all is the "server started" signal we need here.
  local roots
  roots="$(mcp_result_text "$output" 3)"
  echo "$roots" | jq -e \
    'length==1 and .[0].container==true and (.[0].uri | contains("/dav/"))' >/dev/null ||
    fail "server did not serve list_nodes on a read-only cache: $output"
}

# The isolation in #121 must not break the writer on the normal (writable)
# deployment: with a store configured, a leaf read still emits the raw-bytes
# madder://blobs link beside the parsed fields (the #85 Phase-B enrichment).
function mcp_emits_blob_link_with_writable_store { # @test
  init_store

  local obj read
  obj="$(caldav_object_uri task1.ics)"
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_node "$(jq -nc --arg u "$obj" '{uri:$u}')")"
  read="$(mcp_result_text "$output" 3)"

  echo "$read" | grep -q '"summary": "Buy milk"' ||
    fail "read_node(task1) summary: $output"
  # renderContents appends 'raw bytes: madder://blobs/<digest> (...)' for the
  # link-only content entry when a store is configured.
  echo "$read" | grep -q 'raw bytes: madder://blobs/' ||
    fail "expected a madder://blobs raw-bytes link with a writable store: $output"
}

# create -> put -> patch -> delete round-trips one VEVENT through the server,
# each mutation a separate invocation against the persistent testserver.
function mcp_cud_round_trips { # @test
  local obj v1 v2
  obj="$(caldav_object_uri e2e.ics)"
  v1="$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e2e\nSUMMARY:E2E v1\nEND:VEVENT\nEND:VCALENDAR\n')"
  v2="$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e2e\nSUMMARY:E2E v2\nEND:VEVENT\nEND:VCALENDAR\n')"

  mcp_call create_node "$(jq -nc --arg u "$obj" --arg b "$v1" '{uri:$u,body:$b,type:"caldav-object-v1"}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == created* ]] || fail "create: $(mcp_result_text "$output" 3)"

  mcp_call put_node "$(jq -nc --arg u "$obj" --arg b "$v2" '{uri:$u,body:$b}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == put* ]] || fail "put: $(mcp_result_text "$output" 3)"

  mcp_call patch_node "$(jq -nc --arg u "$obj" '{uri:$u,body:"{\"component\":\"VEVENT\",\"event\":{\"summary\":\"E2E patched\"}}"}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == patched* ]] || fail "patch: $(mcp_result_text "$output" 3)"

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

# put on a missing object is strict too: an error result, not a create.
function mcp_put_missing_errors { # @test
  local obj args
  obj="$(caldav_object_uri ghost.ics)"
  args="$(jq -nc --arg u "$obj" \
    --arg b "$(printf 'BEGIN:VCALENDAR\nBEGIN:VTODO\nUID:ghost\nSUMMARY:ghost\nEND:VTODO\nEND:VCALENDAR\n')" \
    '{uri:$u,body:$b}')"

  mcp_call put_node "$args"
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
