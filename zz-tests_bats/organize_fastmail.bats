#! /usr/bin/env bats

# The fastmail organize inbox-triage lane (fastmail tags slice 1,
# docs/plans/2026-09-21-fastmail-tags-slice1.md). Backed by
# cutting-garden-fastmail-testserver, an in-memory JMAP server seeded with the
# deterministic, PII-free triage fixture (fastmailtestserver.SeedTriageFixture:
# threads T1–T4 in the Inbox, T5 archived) and reached through a config
# `[[fastmail.accounts]]` entry whose `session_url` points at it (see
# lib/fastmail.bash).
#
# Every test starts a FRESH testserver (setup/teardown), so a write in one
# vector never leaks into the next. Writes are read back through cg itself —
# `list -format json` of the mailboxes a thread should (not) be in, the
# re-rendered document, and, for per-message keywords, `cg mcp`'s read_node
# on the member email nodes (whose body is the structured JMAP Email) — never
# through a test-only backdoor into the server.
#
# The G# → test index for this lane is the "fastmail tags slice 1" section of
# docs/plans/2026-08-30-native-tags-vectors.md.
#
# Whole-document vectors (G16): pinned port 43113 + serialized tests, see the
# port table in lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/fastmail.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_fastmail_server 43113
  init_store
}

teardown() {
  stop_fastmail_server
}

# bats file_tags=organize

# generate_inbox renders the `--group-by _inbox` triage document and asserts
# it in full — the base every write vector below edits.
generate_inbox() {
  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-5j54at3zuxa692qvl7aarpth3zvj50yvcrf42xthexsxwwjr6rgsmtsq3r
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged from="friend@example.com"] Lunch next week?
	- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# edited_inbox writes $BATS_TEST_TMPDIR/edited.txt: generate_inbox's envelope
# verbatim (same `_base`, so apply diffs against the generated document)
# followed by the edited body read from stdin.
edited_inbox() {
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-5j54at3zuxa692qvl7aarpth3zvj50yvcrf42xthexsxwwjr6rgsmtsq3r
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	EOM
  cat >>"$edited"
  echo "$edited"
}

# email_states URI... reads each member email node through `cg mcp`'s
# read_node (body only: the structured JMAP Email) in one server run and
# prints one compact `{id, mailboxIds, keywords}` line per URI, in argument
# order — the per-message read-back D3's all-members fan-out is observable
# through.
email_states() {
  local -a reqs=()
  local id=3 uri
  for uri in "$@"; do
    reqs+=("$(tools_call "$id" read_node "$(jq -nc --arg u "$uri" '{uri:$u,content:"body"}')")")
    id=$((id + 1))
  done
  mcp_drive "$FASTMAIL_ROOT" "${reqs[@]}"
  local responses="$output" n
  local -a lines=()
  for ((n = 3; n < id; n++)); do
    lines+=("$(mcp_result_text "$responses" "$n" |
      sed '/^raw bytes: /d' | jq -c '{id, mailboxIds, keywords}')")
  done
  output="$(printf '%s\n' "${lines[@]}")"
}

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
	{"uri":"fastmail://test/Inbox/?thread=T1","name":"Your statement is ready","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","_unread","payee-one_medical"]}
	{"uri":"fastmail://test/Inbox/?thread=T2","name":"Interview loop","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-x-msft","req-others"]}
	{"uri":"fastmail://test/Inbox/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox"]}
	{"uri":"fastmail://test/Inbox/?thread=T4","name":"Project wrap-up","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-24-t-10x"]}
	EOM
}

# Vector 1 (D1, D2, D5, D6, Task 5b): `--group-by _inbox` is a dodder-hyphen
# namespace grouping with no continuations — one G10a root heading `# _inbox`
# holding every Inbox thread, newest first, the `_group-by = _inbox` envelope
# directive, the thread type in `_type`. Each box is `[<threadId> <tags…>
# from=<addr>] <subject>`: the plugin-declared thread id (NodeIDer), the
# key-free tag atoms with `_inbox` placement-stripped as the root's Via tag,
# `_unread`/`_flagged` sorting first under plain ASCII order, the `_` root never
# rendering (T2's `req-others`), and T2 carrying the union of its members'
# labels.
function organize_fastmail_inbox_grouped_render { # @test
  generate_inbox
}

