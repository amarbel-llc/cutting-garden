#! /usr/bin/env bats

# The org plugin's read lane (FDR 0026 slice 1, cutting-garden#308): cg list
# and cg mcp over the `test` account, backed by the in-memory WebDAV server
# seeded from zz-tests_bats/fixtures/org (see lib/org.bash). Every test
# starts a fresh server.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/org.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_org_server
  write_org_config
  init_store
}

teardown() {
  stop_org_server
}

# bats file_tags=org

function org_list_root_shows_files_and_folders { # @test
  run_cg list -format json org://test/
  assert_success
  run jq -r '.uri' <<<"$output"
  assert_output - <<-'EOM'
	org://test/_inbox.org
	org://test/projects/
	EOM
}

function org_list_file_shows_headlines_with_tags { # @test
  run_cg list -format json org://test/_inbox.org
  assert_success
  run jq -c '[.uri, .tags]' <<<"$output"
  assert_output - <<-'EOM'
	["org://test/_inbox.org?h=Call+the+plumber+about+the+leak",["project-house","zz-inbox"]]
	["org://test/_inbox.org?id=3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11",["project-garden","req-internet","zz-inbox"]]
	["org://test/_inbox.org?h=Idea%3A+expose+CLOSED+dates",["zz-inbox"]]
	["org://test/_inbox.org?h=Renew+passport",["area-travel","zz-inbox"]]
	EOM
}

function org_mcp_read_node_returns_body_and_fields { # @test
  mcp_drive org://test/ "$(tools_call 3 read_node '{"uri":"org://test/_inbox.org?h=Call+the+plumber+about+the+leak"}')"
  # The text is the structured JSON, then a "raw bytes: madder://…" line.
  run jq -r '.body, .fields.state, .fields.deadline' <<<"$(mcp_result_text "$output" 3 | sed '/^raw bytes:/d')"
  assert_output - <<-'EOM'
	Ask about the shutoff valve too.
	TODO
	2026-10-10
	EOM
}

function org_mcp_read_facets_counts_states_across_folders { # @test
  mcp_drive org://test/ "$(tools_call 3 read_facets '{"uri":"org://test/"}')"
  run jq -c '.facets.state' <<<"$(mcp_result_text "$output" 3)"
  assert_output '{"DONE":1,"DROPPED":1,"NEXT":1,"TODO":1,"WAIT":1,"none":2}'
}

# The first step matches the anchor's children (files and folders); a
# leading combinator is not supported yet (cutting-garden#211).
function org_mcp_query_bare_tag_matches_the_subtree { # @test
  mcp_drive org://test/ "$(tools_call 3 list_nodes '{"uri":"org://test/","query":"[!cutting_garden-org-file-v1, !cutting_garden-org-folder-v1] ->> !cutting_garden-org-headline-v1 project"}')"
  run jq -r '.nodes[].name' <<<"$(mcp_result_text "$output" 3)"
  assert_output - <<-'EOM'
	Call the plumber about the leak
	Draft the plugin doc
	Ask about appends
	Order seeds
	Build a greenhouse
	EOM
}

# A regex carrying (?i) is quoted: a bare `(…)` is a reserved qualifier.
function org_mcp_query_title_regex { # @test
  mcp_drive org://test/ "$(tools_call 3 list_nodes '{"uri":"org://test/","query":"!cutting_garden-org-file-v1 ->> !cutting_garden-org-headline-v1 title~=\"(?i)plumber\""}')"
  run jq -r '.nodes[].uri' <<<"$(mcp_result_text "$output" 3)"
  assert_output "org://test/_inbox.org?h=Call+the+plumber+about+the+leak"
}

# Slice 2 (FDR 0026 D12, #309): quote mints :ID: and adds zz-agent_quoted
# under a WebDAV lock; reading the node again shows the durable id.
function org_mcp_quote_mints_a_durable_id { # @test
  mcp_drive org://test/ \
    "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"org://test/","ops":[{"kind":"patch","id":"_inbox.org?h=Call+the+plumber+about+the+leak","body":"{\"quote\": true}"}]}]}')"
  run jq -r '.groups[0].applied | join(",")' <<<"$(mcp_result_text "$output" 3)"
  assert_output "_inbox.org?h=Call+the+plumber+about+the+leak"

  mcp_drive org://test/ "$(tools_call 4 read_node '{"uri":"org://test/_inbox.org?h=Call+the+plumber+about+the+leak"}')"
  run jq -r '.fields.id_synthetic, (.fields.own_tags | join(" ")), (.fields.id | test("^[0-9a-f-]{36}$"))' \
    <<<"$(mcp_result_text "$output" 4 | sed '/^raw bytes:/d')"
  assert_output - <<-'EOM'
	false
	project-house zz-agent_quoted
	true
	EOM
}

# Slice 3a (FDR 0026): a metadata edit under the same lock — entering a
# done state adds CLOSED, the date lands in the planning line, and the edit
# mints the durable :ID: (the write is read back before it is reported).
function org_mcp_edit_state_and_schedule { # @test
  mcp_drive org://test/ \
    "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"org://test/","ops":[{"kind":"patch","id":"_inbox.org?h=Call+the+plumber+about+the+leak","body":"{\"state\": \"DONE\", \"scheduled\": \"2026-10-20\"}"}]}]}')"
  run jq -r '.groups[0].applied | join(",")' <<<"$(mcp_result_text "$output" 3)"
  assert_output "_inbox.org?h=Call+the+plumber+about+the+leak"

  mcp_drive org://test/ "$(tools_call 4 read_node '{"uri":"org://test/_inbox.org?h=Call+the+plumber+about+the+leak"}')"
  run jq -r '.fields.state, .fields.scheduled, (.fields.closed | test("^[0-9]{4}-")), .fields.id_synthetic' \
    <<<"$(mcp_result_text "$output" 4 | sed '/^raw bytes:/d')"
  assert_output - <<-'EOM'
	DONE
	2026-10-20
	true
	false
	EOM
}

function org_unknown_headline_is_a_clear_error { # @test
  mcp_drive org://test/ "$(tools_call 3 read_node '{"uri":"org://test/_inbox.org?h=Renamed"}')"
  run mcp_result_text "$output" 3
  assert_output --partial "no headline with outline path Renamed in _inbox.org"
}
