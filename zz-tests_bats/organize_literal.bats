#! /usr/bin/env bats

# The organize box-literal lane (native tags slice 1 task 3; design G9 / G13):
# trellis owns the espalier box interior. A box's interior is a trellis Group
# projected to its GROUND subset — id, `!type`, bare/quoted tags, `=`-only
# `name=value` atoms — and every slot is spelled by ONE quoting rule (a value with
# whitespace or a reserved rune is a trellis String, `"_ inbox"`), which the
# bucket headings share. Pinned here:
#
#   - G13: a HAND-EDITED bare token in a box (`work-x`) parses as a tag — even
#     one spelled like a field name — and apply reads it as a MEMBERSHIP add
#     (design G7, slice 2 T3), previewed as a categories change (dry-run here,
#     so nothing is written) rather than silently dropped.
#   - G9: a non-ground interior (`status*=y`) is a loud bad request naming the
#     offending term.
#   - G9: a quoted tag round-trips in a box (parsed, re-spelled in the refusal)
#     and in a heading: the `# "_ inbox"` bucket a whitespace-bearing CATEGORIES
#     value renders as, moved INTO by a bucket move, re-rendered quoted.
#
# The fixture calendar (/dav/lit/, opt-in via CG_TEST_CALDAV_LIT) holds lit1
# "Triage inbox" CATEGORIES `_ inbox`, lit2 "Read book" LOCATION Bank, and lit3
# — the RFC 5545 §3.3.11 TEXT-escaping vector (native tags slice 1.5 F), seeded
# with the actual wire escaping: `SUMMARY:Plan\, then do` and
# `CATEGORIES:planning\, misc` (ONE category containing a literal comma). The
# ical layer unescapes on parse — the escaping is wire-format only, commas never
# require escaping in trailers — so the document shows the trailer
# `Plan, then do` and the ONE quoted tag heading `# "planning, misc"` (the tag
# layer's reserved-rune quoting, same rule as `"_ inbox"`); a write-back
# re-escapes on the wire.
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
  start_caldav_server 43107
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/lit/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# The generated document's `_base` digest, the digest after lit2 has moved
# into the `"_ inbox"` bucket, and the digest after lit3's summary trailer edit.
BASE_GENERATED=blake2b256-7frh9cjnxar4pzj4xpy324stupzgj55snaxmja3p5vmckheg7g8sftyzfe
BASE_MOVED=blake2b256-unlx9rq9jr5kl6hyjd0f3mzcx9cz33tswfrnah0veegnpnt79lgsllzz7r
BASE_SUMMARY_EDITED=blake2b256-mjsll3smgpepl5ma2aysf0k5zvkqlfndyjsepfv2qgzz89lt5kwsxcxd3r

# envelope_header prints the `-group-by (tags)` document's hyphence envelope
# pinned at `_base` $1 — the part every document in this lane shares.
envelope_header() {
  cat <<-EOM
	---
	% generated: \`cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43107/dav/lit/\`
	- _base = @$1
	- _anchor = caldav:http://127.0.0.1:43107/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---
	EOM
}

# lit_doc prints a whole document: the envelope at `_base` $1, then the body
# read from stdin.
lit_doc() {
  envelope_header "$1"
  cat
}

# generate_grouped runs `organize -group-by (tags)` and asserts the document
# in full: the untagged lit2 ungrouped, lit1 under the QUOTED `# "_ inbox"`
# bucket — its CATEGORIES value carries whitespace, so the heading spells it as
# a trellis String — and lit3 under the QUOTED `# "planning, misc"` bucket: its
# wire-escaped `CATEGORIES:planning\, misc` is ONE comma-bearing tag (never
# two), and its trailer shows `Plan, then do` with NO backslash (slice 1.5 F:
# TEXT escaping is invisible above the ical layer).
generate_grouped() {
  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    lit_doc "$BASE_GENERATED" <<-'EOM'

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do
	EOM
  )"
}

# write_lit2_edited writes the generated document to $2 with lit2's ungrouped box
# line replaced by $1 — a hand edit of the box interior against the generated
# `_base`, never a move.
write_lit2_edited() {
  local box="$1" out="$2"
  lit_doc "$BASE_GENERATED" >"$out" <<-EOM

	$box

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do
	EOM
}

