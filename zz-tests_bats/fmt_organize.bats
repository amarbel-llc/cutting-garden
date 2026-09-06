#! /usr/bin/env bats

# The fmt-organize lane (native tags design G4, slice 3): `cg fmt-organize
# <path>` regenerates an organize document FROM ITS ENVELOPE — `_anchor`
# re-resolved to the plugin, `_query` re-selected verbatim, the grouping and
# the `_tag-atoms`/`_tag-strip` levers read from the document itself (the doc
# is authoritative; `[organize]` config defaults are NOT re-consulted) —
# re-renders against the live data, stores the new base blob, and rewrites the
# file IN PLACE (atomic temp + rename) with the fresh `- _base` pin, printing
# a one-line rewritten/unchanged summary. v1 REFUSES (exit 64, file
# untouched) when the body differs from its pinned base — "doc has unapplied
# edits; apply or discard first" — so a pending edit is never silently
# regenerated over (the edit-preserving v2 is cutting-garden#252). Like
# generate, fmt-organize never emits empty (reset) headings.
#
# Fixture: the /dav/lit/ calendar (opt-in via CG_TEST_CALDAV_LIT), augmented
# PER TEST via curl PUT against the in-memory testserver (a changed lit2
# SUMMARY is the live drift fmt refreshes into the document).
#
# Whole-document vectors (G16): pinned port + serialized tests, see
# lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_LIT=1
  # Pin the config dir under the sandboxed $HOME so os.UserConfigDir resolves
  # the lane's own config.toml (and an ambient host value can't leak in). The
  # default is NO config file; the lever lane writes one for generate and then
  # REMOVES it before fmt, proving the document is authoritative.
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  rm -f "$XDG_CONFIG_HOME/cutting-garden/config.toml"
  start_caldav_server 43111
  init_store
  LIT="${CALDAV_SOURCE%/dav/}/dav/lit/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# put_ics URL replaces the live object at URL with the iCalendar body on stdin
# — the lane's per-test live-drift injection (writes only the in-memory
# server).
put_ics() {
  curl -fsS -X PUT --data-binary @- "$1" || fail "PUT $1 failed"
}

