#! /bin/bash -e

# Helpers for the git-over-ssh bats lane. Pair with lib/common.bash
# (run_cg, run_madder, require_bin, fail).
#
# start_git_ssh_server spawns cutting-garden-test-git-sshd — a localhost
# ssh server that runs git's pack helpers — as a coproc (mirroring madder's
# test-sftp-server), reads its one-line handshake, and exports GIT_SSH_ADDR
# and SSH_KNOWN_HOSTS. start_test_ssh_agent runs an ssh-agent holding a
# fresh key, which is how the cutting-garden git plugin authenticates ssh
# (NewSSHAgentAuth). Pair each with its stop_* in teardown.

# start_git_ssh_server requires CG_TEST_GIT_SSHD (set by the bats lane, or
# resolved from PATH) and that `git` is on PATH (the server execs
# git-upload-pack / git-receive-pack).
start_git_ssh_server() {
  require_bin CG_TEST_GIT_SSHD cutting-garden-test-git-sshd
  local bin="${CG_TEST_GIT_SSHD:-cutting-garden-test-git-sshd}"

  local stderr_file="$BATS_TEST_TMPDIR/git-sshd.stderr"
  coproc GIT_SSHD_PROC { "$bin" 2>"$stderr_file"; }
  export GIT_SSHD_STDOUT_FD="${GIT_SSHD_PROC[0]}"
  export GIT_SSHD_STDIN_FD="${GIT_SSHD_PROC[1]}"
  export GIT_SSHD_PID="$GIT_SSHD_PROC_PID"

  local line
  if ! read -r -t 5 -u "$GIT_SSHD_STDOUT_FD" line; then
    local err
    err="$(cat "$stderr_file" 2>/dev/null || echo '<no stderr>')"
    fail "git-sshd handshake timeout after 5s. stderr: $err"
  fi

  # Handshake: "<host:port> <known_hosts_path>".
  local -a fields
  read -ra fields <<<"$line"
  if [[ ${#fields[@]} -ne 2 ]]; then
    fail "git-sshd handshake malformed (want 2 fields, got ${#fields[@]}): $line"
  fi
  export GIT_SSH_ADDR="${fields[0]}"
  export SSH_KNOWN_HOSTS="${fields[1]}"
}

stop_git_ssh_server() {
  if [[ -n ${GIT_SSHD_STDIN_FD:-} ]]; then
    eval "exec ${GIT_SSHD_STDIN_FD}>&-"
    unset GIT_SSHD_STDIN_FD
  fi
  if [[ -n ${GIT_SSHD_STDOUT_FD:-} ]]; then
    eval "exec ${GIT_SSHD_STDOUT_FD}<&-"
    unset GIT_SSHD_STDOUT_FD
  fi
  if [[ -n ${GIT_SSHD_PID:-} ]]; then
    wait "$GIT_SSHD_PID" 2>/dev/null || true
    unset GIT_SSHD_PID
  fi
  unset GIT_SSH_ADDR SSH_KNOWN_HOSTS
}

# start_test_ssh_agent generates a fresh ed25519 key, loads it into a new
# ssh-agent, and exports SSH_AUTH_SOCK. The test ssh server accepts any
# key, so the identity only needs to be present. The agent socket is bound
# under a short /tmp path — AF_UNIX sun_path is ~108 chars and
# $BATS_TEST_TMPDIR can overrun it (madder#207).
start_test_ssh_agent() {
  require_bin SSH_KEYGEN_BIN ssh-keygen
  require_bin SSH_AGENT_BIN ssh-agent
  require_bin SSH_ADD_BIN ssh-add

  local key="$BATS_TEST_TMPDIR/test_ed25519"
  "${SSH_KEYGEN_BIN}" -t ed25519 -N '' -f "$key" -q

  local sock_dir
  sock_dir="$(mktemp -d /tmp/cgssh.XXXXXX)"
  export TEST_SSH_AGENT_SOCK_DIR="$sock_dir"

  # shellcheck disable=SC2153  # SSH_AGENT_BIN is injected via require_bin (line 66), not a typo of SSH_AGENT_PID
  eval "$("${SSH_AGENT_BIN}" -a "$sock_dir/agent.sock" -s)" >/dev/null
  "${SSH_ADD_BIN}" "$key" 2>/dev/null

  export SSH_AUTH_SOCK
  export SSH_AGENT_PID
}

stop_test_ssh_agent() {
  if [[ -n ${SSH_AGENT_PID:-} ]]; then
    kill "$SSH_AGENT_PID" 2>/dev/null || true
    unset SSH_AGENT_PID
  fi
  if [[ -n ${TEST_SSH_AGENT_SOCK_DIR:-} ]]; then
    rm -rf "$TEST_SSH_AGENT_SOCK_DIR"
    unset TEST_SSH_AGENT_SOCK_DIR
  fi
  unset SSH_AUTH_SOCK
}
