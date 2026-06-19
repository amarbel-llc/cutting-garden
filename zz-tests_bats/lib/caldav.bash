#! /bin/bash -e

# Helpers for the caldav bats lane. Pair with lib/common.bash (run_cg,
# run_madder, require_bin, fail).
#
# start_caldav_server spawns cutting-garden-caldav-testserver — a localhost
# in-memory CalDAV server (PROPFIND/REPORT/GET/PUT) — as a coproc (mirroring
# lib/git_ssh.bash's start_git_ssh_server), reads its one-line handshake,
# and exports CALDAV_SOURCE (the `caldav:` source arg pointing at the seeded
# calendar) and CALDAV_CAL_PATH (the calendar's server-absolute path). Pair
# with stop_caldav_server in teardown.
#
# It exists because Radicale cannot start under the nix bats sandbox (it
# calls socket.socketpair(AF_UNIX); amarbel-llc/dodder#117). This server is
# a pure net/http TCP listener, so it runs in-sandbox.

start_caldav_server() {
  require_bin CG_TEST_CALDAV cutting-garden-caldav-testserver
  local bin="${CG_TEST_CALDAV:-cutting-garden-caldav-testserver}"

  local stderr_file="$BATS_TEST_TMPDIR/caldav-server.stderr"
  coproc CALDAV_PROC { "$bin" 2>"$stderr_file"; }
  export CALDAV_STDOUT_FD="${CALDAV_PROC[0]}"
  export CALDAV_STDIN_FD="${CALDAV_PROC[1]}"
  export CALDAV_PID="$CALDAV_PROC_PID"

  local line
  if ! read -r -t 5 -u "$CALDAV_STDOUT_FD" line; then
    local err
    err="$(cat "$stderr_file" 2>/dev/null || echo '<no stderr>')"
    fail "caldav-server handshake timeout after 5s. stderr: $err"
  fi

  # Handshake: "<caldav-source-arg> <calendar-path>".
  local -a fields
  read -ra fields <<<"$line"
  if [[ ${#fields[@]} -ne 2 ]]; then
    fail "caldav-server handshake malformed (want 2 fields, got ${#fields[@]}): $line"
  fi
  export CALDAV_SOURCE="${fields[0]}"
  export CALDAV_CAL_PATH="${fields[1]}"
}

stop_caldav_server() {
  if [[ -n ${CALDAV_STDIN_FD:-} ]]; then
    eval "exec ${CALDAV_STDIN_FD}>&-"
    unset CALDAV_STDIN_FD
  fi
  if [[ -n ${CALDAV_STDOUT_FD:-} ]]; then
    eval "exec ${CALDAV_STDOUT_FD}<&-"
    unset CALDAV_STDOUT_FD
  fi
  if [[ -n ${CALDAV_PID:-} ]]; then
    wait "$CALDAV_PID" 2>/dev/null || true
    unset CALDAV_PID
  fi
  unset CALDAV_SOURCE CALDAV_CAL_PATH
}
