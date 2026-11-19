#! /usr/bin/env bats

# cutting-garden-bridge and the serve-mcp daemon it relays to (step A of
# docs/plans/2026-11-06-centralized-cutting-garden.md): the shipped binaries,
# end to end. Three behaviors the unit tests cannot reach: the fallback exec of
# `cutting-garden mcp` when no daemon answers, a session through a live daemon
# (hello consumed and logged), and what happens when the daemon dies while the
# client is still connected.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output stderr
  # The bridge ships beside cutting-garden in the default package.
  BRIDGE_BIN="$(dirname "$CG_BIN")/cutting-garden-bridge"
  [[ -x $BRIDGE_BIN ]] || fail "cutting-garden-bridge not beside CG_BIN at $BRIDGE_BIN"
  # A unix socket path is capped near 108 bytes; BATS_TEST_TMPDIR is deeper.
  SOCK_DIR="$(mktemp -d /tmp/cgbr.XXXXXX)"
  SOCK="$SOCK_DIR/s.sock"
  INIT_REQ='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}'
  # No account, no wire plugin: nothing here may touch a live service.
  : >"$BATS_TEST_TMPDIR/empty.toml"
  DAEMON_PID=""
}

teardown() {
  if [[ -n ${DAEMON_PID:-} ]]; then
    kill "$DAEMON_PID" 2>/dev/null || true
  fi
  rm -rf "${SOCK_DIR:-}"
  # fd 3 is bats' own; the held-open client pipe uses 8.
  exec 8>&- 2>/dev/null || true
}

# bats file_tags=mcp,bridge

# Background processes must not inherit fd 8 (the held-open client pipe: a
# daemon holding its write end would keep the bridge from ever seeing EOF) or
# fd 3 (bats' own: it would keep bats waiting for the process).
start_daemon() {
  "$CG_BIN" serve-mcp -socket "$SOCK" -config "$BATS_TEST_TMPDIR/empty.toml" \
    2>"$BATS_TEST_TMPDIR/daemon.err" 3>&- 8>&- &
  DAEMON_PID=$!
  local _
  for _ in $(seq 100); do
    [[ -S $SOCK ]] && return 0
    sleep 0.1
  done
  fail "serve-mcp never listened on $SOCK: $(cat "$BATS_TEST_TMPDIR/daemon.err")"
}

# With no daemon the bridge replaces itself with `cutting-garden mcp`: the
# client still gets a working server (the sibling binary was found and exec'd)
# and stderr says why.
function bridge_without_a_daemon_execs_the_in_process_server { # @test
  run --separate-stderr timeout --preserve-status 10s \
    "$BRIDGE_BIN" -socket "$BATS_TEST_TMPDIR/absent.sock" <<<"$INIT_REQ"
  assert_success
  assert_equal "$(echo "$output" | jq -r 'select(.id==1) | .result.serverInfo.name')" "cutting-garden"
  echo "$stderr" | grep -q 'no daemon at' || fail "stderr does not say there was no daemon: $stderr"
}

# Through a live daemon the session works, the hello never reaches the MCP
# server (stdout is protocol frames only), and the daemon logs the principal.
function bridge_through_a_daemon_serves_and_names_the_principal { # @test
  start_daemon
  CLOWN_SESSION_ID=bats-sess-42 run --separate-stderr timeout --preserve-status 10s \
    "$BRIDGE_BIN" -socket "$SOCK" <<<"$INIT_REQ"
  assert_success
  assert_equal "$(echo "$output" | jq -r 'select(.id==1) | .result.serverInfo.name')" "cutting-garden"
  while IFS= read -r line; do
    [[ -z $line ]] && continue
    echo "$line" | jq -e '.id != null' >/dev/null || fail "unexpected frame on stdout: $line"
  done <<<"$output"
  # The log line is written when the session ends; give the daemon a moment.
  local _
  for _ in $(seq 50); do
    grep -q 'session closed' "$BATS_TEST_TMPDIR/daemon.err" && break
    sleep 0.1
  done
  grep -q 'session open principal="bats-sess-42"' "$BATS_TEST_TMPDIR/daemon.err" ||
    fail "daemon did not log the principal: $(cat "$BATS_TEST_TMPDIR/daemon.err")"
}

