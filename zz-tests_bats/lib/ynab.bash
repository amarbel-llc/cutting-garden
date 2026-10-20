#! /bin/bash -e

# Helpers for the ynab bats lane (FDR 0027). Pair with lib/common.bash
# (require_bin, fail).
#
# start_ynab_server spawns cutting-garden-ynab-testserver, an in-memory YNAB
# REST server seeded with its deterministic fixture (see
# plugins/ynab/ynabtestserver/fixture.go for the exact tree), as a coproc (the
# lib/org.bash contract). It reads the one-line handshake `<api-base-url>` and
# exports YNAB_BASE_URL. The server accepts the bearer token `test-token` only.
# The fixture's dates are fixed in 2026, so the lane always names an explicit
# date window and never depends on the plugin's rolling 30-day default. Pair
# with stop_ynab_server in teardown.

start_ynab_server() {
  require_bin CG_TEST_YNAB cutting-garden-ynab-testserver
  local bin="${CG_TEST_YNAB:-cutting-garden-ynab-testserver}"

  local stderr_file="$BATS_TEST_TMPDIR/ynab-server.stderr"
  coproc YNAB_PROC { "$bin" 2>"$stderr_file"; }
  export YNAB_STDOUT_FD="${YNAB_PROC[0]}"
  export YNAB_STDIN_FD="${YNAB_PROC[1]}"
  export YNAB_PID="$YNAB_PROC_PID"

  local line
  if ! read -r -t 5 -u "$YNAB_STDOUT_FD" line; then
    local err
    err="$(cat "$stderr_file" 2>/dev/null || echo '<no stderr>')"
    fail "ynab-server handshake timeout after 5s. stderr: $err"
  fi
  export YNAB_BASE_URL="$line"
}

# write_ynab_config [TOKEN] pins the config dir under the sandboxed $HOME and
# declares the `test` ynab account against the running server, with the token
# read from the environment (TOKEN defaults to the one the server accepts).
write_ynab_config() {
  export XDG_CONFIG_HOME="$HOME/.config"
  export CG_TEST_YNAB_TOKEN="${1:-test-token}"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-EOF
	[[ynab.accounts]]
	name = "test"
	url = "ynab://test/"
	session_url = "${YNAB_BASE_URL}"
	password_source = "env"
	password_key = "CG_TEST_YNAB_TOKEN"
	EOF
}

stop_ynab_server() {
  if [[ -n ${YNAB_STDIN_FD:-} ]]; then
    eval "exec ${YNAB_STDIN_FD}>&-"
    unset YNAB_STDIN_FD
  fi
  if [[ -n ${YNAB_STDOUT_FD:-} ]]; then
    eval "exec ${YNAB_STDOUT_FD}<&-"
    unset YNAB_STDOUT_FD
  fi
  if [[ -n ${YNAB_PID:-} ]]; then
    wait "$YNAB_PID" 2>/dev/null || true
    unset YNAB_PID
  fi
  unset YNAB_BASE_URL
}

# ynab_mcp_list URI FILTER drives ONE list_nodes call (response id 3) against
# the test account, printing nothing; read the result with mcp_result_text.
ynab_mcp_list() {
  mcp_drive ynab://test/ "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$1" --arg f "$2" '{uri:$u,filter:$f}')")"
}

# ynab_mcp_facets URI FILTER drives ONE read_facets call (response id 3).
ynab_mcp_facets() {
  mcp_drive ynab://test/ "$(tools_call 3 read_facets \
    "$(jq -nc --arg u "$1" --arg f "$2" '{uri:$u,filter:$f}')")"
}
