#! /usr/bin/env bats

# The `mutate` command end to end: the CLI front over the same write engine as
# the mutate_nodes MCP tool (internal/mutate). Backed by the fastmail triage
# fixture (lib/fastmail.bash) — a real writable plugin behind an in-process
# server — and every test starts a fresh server. Writes are read back through
# cg itself, and the parity test runs one document through both fronts.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/fastmail.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_fastmail_server
}

teardown() {
  stop_fastmail_server
}

# bats file_tags=fastmail

# move_doc ID BODY prints a one-op patch document under the Inbox anchor.
move_doc() {
  jq -nc --arg a "${FASTMAIL_ROOT}Inbox/" --arg id "$1" --arg body "$2" \
    '{groups:[{anchor:$a,ops:[{kind:"patch",id:$id,body:$body}]}]}'
}

# outcome_of prints the first op's outcome of a result document on $output as
# compact JSON.
outcome_of() {
  jq -c '.groups[0].outcomes[0] | {status, fields, uri, error}' <<<"$output"
}

# A document on standard input applies: exit 0, the result on stdout in the
# caller's ids, and the move is visible through a read-back listing — T2 left
# the Inbox for the Archive.
function mutate_applies_a_document_from_stdin { # @test
  run_cg_stdout mutate - <<<"$(move_doc T2 '{"mailbox": "Archive"}')"
  assert_success
  output="$(outcome_of)"
  assert_output '{"status":"applied","fields":["mailbox"],"uri":"fastmail://test/Archive/?thread=T2","error":null}'

  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  run jq -r '.uri' <<<"$output"
  assert_output $'fastmail://test/Inbox/?thread=T1\nfastmail://test/Inbox/?thread=T3\nfastmail://test/Inbox/?thread=T4'
}

# The same document named as a file argument.
function mutate_applies_a_document_from_a_file { # @test
  move_doc T1 '{"mailbox": "payee/-one_medical"}' >"$BATS_TEST_TMPDIR/doc.json"

  run_cg_stdout mutate "$BATS_TEST_TMPDIR/doc.json"
  assert_success
  output="$(outcome_of)"
  assert_output '{"status":"applied","fields":["mailbox"],"uri":"fastmail://test/payee/-one_medical/?thread=T1","error":null}'
}

# One document, two fronts, identical fresh state: `cg mutate` prints exactly
# what the mutate_nodes tool returns. The server is restarted between the two
# so the second front sees the fixture the first one did.
function mutate_matches_the_mcp_tool { # @test
  local doc
  doc="$(jq -nc --arg a "${FASTMAIL_ROOT}Inbox/" '{groups:[{anchor:$a,ops:[
    {kind:"patch",id:"T2",body:"{\"mailbox\": \"Archive\"}"},
    {kind:"patch",id:"T3",body:"{\"tags\": [\"_inbox\"]}"}]}]}')"

  run_cg_stdout mutate - <<<"$doc"
  assert_success
  local via_cli
  via_cli="$(jq -c . <<<"$output")"

  stop_fastmail_server
  start_fastmail_server

  mcp_drive "$FASTMAIL_ROOT" "$(tools_call 3 mutate_nodes "$doc")"
  local via_mcp
  via_mcp="$(mcp_result_text "$output" 3 | jq -c .)"

  assert_equal "$via_cli" "$via_mcp"
}

# A refusal is exit 1, not trouble: the move to an unknown mailbox applied
# nothing, the result is still printed with the reason, and nothing moved.
function mutate_nothing_applied_exits_one_and_prints_why { # @test
  run_cg_stdout mutate - <<<"$(move_doc T1 '{"mailbox": "Nowhere"}')"
  assert_failure 1
  assert_output --partial 'no mailbox at path'

  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  run jq -r '.uri' <<<"$output"
  assert_line --index 0 "fastmail://test/Inbox/?thread=T1"
}

# A document that is not the expected JSON is EX_USAGE, and nothing is printed
# to stdout.
function mutate_malformed_document_exits_sixty_four { # @test
  run_cg_stdout mutate - <<<'this is not json'
  assert_failure 64
  assert_output ''
  [[ $stderr == *document* ]]
}

# More than one file argument is EX_USAGE too.
function mutate_too_many_arguments_exits_sixty_four { # @test
  run_cg mutate a.json b.json
  assert_failure 64
  assert_output --partial 'too many positional arguments'
}

# Trouble is 2: an anchor whose scheme no plugin serves, and a file that does
# not exist.
function mutate_trouble_exits_two { # @test
  run_cg mutate - <<<'{"groups":[{"anchor":"nope://x/","ops":[{"kind":"delete","id":"a"}]}]}'
  assert_failure 2
  assert_output --partial 'nope'

  run_cg mutate "$BATS_TEST_TMPDIR/absent.json"
  assert_failure 2
}
