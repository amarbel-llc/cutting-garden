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

# RFC 0013 §Conformance Testing — the portable half of the
# traversal-plugin transport gate. The suite is split in three sections:
#
#   PORTABLE — LAUNCH (bats test_tags=portable, names
#   test_portable_*, cookie/announce/rendezvous): pure RFC 0013
#   launch-contract cases every conformant `traversal-serve`
#   implementation MUST pass. A non-Go peer (e.g. a Rust fj-cg)
#   substitutes its own binary via CG_TEST_TRAVERSAL_SERVE (bats-emo
#   require_bin) and runs these unmodified.
#
#   PORTABLE — CONFORMANCE (test_portable_conformance_*): the
#   method-semantics half, driven by the compiled conformance DRIVER
#   binary (CG_CONFORMANCE_TRAVERSAL, cutting-garden#186) rather than a
#   shell JSON-RPC client. This is the "env var the flake sets" escape
#   the TESTPEER note below anticipated: the driver owns the AF_UNIX
#   session + half-close choreography a shell can't do well, so bats
#   only runs it and checks the TAP verdict. It drives the same
#   CG_TEST_TRAVERSAL_SERVE peer.
#
#   TESTPEER (would be test_tags=testpeer, names test_testpeer_*):
#   fixed-tree cases driven from BASH directly. Still deliberately
#   EMPTY: a raw shell JSON-RPC client adds nothing over the driver
#   above and the Go indistinguishability end-to-end
#   (internal/traversal_serve), which own the tree-content rows.
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

# ---------------------------------------------------------------------
# CONFORMANCE — session-level METHOD SEMANTICS, driven by the driver
# BINARY (cutting-garden#186). This is the "shell-honest tree case" the
# file banner anticipated: rather than a brittle shell JSON-RPC client,
# a compiled driver (CG_CONFORMANCE_TRAVERSAL) speaks the whole session
# and emits TAP, so bats only runs it and checks the verdict. It drives
# the SAME CG_TEST_TRAVERSAL_SERVE peer the launch cases use — the two
# together are the full lane a substituted non-Go peer runs (that peer
# supplies its own manifest; this case pins the in-tree testpeer).
# ---------------------------------------------------------------------

# bats test_tags=portable
function test_portable_conformance_driver_passes_testpeer { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"
  require_bin CG_CONFORMANCE_TRAVERSAL cutting-garden-conformance-traversal ||
    skip "cutting-garden-conformance-traversal not available in this lane"

  # The testpeer's manifest. command is the ONLY runtime-dependent field
  # (the peer's built path); the rest is the fixed cgtest tree's shape.
  # patch_unrecognized_only.body is empty on purpose: the testpeer merges
  # every patch key, so no unrecognized field is constructible and the
  # driver SKIPs that point rather than faking it.
  local manifest="$BATS_TEST_TMPDIR/testpeer.manifest.toml"
  cat >"$manifest" <<EOF
command = ["$CG_TEST_TRAVERSAL_SERVE"]
schemes = ["cgtest"]
writable_container = "cgtest://fixture/box"

[create]
type = "cgtest-obj-v1"
body = "conformance probe body"

[patch_recognized]
body = "{\"note\":\"patched\"}"
expect_applied = ["note"]

[patch_unrecognized_only]
body = ""

[patch_wrong_typed]
body = "not json"

[facet_container]
uri = "cgtest://fixture/box"
filter = "state=open"

[container_body]
uri = "cgtest://fixture/box/issue-1"
EOF

  run --separate-stderr "$CG_CONFORMANCE_TRAVERSAL" --manifest "$manifest"
  assert_success
  # Substring, not --regexp with ^: bats matches a regex against the
  # whole multi-line output as ONE string (no per-line/multiline flag),
  # so `^not ok` would never match a mid-output failure — a false-safe
  # assertion. --partial 'not ok' catches a failing point anywhere.
  assert_output --partial '1..12'
  assert_output --partial 'ok 1 - initialize'
  assert_output --partial 'ok 11 - leaf.read: container returns its own body'
  refute_output --partial 'not ok'
}

# bats test_tags=portable
function test_portable_conformance_driver_fails_a_wrong_expectation { # @test
  # The driver's own acceptance property: it MUST be able to fail. A
  # manifest asserting an applied set the peer will not report has to
  # produce a non-ok point and a nonzero exit — a driver that passes
  # everything ratifies nothing (cutting-garden#186).
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"
  require_bin CG_CONFORMANCE_TRAVERSAL cutting-garden-conformance-traversal ||
    skip "cutting-garden-conformance-traversal not available in this lane"

  local manifest="$BATS_TEST_TMPDIR/wrong.manifest.toml"
  cat >"$manifest" <<EOF
command = ["$CG_TEST_TRAVERSAL_SERVE"]
schemes = ["cgtest"]
writable_container = "cgtest://fixture/box"

[create]
type = "cgtest-obj-v1"
body = "conformance probe body"

[patch_recognized]
body = "{\"note\":\"patched\"}"
expect_applied = ["this-key-is-never-reported"]

[patch_wrong_typed]
body = "not json"
EOF

  run --separate-stderr "$CG_CONFORMANCE_TRAVERSAL" --manifest "$manifest"
  assert_failure
  assert_output --partial \
    'not ok 5 - node.patch: recognized fields reported applied'
}
