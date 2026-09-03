#! /usr/bin/env bats

# The organize key-free tag-atom lane (native tags slice 2; design G1/G2/G3):
# an object's tag set renders as bare espalier terms inside its box, governed
# by two DATA-plane envelope levers with `[organize]` config defaults —
#
#   - `_tag-atoms = leading | trailing | none` (default leading): where the tag
#     terms sit relative to the `name=value` atoms (position is
#     presentation-only; the parser collects tags anywhere);
#   - `_tag-strip = placement | none` (default placement): whether a TAG-grouped
#     document strips from each appearance's box exactly the Via tag(s) that
#     produced that placement (#229: placement carries it, never also an atom).
#
# Both levers are OMITTED at their defaults (existing documents byte-identical)
# and content-addressed into `_base` when present; the document's explicit
# field wins over config (G3). Since slice 2 Task 3 box tag EDITS are
# membership writes (design G7): an added/removed atom folds through the
# interpreter's EXACT Complete into the same full-set CATEGORIES write a
# heading move takes; appearances must agree on the non-placement tags
# (disagreement conflicts, exit 2), a tag removed from a box while its bucket
# placement stands is a placement-vs-box conflict (exit 2), and under
# `_tag-strip = none` the box is authoritative — membership = box tags ∪ the
# current placements' bucket tags, so a moved line's old tag stays until the
# box is edited.
#
# Fixtures: the /dav/lit/ + /dav/ns/ calendars (opt-in env gates), augmented
# PER TEST via curl PUT against the in-memory testserver (a second tag on lit1,
# a `chore` tag on lit2, the bare `project` on nsD) so the seeded fixtures —
# and every other lane's vectors — stay untouched.
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_LIT=1 CG_TEST_CALDAV_NS=1
  # Pin the config dir under the sandboxed $HOME so os.UserConfigDir resolves
  # the lane's own config.toml (and an ambient host value can't leak in). Each
  # test writes the config it needs; the default is NO config file.
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  rm -f "$XDG_CONFIG_HOME/cutting-garden/config.toml"
  start_caldav_server 43110
  init_store
  LIT="${CALDAV_SOURCE%/dav/}/dav/lit/"
  NS="${CALDAV_SOURCE%/dav/}/dav/ns/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# put_ics URL replaces the live object at URL with the iCalendar body on stdin
# — the lane's per-test fixture augmentation (writes only the in-memory
# server).
put_ics() {
  curl -fsS -X PUT --data-binary @- "$1" || fail "PUT $1 failed"
}

# generate_lit_status runs the default-config status-grouped generate over
# /dav/lit/ and asserts the G1 LEADING default in full: each tag set renders
# after the id, BEFORE the `name=value` atoms, through the one quoting rule
# (`"_ inbox"`, `"planning, misc"`), and — G3 — NEITHER lever field appears in
# the envelope (defaults are omitted; the document is byte-identical to what a
# pre-slice-2 build emitted, plus the tag atoms). A FIELD grouping strips
# nothing, so every tag shows.
generate_lit_status() {
  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-8hc5rhypmupd73xp0sw7jxavvhf9f406auk3rnujyptsgk2kag4sp27vk0
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
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
}

# G1: the leading default (and G3's omitted-at-default envelope).
function organize_tagatoms_leading_default { # @test
  generate_lit_status
}

# A box whose tag set is UNCHANGED from the base rides along untouched
# (planTagAtomDeltas yields no delta): moving lit1 (its `"_ inbox"` tag in the
# box) into `## =needs-action` writes the STATUS only, CATEGORIES
# curl-verified, and the re-render shows the tag still in the box (field
# grouping: no strip).
function organize_tagatoms_unchanged_tags_pass_through { # @test
  generate_lit_status
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-8hc5rhypmupd73xp0sw7jxavvhf9f406auk3rnujyptsgk2kag4sp27vk0
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
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

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit1.ics  status={+needs-action+}]  Triage inbox

organize: wrote 1 change(s)
EOF

  # The STATUS landed and the CATEGORIES survived, as exact full lines (the
  # rewritten body is CRLF-serialized with a volatile DTSTAMP).
  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit1.ics"
  assert_success
  assert_line --regexp $'^STATUS:NEEDS-ACTION\r?$'
  assert_line --regexp $'^CATEGORIES:_ inbox\r?$'

  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-keglll539yhq9unxd6tagtjaucvrq2xhka7mj4jw9sprsmddr8lqt7naup
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
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
}

