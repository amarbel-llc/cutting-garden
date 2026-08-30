#! /usr/bin/env bats

# The organize heading-depth lane (native tags slice 1 task 5; design G10):
# heading depth is STRUCTURE-ONLY and an EMPTY heading is a context RESET.
#
#   - Depth normalization: the shallowest `#` level present in a body is the
#     root and deeper levels nest relative to it, so a hand-written document
#     starting at `##` applies identically to the `#` form. Generation emits
#     MINIMAL depth: a `(tags)` grouping has no dimension heading, so its buckets
#     sit at `# <tag>` (a field grouping keeps `# dim=` / `## =value`).
#   - Resets: an empty heading at depth N pops the heading context at N and
#     deeper — the object lines that follow land under the depth N−1 heading,
#     exactly as if written beneath it — and a bare `#` returns to the ungrouped
#     context. A reset deeper than the current context is a no-op. Apply reads
#     placement THROUGH the resets, so each is a real membership change,
#     curl-verified on the live objects. Generation never emits a reset.
#
# The fixture calendar (/dav/fields/, opt-in via CG_TEST_CALDAV_FIELDS) holds
# field2 "Read book" CATEGORIES work,errand, field3 "Water plants" CATEGORIES
# work, and the untagged field1 "Pay rent" / field4 "Someday idea".
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server 43109
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# The generated document's `_base` digest, and the digests after each edit.
BASE_GENERATED=blake2b256-3cqzc5cknaq8v5t9xuc0jp07egvllras05avln3tavafg9dtwm2qwznkhy
BASE_AFTER_DOUBLE=blake2b256-6yqhaezupdrhmgedkxej8z0jenfzwv2l59xs0f5upk7l5gckgumqv72lca
BASE_AFTER_RESET=blake2b256-krd9lnkm0a3dhq9hn9sk5ptn9lypst383u7luwwkf9n9sw555zjs4c7gce
BASE_AFTER_NOOP=blake2b256-jwexalh3hhel7nvzfcrxkzxsew9skq88cdqsg7apfzwr9nzzvmtstzwv8v

# envelope_header prints the `-group-by (tags)` document's hyphence envelope
# pinned at `_base` $1 — the part every document in this lane shares.
envelope_header() {
  cat <<-EOM
	---
	% generated: \`cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43109/dav/fields/\`
	- _base = @$1
	- _anchor = caldav:http://127.0.0.1:43109/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---
	EOM
}

# headings_doc prints a whole document: the envelope at `_base` $1, then the
# body read from stdin.
headings_doc() {
  envelope_header "$1"
  cat
}

# generate_grouped runs `organize -group-by (tags)` and asserts the document in
# full: NO dimension heading, the buckets at MINIMAL depth (`# errand`,
# `# work` — never `##`), the untagged field1/field4 ungrouped, the two-tag
# field2 under both buckets.
generate_grouped() {
  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    headings_doc "$BASE_GENERATED" <<-'EOM'

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field4.ics] Someday idea

	# errand

	- [field2.ics priority=5] Read book

	# work

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants
	EOM
  )"
}

# assert_categories curl-reads the live object $1 and asserts its CATEGORIES is
# exactly $2 (or absent when $2 is empty).
assert_categories() {
  local ics="$1" want="$2"
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/$ics"
  assert_success
  if [[ -z $want ]]; then
    refute_output --partial 'CATEGORIES'
  else
    assert_output --partial "CATEGORIES:$want"
  fi
}

# G10 depth normalization (generator side): a `(tags)` document puts its buckets
# at `#` with no dimension heading above them.
function organize_headings_tags_buckets_at_minimal_depth { # @test
  generate_grouped
  refute_output --regexp '^##'
  refute_output --partial 'categories='
}

# G10 depth normalization (parser side): the SAME move as organize_tags.bats
# (field3 from work to errand), hand-written with every heading one level deeper
# (`##`), applies identically — the same one-change summary, the same live
# CATEGORIES write, and the re-rendered document is byte-identical to the one the
# `#` form produces.
function organize_headings_double_hash_document_applies_identically { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  headings_doc "$BASE_GENERATED" >"$edited" <<-'EOM'

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field4.ics] Someday idea

	## errand

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants

	## work

	- [field2.ics priority=5] Read book
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field3.ics  categories=[-work-]{+errand+}]

organize: wrote 1 change(s)
EOF

  assert_categories field3.ics errand
  assert_categories field2.ics 'work,errand'

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    headings_doc "$BASE_AFTER_DOUBLE" <<-'EOM'

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field4.ics] Someday idea

	# errand

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants

	# work

	- [field2.ics priority=5] Read book
	EOM
  )"
}

# G10 resets — the design's example as a real edit against the fixture:
#
#   # work
#   - field3      ← under work (unchanged: it already carries work)
#   ## errand
#   - field4      ← under errand, the deepest heading: gains errand
#   ##
#   - field1      ← the `##` pops errand: under work ONLY — gains work
#   #
#   - field2      ← the `#` returns to ungrouped: loses work AND errand
#
# Each placement is a distinct membership change (three in all), so the apply
# summary and the live CATEGORIES prove that a line under `##` landed under the
# parent heading (not errand, not ungrouped) and a line under `#` landed
# ungrouped. The re-rendered document — generated, so at minimal depth with no
# resets — shows the resolved placement.
function organize_headings_reset_pops_to_parent_and_ungrouped { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  headings_doc "$BASE_GENERATED" >"$edited" <<-'EOM'

	# work

	- [field3.ics priority=9] Water plants

	## errand

	- [field4.ics] Someday idea

	##

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent

	#

	- [field2.ics priority=5] Read book
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 3 change(s):

  - [field1.ics  categories={+work+}]
  - [field2.ics  categories=[-errand,work-]]
  - [field4.ics  categories={+errand+}]

organize: wrote 3 change(s)
EOF

  assert_categories field1.ics work
  assert_categories field2.ics ''
  assert_categories field3.ics work
  assert_categories field4.ics errand

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    headings_doc "$BASE_AFTER_RESET" <<-'EOM'

	- [field2.ics priority=5] Read book

	# errand

	- [field4.ics] Someday idea

	# work

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field3.ics priority=9] Water plants
	EOM
  )"
}

# G10 resets: a reset DEEPER than the current context is a no-op. Under `# work`
# (context depth 1) a `##` pops nothing, so field4 written after it lands under
# work — one change, and the live object gains work.
function organize_headings_reset_deeper_than_current_is_noop { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  headings_doc "$BASE_GENERATED" >"$edited" <<-'EOM'

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent

	# errand

	- [field2.ics priority=5] Read book

	# work

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants

	##

	- [field4.ics] Someday idea
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [field4.ics  categories={+work+}]

organize: wrote 1 change(s)
EOF

  assert_categories field4.ics work

  run_cg organize -group-by '(tags)' "$CAL"
  assert_success
  assert_output "$(
    headings_doc "$BASE_AFTER_NOOP" <<-'EOM'

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent

	# errand

	- [field2.ics priority=5] Read book

	# work

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants
	- [field4.ics] Someday idea
	EOM
  )"
}

# G10: generation never emits a reset — no heading line in a generated document
# is empty (resets are a hand-edit affordance only).
function organize_headings_generate_never_emits_reset { # @test
  generate_grouped
  refute_output --regexp '^#+[[:space:]]*$'
}
