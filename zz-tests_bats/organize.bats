#! /usr/bin/env bats

# The organize pipeline lane (FDR 0023, RFC 0015): generate a hyphence-envelope
# organize document from a caldav calendar, edit it, and apply the edit as a
# substrate write — proving select -> group -> render -> edit -> three-way-merge
# -> PatchNode -> verify end to end against cutting-garden-caldav-testserver.
#
# The document is the RFC 0015 heading-ladder dialect: a `---` hyphence envelope
# (% comment, - _base/_anchor/_type, ! organize-base-v1), then a `# status=`
# dimension heading with pre-rendered `## =value` buckets, and object lines as
# espalier boxes `- [<id>] <desc>` (envelope `_type` spelling). The writable
# dimension exercised is `status` (a passthrough enum, Slice 2a). The seeded
# VTODOs carry no STATUS, so they start ungrouped (above the dimension heading);
# moving task1 under a `## =completed` bucket ASSIGNS its status through PatchNode.
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/vectors.bash"
  export output
  start_caldav_server 24101
  init_store
  # The Personal calendar holds task1.ics + task2.ics (VTODO); its VEVENT
  # (event1.ics) windows out of the object listing (#176/#177).
  CAL="${CALDAV_SOURCE%/dav/}/dav/cal/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_doc runs `organize -group-by status=` (the field spelling, design