# Vector 2 (D2, D5): with no TAG placement, nothing strips `_inbox`, so it
# rides in every box. organize has no ungrouped mode (it requires
# --group-by), so the vector groups by the read-only `has_attachment`
# categorical instead — every fixture thread lands in one `## =no` bucket and
# the lines are the grouped render's, `_inbox` restored.
function organize_fastmail_inbox_ungrouped_render { # @test
  run_cg organize -group-by has_attachment= "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by has_attachment= fastmail://test/Inbox/`
	- _base = @blake2b256-wq3t4k596lfsdchex9glgwuvwk88snq9wz3wjuqn6kfamudk44ks8rzh86
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	! organize-base-v1
	---

	# has_attachment=

	## =no

	- [T1 _inbox _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 _inbox proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged _inbox from="friend@example.com"] Lunch next week?
	- [T4 _inbox proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 3 (D3, D5): archiving by move — T1's line moved above `# _inbox`
# removes exactly the bare `_inbox` (the G10a root bucket's reconstruction).
# The fastmail write clears the inbox mailbox from T1's member; since it keeps
# its `payee/-one_medical` label it is NOT filed into Archive (the archive
# invariant only fires on a member left with no mailbox).
function organize_fastmail_archive_by_move { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready

		# _inbox

		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged from="friend@example.com"] Lunch next week?
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T1  tags=[-_inbox,_unread,payee-one_medical-]{+_unread,payee-one_medical+}]  Your statement is ready

organize: wrote 1 change(s)
EOF

  # T1 left the Inbox, is still under its label, and did NOT land in Archive.
  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Inbox/?thread=T2","name":"Interview loop","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-x-msft","req-others"]}
	{"uri":"fastmail://test/Inbox/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox"]}
	{"uri":"fastmail://test/Inbox/?thread=T4","name":"Project wrap-up","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-24-t-10x"]}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}payee/-one_medical/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/payee/-one_medical/?thread=T1","name":"Your statement is ready","type":"cutting_garden-fastmail-thread-v1","tags":["_unread","payee-one_medical"]}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}Archive/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Archive/?thread=T5","name":"Resume received","type":"cutting_garden-fastmail-thread-v1","tags":["area-career-resume"]}
	EOM

  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-c7h3shs0jqfmdlgmlzz979pmes66rxuntfm2fsp9xdrn5vs6395sh07prk
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged from="friend@example.com"] Lunch next week?
	- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 4 (D3 archive invariant): T3 has no label, so moving it out of
# `# _inbox` would leave its one member in no mailbox — JMAP forbids that, and
# the plugin files it into the `archive` role mailbox instead.
function organize_fastmail_archive_unlabeled_lands_in_archive { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		- [T3 _flagged from="friend@example.com"] Lunch next week?

		# _inbox

		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T3  tags=[-_flagged,_inbox-]{+_flagged+}]  Lunch next week?

organize: wrote 1 change(s)
EOF

  run_cg list -format json "${FASTMAIL_ROOT}Archive/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Archive/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged"]}
	{"uri":"fastmail://test/Archive/?thread=T5","name":"Resume received","type":"cutting_garden-fastmail-thread-v1","tags":["area-career-resume"]}
	EOM

  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-sq7q4ks7hmpjm6p4axcg4yvt5mvvw6m754xwmcs37us75a8q6t6qev6u6w
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 5 (D2, D3 all-members): state atoms edited in place under
# `# _inbox` — T1 loses `_unread` and gains `_flagged`; T2 (three members, one
# of them Sent-only) gains `_flagged`. Keyword writes fan out to EVERY member,
# so all four messages end with `$seen` + `$flagged`, and no member's
# mailboxes moved.
function organize_fastmail_state_atoms { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		# _inbox

		- [T1 _flagged payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 _flagged proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged from="friend@example.com"] Lunch next week?
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 2 change(s):

  - [T1  tags=[-_inbox,_unread,payee-one_medical-]{+_flagged,_inbox,payee-one_medical+}]  Your statement is ready
  - [T2  tags=[-_inbox,proj-x-msft,req-others-]{+_flagged,_inbox,proj-x-msft,req-others+}]  Interview loop

organize: wrote 2 change(s)
EOF

  email_states \
    'fastmail://test/Inbox/?email=M1&thread=T1' \
    'fastmail://test/Inbox/?email=M2a&thread=T2' \
    'fastmail://test/Inbox/?email=M2b&thread=T2' \
    'fastmail://test/Inbox/?email=M2c&thread=T2'
  assert_output - <<-'EOM'
	{"id":"M1","mailboxIds":{"mb-inbox":true,"mb-one_medical":true},"keywords":{"$flagged":true,"$seen":true}}
	{"id":"M2a","mailboxIds":{"mb-inbox":true,"mb-msft":true},"keywords":{"$flagged":true,"$seen":true}}
	{"id":"M2b","mailboxIds":{"mb-sent":true},"keywords":{"$flagged":true,"$seen":true}}
	{"id":"M2c","mailboxIds":{"mb-msft":true,"mb-req-others":true},"keywords":{"$flagged":true,"$seen":true}}
	EOM

  run_cg list -format json "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Inbox/?thread=T1","name":"Your statement is ready","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox","payee-one_medical"]}
	{"uri":"fastmail://test/Inbox/?thread=T2","name":"Interview loop","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox","proj-x-msft","req-others"]}
	{"uri":"fastmail://test/Inbox/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox"]}
	{"uri":"fastmail://test/Inbox/?thread=T4","name":"Project wrap-up","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","proj-24-t-10x"]}
	EOM

  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-0836w05zfjp97sszyu06wzardnfnkdc0v833q9frd73rap3rvxeqpyluwh
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T1 _flagged payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 _flagged proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged from="friend@example.com"] Lunch next week?
	- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 6 (D2): `_trash` is a soft mutation — adding it files T4 into the
