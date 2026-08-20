#! /usr/bin/env bats

# The organize categories tag lane (tags slice 1, RFC 0019; cutting-garden#231):
# `categories` is a read-only, MULTI-VALUED tag dimension with naive
# (exact-match) semantics. Grouping a calendar by `categories` files each object
# under a `## =<tag>` bucket for EVERY tag it carries — a two-tag task appears
# under both buckets — and `list --facets --filter 'categories=<tag>'` narrows
# the facet summary to the objects carrying that tag. The dimension is read-only
# in slice 1: applying a MOVED categories document is rejected. Because a two-tag
# task renders under both buckets, the generated (and pinned-base) document
# carries its box line twice, and the single-bucket apply merge rejects that
# duplication loudly ("appears twice") BEFORE it ever reaches the read-only
# FacetWriteNone gate — so the end-to-end proof is that no membership move lands.
# The FacetWriteNone rejection itself (checkMoveWritable) is unit-tested in
# internal/organize/apply_test.go and plugins/caldav/facet_apply_test.go; multi-
# membership makes it unreachable through a rendered document, which is why the
# e2e rejection surfaces as "appears twice" rather than the read-only message.
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
# `## =<value>` heading ($1), stopping at the next heading — so a lane can assert
# which tasks a tag bucket contains.
lines_under_bucket() {
  awk -v h="## =$1" '
    $0 == h { in_bucket = 1; next }
    /^#/ { in_bucket = 0 }
    in_bucket && /^- \[/ { print }
  ' "$DOC"
}

# Grouping by categories files a two-tag task under BOTH its buckets and a
# one-tag task under its single bucket — multi-membership (tags design D7). The
# dimension heading is the bare `categories=` (naive: no granularity suffix), and
# observed tag values sort ascending (errand before work).
function organize_categories_multi_membership { # @test
  generate_grouped
  assert_line '# categories='
  assert_line '## =errand'
  assert_line '## =work'

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

# categories is read-only in slice 1: applying a moved categories document is
# rejected end to end. Move the one-tag task field3 from work to errand and
# commit; the apply refuses non-zero because the two-tag task's box line is
# duplicated across its buckets (multi-membership cannot round-trip through the
# single-bucket merge) — no membership change is ever written.
function organize_categories_apply_rejected { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt" line
  line="$(grep 'field3.ics' "$DOC")"
  [[ -n $line ]] || fail "no field3 box in document: $(cat "$DOC")"
  awk -v ln="$line" -v h='## =errand' '
    $0 == ln { next }
    { print }
    $0 == h { print ""; print ln }
  ' "$DOC" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_failure
  assert_output --partial 'appears twice'
}