# A bridge older than the hello (or run with no session id) sends none; the
# daemon serves it as anonymous.
function bridge_without_a_session_id_is_served_anonymously { # @test
  start_daemon
  env -u CLOWN_SESSION_ID -u SPINCLASS_SESSION_ID \
    timeout --preserve-status 10s "$BRIDGE_BIN" -socket "$SOCK" <<<"$INIT_REQ" >"$BATS_TEST_TMPDIR/out"
  jq -e 'select(.id==1) | .result.serverInfo.name == "cutting-garden"' "$BATS_TEST_TMPDIR/out" >/dev/null ||
    fail "no initialize response: $(cat "$BATS_TEST_TMPDIR/out")"
  local _
  for _ in $(seq 50); do
    grep -q 'session closed' "$BATS_TEST_TMPDIR/daemon.err" && break
    sleep 0.1
  done
  grep -q 'session open principal=""' "$BATS_TEST_TMPDIR/daemon.err" ||
    fail "anonymous session not logged: $(cat "$BATS_TEST_TMPDIR/daemon.err")"
}

# The daemon dies and does not come back while the client stays connected and
# silent: once the reconnect window has passed the bridge exits with status 3,
# without waiting for the client's next message, so its host sees the child die.
function bridge_exits_3_when_the_daemon_dies_mid_session { # @test
  start_daemon
  mkfifo "$BATS_TEST_TMPDIR/in"
  "$BRIDGE_BIN" -socket "$SOCK" -reconnect-for 1s <"$BATS_TEST_TMPDIR/in" \
    >"$BATS_TEST_TMPDIR/out" 2>"$BATS_TEST_TMPDIR/bridge.err" 3>&- &
  local bridge_pid=$!
  exec 8>"$BATS_TEST_TMPDIR/in"
  echo "$INIT_REQ" >&8

  local _
  for _ in $(seq 100); do
    grep -q '"id":1' "$BATS_TEST_TMPDIR/out" && break
    sleep 0.1
  done
  grep -q '"id":1' "$BATS_TEST_TMPDIR/out" || fail "no initialize response before the kill"

  kill "$DAEMON_PID"
  wait "$DAEMON_PID" 2>/dev/null || true
  DAEMON_PID=""

  for _ in $(seq 100); do
    kill -0 "$bridge_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$bridge_pid" 2>/dev/null; then
    kill "$bridge_pid"
    fail "bridge kept running after the daemon died"
  fi
  local status=0
  wait "$bridge_pid" || status=$?
  assert_equal "$status" 3
  grep -q 'did not come back' "$BATS_TEST_TMPDIR/bridge.err" ||
    fail "bridge stderr does not say the daemon did not come back: $(cat "$BATS_TEST_TMPDIR/bridge.err")"
}

# The daemon is restarted (an upgrade, a crash and a unit restart) while the
# client stays attached: the bridge reconnects, replays the client's initialize
# handshake, and the client's next request is answered by the new daemon with
# no EOF and no second initialize reply. A clown plugin server that exits is not
# restarted by clown, so this is what keeps the session's tools.
function bridge_survives_a_daemon_restart { # @test
  start_daemon
  mkfifo "$BATS_TEST_TMPDIR/in"
  "$BRIDGE_BIN" -socket "$SOCK" -reconnect-for 20s <"$BATS_TEST_TMPDIR/in" \
    >"$BATS_TEST_TMPDIR/out" 2>"$BATS_TEST_TMPDIR/bridge.err" 3>&- &
  local bridge_pid=$!
  exec 8>"$BATS_TEST_TMPDIR/in"
  echo "$INIT_REQ" >&8

  local _
  for _ in $(seq 100); do
    grep -q '"id":1' "$BATS_TEST_TMPDIR/out" && break
    sleep 0.1
  done
  grep -q '"id":1' "$BATS_TEST_TMPDIR/out" || fail "no initialize response before the restart"
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}' >&8

  kill "$DAEMON_PID"
  wait "$DAEMON_PID" 2>/dev/null || true
  DAEMON_PID=""
  start_daemon

  echo '{"jsonrpc":"2.0","id":2,"method":"resources/list"}' >&8
  for _ in $(seq 100); do
    grep -q '"id":2' "$BATS_TEST_TMPDIR/out" && break
    sleep 0.1
  done
  grep -q '"id":2' "$BATS_TEST_TMPDIR/out" ||
    fail "no reply from the restarted daemon: $(cat "$BATS_TEST_TMPDIR/bridge.err")"
  kill -0 "$bridge_pid" 2>/dev/null || fail "the bridge ended though the daemon came back"
  assert_equal "$(grep -c '"id":1' "$BATS_TEST_TMPDIR/out")" 1

  exec 8>&-
  local status=0
  wait "$bridge_pid" || status=$?
  assert_equal "$status" 0
}