# G10) and asserts the emitted document in full: the fenced envelope with the
# framework fields + type, the two VTODOs
# ungrouped (no STATUS yet), then the `# status=` dimension heading with its
# pre-rendered, empty `## =value` buckets. The lone VEVENT windows out of the
# object listing, so it is not organized.
generate_doc() {
  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize 'caldav:http://127.0.0.1:24101/dav/cal/ -> _terminal=no' status=`
	- _base = @blake2b256-tvyyx0hefvhj8gmt65qy68ggwv4sgza4n4cztk3jeu4956r573qs3nkvch
	- _anchor = caldav:http://127.0.0.1:24101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task1.ics] Buy milk
	- [task2.ics] Walk dog

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# write_task1_completed writes the edited document to $1: task1's box is pulled
# out of the ungrouped section and re-filed under the pre-rendered
# `## =completed` bucket, against the generated `_base`. The full input document
# is spelled out (G16) rather than spliced.
write_task1_completed() {
  cat >"$1" <<-'EOM'
	---
	% generated: `cg organize 'caldav:http://127.0.0.1:24101/dav/cal/ -> _terminal=no' status=`
	- _base = @blake2b256-tvyyx0hefvhj8gmt65qy68ggwv4sgza4n4cztk3jeu4956r573qs3nkvch
	- _anchor = caldav:http://127.0.0.1:24101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task2.ics] Walk dog

	# status=

	## =needs-action

	## =in-process

	## =completed

	- [task1.ics] Buy milk

	## =cancelled
	EOM
}

# write_task1_cancelled is write_task1_completed's sibling: the same edit
# against the same generated `_base`, but re-filing task1 under `## =cancelled`
# — the conflicting second edit the conflict lane applies after a completed move
# has already landed.
write_task1_cancelled() {
  cat >"$1" <<-'EOM'
	---
	% generated: `cg organize 'caldav:http://127.0.0.1:24101/dav/cal/ -> _terminal=no' status=`
	- _base = @blake2b256-tvyyx0hefvhj8gmt65qy68ggwv4sgza4n4cztk3jeu4956r573qs3nkvch
	- _anchor = caldav:http://127.0.0.1:24101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task2.ics] Walk dog

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled

	- [task1.ics] Buy milk
	EOM
}

# assert_task1_completed re-renders the calendar and asserts the "after"
# document in full. organize's default `_terminal=no` selection windows a
# completed task OUT of the document, so task1 is simply gone: only task2 remains
# (still ungrouped), every bucket is empty, and the `_base` pin moved with the
# content. The live status itself is proven by the `list -query` check.
assert_task1_completed() {
  run_cg organize -group-by status= "$CAL"
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize 'caldav:http://127.0.0.1:24101/dav/cal/ -> _terminal=no' status=`
	- _base = @blake2b256-7l754q228ug7lantv0sf7t2q3tkv2jquwfx30dsfatj4839yv96qqzlhzm
	- _anchor = caldav:http://127.0.0.1:24101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task2.ics] Walk dog

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# Generate emits the hyphence-envelope dialect: the fenced envelope with the
# framework fields + type, the `# status=` dimension heading, its pre-rendered
# `## =value` buckets, and the two VTODOs as bare espalier boxes.
function organize_generate_emits_envelope { # @test
  generate_doc
}

# The core tracer: generate, move task1 under `## =completed`, apply with
# --commit, and confirm the status landed on the live object via a facet query
# and the re-rendered document. The move writes the CANONICAL RFC 5545 uppercase
# (`STATUS:COMPLETED`, curl-verified) — the case-fold codec's Parse folds the
# lowercase bucket up; lowercase is never persisted.
function organize_apply_status_move_commits { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_task1_completed "$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [task1.ics status={+completed+}] Buy milk

organize: wrote 1 change(s)
EOF

  # The stored property is canonical RFC 5545 UPPERCASE — never lowercase.
  # A written body is CRLF-serialized and carries a volatile DTSTAMP
  # (plugins/caldav/ical TaskToIcal), so the whole body cannot be pinned; the
  # property is asserted as an exact full line (anchored, tolerating the CR).
  run curl -fsS "${CALDAV_SOURCE#caldav:}cal/task1.ics"
  assert_success
  assert_line --regexp $'^STATUS:COMPLETED\r?$'
  refute_output --partial 'STATUS:completed'

  # The full query listing: task1 matched, task2 (still status-less) did not.
  # The mesa plain form (TAB-separated, native tags slice 4): caldav declares
  # the categories tag dimension, so the TAGS column is present — empty (a
  # trailing TAB) for these untagged fixtures.
  run_cg list -query 'status=completed' "$CAL"
  assert_success
  assert_tab_table \
    "$(tab_row URI NAME TYPE TAGS)" \
    "$(tab_row caldav:http://127.0.0.1:24101/dav/cal/task1.ics task1.ics caldav-object-vtodo-v1 '')"

  # The old uppercase spelling still matches — the FoldCase dimension folds
  # BOTH sides of the predicate (FDR 0025 case-fold matching rule).
  run_cg list -query 'status=COMPLETED' "$CAL"
  assert_success
  assert_tab_table \
    "$(tab_row URI NAME TYPE TAGS)" \
    "$(tab_row caldav:http://127.0.0.1:24101/dav/cal/task1.ics task1.ics caldav-object-vtodo-v1 '')"

  assert_task1_completed
}

# The default is a dry-run: apply without --commit prints the intended move but
# does not write, so the live status query still finds nothing and the document
# re-renders unchanged.
function organize_apply_dry_run_does_not_write { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_task1_completed "$edited"

  run_cg organize -apply "$edited"
  assert_success
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [task1.ics status={+completed+}] Buy milk

organize: dry-run — nothing written
EOF

  # The live status query finds nothing — mesa's zero-row plain form renders
  # nothing at all (native tags slice 4).
  run_cg list -query 'status=completed' "$CAL"
  assert_success
  assert_output ''

  # The re-render is byte-identical to the generated vector — proves no write.
  generate_doc
}

# -commit-directly reads the edited document from stdin and commits it — the
# scripted re-apply path (dodder's commit-directly mode; the mode itself is the
# commit assertion). Proves stdin ingestion writes through to the live object.
function organize_commit_directly_from_stdin_writes { # @test
  generate_doc
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_task1_completed "$edited"

  run_cg organize -commit-directly <"$edited"
  assert_success
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [task1.ics status={+completed+}] Buy milk

organize: wrote 1 change(s)
EOF

  run_cg list -query 'status=completed' "$CAL"
  assert_success
  assert_tab_table \
    "$(tab_row URI NAME TYPE TAGS)" \
    "$(tab_row caldav:http://127.0.0.1:24101/dav/cal/task1.ics task1.ics caldav-object-vtodo-v1 '')"

  assert_task1_completed
}

# A pinned base whose live state has drifted is a conflict, not a silent clobber:
# commit one move, then apply a second edit built against the ORIGINAL base — the
# merge must reject. completed and cancelled are BOTH terminal, so the after-
# render alone cannot tell a rejected cancelled move from a silently applied one
# (task1 is windowed out either way); the discriminating check is the live
# status query — task1 is still completed, never cancelled.
function organize_apply_conflict_rejects { # @test
  generate_doc

  local edited_a="$BATS_TEST_TMPDIR/edited_a.txt"
  write_task1_completed "$edited_a"
  run_cg organize -apply "$edited_a" -commit
  assert_success

  local edited_b="$BATS_TEST_TMPDIR/edited_b.txt"
  write_task1_cancelled "$edited_b"
  run_cg organize -apply "$edited_b" -commit
  assert_failure 2
  # The whole rejection: completed windowed task1 OUT of the `_terminal=no`
  # live listing, so the merge sees no live bucket at all (live="") — still a
  # drift from the pinned base, and the exact refused move is named.
  assert_vector - <<-'EOM'
	cutting-garden: organize --apply: 1 conflict(s) — the live state drifted from the pinned base; regenerate and re-edit:
	  task1.ics: base="" live="" (your edit moved it to "cancelled")
	EOM

  run_cg list -query 'status=completed' "$CAL"
  assert_success
  assert_tab_table \
    "$(tab_row URI NAME TYPE TAGS)" \
    "$(tab_row caldav:http://127.0.0.1:24101/dav/cal/task1.ics task1.ics caldav-object-vtodo-v1 '')"

  run_cg list -query 'status=cancelled' "$CAL"
  assert_success
  assert_output ''

  assert_task1_completed
}

# write_personal_account declares a caldav account NAMED `personal` at the
# Personal calendar. The account URL is the opaque `caldav:http://…` form: a
# `caldav://` URL means HTTPS, and the testserver is plain HTTP.
write_personal_account() {
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-EOF
	[[caldav.accounts]]
	name = "personal"
	url = "${CAL}"
	EOF
}

# A selection may open with a configured root's NAME as a bound type (RFC 0020
# §3.4, §4.2): `!personal` resolves through the account's `name` to the same
# root the URL names, so the document is generate_doc's — anchor, query and
# body — with only the provenance note (and so `_base`) spelling the selection
# as typed.
function organize_selection_by_root_name { # @test
  write_personal_account

  run_cg organize '!personal' status=
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize '!personal -> _terminal=no' status=`
	- _base = @blake2b256-vd0lh2p6krwzjsxrs57meu9fw4ty08rzntepy0admq0csfyp5jvqrlqyk3
	- _anchor = caldav:http://127.0.0.1:24101/dav/cal/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task1.ics] Buy milk
	- [task2.ics] Walk dog

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# Terms after the bound type select WITHIN its root, in the same step or after
# a `->`; both spell one selection, so both yield the same document.
function organize_selection_by_root_name_with_terms { # @test
  write_personal_account

  run_cg organize '!personal summary*=milk' status=
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize '!personal -> summary*=milk _terminal=no' status=`
	- _base = @blake2b256-ds7agzrg67qrhvmj3yrnr06lypu6dmuqhwpus8ppegs2zgdt7yzszp96gv
	- _anchor = caldav:http://127.0.0.1:24101/dav/cal/
	- _query = summary*=milk _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [task1.ics] Buy milk

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
  local by_step="$output"

  run_cg organize '!personal -> summary*=milk' status=
  assert_success
  assert_equal "$output" "$by_step"
}

# An unknown root name is a usage error naming it — never a fall-through to
# treating `!nosuch` as a plugin node type with no origin.
function organize_selection_unknown_root_name_is_usage_error { # @test
  write_personal_account

  run_cg organize '!nosuch' status=
  assert_failure 64
  assert_output --partial '`!nosuch` is not a configured root name'
}
