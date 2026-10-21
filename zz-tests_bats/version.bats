setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=version

# Covers the version-burnin wiring end-to-end: version.env at repo root is
# the source of truth, flake.nix reads it via builtins.readFile and passes
# it (plus the flake rev) to buildGoApplication's version/commit attrs, the
# fork auto-injects them as -X main.version / -X main.commit on every
# subPackage, cmd mains forward them into internal/buildinfo, and
# `cutting-garden version` prints them. The bats-capture lane builds under
# buildGoApplication, so the ldflags are injected and the dev+unknown
# defaults must never reach this suite.

function version_prints_self_identification_line { # @test
  run_cg version
  assert_success

  # Self-line per eng-versioning(7): "cutting-garden <version>+<commit>".
  # The commit may carry a "-dirty" suffix on worktree builds, hence the
  # [^+]+ (not [^+-]+) tail. dev+unknown defaults are excluded below.
  assert_output --regexp '^cutting-garden [^+]+\+[^+]+$'
}

function version_matches_source_of_truth { # @test
  # Guards against drift between bump-version's sed target (version.env)
  # and the ldflag target — the version prefix must match version.env
  # exactly.
  run_cg version
  assert_success

  local got_version
  got_version="$(echo "$output" | awk '{print $2}' | cut -d+ -f1)"

  local expected_version
  expected_version="$(grep 'CUTTING_GARDEN_VERSION=' \
    "${BATS_TEST_DIRNAME}/../version.env" | cut -d= -f2)"

  assert_equal "$got_version" "$expected_version"
  # The nix lane always burns in a real version; the dev fallback escaping
  # into a built binary is the bug this suite exists to catch.
  refute [ "$got_version" = "dev" ]
}

function mcp_server_info_version_carries_the_build_commit { # @test
  # cutting-garden#343: a client (moxy, the Krone connector) sees only
  # serverInfo.version, so it must carry the same "<version>+<commit>" the
  # `version` subcommand prints, or a deploy cannot be confirmed from outside.
  run_cg version
  assert_success
  local want
  want="$(echo "$output" | awk '{print $2}')"

  local reqfile="$BATS_TEST_TMPDIR/mcp_req.jsonl"
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}' >"$reqfile"
  run --separate-stderr timeout --preserve-status 10s "${CG_BIN:-cutting-garden}" mcp <"$reqfile"
  assert_success

  local got
  got="$(echo "$output" | jq -r 'select(.id==1) | .result.serverInfo.version')"
  assert_equal "$got" "$want"
  [[ $got =~ ^[^+]+\+[^+]+$ ]] || fail "serverInfo.version $got is not <version>+<commit>"
}

function version_rejects_trailing_arguments { # @test
  run_cg version extra
  # EX_USAGE for trailing positional args.
  assert_failure 64
}