# G7 add: `urgent` typed into lit2's (untagged) box under a status-grouped
# document is a MEMBERSHIP add on the tag dimension — the diff shows a
# categories membership line (with the #247 trailer), the live CATEGORIES
# gains exactly the typed tag (LOCATION untouched), and the re-render shows
# the atom in the box.
function organize_tagatoms_add_writes_membership { # @test
  generate_lit_status
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-8hc5rhypmupd73xp0sw7jxavvhf9f406auk3rnujyptsgk2kag4sp27vk0
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit2.ics urgent location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit2.ics  categories={+urgent+}]  Read book

organize: wrote 1 change(s)
EOF

  # The membership write added exactly the typed tag; LOCATION survived.
  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit2.ics"
  assert_success
  assert_line --regexp $'^CATEGORIES:urgent\r?$'
  assert_line --regexp $'^LOCATION:Bank\r?$'

  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-l8mjkejmjnachg32hqa72g8z4867a420950sra4uxrpfczt4qdeq5apgma
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit2.ics urgent location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# G7 remove: lit1's rendered `"_ inbox"` deleted from its box is a MEMBERSHIP
# remove — the diff line spells the quoted tag through the one quoting rule
# (#248), the live object loses its CATEGORIES line entirely, and the
# re-render shows a bare box.
function organize_tagatoms_remove_writes_membership { # @test
  generate_lit_status
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-8hc5rhypmupd73xp0sw7jxavvhf9f406auk3rnujyptsgk2kag4sp27vk0
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit1.ics] Triage inbox
	- [lit2.ics location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit1.ics  categories=[-"_ inbox"-]]  Triage inbox

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit1.ics"
  assert_success
  refute_output --partial 'CATEGORIES'

  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-f8ggv86gmwv07y08f764s6fjfvz8avah7ymzqw6xtk0rgnzyrh3q8nrcjr
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [lit1.ics] Triage inbox
	- [lit2.ics location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# trailing_config writes the `[organize] tag_atoms = trailing` config and seeds
# lit2 with a `chore` tag so a box carries BOTH a `name=value` atom and a tag —
# the only shape where the position is observable.
trailing_config_and_chore() {
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
}

# generate_lit_trailing asserts the G1 TRAILING render + the G3 config-default
# echo: the `[organize] tag_atoms = "trailing"` default is non-default, so the
# generated envelope carries `- _tag-atoms = trailing` (content-addressed into
# `_base`), and lit2's tag renders AFTER its location atom.
generate_lit_trailing() {
  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-mvdp7h560r84657w9mfld8vuwfk28x8up4mvxp0pkwhdekurp23q90l3vu
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
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
}

# G1 trailing + G3 config default: the envelope field appears and the tag
# follows the atom.
function organize_tagatoms_trailing_config { # @test
  trailing_config_and_chore
  generate_lit_trailing
}

# G3 doc-wins: against the SAME trailing config, hand-edit the document's
# `- _tag-atoms` to `leading` and respell lit2's box leading — apply reads the
# DOC's lever, and neither the repositioned nor the reordered tags read as an
# edit (a tag set is a SET; position is presentation) — so the real edit in the
# document (lit1 moved into `## =needs-action`) applies cleanly.
function organize_tagatoms_doc_wins { # @test
  trailing_config_and_chore
  generate_lit_trailing
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-mvdp7h560r84657w9mfld8vuwfk28x8up4mvxp0pkwhdekurp23q90l3vu
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _tag-atoms = leading
	! organize-base-v1
	---

	- [lit2.ics chore location=Bank] Read book
	- [lit3.ics "planning, misc"] Plan, then do

	# status=

	## =needs-action

	- [lit1.ics "_ inbox"] Triage inbox

	## =in-process

	## =completed

	## =cancelled
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit1.ics  status={+needs-action+}]  Triage inbox

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit1.ics"
  assert_success
  assert_line --regexp $'^STATUS:NEEDS-ACTION\r?$'
  assert_line --regexp $'^CATEGORIES:_ inbox\r?$'
}

