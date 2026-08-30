#! /usr/bin/env bats

# The organize box-literal lane (native tags slice 1 task 3; design G9 / G13):
# trellis owns the espalier box interior. A box's interior is a trellis Group
# projected to its GROUND subset — id, `!type`, bare/quoted tags, `=`-only
# `name=value` atoms — and every slot is spelled by ONE quoting rule (a value with
# whitespace or a reserved rune is a trellis String, `"_ inbox"`), which the
# bucket headings share. Pinned here:
#
#   - G13: a HAND-EDITED bare token in a box (`work-x`) parses as a tag — even
#     one spelled like a field name — and apply REFUSES it loudly (exit 64,
#     "not writable yet") rather than silently dropping it; nothing is written.
#   - G9: a non-ground interior (`status*=y`) is a loud bad request naming the
#     offending term.
#   - G9: a quoted tag round-trips in a box (parsed, re-spelled in the refusal)
#     and in a heading: the `# "_ inbox"` bucket a whitespace-bearing CATEGORIES
#     value renders as, moved INTO by a bucket move, re-rendered quoted.
#
# The fixture calendar (/dav/lit/, opt-in via CG_TEST_CALDAV_LIT) holds lit1
# "Triage inbox" CATEGORIES `_ inbox` and lit2 "Read book" LOCATION Bank.
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

# The generated document's `_base` digest, and the digest after lit2 has moved
# into the `"_ inbox"` bucket.
BASE_GENERATED=blake2b256-uhrs68g8fye4lj3pfwt90rj54qfnra2j03nxufxjj5gz5aqdv8vs7tkhaw
BASE_MOVED=blake2b256-5n9sc827ktjjarvnz7gvnqzstp2pdsdv5t8ywh492306dm7s0m3sv9kgkp

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
# in full: the untagged lit2 ungrouped, and lit1 under the QUOTED `# "_ inbox"`
# bucket — its CATEGORIES value carries whitespace, so the heading spells it as a
# trellis String.
generate_grouped() {
  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    lit_doc "$BASE_GENERATED" <<-'EOM'

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics] Triage inbox
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
	EOM
}

# assert_lit2_untouched proves a refused apply wrote NOTHING: lit2 still carries
# no CATEGORIES and its LOCATION is unchanged, and the re-rendered document is
# byte-identical to the generated one (same `_base`).
assert_lit2_untouched() {
  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit2.ics"
  assert_success
  refute_output --partial 'CATEGORIES'
  assert_output --partial 'LOCATION:Bank'
  generate_grouped
}

# G13 / G9: a bare token after the id is a TAG (bare is always a tag, even a
# token that names a field like `status`), it survives the parse — the refusal
# re-spells it verbatim — and apply refuses the line as a bad request (64) rather
# than silently dropping the token. Nothing is written.
function organize_literal_bare_token_is_tag_apply_refuses { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_lit2_edited '- [lit2.ics work-x status location=Bank] Read book' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_output --partial 'organize --apply: object lit2.ics carries tag atoms work-x status: tag atoms are not writable yet (native tags slice 2)'

  assert_lit2_untouched
}

# G9: a QUOTED tag token in a box parses (the doddish String decodes to `_ inbox`)
# and is re-spelled quoted by the same rule — the refusal names it as `"_ inbox"`.
function organize_literal_quoted_box_token_parses { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  write_lit2_edited '- [lit2.ics "_ inbox" location=Bank] Read book' "$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_output --partial 'organize --apply: object lit2.ics carries tag atoms "_ inbox": tag atoms are not writable yet (native tags slice 2)'

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
  assert_output --partial 'box literal "lit2.ics status*=y location=Bank": term `status*=y` is not ground: only `=` is ground (`*=` is a query operator)'

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
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit2.ics  categories={+_ inbox+}]

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit2.ics"
  assert_success
  assert_output --partial 'CATEGORIES:_ inbox'

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    lit_doc "$BASE_MOVED" <<-'EOM'

	# "_ inbox"

	- [lit1.ics] Triage inbox
	- [lit2.ics location=Bank] Read book
	EOM
  )"
}
