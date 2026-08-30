#! /usr/bin/env bats

# The organize categories tag lane (tags slice 2, RFC 0019; cutting-garden#231):
# `categories` is a WRITABLE, MULTI-VALUED tag dimension with naive (exact-match)
# semantics. Grouping a calendar by `categories` uses the hoisted tag-grouping
# dialect (RFC 0019 slice 3 B3): a `- _group-by = categories` envelope directive,
# NO `# categories=` parent heading, and a bare no-`=` `## <tag>` bucket for EVERY
# tag an object carries — a two-tag task appears under both buckets — and
# `list --facets --filter 'categories=<tag>'` narrows the facet
# summary to the objects carrying that tag. Slice 2 makes the dimension writable:
# the apply engine dispatches on the grouped dimension's write cardinality BEFORE
# the single-bucket move merge (which would reject a multi-membership document as
# "appears twice"), routing a write:many dimension through the SET-merge membership
# path (planMemberships → BuildMembershipWritePatch). Reorganizing a task between
# `## <tag>` buckets now REWRITES its CATEGORIES to the interpreter-resolved set.
#
# The fixture calendar (/dav/fields/, opt-in via CG_TEST_CALDAV_FIELDS) holds
# field2 "Read book" CATEGORIES work,errand (two-tag) and field3 "Water plants"
# CATEGORIES work (one-tag); field1/field4 carry no CATEGORIES. Reusing the
# fields calendar keeps CATEGORIES groupable-only (never a box atom), so the
# priority/field-edit lanes' exact box assertions are undisturbed.
#
# Every organize step is a WHOLE-DOCUMENT vector (native tags design G16) with
# verbatim `_base` digests, so the testserver is pinned to this file's own port
# (43102) and the file's tests are serialized (see lib/caldav.bash).

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server 43102
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_grouped runs `organize -group-by categories` and asserts the document
# in full: the hoisted dialect (a `- _group-by = categories` envelope directive,
# no `# categories=` parent heading, bare `## <tag>` buckets sorted ascending —
# errand before work), the untagged field1/field4 ungrouped, and the two-tag
# field2 filed under BOTH buckets while the one-tag field3 sits under work only.
generate_grouped() {
  run_cg organize -group-by categories "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by categories -query "_terminal=no" caldav:http://127.0.0.1:43102/dav/fields/`
	- _base = @blake2b256-gy636x6nfyzjtmrkppx058xe7aw5p34258hflt895tvmlg7margss8yxtf
	- _anchor = caldav:http://127.0.0.1:43102/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = categories
	! organize-base-v1
	---

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field4.ics] Someday idea

	## errand

	- [field2.ics priority=5] Read book

	## work

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants
	EOM
}

# Grouping by categories files a two-tag task under BOTH its buckets and a
# one-tag task under its single bucket — multi-membership (tags design D7). The
# grouping is hoisted: a `- _group-by = categories` envelope directive, no
# `# categories=` parent heading, and bare `## <tag>` buckets (observed tag values
# sort ascending — errand before work).
function organize_categories_multi_membership { # @test
  generate_grouped
}

# `list --facets --filter 'categories=<tag>'` narrows the summary to the objects
# carrying that tag: the two work-tagged tasks (component VTODO 2, not the full
# 4), whose categories histogram reads work=2, errand=1 — field2 contributes
# BOTH tags, so errand stays visible even under a work filter. The untagged
# field1/field4 (0_must / 3_unspecified bands) drop out of the narrowed summary.
function list_facets_categories_filter { # @test
  run_cg list -facets -filter 'categories=work' "$CAL"
  assert_success
  assert_output --partial 'work 2'
  assert_output --partial 'errand 1'
  assert_output --partial 'VTODO 2'
  refute_output --partial 'VTODO 4'
  refute_output --partial '0_must'
  refute_output --partial '3_unspecified'
}

# categories is writable in slice 2: applying a moved categories document REWRITES
# the object's CATEGORIES to the merged set. Move the one-tag task field3 from work
# to errand and commit; the membership path folds the base->edited set-diff
# (remove work, add errand) onto the live {work} set through the naive interpreter,
# so field3's CATEGORIES becomes errand while the two-tag field2 (present under both
# buckets, unmoved) is left untouched — and the re-rendered document files field3
# under errand only.
function organize_categories_apply_writes { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by categories -query "_terminal=no" caldav:http://127.0.0.1:43102/dav/fields/`
	- _base = @blake2b256-gy636x6nfyzjtmrkppx058xe7aw5p34258hflt895tvmlg7margss8yxtf
	- _anchor = caldav:http://127.0.0.1:43102/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = categories
	! organize-base-v1
	---

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

  # The membership write rewrote field3's CATEGORIES to errand on the live object.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field3.ics"
  assert_success
  assert_output --partial 'CATEGORIES:errand'
  refute_output --partial 'CATEGORIES:work'

  run_cg organize -group-by categories "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by categories -query "_terminal=no" caldav:http://127.0.0.1:43102/dav/fields/`
	- _base = @blake2b256-f2ydp8y7sx3ff9mtzqhgnyty7zkv8d98lw5xcxdpeaka8ygq6ztq92kk8w
	- _anchor = caldav:http://127.0.0.1:43102/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = categories
	! organize-base-v1
	---

	- [field1.ics location=Bank status=NEEDS-ACTION priority=1] Pay rent
	- [field4.ics] Someday idea

	## errand

	- [field2.ics priority=5] Read book
	- [field3.ics priority=9] Water plants

	## work

	- [field2.ics priority=5] Read book
	EOM
}
