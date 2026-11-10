#! /usr/bin/env bats

# The host runner (.#cutting-garden-bats-host, `just debug-test-bats`) rebuilds
# its environment with `env -i`, so it must pass an external peer through by
# name (cutting-garden#407): a non-Go RFC 0013 traversal peer self-verifies by
# setting CG_TEST_TRAVERSAL_SERVE (an RFC 0008 capture peer, CG_TEST_CAPTURE_SERVE)
# and running traversal_serve.bats's `portable` cases (capture_serve.bats). Each
# case runs the host runner over a one-case probe file and reads back what the
# probe's require_bin resolved. The hermetic lanes carry the host runner as
# CG_BATS_HOST; under the host runner itself these cases skip.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
  require_bin CG_BATS_HOST cutting-garden-bats-host ||
    skip "the host runner is only in the hermetic lanes (CG_BATS_HOST)"
  PROBE="$BATS_TEST_TMPDIR/probe"
  mkdir -p "$PROBE"
  STUB="$PROBE/stub-peer"
  printf '#!/bin/sh\nexit 0\n' >"$STUB"
  chmod +x "$STUB"
}

# bats file_tags=bats_host

# run_probe VAR [env args...] runs the host runner over a probe whose one case
# resolves VAR through require_bin and writes it to $PROBE/resolved, with the
# given env arguments ahead of the runner. It leaves the resolved path in
# $output.
run_probe() {
  local var="$1"
  shift
  cat >"$PROBE/probe.bats" <<EOF
setup() {
  load "$BATS_TEST_DIRNAME/lib/common.bash"
}

@test probe {
  require_bin $var unused-name
  printf '%s\n' "\$$var" >"$PROBE/resolved"
}
EOF
  run env "$@" "$CG_BATS_HOST" "$PROBE/probe.bats"
  assert_success
  output="$(cat "$PROBE/resolved")"
}

function bats_host_passes_a_substituted_traversal_peer { # @test
  run_probe CG_TEST_TRAVERSAL_SERVE CG_TEST_TRAVERSAL_SERVE="$STUB"
  assert_output "$STUB"
}

function bats_host_passes_a_substituted_capture_peer { # @test
  run_probe CG_TEST_CAPTURE_SERVE CG_TEST_CAPTURE_SERVE="$STUB"
  assert_output "$STUB"
}

# Unset, each defaults to the in-tree peer.
function bats_host_defaults_to_the_in_tree_peers { # @test
  run_probe CG_TEST_TRAVERSAL_SERVE -u CG_TEST_TRAVERSAL_SERVE
  assert_output --regexp '/bin/cutting-garden-test-traversal-serve$'
  run_probe CG_TEST_CAPTURE_SERVE -u CG_TEST_CAPTURE_SERVE
  assert_output --regexp '/bin/cutting-garden-test-capture-serve$'
}
