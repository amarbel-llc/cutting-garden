setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=capture_serve

# Bring-up smoke for the RFC 0008 capture-serve test peer under the nix
# sandbox. This pins the packaging + platform half of the transport: a
# SOCK_SEQPACKET ("unixpacket") listener binds inside the sandbox (the
# Phase 0 open question), the built binary announces with the documented
# six-field line echoing the launch cookie, and the stdin-EOF lifecycle
# signal alone ends it cleanly. The full protocol conformance (byte
# identity across a spawned session) lives in the go tests
# (internal/capture_serve launch tests), which re-exec the same
# capture_serve_testpeer.Main this binary wraps.

function test_peer_announces_and_exits_cleanly_on_stdin_eof { # @test
  require_bin CG_TEST_CAPTURE_SERVE cutting-garden-test-capture-serve ||
    skip "cutting-garden-test-capture-serve not available in this lane"

  local cookie=bats-cookie-1234
  # </dev/null is an immediate stdin EOF: the peer must still announce
  # first (announce precedes the lifecycle watcher), then exit 0 without
  # ever accepting a connection.
  run env CAPTURE_PLUGIN_COOKIE="$cookie" "$CG_TEST_CAPTURE_SERVE" </dev/null
  assert_success
  # cookie|version|network|address|metadata|subprotocol — and nothing else
  # on stdout/stderr (stdout is protocol-only under RFC 0008).
  assert_output --regexp "^${cookie}\|capture-plugin/v2\|unixpacket\|[^|]+\|[^|]*\|capture-plugin$"
}

function test_peer_refuses_to_start_without_cookie { # @test
  require_bin CG_TEST_CAPTURE_SERVE cutting-garden-test-capture-serve ||
    skip "cutting-garden-test-capture-serve not available in this lane"

  run --separate-stderr "$CG_TEST_CAPTURE_SERVE" </dev/null
  assert_failure
  # The refusal must keep stdout protocol-silent; the diagnostic goes to
  # stderr (the accidental-direct-invocation guard).
  [[ -z $output ]] || fail "stdout not empty on cookie refusal: $output"
  [[ -n $stderr ]] || fail "expected a diagnostic on stderr"
}