# G1 none (via the G3 config default): no box carries a tag atom, and the
# non-default lever is echoed into the envelope so `_base` records how the
# document was rendered.
function organize_tagatoms_none_config { # @test
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[organize]
	tag_atoms = "none"
	EOF

  run_cg organize -group-by status= "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-xxs9z8jfqfjawe24ln5nhlrxdu8fayhxfrw2s4l35krkqqff5vqswucckt
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _tag-atoms = none
	! organize-base-v1
	---

	- [lit1.ics] Triage inbox
	- [lit2.ics location=Bank] Read book
	- [lit3.ics] Plan, then do

	# status=

	## =needs-action

	## =in-process

	## =completed

	## =cancelled
	EOM
}

# G2 placement strip under `(tags)`: lit1 seeded with TWO tags appears under
# both buckets, each appearance's box dropping exactly its own bucket's tag
# (Via == the bucket) and KEEPING the sibling — `urgent` under `# "_ inbox"`,
# `"_ inbox"` under `# urgent` — while the single-tag lit3 renders bare under
# its bucket.
function organize_tagatoms_strip_placement_keeps_sibling { # @test
  put_ics "${CALDAV_SOURCE#caldav:}lit/lit1.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit1
	SUMMARY:Triage inbox
	CATEGORIES:_ inbox,urgent
	END:VTODO
	END:VCALENDAR
	EOF

  run_cg organize -group-by '(tags)' "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-pprpzrjd28t3ldp04fgp4fy9pxsmfq9upt75rek0nh0058hg0qhs6v6csd
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics urgent] Triage inbox

	# "planning, misc"

	- [lit3.ics] Plan, then do

	# urgent

	- [lit1.ics "_ inbox"] Triage inbox
	EOM
}

# A whole-dimension BUCKET-TO-BUCKET move: against the two-tag strip document
# above, lit3 moves from `# "planning, misc"` to `# urgent` — the moved line's
# (empty) tag set is placement-explained on both sides, and the target
# bucket's lit1 keeps its `"_ inbox"` sibling atom untouched — so the
# membership write lands (CATEGORIES rewritten to urgent, curl-verified), the
# diff line quoting the comma-bearing tag through the one rule (#248) with the
# summary trailer (#247), and the re-render files lit3 under urgent.
function organize_tagatoms_whole_dim_move_applies { # @test
  put_ics "${CALDAV_SOURCE#caldav:}lit/lit1.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit1
	SUMMARY:Triage inbox
	CATEGORIES:_ inbox,urgent
	END:VTODO
	END:VCALENDAR
	EOF
  run_cg organize -group-by '(tags)' "$LIT"
  assert_success

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-pprpzrjd28t3ldp04fgp4fy9pxsmfq9upt75rek0nh0058hg0qhs6v6csd
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics urgent] Triage inbox

	# "planning, misc"

	# urgent

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit3.ics] Plan, then do
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [lit3.ics  categories=[-"planning, misc"-]{+urgent+}]  Plan, then do

organize: wrote 1 change(s)
EOF

  run curl -fsS "${CALDAV_SOURCE#caldav:}lit/lit3.ics"
  assert_success
  assert_line --regexp $'^CATEGORIES:urgent\r?$'

  run_cg organize -group-by '(tags)' "$LIT"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/lit/`
	- _base = @blake2b256-8dr2vzg9qwj9qv58y03are8r9q0xvcct3h8mej8vh5p6fv4qvt3sl46j9w
	- _anchor = caldav:http://127.0.0.1:43110/dav/lit/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [lit2.ics location=Bank] Read book

	# "_ inbox"

	- [lit1.ics urgent] Triage inbox

	# urgent

	- [lit1.ics "_ inbox"] Triage inbox
	- [lit3.ics] Plan, then do
	EOM
}

# seed_two_tag_lit1 gives lit1 the `_ inbox,urgent` pair, and
# generate_strip_doc writes the resulting `(tags)` strip document (the same
# document organize_tagatoms_strip_placement_keeps_sibling pins in full) to
# $1 — the shared fixture of the G7 conflict vectors.
seed_two_tag_lit1() {
  put_ics "${CALDAV_SOURCE#caldav:}lit/lit1.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:lit1
	SUMMARY:Triage inbox
	CATEGORIES:_ inbox,urgent
	END:VTODO
	END:VCALENDAR
	EOF
}

generate_strip_doc() {
  run_cg organize -group-by '(tags)' "$LIT"
  assert_success
  printf '%s\n' "$output" >"$1"
}

# G7 cross-appearance disagreement: lit1 sits under BOTH `# "_ inbox"` and
# `# urgent`; typing `foo` into ONE box only (the `"_ inbox"` appearance) is
# ambiguous — the appearances no longer agree on the non-placement tags — and
# apply refuses with exit 2 (trouble, not usage), the message naming the tag
# and BOTH appearances. Nothing is written (same `_base` on re-render).
function organize_tagatoms_cross_appearance_disagreement_conflicts { # @test
  seed_two_tag_lit1
  local doc="$BATS_TEST_TMPDIR/doc.txt" edited="$BATS_TEST_TMPDIR/edited.txt"
  generate_strip_doc "$doc"
  sed 's/^- \[lit1.ics urgent\] Triage inbox$/- [lit1.ics urgent foo] Triage inbox/' "$doc" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 2
  assert_output - <<'EOF'
cutting-garden: organize --apply: 1 tag conflict(s) — box tag atoms disagree with placement or across appearances; re-edit the document:
  object lit1.ics: appearances disagree on tag foo: present under "_ inbox", absent under urgent
EOF

  # Nothing landed: the re-render is byte-identical to the generated document.
  run_cg organize -group-by '(tags)' "$LIT"
  assert_success
  assert_output "$(cat "$doc")"
}