# generate_status_doc generates the default status-grouped document over
# /dav/lit/, asserts it in full (the pre-fmt baseline every test starts
# from), and writes it to $1 — the on-disk document fmt-organize operates on.
generate_status_doc() {
  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43111/dav/lit/`
	- _base = @blake2b256-ds5v08vsq056f25466mt43k9d82t3yr9980n3ejd6dnhkunh6yss3japhu
	- _anchor = caldav:http://127.0.0.1:43111/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit2.ics location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
  printf '%s\n' "$output" >"$1"
}

# drift_lit2_summary changes lit2's live SUMMARY out from under a generated
# document (LOCATION kept) — the live drift the fmt lanes refresh into the
# file.
drift_lit2_summary() {
  put_ics "${CALDAV_SOURCE#caldav:}lit/lit2.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit2
	SUMMARY:Read many books
	LOCATION:Bank
	END:VTODO
	END:VCALENDAR
	EOF
}

# G4 regenerate: with the live data drifted (lit2's SUMMARY changed), fmt
# rewrites the file in place — the refreshed body carries the new SUMMARY, the
# envelope a fresh `- _base` pin, and the provenance/`_query`/`_anchor` lines
# carry forward verbatim — and the one-line summary names the old and new
# digests.
function fmt_organize_regenerates_and_rewrites_base { # @test
  local doc="$BATS_TEST_TMPDIR/triage.txt"
  generate_status_doc "$doc"
  drift_lit2_summary

  run_cg fmt-organize "$doc"
  assert_success
  assert_output "fmt-organize: $doc rewritten — _base @blake2b256-ds5v08vsq056f25466mt43k9d82t3yr9980n3ejd6dnhkunh6yss3japhu → @blake2b256-2lgqcr9ssgc9v7pzrxku400r9e3phm4hutqktu69lknt43lp8vdscjxk7g"

  run cat "$doc"
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43111/dav/lit/`
	- _base = @blake2b256-2lgqcr9ssgc9v7pzrxku400r9e3phm4hutqktu69lknt43lp8vdscjxk7g
	- _anchor = caldav:http://127.0.0.1:43111/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit2.ics location=Bank] Read many books
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# Unchanged live data → the file is byte-identical after fmt (the regenerate
# reproduces the exact document, `- _base` included) and the summary says so.
function fmt_organize_unchanged_is_byte_identical { # @test
  local doc="$BATS_TEST_TMPDIR/triage.txt" before="$BATS_TEST_TMPDIR/before.txt"
  generate_status_doc "$doc"
  cp "$doc" "$before"

  run_cg fmt-organize "$doc"
  assert_success
  assert_output "fmt-organize: $doc unchanged"

  run cmp "$before" "$doc"
  assert_success
}

# G4 refuse: a hand-edited body (lit1's line moved under `## =needs-action`)
# no longer matches the pinned base, so fmt refuses with exit 64 (EX_USAGE —
# the caller's document, not trouble) and the file is NOT modified. The
# server is stopped BEFORE the fmt call: the clean-body gate fires before any
# network touch, so the refusal works offline (the debug recipe's fmt-refuse
# leg is the host-run twin).
function fmt_organize_refuses_unapplied_edits { # @test
  local doc="$BATS_TEST_TMPDIR/triage.txt" edited="$BATS_TEST_TMPDIR/edited.txt"
  local before="$BATS_TEST_TMPDIR/before.txt"
  generate_status_doc "$doc"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43111/dav/lit/`
	- _base = @blake2b256-ds5v08vsq056f25466mt43k9d82t3yr9980n3ejd6dnhkunh6yss3japhu
	- _anchor = caldav:http://127.0.0.1:43111/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit2.ics location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	- [lit1.ics "_ inbox"] Triage inbox

	## =in-process

	## =completed

	## =cancelled
	EOM
  cp "$edited" "$before"

  # Offline: the refusal must not need the live anchor (teardown's second
  # stop_caldav_server is a guarded no-op).
  stop_caldav_server

  run_cg fmt-organize "$edited"
  assert_failure 64
  assert_output "cutting-garden: fmt-organize: $edited: doc has unapplied edits; apply or discard first (the body no longer matches its pinned \`- _base\` — write the edits with \`cg organize -apply $edited\`, or regenerate to discard them)"

  run cmp "$before" "$edited"
  assert_success
}

# Lever round-trip: a document generated under `[organize] tag_atoms =
# "trailing"` carries `- _tag-atoms = trailing` (G3's non-default echo). The
# config file is then REMOVED before fmt — the document is authoritative — and
# the refreshed file KEEPS the lever field and the trailing rendering (lit2's
# `chore` tag after its location atom).
function fmt_organize_keeps_trailing_lever { # @test
  local doc="$BATS_TEST_TMPDIR/triage.txt"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[organize]
	tag_atoms = "trailing"
	EOF
  put_ics "${CALDAV_SOURCE#caldav:}lit/lit2.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit2
	SUMMARY:Read book
	LOCATION:Bank
	CATEGORIES:chore
	END:VTODO
	END:VCALENDAR
	EOF

  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43111/dav/lit/`
	- _base = @blake2b256-qqmuy48yta4mxp3q4362u9cefcdjafrwcax87q9n376dv8fpv0nqjz9hsh
	- _anchor = caldav:http://127.0.0.1:43111/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _tag-atoms = trailing
	! organize-base-v1
	---

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit2.ics location=Bank chore] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
  printf '%s\n' "$output" >"$doc"

  rm -f "$XDG_CONFIG_HOME/cutting-garden/config.toml"
  put_ics "${CALDAV_SOURCE#caldav:}lit/lit2.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit2
	SUMMARY:Read many books
	LOCATION:Bank
	CATEGORIES:chore
	END:VTODO
	END:VCALENDAR
	EOF

  run_cg fmt-organize "$doc"
  assert_success
  assert_output "fmt-organize: $doc rewritten — _base @blake2b256-qqmuy48yta4mxp3q4362u9cefcdjafrwcax87q9n376dv8fpv0nqjz9hsh → @blake2b256-wsxykeay2ura3f5re6zme58hg3953ptkccaqp6xng7v9fcgf37uqfnsevr"

  run cat "$doc"
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43111/dav/lit/`
	- _base = @blake2b256-wsxykeay2ura3f5re6zme58hg3953ptkccaqp6xng7v9fcgf37uqfnsevr
	- _anchor = caldav:http://127.0.0.1:43111/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _tag-atoms = trailing
	! organize-base-v1
	---

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit2.ics location=Bank chore] Read many books
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# G4/G10: fmt never emits an empty (reset) heading — the refreshed file's
# whole-document asserts above already prove it, and this refute pins the rule
# by name on a refreshed document (generate can't emit one either;
# TestGenerateNeverEmitsResetHeading pins that half).
function fmt_organize_never_emits_reset_heading { # @test
  local doc="$BATS_TEST_TMPDIR/triage.txt"
  generate_status_doc "$doc"
  drift_lit2_summary

  run_cg fmt-organize "$doc"
  assert_success

  run cat "$doc"
  refute_line --regexp '^#+[[:space:]]*$'
}
