#! /usr/bin/env bats

# The fastmail write lane through `cg mcp`'s mutate_nodes (FDR 0024,
# cutting-garden#401): a thread's `mailbox` patch field moves it out of the
# mailbox its URI is listed under and into the named one, so a capability
# profile can allow "move between mailboxes" as one cg-field-v1 step without
# allowing a delete. Backed by the triage fixture (lib/fastmail.bash,
# fastmailtestserver.SeedTriageFixture); every test starts a fresh server, and
# writes are read back through cg itself.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/vectors.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/fastmail.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_fastmail_server
  init_store
}

teardown() {
  stop_fastmail_server
}

# bats file_tags=fastmail

# drive_patch ID BODY patches one thread under the Inbox anchor through one
# mutate_nodes call (response id 3), leaving the frames in $output.
drive_patch() {
  mcp_drive "$FASTMAIL_ROOT" "$(tools_call 3 mutate_nodes "$(jq -nc \
    --arg a "${FASTMAIL_ROOT}Inbox/" --arg id "$1" --arg body "$2" \
    '{groups:[{anchor:$a,ops:[{kind:"patch",id:$id,body:$body}]}]}')")"
}

# mutate_thread ID BODY runs drive_patch and prints the op's outcome as
# compact JSON.
mutate_thread() {
  drive_patch "$1" "$2"
  output="$(mcp_result_text "$output" 3 | jq -c '.groups[0].outcomes[0] | {status, fields, uri, error}')"
}

# refuse_thread ID BODY runs drive_patch, asserts the call is a tool error,
# and leaves its text in $output.
refuse_thread() {
  drive_patch "$1" "$2"
  local frames="$output"
  assert_equal "$(mcp_is_error "$frames" 3)" true
  output="$(mcp_result_text "$frames" 3)"
}

# T2 has three members: M2a in the Inbox (and msft), M2b in Sent, M2c in
# req-others (and msft). Moving it from the Inbox to the Archive moves only
# M2a, keeps its msft label, and leaves the other two where they are. The
# outcome names the field and the thread's new address under the Archive.
function fastmail_mutate_move_thread_to_archive { # @test
  mutate_thread T2 '{"mailbox": "Archive"}'
  assert_vector - <<-'EOM'
	{"status":"applied","fields":["mailbox"],"uri":"fastmail://test/Archive/?thread=T2","error":null}
	EOM

  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  run jq -r '.uri' <<<"$output"
  assert_output - <<-'EOM'
	fastmail://test/Inbox/?thread=T1
	fastmail://test/Inbox/?thread=T3
	fastmail://test/Inbox/?thread=T4
	EOM

  local -a reqs=()
  local id=3 email
  for email in M2a M2b M2c; do
    reqs+=("$(tools_call "$id" read_node "$(jq -nc \
      --arg u "${FASTMAIL_ROOT}Archive/?thread=T2&email=$email" '{uri:$u,content:"body"}')")")
    id=$((id + 1))
  done
  mcp_drive "$FASTMAIL_ROOT" "${reqs[@]}"
  local frames="$output" n
  output="$(for n in 3 4 5; do
    mcp_result_text "$frames" "$n" | sed '/^raw bytes: /d' | jq -c '{id, mailboxIds}'
  done)"
  assert_vector - <<-'EOM'
	{"id":"M2a","mailboxIds":{"mb-archive":true,"mb-msft":true}}
	{"id":"M2b","mailboxIds":{"mb-sent":true}}
	{"id":"M2c","mailboxIds":{"mb-msft":true,"mb-req-others":true}}
	EOM
}

# A move names its target by full name-path, the same path a mailbox URI
# carries: T1 into the nested payee/-one_medical label it already has leaves
# only that one mailbox.
function fastmail_mutate_move_thread_to_nested_mailbox { # @test
  mutate_thread T1 '{"mailbox": "payee/-one_medical"}'
  assert_vector - <<-'EOM'
	{"status":"applied","fields":["mailbox"],"uri":"fastmail://test/payee/-one_medical/?thread=T1","error":null}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}payee/-one_medical/"
  assert_success
  assert_vector - <<-'EOM'
	{"uri":"fastmail://test/payee/-one_medical/?thread=T1","name":"Your statement is ready","type":"cutting_garden-fastmail-thread-v1","tags":["_unread","payee-one_medical"]}
	EOM
}

# An unknown target is refused by name and writes nothing; so is a body that
# also names `tags` (a move and a tag-set replacement in one patch).
function fastmail_mutate_move_refusals { # @test
  refuse_thread T1 '{"mailbox": "Nowhere"}'
  assert_output --partial 'no mailbox at path \"Nowhere\"'

  refuse_thread T1 '{"mailbox": "Archive", "tags": ["_inbox"]}'
  assert_output --partial 'not both'

  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  run jq -r '.uri' <<<"$output"
  assert_line --index 0 "fastmail://test/Inbox/?thread=T1"
}

# describe_node_types reports the thread as writable, its patch body naming
# both fields, with a move as the example.
function fastmail_mutate_describes_thread_body { # @test
  mcp_drive "$FASTMAIL_ROOT" "$(tools_call 3 describe_node_types '{"scheme":"fastmail"}')"
  output="$(mcp_result_text "$output" 3 | jq -c '
    .[].types[] | select(.tag == "cutting_garden-fastmail-thread-v1")
    | {writable, example: .body.example,
       names_fields: (.body.accepts | join(" ") | test("\"mailbox\"") and test("\"tags\""))}')"
  assert_output '{"writable":true,"example":{"mailbox":"Archive"},"names_fields":true}'
}