# G7 placement-vs-box conflict (rule 1): deleting the `urgent` atom from
# lit1's `# "_ inbox"` box while the line still sits under `# urgent` is
# "placement says X, box says not-X" — exit 2, and nothing is written.
function organize_tagatoms_placement_vs_box_conflicts { # @test
  seed_two_tag_lit1
  local doc="$BATS_TEST_TMPDIR/doc.txt" edited="$BATS_TEST_TMPDIR/edited.txt"
  generate_strip_doc "$doc"
  sed 's/^- \[lit1.ics urgent\] Triage inbox$/- [lit1.ics] Triage inbox/' "$doc" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure 2
  assert_output - <<'EOF'
cutting-garden: organize --apply: 1 tag conflict(s) — box tag atoms disagree with placement or across appearances; re-edit the document:
  object lit1.ics: placement says urgent (still under urgent), box says not-urgent (removed under "_ inbox")
EOF

  run_cg organize -group-by '(tags)' "$LIT"
  assert_success
  assert_output "$(cat "$doc")"
}

# G2's G10a root strip: nsD seeded with `other,project` files DIRECTLY under
# the `# project` root heading (the bare namespace tag IS the root membership),
# its box keeping the out-of-namespace `other` while the placement-realizing
# `project` strips — and the continuations strip their Via tags as before.
function organize_tagatoms_ns_root_strip { # @test
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "dodder-hyphen"
	EOF
  put_ics "${CALDAV_SOURCE#caldav:}ns/nsD.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:nsD
	SUMMARY:Loose idea
	CATEGORIES:other,project
	END:VTODO
	END:VCALENDAR
	EOF

  run_cg organize -group-by project "$NS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/ns/`
	- _base = @blake2b256-ypuy3mdaxzzvt7mru0td8409fsjg9r4lke92uz50tclrp4ev6xqqq03gu8
	- _anchor = caldav:http://127.0.0.1:43110/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	! organize-base-v1
	---

	# project

	- [nsD.ics other] Loose idea

	## -client

	- [nsA.ics] Acme retainer
	- [nsB.ics] Baxter audit

	## -cutting_garden

	- [nsC.ics] CG roadmap
	EOM
}