# assert_lit2_untouched proves a refused apply wrote NOTHING: the live object
# is byte-identical to the seeded fixture (a write would have rewritten it with
# PRODID/DTSTAMP — and above all, no CATEGORIES ever appeared), and the
# re-rendered document is byte-identical to the generated one (same `_base`).
assert_lit2_untouched() {
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

# G13 / G9: a bare token after the id is a TAG (bare is always a tag, even a
# token that names a field like `status`) — it survives the parse and apply
# reads it as a MEMBERSHIP add (design G7), previewed as a categories change.
# The apply runs headless without -commit, so it is a dry-run: the preview
# proves the parse and nothing is written.
function organize_literal_bare_token_is_tag { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_lit2_edited '- [lit2.ics work-x status location=Bank] Read book' "$edited"

  run_cg organize -apply "$edited"
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit2.ics  categories={+status,work-x+}]  Read book

organize: dry-run — nothing written
EOF

  assert_lit2_untouched
}

# G9: a QUOTED tag token in a box parses (the doddish String decodes to
# `_ inbox`) and is re-spelled quoted by the same rule — the membership
# preview names it as `"_ inbox"` (#248). Dry-run: nothing is written.
function organize_literal_quoted_box_token_parses { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_lit2_edited '- [lit2.ics "_ inbox" location=Bank] Read book' "$edited"

  run_cg organize -apply "$edited"
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit2.ics  categories={+"_ inbox"+}]  Read book

organize: dry-run — nothing written
EOF

  assert_lit2_untouched
}

# G9: a non-ground interior — a field addressed with a query operator (`status*=y`)
# — is a loud bad request NAMING the offending term, never a silent one-token
# mis-parse. Nothing is written.
function organize_literal_non_ground_interior_rejects { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_lit2_edited '- [lit2.ics status*=y location=Bank] Read book' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  # shellcheck disable=SC2016  # the backticks are the message's own quoting, not expansion
  assert_output 'cutting-garden: organize: body line 1: box literal "lit2.ics status*=y location=Bank": term `status*=y` is not ground: only `=` is ground (`*=` is a query operator)'

  assert_lit2_untouched
}

# G9: the quoted heading round-trips. Move lit2 under `# "_ inbox"` and commit:
# the parser decodes the quoted bucket value to `_ inbox`, the membership write
# stores exactly that CATEGORIES value, and the re-rendered document spells the
# bucket quoted again with both tasks beneath it.
function organize_literal_quoted_tag_heading_round_trips { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  lit_doc "$BASE_GENERATED" >"$edited" <<-'EOM'

	# "_ inbox"

	- [lit1.ics] Triage inbox
	- [lit2.ics location=Bank] Read book

	# "planning, misc"

	- [lit3.ics] Plan, then do
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit2.ics  categories={+"_ inbox"+}]  Read book

organize: wrote 1 change(s)
EOF

  assert_categories "${CALDAV_SOURCE#caldav:}lit/lit2.ics" '_ inbox'

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    lit_doc "$BASE_MOVED" <<-'EOM'

	# "_ inbox"

	- [lit1.ics] Triage inbox
	- [lit2.ics location=Bank] Read book

	# "planning, misc"

	- [lit3.ics] Plan, then do
	EOM
  )"
}

# Slice 1.5 F (the user-reported live-UAT bug): RFC 5545 TEXT escaping is
# wire-format only. lit3 is STORED as `SUMMARY:Plan\, then do` +
# `CATEGORIES:planning\, misc`; generate_grouped (run first) already proved the
# document shows the unescaped trailer `Plan, then do` and ONE quoted
# comma-bearing tag heading `# "planning, misc"`. Here the trailer edit
# (append " now") round-trips back THROUGH the escaping: the stored wire form
# is the exact escaped line `SUMMARY:Plan\, then do now`, the CATEGORIES line
# keeps its escape, and the re-rendered document reads unescaped again.
function organize_literal_text_escaping_round_trips { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  lit_doc "$BASE_GENERATED" >"$edited" <<-'EOM'

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do now
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit3.ics]  Plan, then do {+now+}

organize: wrote 1 change(s)
EOF

  # The ESCAPED wire form, exact lines (the rewritten body is CRLF-serialized
  # with a volatile DTSTAMP, so the whole body cannot be pinned).
  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit3.ics"
  assert_success
  assert_line --regexp $'^SUMMARY:Plan\\\\, then do now\r?$'
  assert_line --regexp $'^CATEGORIES:planning\\\\, misc\r?$'

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    lit_doc "$BASE_SUMMARY_EDITED" <<-'EOM'

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do now
	EOM
  )"
}