# trash role mailbox WITHOUT touching its other memberships, so it is still in
# the Inbox and still labelled `proj-24-t-10x`.
function organize_fastmail_trash_atom { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		# _inbox

		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged from="friend@example.com"] Lunch next week?
		- [T4 _trash proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T4  tags=[-_inbox,proj-24-t-10x-]{+_inbox,_trash,proj-24-t-10x+}]  Project wrap-up

organize: wrote 1 change(s)
EOF

  run_cg list -format json "${FASTMAIL_ROOT}Trash/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Trash/?thread=T4","name":"Project wrap-up","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","_trash","proj-24-t-10x"]}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}zz-archive/proj/-24-t/-10x/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/zz-archive/proj/-24-t/-10x/?thread=T4","name":"Project wrap-up","type":"cutting_garden-fastmail-thread-v1","tags":["_inbox","_trash","proj-24-t-10x"]}
	EOM

  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-5kejh0cvvtv2m2pj29nv88w3q68hnz4frhz8j6f56cl9dw590dmsxp3edp
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged from="friend@example.com"] Lunch next week?
	- [T4 _trash proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 7 (D4a): `payee-acme` realizes no mailbox, and the existing `payee`
# mailbox's tag is a proper `-`-boundary prefix of it, so the apply creates
# `-acme` as a continuation child of `payee` (Mailbox/set create, referenced
# from the same request's Email/set) and T3 carries the new tag.
function organize_fastmail_create_continuation_tag { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		# _inbox

		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged payee-acme from="friend@example.com"] Lunch next week?
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T3  tags=[-_flagged,_inbox-]{+_flagged,_inbox,payee-acme+}]  Lunch next week?

organize: wrote 1 change(s)
EOF

  run_cg list -format json "${FASTMAIL_ROOT}payee/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/payee/-acme/","name":"-acme","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/payee/-one_medical/","name":"-one_medical","type":"cutting_garden-fastmail-mailbox-v1"}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}payee/-acme/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/payee/-acme/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox","payee-acme"]}
	EOM

  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-ramncahpzrmznen678rl4ssz3p29qvwfxkhwt4e0vjvhck8fj9uq5k2s7t
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged payee-acme from="friend@example.com"] Lunch next week?
	- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 8 (D4b): `proj-trips-26-10-hike` shares the `proj-trips-26` prefix
# with the existing `proj-trips-26-09-yoga` (under `area/-travel`), so per the
# plan's D4 worked example (`proj-trips-26-09-yoga_retreat` → a bare child of
# `area/-travel`) it is created as a BARE sibling there. The bare interior
# `zz-archive/proj` (tag `proj`) is a proper prefix too, but D4 ranks
# candidates by shared `-` segments: (b) `proj-trips-26-09-yoga` shares 3,
# (a) `proj` only 1, so the new tag does NOT land inside the archive.
function organize_fastmail_create_sibling_tag { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		# _inbox

		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged proj-trips-26-10-hike from="friend@example.com"] Lunch next week?
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T3  tags=[-_flagged,_inbox-]{+_flagged,_inbox,proj-trips-26-10-hike+}]  Lunch next week?

organize: wrote 1 change(s)
EOF

  run_cg list -format json "${FASTMAIL_ROOT}area/-travel/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/area/-travel/proj-trips-26-09-yoga/","name":"proj-trips-26-09-yoga","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/area/-travel/proj-trips-26-10-hike/","name":"proj-trips-26-10-hike","type":"cutting_garden-fastmail-mailbox-v1"}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}area/-travel/proj-trips-26-10-hike/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/area/-travel/proj-trips-26-10-hike/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox","proj-trips-26-10-hike"]}
	EOM
}

# Vector 9 (D4c): `misc-thing` shares no `-`-boundary prefix with any existing
# tag, so its mailbox is created bare at the account root.
function organize_fastmail_create_root_tag { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		# _inbox

		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged misc-thing from="friend@example.com"] Lunch next week?
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T3  tags=[-_flagged,_inbox-]{+_flagged,_inbox,misc-thing+}]  Lunch next week?

