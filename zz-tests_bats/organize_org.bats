#! /usr/bin/env bats

# The org plugin's organize lane (FDR 0026 slice 3b): generate an organize
# document over an Orgzly inbox file, edit it, and apply — a state move,
# atom edits and a tag edit land in the .org file under the plugin's WebDAV
# lock, read back before success is reported. Backed by the in-memory WebDAV
# server seeded from zz-tests_bats/fixtures/org (lib/org.bash); documents
# anchor at org://test/, so the server's port never appears in them.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/org.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/vectors.bash"
  export output
  start_org_server
  write_org_config
  init_store
}

teardown() {
  stop_org_server
}

# bats file_tags=organize,org

generate_inbox_by_state() {
  run_cg organize -group-by state= org://test/_inbox.org
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize 'org://test/_inbox.org -> _terminal=no' state=`
	- _base = @blake2b256-el7tr6rwg66jvppu2tygeyyzln8tut2xhkhsyvg4vmw6wq6qgcfqynyg5s
	- _anchor = org://test/_inbox.org
	- _query = _terminal=no
	- _type = !cutting_garden-org-headline-v1
	! organize-base-v1
	---

	# state=

	## =TODO

	- ["h=Call+the+plumber+about+the+leak" project-house zz-inbox deadline=2026-10-10] Call the plumber about the leak

	## =NEXT

	- ["id=3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11" project-garden req-internet zz-inbox scheduled=2026-10-14] Draft the plugin doc

	## =DONE

	## =CANCELLED

	## =none

	- ["h=Idea%3A+expose+CLOSED+dates" zz-inbox] Idea: expose CLOSED dates
	EOM
}

function organize_org_generate_by_state { # @test
  generate_inbox_by_state
}

# inbox_file prints the server's _inbox.org with minted ids and the CLOSED
# time (both vary per run) normalized.
inbox_file() {
  org_dav GET /_inbox.org |
    sed -E 's/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/<uuid>/g; s/CLOSED: \[[^]]*\]/CLOSED: [now]/'
}

# One apply: the plumber moves to DONE (CLOSED added, :ID: minted), the doc
# is rescheduled through its date atom (its time of day kept), and the idea
# gains an own tag and a new title through its tag atoms and trailer.
function organize_org_apply_state_atoms_tags_title { # @test
  generate_inbox_by_state
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize 'org://test/_inbox.org -> _terminal=no' state=`
	- _base = @blake2b256-el7tr6rwg66jvppu2tygeyyzln8tut2xhkhsyvg4vmw6wq6qgcfqynyg5s
	- _anchor = org://test/_inbox.org
	- _query = _terminal=no
	- _type = !cutting_garden-org-headline-v1
	! organize-base-v1
	---

	# state=

	## =TODO

	## =NEXT

	- ["id=3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11" project-garden req-internet zz-inbox scheduled=2026-10-21] Draft the plugin doc

	## =DONE

	- ["h=Call+the+plumber+about+the+leak" project-house zz-inbox deadline=2026-10-10] Call the plumber about the leak

	## =CANCELLED

	## =none

	- ["h=Idea%3A+expose+CLOSED+dates" zz-inbox someday] Idea: expose CLOSED dates in org
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_vector - <<-'EOM'
	organize: 3 change(s):

	  - ["h=Call+the+plumber+about+the+leak" project-house zz-inbox deadline=2026-10-10 state=[-TODO-]{+DONE+}] Call the plumber about the leak
	  - ["h=Idea%3A+expose+CLOSED+dates" {+someday+} zz-inbox] Idea: expose CLOSED dates {+in+} {+org+}
	  - ["id=3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11" project-garden req-internet zz-inbox scheduled=[-2026-10-14-]{+2026-10-21+}] Draft the plugin doc

	organize: wrote 3 change(s)
	EOM

  run inbox_file
  assert_output - <<-'EOM'
	#+FILETAGS: :zz-inbox:
	* DONE Call the plumber about the leak              :project-house:
	  CLOSED: [now] DEADLINE: <2026-10-10 Fri>
	:PROPERTIES:
	:ID:       <uuid>
	:END:
	  Ask about the shutoff valve too.
	* NEXT Draft the plugin doc                         :project-garden:req-internet:
	  SCHEDULED: <2026-10-21 Wed>
	  :PROPERTIES:
	  :ID:       <uuid>
	  :END:
	** Ask about appends
	* Idea: expose CLOSED dates in org :someday:
	:PROPERTIES:
	:ID:       <uuid>
	:END:
	* DONE Renew passport                               :area-travel:
	  CLOSED: [now]
	EOM
  # The doc's existing :ID: (itself a UUID, so normalized above) is kept.
  run org_dav GET /_inbox.org
  assert_output --partial ':ID:       3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11'
}

# An inherited tag lives in #+FILETAGS (or on a parent headline): removing it
# from one headline's box is refused, naming the tag, and nothing is written.
function organize_org_inherited_tag_removal_is_refused { # @test
  generate_inbox_by_state
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize 'org://test/_inbox.org -> _terminal=no' state=`
	- _base = @blake2b256-el7tr6rwg66jvppu2tygeyyzln8tut2xhkhsyvg4vmw6wq6qgcfqynyg5s
	- _anchor = org://test/_inbox.org
	- _query = _terminal=no
	- _type = !cutting_garden-org-headline-v1
	! organize-base-v1
	---

	# state=

	## =TODO

	- ["h=Call+the+plumber+about+the+leak" project-house deadline=2026-10-10] Call the plumber about the leak

	## =NEXT

	- ["id=3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11" project-garden req-internet zz-inbox scheduled=2026-10-14] Draft the plugin doc

	## =DONE

	## =CANCELLED

	## =none

	- ["h=Idea%3A+expose+CLOSED+dates" zz-inbox] Idea: expose CLOSED dates
	EOM

  local before
  before="$(org_dav GET /_inbox.org)"
  run_cg organize -apply "$edited" -commit
  assert_failure
  assert_output --partial '"zz-inbox" is inherited'
  run org_dav GET /_inbox.org
  assert_output "$before"
}
