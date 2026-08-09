setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=health

function health_reports_plugins_and_capabilities { # @test
  # health needs no blob store; it only enumerates registered plugins.
  run_cg health
  assert_success

  # The aligned table carries the header and every built-in plugin.
  assert_output --partial 'PLUGIN'
  assert_output --partial 'SCHEMES'
  assert_output --partial 'TRAVERSAL'
  assert_output --partial 'caldav'
  assert_output --partial 'ytdlp'
  assert_output --partial 'git'
}

function health_json_is_machine_readable { # @test
  run_cg health -format json
  assert_success

  # caldav is the RootLister reference: the calendar container plus one leaf
  # type per component (VTODO/VEVENT/VJOURNAL) — four declared traversal types.
  local n
  n="$(echo "$output" |
    jq -rs 'map(select(.plugin=="caldav")) | .[0].traversal_types | length')"
  [[ $n -eq 4 ]] ||
    fail "caldav traversal_types length = '$n', want 4; output:"$'\n'"$output"

  # ytdlp has no restore; git restores via the capture protocol.
  local ytdlp_restore git_protocol
  ytdlp_restore="$(echo "$output" |
    jq -rs 'map(select(.plugin=="ytdlp")) | .[0].restore')"
  git_protocol="$(echo "$output" |
    jq -rs 'map(select(.plugin=="git")) | .[0].protocol_kind')"
  [[ $ytdlp_restore == "no" ]] ||
    fail "ytdlp restore = '$ytdlp_restore', want 'no'"
  [[ $git_protocol == "git" ]] ||
    fail "git protocol_kind = '$git_protocol', want 'git'"
}

function health_rejects_bad_format { # @test
  run_cg health -format yaml
  # EX_USAGE for a bad flag value.
  assert_failure 64
}
