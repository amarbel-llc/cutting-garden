setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/git_ssh.bash"
  export output
}

teardown() {
  stop_test_ssh_agent
  stop_git_ssh_server
}

# bats file_tags=ssh

# Drives the git plugin over a real ssh transport end-to-end: capture, a
# clean diff, and a restore-push, against a repo served by the test
# git-over-ssh server. Exercises the plugin's ssh-agent auth and go-git's
# known_hosts host-key check through the actual `cutting-garden` binary.
function capture_diff_restore_over_ssh { # @test

  require_bin GIT_BIN git || skip "git not available in this lane"
  require_bin CG_TEST_GIT_SSHD cutting-garden-test-git-sshd ||
    skip "cutting-garden-test-git-sshd not available in this lane"

  init_store
  start_git_ssh_server
  start_test_ssh_agent

  # Build a source repo (one commit ⇒ commit/tree/blob).
  local repo="$BATS_TEST_TMPDIR/srcrepo"
  mkdir -p "$repo"
  "$GIT_BIN" -C "$repo" init -q -b main
  "$GIT_BIN" -C "$repo" config user.email test@example.com
  "$GIT_BIN" -C "$repo" config user.name Test
  echo "hello" >"$repo/README.md"
  "$GIT_BIN" -C "$repo" add -A
  "$GIT_BIN" -C "$repo" commit -q -m initial

  # Capture over ssh.
  run_cg capture -format json "git:ssh://git@$GIT_SSH_ADDR$repo#main"
  assert_success
  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id in output: $output"

  run_madder cat "$rid"
  assert_success
  assert_line '! cutting_garden-capture-receipt-git-v1'

  # Clean diff over ssh: the tip has not moved (exit 0, no drift lines).
  run_cg diff "$rid" "git:ssh://git@$GIT_SSH_ADDR$repo#main"
  assert_success
  [[ -z $output ]] || fail "expected no drift over ssh, got: $output"

  # Restore-push over ssh into a bare "remote".
  local bare="$BATS_TEST_TMPDIR/dest.git"
  "$GIT_BIN" init -q --bare "$bare"
  run_cg restore "$rid" "ssh://git@$GIT_SSH_ADDR$bare"
  assert_success

  # The bare remote now carries main at the captured tip.
  local srctip
  srctip="$("$GIT_BIN" -C "$repo" rev-parse refs/heads/main)"
  run "$GIT_BIN" -C "$bare" rev-parse refs/heads/main
  assert_success
  [[ $output == "$srctip" ]] || fail "pushed tip $output != source tip $srctip"
}
