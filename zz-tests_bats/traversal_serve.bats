setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
  peer_pid=
}

teardown() {
  # Reap a peer a failed assertion left behind so the suite never hangs
  # on an orphaned accept loop.
  if [[ -n ${peer_pid:-} ]]; then
    kill "$peer_pid" 2>/dev/null || true
  fi
}

# bats file_tags=traversal_serve

# RFC 0013 §Conformance Testing — the launch-pattern half of the
# traversal-plugin transport gate. The suite is split in two sections:
#
#   PORTABLE (bats test_tags=portable, names test_portable_*): pure
#   RFC 0013 launch-contract cases every conformant `traversal-serve`
#   implementation MUST pass. A non-Go peer (e.g. a Rust fj-cg)
#   substitutes its own binary via CG_TEST_TRAVERSAL_SERVE (bats-emo
#   require_bin) and runs these unmodified — this file is the
#   cross-implementation ratification lane.
#
#   TESTPEER (would be test_tags=testpeer, names test_testpeer_*):
#   cases pinned to the Go test peer's fixed cgtest tree. Deliberately
#   EMPTY in v1: exercising the NDJSON JSON-RPC session from bash needs
#   an AF_UNIX stream client (socat/nc -U, not in this lane) plus
#   half-close choreography that fights the stdin-EOF lifecycle — a
#   brittle shell JSON-RPC client adds nothing, because RFC 0013
#   §Covered Requirements assigns every method-level and tree-content
#   row to the Go transport tests and the indistinguishability
#   end-to-end (internal/traversal_serve). If a shell-honest tree case
#   ever appears, gate it on an env var the flake sets for the in-repo
#   run so a substituted binary skips it.
#
# Protocol facts pinned here (RFC 0013 §Launch and rendezvous):
# cookie env TRAVERSAL_PLUGIN_COOKIE; one stdout announce line
# `cookie|traversal-plugin/v1|unix|<socket>|<metadata>|traversal-plugin`;
# rendezvous socket lives in a fresh mode-0700 directory removed on
# exit; stdin EOF unblocks a pending accept and the peer exits 0.

announce_regexp() {
  local cookie="$1"
  printf '^%s\\|traversal-plugin/v1\\|unix\\|[^|]+\\|[^|]*\\|traversal-plugin$' \
    "$cookie"
}

# start_peer COOKIE launches the peer with stdin held open on a fifo
# (so it stays alive, listening) and waits — bounded — for the announce
# line. Sets: peer_pid, peer_out, peer_err, peer_stdin_fd, announce.
start_peer() {
  local cookie="$1"
  peer_out="$BATS_TEST_TMPDIR/peer.out"
  peer_err="$BATS_TEST_TMPDIR/peer.err"
  local fifo="$BATS_TEST_TMPDIR/peer.in"
  mkfifo "$fifo"

  # 3>&- : don't inherit bats' fd 3 (a held-open copy stalls the run).
  env TRAVERSAL_PLUGIN_COOKIE="$cookie" "$CG_TEST_TRAVERSAL_SERVE" \
    <"$fifo" >"$peer_out" 2>"$peer_err" 3>&- &
  peer_pid=$!

  # Opening the fifo write-side unblocks the peer's read-side open and
  # keeps its stdin from EOF until stop_peer closes this fd.
  exec {peer_stdin_fd}>"$fifo"

  for _ in {1..100}; do
    if [[ -s $peer_out ]]; then break; fi
    kill -0 "$peer_pid" 2>/dev/null ||
      fail "peer exited before announcing (stderr: $(cat "$peer_err"))"
    sleep 0.1
  done
  [[ -s $peer_out ]] || fail "no announce line within 10s"
  announce="$(head -n 1 "$peer_out")"
}

# stop_peer closes the held stdin (the RFC 0013 lifecycle EOF), then
# asserts the peer exits 0 promptly with no stdout past the announce.
stop_peer() {
  exec {peer_stdin_fd}>&-

  for _ in {1..100}; do
    kill -0 "$peer_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$peer_pid" 2>/dev/null; then
    kill "$peer_pid" 2>/dev/null || true
    fail "peer did not exit within 10s of stdin EOF"
  fi

  local status=0
  wait "$peer_pid" || status=$?
  peer_pid=
  [[ $status -eq 0 ]] ||
    fail "peer exit status $status (stderr: $(cat "$peer_err"))"

  # stdout is protocol-only: nothing may follow the announce.
  [[ "$(cat "$peer_out")" == "$announce" ]] ||
    fail "stdout grew past the announce: $(cat "$peer_out")"
}

# ---------------------------------------------------------------------
# PORTABLE — any conformant traversal-serve binary must pass these.
# ---------------------------------------------------------------------

# bats test_tags=portable
function test_portable_refuses_to_start_without_cookie { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  run --separate-stderr "$CG_TEST_TRAVERSAL_SERVE" </dev/null
  assert_failure
  # The refusal must keep stdout protocol-silent; stderr MAY say
  # anything (deliberately unasserted for cross-implementation runs).
  [[ -z $output ]] || fail "stdout not empty on cookie refusal: $output"
}

# bats test_tags=portable
function test_portable_announce_is_wellformed_and_socket_live { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  local cookie=bats-trav-cookie-$RANDOM
  start_peer "$cookie"

  printf '%s' "$announce" | grep -qE "$(announce_regexp "$cookie")" ||
    fail "malformed announce: $announce"

  local sock
  sock="$(awk -F'|' '{print $4}' <<<"$announce")"
  [[ -S $sock ]] || fail "announced address is not a live socket: $sock"

  stop_peer
}

# bats test_tags=portable
function test_portable_stdin_eof_unblocks_accept_and_exits_zero { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  local cookie=bats-trav-cookie-$RANDOM
  # </dev/null is an immediate stdin EOF: the peer must still announce
  # first, then exit 0 without ever accepting a connection. The timeout
  # bounds "promptly"; --preserve-status turns a hang into a failure.
  run --separate-stderr timeout --preserve-status 10s \
    env TRAVERSAL_PLUGIN_COOKIE="$cookie" "$CG_TEST_TRAVERSAL_SERVE" \
    </dev/null
  assert_success
  # Exactly the one announce line on stdout, nothing after it.
  assert_output --regexp "$(announce_regexp "$cookie")"
}

# bats test_tags=portable
function test_portable_rendezvous_dir_0700_and_removed_on_exit { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  local cookie=bats-trav-cookie-$RANDOM
  start_peer "$cookie"

  local sock dir
  sock="$(awk -F'|' '{print $4}' <<<"$announce")"
  dir="$(dirname "$sock")"
  [[ "$(file_mode "$dir")" == 700 ]] ||
    fail "rendezvous dir $dir mode $(file_mode "$dir"), want 700"

  stop_peer
  [[ ! -e $dir ]] || fail "rendezvous dir survived exit: $dir"
}

# ---------------------------------------------------------------------
# TESTPEER — Go test peer's fixed cgtest tree. Intentionally empty; see
# the file banner. Tree conformance is pinned by the Go
# indistinguishability end-to-end (internal/traversal_serve).
# ---------------------------------------------------------------------
