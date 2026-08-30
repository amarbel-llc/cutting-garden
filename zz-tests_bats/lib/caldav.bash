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
#
# An optional $1 pins the server's listen port (CG_TEST_CALDAV_PORT): the
# organize lanes need it because the server URL lands in the organize
# document's `_anchor` + provenance lines and thus its `_base` digest, so
# whole-document vectors only reproduce against a stable port. The bats lane
# runs files AND tests concurrently (`--jobs`), so a lane pinning a port MUST
# (a) pick one no other file uses and (b) serialize its own tests via
# `setup_file() { export BATS_NO_PARALLELIZE_WITHIN_FILE=true; }` — each test
# starts its own server on the same port, and stop_caldav_server waits for the
# previous one to exit first. Assigned ports (pick the next free one for a new
# lane):
#
#   43101  organize.bats
#   43102  organize_tags.bats
#   43103  organize_ns.bats
#   43104  organize_date.bats
#   43105  organize_priority.bats
#   43106  organize_fields.bats
#   43107  organize_literal.bats
#   43108  organize_groupby.bats
#   43109  organize_headings.bats

# assert_categories URL EXPECTED curl-reads the live iCalendar object at URL (the
# plain-HTTP form, `${CALDAV_SOURCE#caldav:}<cal>/<id>.ics`) and asserts its
# CATEGORIES line is exactly EXPECTED — or absent when EXPECTED is empty. The
# organize tag lanes' write-back check.
assert_categories() {
  local url="$1" want="$2"
  run curl -fsS "$url"
  assert_success
  if [[ -z $want ]]; then
    refute_line --regexp '^CATEGORIES'
  else
    assert_line --regexp "^CATEGORIES:${want}[[:space:]]*$"
  fi
}

start_caldav_server() {
  require_bin CG_TEST_CALDAV cutting-garden-caldav-testserver
  local bin="${CG_TEST_CALDAV:-cutting-garden-caldav-testserver}"
  local port="${1:-}"

  local stderr_file="$BATS_TEST_TMPDIR/caldav-server.stderr"
  coproc CALDAV_PROC { CG_TEST_CALDAV_PORT="$port" "$bin" 2>"$stderr_file"; }
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