organize: wrote 1 change(s)
EOF

  run_cg list -format json "$FASTMAIL_ROOT"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/Archive/","name":"Archive","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/Inbox/","name":"Inbox","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/Sent/","name":"Sent","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/Trash/","name":"Trash","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/_/","name":"_","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/area/","name":"area","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/misc-thing/","name":"misc-thing","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/payee/","name":"payee","type":"cutting_garden-fastmail-mailbox-v1"}
	{"uri":"fastmail://test/zz-archive/","name":"zz-archive","type":"cutting_garden-fastmail-mailbox-v1"}
	EOM
  run_cg list -format json "${FASTMAIL_ROOT}misc-thing/"
  assert_success
  assert_output - <<-'EOM'
	{"uri":"fastmail://test/misc-thing/?thread=T3","name":"Lunch next week?","type":"cutting_garden-fastmail-thread-v1","tags":["_flagged","_inbox","misc-thing"]}
	EOM

  run_cg organize -group-by _inbox "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by _inbox fastmail://test/Inbox/`
	- _base = @blake2b256-gz8ymssvgh72e8j77zj7yfp6dfaj2c3wxvvnt02gcd76t9dkchpqmmyaku
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	- _group-by = _inbox
	! organize-base-v1
	---

	# _inbox

	- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged misc-thing from="friend@example.com"] Lunch next week?
	- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
}

# Vector 10 (D2): `_sent` typed into a box is a loud bad request naming the
# tag — exit 64 (EX_USAGE: D2 specifies a bad request, which the CLI maps to
# 64) after the diff preview — and nothing is written: the re-render is
# byte-identical, `_base` included, and no mailbox was created.
function organize_fastmail_reserved_state_tag_rejected { # @test
  generate_inbox
  local edited
  edited="$(
    edited_inbox <<-'EOM'
		# _inbox

		- [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
		- [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
		- [T3 _flagged _sent from="friend@example.com"] Lunch next week?
		- [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
	EOM
  )"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_output - <<'EOF'
organize: 1 change(s):

  - [T3  tags=[-_flagged,_inbox-]{+_flagged,_inbox,_sent+}]  Lunch next week?

cutting-garden: fastmail plugin: "_sent" is reserved and cannot be written; the writable state tags are _flagged, _inbox, _trash and _unread
EOF

  generate_inbox
}

# Vector 11 (D6, design G12): the thread type's designated tag set is the
# `tags` field under the dodder-hyphen interpreter it declares (no [tags]
# override in the lane's config) — as reported by `cg mcp`'s
# describe_node_types `tag_set` for every fastmail node type (only the thread
# carries one). The `list -format json` NDJSON half of this vector is the
# smoke test above.
function organize_fastmail_list_json_carries_tags { # @test
  mcp_drive "$FASTMAIL_ROOT" "$(tools_call 3 describe_node_types '{}')"
  output="$(mcp_result_text "$output" 3 | jq -c '
    .[].types[] | select(.tag | startswith("cutting_garden-fastmail-"))
    | {tag} + (if has("tag_set") then {tag_set} else {} end)')"
  assert_output - <<-'EOM'
	{"tag":"cutting_garden-fastmail-mailbox-v1"}
	{"tag":"cutting_garden-fastmail-thread-v1","tag_set":{"field":"tags","interpreter":"dodder-hyphen"}}
	{"tag":"cutting_garden-fastmail-email-v1"}
	{"tag":"cutting_garden-fastmail-email-raw-v1"}
	EOM
}

# Vector 12 (D6): `year` is retired in favor of `date`, a groupable FieldDate
# (the newest member's receivedAt), so `--group-by date=(month)` coarsens it
# into `## =<YYYY-MM>` buckets through the #230 prefix machinery. A field
# grouping places no tag, so `_inbox` rides in every box.
function organize_fastmail_group_by_date_month { # @test
  run_cg organize -group-by 'date=(month)' "${FASTMAIL_ROOT}Inbox/"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by date=(month) fastmail://test/Inbox/`
	- _base = @blake2b256-mc35yz5yvrtdltm4l69qj7dw7d9ku4mxk2x4hussmw4lzc83zcnqyujs02
	- _anchor = fastmail://test/Inbox/
	- _type = !cutting_garden-fastmail-thread-v1
	! organize-base-v1
	---

	# date=(month)

	## =2026-08

	- [T4 _inbox proj-24-t-10x from="team@example.com"] Project wrap-up

	## =2026-09

	- [T1 _inbox _unread payee-one_medical from="billing@example.com"] Your statement is ready
	- [T2 _inbox proj-x-msft req-others from="recruiter@example.com"] Interview loop
	- [T3 _flagged _inbox from="friend@example.com"] Lunch next week?
	EOM
}
