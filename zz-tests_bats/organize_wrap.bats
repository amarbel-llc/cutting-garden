#! /usr/bin/env bats

# The organize wrapped-box lane (cutting-garden#261, RFC 0015 §Object lines): a
# box may span several physical lines. While its `[` group is unbalanced every
# following line continues the INTERIOR; after the closing `]` the DESCRIPTION
# span continues on a line starting with neither `-` nor `#`, or on a `\-` /
# `\#` escaped line (backslash stripped, single-space joined). A line-leading
# `-` always opens a new box; a blank line or heading closes the span; an
# escape with no open span is a loud bad request. Pinned here against the lit
# fixture calendar (see organize_literal.bats):
#
#   - a hand-WRAPPED edited document (interior wrap + `\-` / `\#` description
#     continuations) previews and commits exactly like its single-line twin,
#     and the re-rendered document reads back single-line;
#   - a `\-` line after a blank line is refused naming the line, nothing written.
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_LIT=1
  start_caldav_server 43115
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/lit/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# The generated document's `_base` digest, and the digest after the wrapped
# edit's commit.
BASE_GENERATED=blake2b256-jxrdjyuwmku8m25ak4cdc37jtlddkusxupplfvj0n8zjgvjyxzyqpfmsvg
BASE_WRAPPED_EDITED=blake2b256-94v6p5cl9cqwczcusefw7mnuy554ettsu25yemnn480h6usufsystkfn4f

# wrap_doc prints a whole `-group-by (tags)` document pinned at `_base` $1, the
# body read from stdin.
wrap_doc() {
  cat <<-EOM
	---
	% generated: \`cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43115/dav/lit/\`
	- _base = @$1
	- _anchor = caldav:http://127.0.0.1:43115/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---
	EOM
  cat
}

generate_grouped() {
  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    wrap_doc "$BASE_GENERATED" <<-'EOM'

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do
	EOM
  )"
}

# write_unwrapped_edit writes the single-line edited document to $1: lit2 gains
# the tag `work-x`, lit3's trailer becomes `Plan, then do - now #1`.
write_unwrapped_edit() {
  wrap_doc "$BASE_GENERATED" >"$1" <<-'EOM'

	- [lit2.ics work-x location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do - now #1
	EOM
}

# write_wrapped_edit writes the SAME edit hand-wrapped to $1: lit2's interior
# spans three lines, lit3's description continues on a `\-` and a `\#` line.
write_wrapped_edit() {
  wrap_doc "$BASE_GENERATED" >"$1" <<-'EOM'

	- [lit2.ics
	    work-x
	    location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do
	  \- now
	  \#1
	EOM
}

# assert_nothing_written proves a refused apply wrote NOTHING: lit2 is
# byte-identical to the seeded fixture and the document regenerates unchanged.
assert_nothing_written() {
  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit2.ics"
  assert_success
  assert_output - <<-'EOM'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit2
	SUMMARY:Read book
	LOCATION:Bank
	END:VTODO
	END:VCALENDAR
	EOM
  generate_grouped
}

# The wrapped document previews exactly like its single-line twin (the same
# change set, rendered identically), then commits and reads back single-line.
function organize_wrap_applies_like_unwrapped { # @test
  generate_grouped
  local unwrapped="$BATS_TEST_TMPDIR/unwrapped.txt" wrapped="$BATS_TEST_TMPDIR/wrapped.txt"
  write_unwrapped_edit "$unwrapped"
  write_wrapped_edit "$wrapped"

  run_cg organize -apply "$unwrapped"
  assert_success
  assert_output - <<'EOF'
organize: 2 change(s):

  - [lit2.ics {+work-x+} location=Bank] Read book
  - [lit3.ics] Plan, then do {+-+} {+now+} {+#1+}

organize: dry-run — nothing written
EOF

  # The wrapped twin previews byte-identically to the dry-run above.
  run_cg organize -apply "$wrapped"
  assert_success
  assert_output - <<'EOF'
organize: 2 change(s):

  - [lit2.ics {+work-x+} location=Bank] Read book
  - [lit3.ics] Plan, then do {+-+} {+now+} {+#1+}

organize: dry-run — nothing written
EOF

  run_cg organize -apply "$wrapped" -commit
  assert_success
  assert_output - <<'EOF'
organize: 2 change(s):

  - [lit2.ics {+work-x+} location=Bank] Read book
  - [lit3.ics] Plan, then do {+-+} {+now+} {+#1+}

organize: wrote 2 change(s)
EOF

  assert_categories "${CALDAV_SOURCE#caldav:}lit/lit2.ics" 'work-x'

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    wrap_doc "$BASE_WRAPPED_EDITED" <<-'EOM'

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do - now #1

	# work-x

	- [lit2.ics location=Bank] Read book
	EOM
  )"
}

# A `\-` continuation after a blank line has no open description span: a loud
# bad request naming the physical body line, and nothing is written.
function organize_wrap_escape_outside_span_rejects { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  wrap_doc "$BASE_GENERATED" >"$edited" <<-'EOM'

	- [lit2.ics work-x location=Bank] Read book

	\- orphan

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do
	EOM

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  # shellcheck disable=SC2016  # the backticks are the message's own quoting, not expansion
  assert_output 'cutting-garden: organize: body line 3: "\\- orphan" continues a description, but no box description is open (a `\-` / `\#` continuation must follow a box line, not a blank line, a heading, or the start of the body)'

  assert_nothing_written
}
