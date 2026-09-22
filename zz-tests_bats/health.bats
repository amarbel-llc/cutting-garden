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

  # "(default)" is the file plugin's empty schemeless claim, rendered
  # readably instead of a bare leading comma.
  assert_output --partial '(default)'
}

# health_json_capabilities_per_plugin is the ONE assertion that genuinely
# needs the real plugins linked, so it lives here rather than in
# internal/health's package tests — those register in-package fakes so a
# framework test lane does not depend on plugins/ sources
# (docs/plans/2026-09-21-invalidation-cone-moves.md D6).
function health_json_capabilities_per_plugin { # @test
  run_cg health -format json
  assert_success

  # cap PLUGIN FIELD WANT: one plugin's column from the NDJSON report.
  cap() {
    echo "$output" |
      jq -rs --arg p "$1" --arg f "$2" 'map(select(.plugin==$p)) | .[0][$f]'
  }
  assert_cap() {
    local got
    got="$(cap "$1" "$2")"
    [[ $got == "$3" ]] ||
      fail "$1 $2 = '$got', want '$3'; output:"$'\n'"$output"
  }
  traversal() {
    echo "$output" |
      jq -rs --arg p "$1" \
        'map(select(.plugin==$p)) | .[0].traversal_types // [] | join(",")'
  }

  # Every built-in plugin is enumerated.
  local name
  for name in file git ytdlp caldav optical gphotos; do
    [[ $(cap "$name" plugin) == "$name" ]] ||
      fail "plugin '$name' not enumerated; output:"$'\n'"$output"
  done

  # file: full capture/restore/diff, no protocol, and RootLister traversal —
  # intrinsic-PWD roots (RFC 0007) declaring directory + file node types.
  assert_cap file capture true
  assert_cap file restore yes
  assert_cap file diff true
  assert_cap file protocol_kind null
  [[ $(traversal file) == "cutting_garden-file-directory-v1,cutting_garden-file-object-v1" ]] ||
    fail "file traversal = '$(traversal file)'"

  # git: capture/diff, restore via the RFC 0002 capture protocol, kind "git".
  assert_cap git capture true
  assert_cap git restore protocol
  assert_cap git diff true
  assert_cap git protocol_kind git

  # ytdlp: capture/diff, no restore, no protocol.
  assert_cap ytdlp capture true
  assert_cap ytdlp restore no
  assert_cap ytdlp diff true
  assert_cap ytdlp protocol_kind null

  # optical: capture only — no restore, no diff, no protocol, no traversal.
  assert_cap optical capture true
  assert_cap optical restore no
  assert_cap optical diff false
  assert_cap optical protocol_kind null
  [[ -z $(traversal optical) ]] ||
    fail "optical traversal = '$(traversal optical)', want none"

  # gphotos: capture/diff, no restore, no protocol, no traversal.
  assert_cap gphotos capture true
  assert_cap gphotos restore no
  assert_cap gphotos diff true
  [[ -z $(traversal gphotos) ]] ||
    fail "gphotos traversal = '$(traversal gphotos)', want none"

  # caldav is the RootLister reference: the calendar container plus one leaf
  # type per component (VTODO/VEVENT/VJOURNAL) — four declared traversal types.
  assert_cap caldav capture true
  assert_cap caldav restore yes
  assert_cap caldav diff true
  [[ $(traversal caldav) == "caldav-calendar-v1,caldav-object-vtodo-v1,caldav-object-vevent-v1,caldav-object-vjournal-v1" ]] ||
    fail "caldav traversal = '$(traversal caldav)', want the calendar container plus three per-component leaf types"
}

function health_rejects_bad_format { # @test
  run_cg health -format yaml
  # EX_USAGE for a bad flag value.
  assert_failure 64
}
