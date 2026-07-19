# cutting-garden#165 host-level proof: a single misconfigured RFC 0013
# wire plugin must not take the whole `cutting-garden mcp` process down.
# Production symptom: fj-cg crashed on startup (EROFS on its rendezvous
# dir) and cutting-garden failed its OWN MCP initialize handshake with
# its host (moxy), taking every OTHER scheme (caldav, file, ...) down
# with it. This exercises the real no-arg aggregation path (`mcp` with
# no positional root — the one that walks EVERY configured plugin,
# including [[traversal_plugins]] wire plugins) against a real config
# file, unlike mcp.bats's CALDAV_SOURCE-scoped tests which pass an
# explicit root and never touch aggregation.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output stderr
}

# bats file_tags=mcp,traversal_serve

# A [[traversal_plugins]] stanza whose command does not exist must not
# stop cutting-garden's own MCP initialize handshake, and must not
# suppress the intrinsic file-plugin root that would otherwise be
# surfaced. The failure is logged to stderr (never stdout — stdout is
# the JSON-RPC transport) naming the broken plugin.
function mcp_isolates_a_dead_wire_plugin_bad_command { # @test
  mkdir -p "$HOME/.config/cutting-garden"
  cat >"$HOME/.config/cutting-garden/config.toml" <<-'EOF'
	[[traversal_plugins]]
	name = "cg165-badcommand"
	command = ["/nonexistent/cutting-garden-does-not-exist"]
	schemes = ["cg165-badcommand"]
	EOF

  local reqfile="$BATS_TEST_TMPDIR/mcp_req.jsonl"
  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    tools_call 3 list_nodes '{}'
  } >"$reqfile"

  run --separate-stderr timeout --preserve-status 10s "${CG_BIN:-cutting-garden}" mcp <"$reqfile"
  assert_success

  # cutting-garden's OWN initialize handshake with its host must
  # succeed — this is the exact call that failed in production.
  local server_name
  server_name="$(echo "$output" | jq -r 'select(.id==1) | .result.serverInfo.name')"
  assert_equal "$server_name" "cutting-garden"

  # stdout is protocol-only: every line must parse as JSON-RPC (a
  # warning belongs on stderr, never here).
  while IFS= read -r line; do
    [[ -z $line ]] && continue
    echo "$line" | jq -e . >/dev/null || fail "non-JSON line on stdout: $line"
  done <<<"$output"

  # The intrinsic file-plugin root (always present, config or not)
  # survived aggregation alongside the dead wire plugin.
  local roots
  roots="$(mcp_result_text "$output" 3)"
  echo "$roots" | jq -e 'any(.[]; .uri | startswith("file://"))' >/dev/null ||
    fail "file plugin's root missing from aggregation: $roots"

  # The dead plugin contributed no root of its own.
  echo "$roots" | jq -e '[.[] | select(.uri | startswith("cg165-badcommand://"))] | length == 0' >/dev/null ||
    fail "dead plugin's scheme unexpectedly present: $roots"

  # stderr names the broken plugin.
  echo "$stderr" | grep -q 'cg165-badcommand' ||
    fail "stderr does not mention the dead plugin: $stderr"
}

# Same isolation contract when the wire plugin's command IS runnable but
# crashes/exits before it ever writes its RFC 0013 announce line — the
# literal "read announce line (child exited before announcing?): EOF"
# symptom cutting-garden#165 reports from production (fj-cg's EROFS on
# its rendezvous dir).
function mcp_isolates_a_dead_wire_plugin_crash_before_announce { # @test
  mkdir -p "$HOME/.config/cutting-garden"
  cat >"$HOME/.config/cutting-garden/config.toml" <<-'EOF'
	[[traversal_plugins]]
	name = "cg165-crash"
	command = ["false"]
	schemes = ["cg165-crash"]
	EOF

  local reqfile="$BATS_TEST_TMPDIR/mcp_req.jsonl"
  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    tools_call 3 list_nodes '{}'
  } >"$reqfile"

  run --separate-stderr timeout --preserve-status 10s "${CG_BIN:-cutting-garden}" mcp <"$reqfile"
  assert_success

  local server_name
  server_name="$(echo "$output" | jq -r 'select(.id==1) | .result.serverInfo.name')"
  assert_equal "$server_name" "cutting-garden"

  local roots
  roots="$(mcp_result_text "$output" 3)"
  echo "$roots" | jq -e 'any(.[]; .uri | startswith("file://"))' >/dev/null ||
    fail "file plugin's root missing from aggregation: $roots"
  echo "$roots" | jq -e '[.[] | select(.uri | startswith("cg165-crash://"))] | length == 0' >/dev/null ||
    fail "dead plugin's scheme unexpectedly present: $roots"

  echo "$stderr" | grep -q 'cg165-crash' ||
    fail "stderr does not mention the crashed plugin: $stderr"
}