# G2 strips ALL contributors, not just the interpreter's representative Via:
# nsE carries TWO tags rolling to `-client` (project-client-acme AND
# project-client-baxter) plus the out-of-namespace `urgent` — its `## -client`
# box drops BOTH contributors (symmetric with the RFC 0019 §6.2 write-back's
# whole-subtree removal) and keeps only the sibling.
function organize_tagatoms_ns_strip_all_contributors { # @test
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "dodder-hyphen"
	EOF
  put_ics "${CALDAV_SOURCE#caldav:}ns/nsE.ics" <<-'EOF'
	BEGIN:VCALENDAR
	VERSION:2.0
	BEGIN:VTODO
	UID:nsE
	SUMMARY:Two clients
	CATEGORIES:project-client-acme,project-client-baxter,urgent
	END:VTODO
	END:VCALENDAR
	EOF

  run_cg organize -group-by project "$NS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/ns/`
	- _base = @blake2b256-4ym7tmy7k05a2al2p80nrjn4dleg2za723f8anlqmxe98np94r5s9s2ags
	- _anchor = caldav:http://127.0.0.1:43110/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	! organize-base-v1
	---

	- [nsD.ics other] Loose idea

	# project

	## -client

	- [nsA.ics] Acme retainer
	- [nsB.ics] Baxter audit
	- [nsE.ics urgent] Two clients

	## -cutting_garden

	- [nsC.ics] CG roadmap
	EOM
}

# G2 `_tag-strip = none` (via the G3 config default): every box keeps its FULL
# tag set — the placement (Via) tags stay — and the non-default lever is echoed
# into the envelope.
function organize_tagatoms_strip_none { # @test
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "dodder-hyphen"

	[organize]
	tag_strip = "none"
	EOF

  run_cg organize -group-by project "$NS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/ns/`
	- _base = @blake2b256-kxd2w9tpk6qfpaafplceqf4kv7ggevz3zhn6zvprsxrgaja05n7qf7z4t9
	- _anchor = caldav:http://127.0.0.1:43110/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	- _tag-strip = none
	! organize-base-v1
	---

	- [nsD.ics other] Loose idea

	# project

	## -client

	- [nsA.ics project-client-acme] Acme retainer
	- [nsB.ics project-client-baxter] Baxter audit

	## -cutting_garden

	- [nsC.ics project-cutting_garden] CG roadmap
	EOM
}

# G7 under `_tag-strip = none`: a MOVED line is NOT an edit of its box — the
# box is authoritative, and membership = box tags ∪ the current placements'
# bucket tags. Moving nsA from `## -client` to `## -cutting_garden` with its
# `project-client-acme` atom untouched ADDS the new bucket's reconstructed
# tag and KEEPS the old one (the pre-T3 gate falsely refused this move; the
# `-client` membership survives until the box atom is deleted). The re-render
# files nsA under BOTH buckets, full sets in every box.
function organize_tagatoms_strip_none_move_is_not_an_edit { # @test
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "dodder-hyphen"

	[organize]
	tag_strip = "none"
	EOF
  # Generate first so the pinned base blob (@…kxd2w9…, the strip_none vector)
  # lands in this test's fresh store.
  run_cg organize -group-by project "$NS"
  assert_success
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/ns/`
	- _base = @blake2b256-kxd2w9tpk6qfpaafplceqf4kv7ggevz3zhn6zvprsxrgaja05n7qf7z4t9
	- _anchor = caldav:http://127.0.0.1:43110/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	- _tag-strip = none
	! organize-base-v1
	---

	- [nsD.ics other] Loose idea

	# project

	## -client

	- [nsB.ics project-client-baxter] Baxter audit

	## -cutting_garden

	- [nsA.ics project-client-acme] Acme retainer
	- [nsC.ics project-cutting_garden] CG roadmap
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [nsA.ics  categories=[-project-client-acme-]{+project-client-acme,project-cutting_garden+}]  Acme retainer

organize: wrote 1 change(s)
EOF

  # The old tag STAYS; only the new bucket's reconstructed tag was added.
  run curl -fsS "${CALDAV_SOURCE#caldav:}ns/nsA.ics"
  assert_success
  assert_line --regexp $'^CATEGORIES:project-client-acme,project-cutting_garden\r?$'

  run_cg organize -group-by project "$NS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43110/dav/ns/`
	- _base = @blake2b256-p7z0uhg0xkhkdl3fue7uu7gywwlqt02nzd7svf6an9t0ak6dnqnsdlqfvh
	- _anchor = caldav:http://127.0.0.1:43110/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	- _tag-strip = none
	! organize-base-v1
	---

	- [nsD.ics other] Loose idea

	# project

	## -client

	- [nsA.ics project-client-acme project-cutting_garden] Acme retainer
	- [nsB.ics project-client-baxter] Baxter audit

	## -cutting_garden

	- [nsA.ics project-client-acme project-cutting_garden] Acme retainer
	- [nsC.ics project-cutting_garden] CG roadmap
	EOM
}
