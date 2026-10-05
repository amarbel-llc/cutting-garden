#! /bin/bash -e

# Helpers for the fastmail organize bats lane (fastmail tags slice 1). Pair
# with lib/common.bash (run_cg, require_bin, fail).
#
# start_fastmail_server spawns cutting-garden-fastmail-testserver — an
# in-memory JMAP server seeded with fastmailtestserver.SeedTriageFixture (see
# plugins/fastmail/fastmailtestserver/triage_fixture.go for the exact
# mailbox/thread/message ids) — as a coproc (the lib/caldav.bash contract),
# reads its one-line handshake `<session-url> <account-id>`, writes the lane's
# config.toml declaring the `test` account with that session_url, and exports
# FASTMAIL_ROOT (`fastmail://test/`) and FASTMAIL_SESSION_URL. Pair with
# stop_fastmail_server in teardown.
#
# The server listens on a kernel-assigned port by default, and a lane should
# leave it that way: the port lives only in config.toml's session_url. Documents
# anchor at `fastmail://test/`, so it reaches no vector and no `_base` digest —
# organize_fastmail.bats passes unchanged on any port (cutting-garden#254). An
# optional $1 still pins it (CG_TEST_FASTMAIL_PORT) for a caller that needs a
# known address.
#
# No [tags] interpreter override is written: the fastmail thread type's `tags`
# field DECLARES dodder-hyphen itself, so the lane exercises that plugin
# default exactly as a real account's config (no [tags] table) would. No
# token is needed either: the testserver accepts any (or no) bearer token.

start_fastmail_server() {
  require_bin CG_TEST_FASTMAIL cutting-garden-fastmail-testserver
  local bin="${CG_TEST_FASTMAIL:-cutting-garden-fastmail-testserver}"
  local port="${1:-}"

  local stderr_file="$BATS_TEST_TMPDIR/fastmail-server.stderr"
  coproc FASTMAIL_PROC { CG_TEST_FASTMAIL_PORT="$port" "$bin" 2>"$stderr_file"; }
  export FASTMAIL_STDOUT_FD="${FASTMAIL_PROC[0]}"
  export FASTMAIL_STDIN_FD="${FASTMAIL_PROC[1]}"
  export FASTMAIL_PID="$FASTMAIL_PROC_PID"

  local line
  if ! read -r -t 5 -u "$FASTMAIL_STDOUT_FD" line; then
    local err
    err="$(cat "$stderr_file" 2>/dev/null || echo '<no stderr>')"
    fail "fastmail-server handshake timeout after 5s. stderr: $err"
  fi

  # Handshake: "<session-url> <account-id>".
  local -a fields
  read -ra fields <<<"$line"
  if [[ ${#fields[@]} -ne 2 ]]; then
    fail "fastmail-server handshake malformed (want 2 fields, got ${#fields[@]}): $line"
  fi
  export FASTMAIL_SESSION_URL="${fields[0]}"
  export FASTMAIL_ROOT="fastmail://test/"

  write_fastmail_config
}

# write_fastmail_config pins the config dir under the sandboxed $HOME (so
# os.UserConfigDir resolves the lane's own config.toml and an ambient host
# value can't leak in) and declares the `test` account pointed at the running
# testserver's session endpoint.
write_fastmail_config() {
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-EOF
	[[fastmail.accounts]]
	name = "test"
	url = "fastmail://test/"
	session_url = "${FASTMAIL_SESSION_URL}"
	EOF
}

stop_fastmail_server() {
  if [[ -n ${FASTMAIL_STDIN_FD:-} ]]; then
    eval "exec ${FASTMAIL_STDIN_FD}>&-"
    unset FASTMAIL_STDIN_FD
  fi
  if [[ -n ${FASTMAIL_STDOUT_FD:-} ]]; then
    eval "exec ${FASTMAIL_STDOUT_FD}<&-"
    unset FASTMAIL_STDOUT_FD
  fi
  if [[ -n ${FASTMAIL_PID:-} ]]; then
    wait "$FASTMAIL_PID" 2>/dev/null || true
    unset FASTMAIL_PID
  fi
  unset FASTMAIL_SESSION_URL FASTMAIL_ROOT
}
