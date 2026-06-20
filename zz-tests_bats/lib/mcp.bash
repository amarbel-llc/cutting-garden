#! /bin/bash -e

# Helpers for the mcp bats lane: drive `cutting-garden mcp` (a long-lived
# stdio JSON-RPC server) with a scripted request sequence and capture its
# responses. Pair with lib/common.bash (run_cg, CG_BIN) and lib/caldav.bash
# (start_caldav_server, CALDAV_SOURCE).

# caldav_object_uri BASENAME echoes the caldav: object URI for BASENAME under
# the seeded calendar (/dav/cal/), derived from CALDAV_SOURCE
# (caldav:<http>/dav/, the calendar-home arg the testserver hands back).
caldav_object_uri() {
  echo "${CALDAV_SOURCE%/dav/}/dav/cal/$1"
}

# mcp_drive ROOT REQ... runs `cutting-garden mcp ROOT`, feeding a JSON-RPC
# initialize + initialized handshake followed by REQ... on stdin (one request
# per line), and captures the server's stdout (protocol frames only) into
# $output via --separate-stderr. The server exits on stdin EOF, so the fixed
# request set drives a complete exchange. Exit status is intentionally not
# asserted (a stdio server's termination on EOF vs a timeout kill is not a
# meaningful signal); assert on the parsed responses instead.
mcp_drive() {
  local root="$1"
  shift
  local reqfile="$BATS_TEST_TMPDIR/mcp_req.jsonl"
  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    printf '%s\n' "$@"
  } >"$reqfile"
  run --separate-stderr timeout --preserve-status 10s \
    "${CG_BIN:-cutting-garden}" mcp "$root" <"$reqfile"
}

# mcp_result_text OUT ID echoes the tools/call result text of response ID.
mcp_result_text() {
  echo "$1" | jq -r --argjson id "$2" 'select(.id==$id) | .result.content[0].text'
}

# mcp_is_error OUT ID echoes "true" when tools/call response ID is an error
# result, "false" otherwise.
mcp_is_error() {
  echo "$1" | jq -r --argjson id "$2" 'select(.id==$id) | (.result.isError == true)'
}

# tools_call REQ_ID NAME ARGS_JSON builds a tools/call request line.
tools_call() {
  echo "{\"jsonrpc\":\"2.0\",\"id\":$1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}"
}

# mcp_call NAME ARGS_JSON drives ONE tools/call (response id 3) against
# CALDAV_SOURCE in its own `cutting-garden mcp` invocation. One mutation per
# process serializes dependent mutations (create -> update -> delete) that
# would otherwise race within a single connection's concurrent dispatch.
mcp_call() {
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 "$1" "$2")"
}
