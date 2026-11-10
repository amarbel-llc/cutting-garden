# RFC 0007 § Conformance Testing: the config subsystem driven end to end
# through the real binary (cutting-garden#93). The logic itself is covered by
# Go unit tests (internal/cgconfig, internal/command_components,
# plugins/caldav); these cases pin what only the binary shows — the file
# location, root aggregation through `list` and `mcp`, and the load-time
# warn/abort behaviour.
#
# NOT covered here: the credential-precedence row (a node matching an
# account uses its password_env; a non-matching host falls back to
# CALDAV_USERNAME/CALDAV_PASSWORD). The caldav testserver does not record or
# require the credentials a client presents, so the binary cannot be observed
# choosing between them; the unit tests in plugins/caldav carry that row until
# the testserver learns to.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output stderr
  # Pin the config dir under the sandboxed $HOME so os.UserConfigDir resolves
  # this test's own file and an ambient host value can't leak in.
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
}

# bats file_tags=config

write_config() {
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml"
}

# § File Location: a missing config.toml is an empty config, not an error —
# and nothing is said about it.
function missing_config_is_an_empty_config { # @test
  [[ ! -e $XDG_CONFIG_HOME/cutting-garden/config.toml ]]
  run_cg_stdout list -format json
  assert_success
  assert_equal "$stderr" ""
}

# § Root Sources, intrinsic: the file plugin surfaces the working directory
# as a root with no config at all.
function file_plugin_surfaces_the_working_directory_with_no_config { # @test
  run_cg_stdout list -format json
  assert_success
  local want
  want="$(pwd -P)"
  echo "$output" | jq -e --arg want "$want" \
    'select(.uri | startswith("file://") and contains($want))' >/dev/null ||
    fail "no file:// root for $want in: $output"
}

# § Root-Provider, aggregation: a seeded caldav account is a root of the
# no-argument `list`...
function seeded_caldav_account_is_a_root_of_list { # @test
  write_config <<-'EOF'
	[[caldav.accounts]]
	name = "cg93-account"
	url = "caldav:https://caldav.cg93.example/dav/"
	EOF
  run_cg_stdout list -format json
  assert_success
  echo "$output" | jq -e 'select(.uri | contains("caldav.cg93.example"))' >/dev/null ||
    fail "the seeded account is not a root of list: $output"
  # Credentials never ride a surfaced URI (§ Security Considerations).
  [[ $output != *'@caldav.cg93.example'* ]] ||
    fail "a surfaced root carries userinfo: $output"
}

# ... and of an MCP resources/list round trip.
function seeded_caldav_account_is_a_root_of_mcp_resources_list { # @test
  write_config <<-'EOF'
	[[caldav.accounts]]
	name = "cg93-account"
	url = "caldav:https://caldav.cg93.example/dav/"
	EOF
  local reqfile="$BATS_TEST_TMPDIR/mcp_req.jsonl"
  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    echo '{"jsonrpc":"2.0","id":2,"method":"resources/list"}'
  } >"$reqfile"
  run --separate-stderr timeout --preserve-status 10s "${CG_BIN:-cutting-garden}" mcp <"$reqfile"
  assert_success
  echo "$output" | jq -e 'select(.id==2) | .result.resources | any(.[]; .uri | contains("caldav.cg93.example"))' >/dev/null ||
    fail "the seeded account is not in resources/list: $output"
  echo "$output" | jq -e 'select(.id==2) | .result.resources | any(.[]; .uri | startswith("file://"))' >/dev/null ||
    fail "the intrinsic file root is missing from resources/list: $output"
}

# § Loading and Validation: an unknown key is reported on stderr, naming it,
# and does not stop the command.
function unknown_key_warns_but_does_not_abort { # @test
  write_config <<-'EOF'
	cg93_unknown_key = "x"
	EOF
  run_cg_stdout list -format json
  assert_success
  echo "$stderr" | grep -q 'cg93_unknown_key' ||
    fail "stderr does not name the unknown key: $stderr"
  echo "$output" | jq -e 'select(.uri | startswith("file://"))' >/dev/null ||
    fail "the run did not continue to list roots: $output"
}

# § Loading and Validation (amended, cutting-garden#325): a plugin section
# that fails validation, here a duplicate account name, is skipped with a
# warning naming the entry, and the command runs on with the other roots.
function invalid_plugin_section_is_skipped_with_a_warning { # @test
  write_config <<-'EOF'
	[[caldav.accounts]]
	name = "cg93-dup"
	url = "caldav:https://one.cg93.example/dav/"

	[[caldav.accounts]]
	name = "cg93-dup"
	url = "caldav:https://two.cg93.example/dav/"
	EOF
  run_cg_stdout list -format json
  assert_success
  echo "$stderr" | grep -q 'skipping \[caldav\]: .*cg93-dup' ||
    fail "stderr does not name the skipped section and entry: $stderr"
  echo "$output" | jq -e 'select(.uri | startswith("file://"))' >/dev/null ||
    fail "the run did not continue to list roots: $output"
  [[ $output != *cg93.example* ]] || fail "a skipped account surfaced as a root: $output"
}

# The mcp server names the skipped section's error on its scheme in
# describe_node_types, so a client seeing a scheme with no roots also sees
# why (cutting-garden#325); a scheme whose section loaded carries no member.
function skipped_plugin_section_is_named_in_describe_node_types { # @test
  write_config <<-'EOF'
	[[caldav.accounts]]
	name = "cg93-dup"
	url = "caldav:https://one.cg93.example/dav/"

	[[caldav.accounts]]
	name = "cg93-dup"
	url = "caldav:https://two.cg93.example/dav/"
	EOF
  local reqfile="$BATS_TEST_TMPDIR/mcp_req.jsonl"
  {
    echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bats","version":"0"}}}'
    echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"describe_node_types","arguments":{}}}'
  } >"$reqfile"
  run --separate-stderr timeout --preserve-status 10s "${CG_BIN:-cutting-garden}" mcp <"$reqfile"
  local text
  text="$(echo "$output" | jq -r 'select(.id==2) | .result.content[0].text')"
  echo "$text" | jq -e '.[] | select(.scheme == "caldav") | .skipped_config | test("cg93-dup")' >/dev/null ||
    fail "caldav's describe entry does not carry the skip error: $text"
  echo "$text" | jq -e '[.[] | select(.scheme != "caldav") | has("skipped_config")] | any | not' >/dev/null ||
    fail "a scheme whose section loaded carries skipped_config: $text"
}

# Under CG_STRICT_CONFIG=1, a duplicate account name aborts with EX_USAGE
# naming the entry...
function duplicate_account_name_aborts_with_usage_error_when_strict { # @test
  write_config <<-'EOF'
	[[caldav.accounts]]
	name = "cg93-dup"
	url = "caldav:https://one.cg93.example/dav/"

	[[caldav.accounts]]
	name = "cg93-dup"
	url = "caldav:https://two.cg93.example/dav/"
	EOF
  CG_STRICT_CONFIG=1 run_cg_stdout list -format json
  assert_failure 64
  echo "$stderr" | grep -q 'cg93-dup' ||
    fail "stderr does not name the duplicate entry: $stderr"
}

# ... and so does an empty url.
function empty_account_url_aborts_with_usage_error_when_strict { # @test
  write_config <<-'EOF'
	[[caldav.accounts]]
	name = "cg93-nourl"
	url = ""
	EOF
  CG_STRICT_CONFIG=1 run_cg_stdout list -format json
  assert_failure 64
  echo "$stderr" | grep -q 'cg93-nourl' ||
    fail "stderr does not name the entry with the empty url: $stderr"
}
