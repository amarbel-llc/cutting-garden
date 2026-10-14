#! /bin/bash -e

# Helpers for the org bats lanes (FDR 0026). Pair with lib/common.bash
# (require_bin, fail).
#
# start_org_server spawns cutting-garden-org-testserver, an in-memory WebDAV
# server seeded from zz-tests_bats/fixtures/org, as a coproc (the
# lib/fastmail.bash contract). It reads the one-line handshake `<root-url>`
# and exports ORG_WEBDAV_URL. The server accepts Basic auth test:test only.
# Pair with stop_org_server in teardown.

ORG_FIXTURE_DIR="$(dirname "${BASH_SOURCE[0]}")/../fixtures/org"

start_org_server() {
  require_bin CG_TEST_ORG cutting-garden-org-testserver
  local bin="${CG_TEST_ORG:-cutting-garden-org-testserver}"
  local port="${1:-}"

  local stderr_file="$BATS_TEST_TMPDIR/org-server.stderr"
  coproc ORG_PROC { CG_TEST_ORG_PORT="$port" "$bin" "$ORG_FIXTURE_DIR" 2>"$stderr_file"; }
  export ORG_STDOUT_FD="${ORG_PROC[0]}"
  export ORG_STDIN_FD="${ORG_PROC[1]}"
  export ORG_PID="$ORG_PROC_PID"

  local line
  if ! read -r -t 5 -u "$ORG_STDOUT_FD" line; then
    local err
    err="$(cat "$stderr_file" 2>/dev/null || echo '<no stderr>')"
    fail "org-server handshake timeout after 5s. stderr: $err"
  fi
  export ORG_WEBDAV_URL="$line"
}

# org_dav METHOD PATH [DEPTH] issues an authenticated request against the
# running server, printing the body.
org_dav() {
  local method="$1" path="$2" depth="${3:-}"
  local -a args=(-sS -u test:test -X "$method")
  if [[ -n $depth ]]; then
    args+=(-H "Depth: $depth")
  fi
  curl "${args[@]}" "${ORG_WEBDAV_URL}${path}"
}

stop_org_server() {
  if [[ -n ${ORG_STDIN_FD:-} ]]; then
    eval "exec ${ORG_STDIN_FD}>&-"
    unset ORG_STDIN_FD
  fi
  if [[ -n ${ORG_STDOUT_FD:-} ]]; then
    eval "exec ${ORG_STDOUT_FD}<&-"
    unset ORG_STDOUT_FD
  fi
  if [[ -n ${ORG_PID:-} ]]; then
    wait "$ORG_PID" 2>/dev/null || true
    unset ORG_PID
  fi
  unset ORG_WEBDAV_URL
}
