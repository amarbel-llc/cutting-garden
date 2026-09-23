#! /usr/bin/env bats

# The fastmail organize inbox-triage lane (fastmail tags slice 1,
# docs/plans/2026-09-21-fastmail-tags-slice1.md). Backed by
# cutting-garden-fastmail-testserver, an in-memory JMAP server seeded with the
# deterministic, PII-free triage fixture (fastmailtestserver.SeedTriageFixture:
# threads T1–T4 in the Inbox, T5 archived) and reached through a config
# `[[fastmail.accounts]]` entry whose `session_url` points at it (see
# lib/fastmail.bash).
#
# Whole-document vectors (G16): pinned port 43113 + serialized tests, see the
# port table in lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/fastmail.bash"
  export output
  start_fastmail_server 43113
  init_store
}

teardown() {
  stop_fastmail_server
}

# bats file_tags=organize

# The smoke vector: the plugin reaches the testserver through the config's
# session_url and lists exactly the four Inbox threads newest first (T5 is
# archived, so it is absent), each carrying its presented tag set — leaf label
# tags joined from the nearest bare ancestor (D1; the `_` root never renders),
# the `_inbox`/`_unread`/`_flagged` state tags (D2), and T2's union across its
# three members (the Sent-only member contributes nothing).
function organize_fastmail_list_json_smoke { # @test
  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Inbox?thread=T1","name":"Your statement is ready","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","_unread","payee-one_medical"]}
	{"uri":"fastmail://test/Inbox?thread=T2","name":"Interview loop","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-x-msft","req-others"]}
	{"uri":"fastmail://test/Inbox?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox"]}
	{"uri":"fastmail://test/Inbox?thread=T4","name":"Project wrap-up","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-24-t-10x"]}
	EOM
}
