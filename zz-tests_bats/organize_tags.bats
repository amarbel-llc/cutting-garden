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

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1
  start_caldav_server
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/fields/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_grouped runs `organize -group-by categories` and saves the document.
generate_grouped() {
  run_cg organize -group-by categories "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/categories.txt"
  printf '%s\n' "$output" >"$DOC"
}

# lines_under_bucket prints the object box lines directly beneath the
# `## <value>` heading ($1), stopping at the next heading — so a lane can assert
# which tasks a tag bucket contains.
lines_under_bucket() {
  awk -v h="## $1" '
    $0 == h { in_bucket = 1; next }
    /^#/ { in_bucket = 0 }
    in_bucket && /^- \[/ { print }
  ' "$DOC"
}

# Grouping by categories files a two-tag task under BOTH its buckets and a
# one-tag task under its single bucket — multi-membership (tags design D7). The
# grouping is hoisted: a `- _group-by = categories` envelope directive, no
# `# categories=` parent heading, and bare `## <tag>` buckets (observed tag values
# sort ascending — errand before work).
function organize_categories_multi_membership { # @test
  generate_grouped
  assert_line '- _group-by = categories'
  refute_line '# categories='
  assert_line '## errand'
  assert_line '## work'

  # field2 (work,errand) is filed under BOTH buckets; field3 (work) under work only.
  run lines_under_bucket errand
  assert_output --partial 'field2.ics'
  refute_output --partial 'field3.ics'

  run lines_under_bucket work
  assert_output --partial 'field2.ics'
  assert_output --partial 'field3.ics'
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
# buckets, unmoved) is left untouched.
function organize_categories_apply_writes { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt" line
  line="$(grep 'field3.ics' "$DOC")"
  [[ -n $line ]] || fail "no field3 box in document: $(cat "$DOC")"
  awk -v ln="$line" -v h='## errand' '
    $0 == ln { next }
    { print }
    $0 == h { print ""; print ln }
  ' "$DOC" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output --partial '  - [field3.ics  categories=[-work-]{+errand+}]'
  assert_output --partial 'organize: wrote 1 change(s)'

  # The membership write rewrote field3's CATEGORIES to errand on the live object.
  run curl -fsS "${CALDAV_SOURCE#caldav:}fields/field3.ics"
  assert_success
  assert_output --partial 'CATEGORIES:errand'
  refute_output --partial 'CATEGORIES:work'
}
