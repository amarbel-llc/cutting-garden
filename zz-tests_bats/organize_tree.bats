#! /usr/bin/env bats

# The `(tree)` organize grouping over a tag-vocabulary org file (#353, with the
# boxed heading #377 and the org name join #378): a name tree rendered as nested
# continuation headings. Read-only rendering only; the edit semantics are a later
# slice. Backed by the in-memory WebDAV server seeded from
# zz-tests_bats/fixtures/org (lib/org.bash); documents anchor at org://test/.
#
# Rendering rules the goldens encode (hand-written from them; goldens/organize_tree/):
#   1. A name splits on every `-`; `_` is literal. The level-1 headline of a
#      tag-vocabulary file is the family; a member `-a-b-c` under family `f` is
#      the full name `f-a-b-c`.
#   2. One continuation heading per segment, a single-child chain NOT collapsed
#      (`# f` > `## -a` > `### -b` > `- -c`).
#   3. A heading is BOXED (`# ["id=…"] f`) when a node with exactly that prefix
#      name exists, SYNTHETIC (`## -a`, no box) when none does.
#   4. A line's text is its name relative to its heading. Leaves list before
#      sub-headings under a heading; childless top-level nodes sit above the
#      first heading.
#   5. Source order otherwise.
#
# Envelope: `- _group-by = (tree)`, and a `- _nest = full | 1 | none` lever whose
# BUILT-IN default is `full`, OMITTED at that default (as `_tag-atoms`/`_tag-strip`
# are at theirs); any other value is echoed, and an `[organize] nest` config
# default that differs from `full` is echoed the same way. The document's own
# field wins over config. `_nest` is presentation: changing it is not an
# unapplied edit.
#
# Config the lane WANTS (neither key exists yet; lib/org.bash
# write_org_tag_vocab_config): a per-account `tag_files = ["_tags.org"]` naming
# the account's tag-vocabulary files, and `[organize] nest = "none"|"1"|"full"`.
#
# The `- _base` pin of a hand-written golden is a placeholder: see
# lib/golden.bash for how `just test-bats-update-vectors organize_tree.bats`
# fills it in.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/org.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/golden.bash"
  export output
  start_org_server
  init_store

  # The query decides WHICH nodes are in the document (every headline of the
  # file, at any depth: the forward closure, #211); the grouping decides the
  # layout.
  TREE_SELECTION='org://test/_tags.org ->> !cutting_garden-org-headline-v1'
}

teardown() {
  stop_org_server
}

# bats file_tags=organize,org

# (a) The whole tree, at the default nesting a tag-vocabulary file gets.
function organize_tree_full_document { # @test
  skip "needs the nested layout (S3 of cutting-garden#353)"
  write_org_tag_vocab_config

  run_cg organize "$TREE_SELECTION" '(tree)'
  assert_success
  assert_golden tree_full
}

# (b) `_nest = none`: the same nodes as one flat list of full names.
function organize_tree_nest_none_is_flat { # @test
  write_org_tag_vocab_config none

  run_cg organize "$TREE_SELECTION" '(tree)'
  assert_success
  assert_golden tree_nest_none
}

# (c) fmt-organize regenerates the same document: the live data is unchanged,
# so the file is byte-identical and the summary says so.
function organize_tree_fmt_organize_regenerates_the_same_document { # @test
  skip "needs the nested layout (S3 of cutting-garden#353)"
  write_org_tag_vocab_config
  local doc="$BATS_TEST_TMPDIR/tree.txt"

  run_cg organize "$TREE_SELECTION" '(tree)'
  assert_success
  printf '%s\n' "$output" >"$doc"

  run_cg fmt-organize "$doc"
  assert_success
  assert_output "fmt-organize: $doc unchanged"

  run cat "$doc"
  assert_golden tree_full
}

# (d) `_nest` is presentation: a document respelled flat (its `_nest` changed)
# is not an unapplied edit, so applying it is a refusal-free no-op.
function organize_tree_nest_change_is_a_no_op_apply { # @test
  skip "needs the nested layout (S3 of cutting-garden#353)"
  write_org_tag_vocab_config
  local edited="$BATS_TEST_TMPDIR/edited.txt"

  run_cg organize "$TREE_SELECTION" '(tree)'
  assert_success
  golden_with_pin_of tree_nest_none "$output" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output "organize: no changes to apply"
}

# --- the read-only flat layout (S2): what runs before the nested layout ---

# generate_flat_doc writes the flat document to $1.
generate_flat_doc() {
  write_org_tag_vocab_config none
  run_cg organize "$TREE_SELECTION" '(tree)'
  assert_success
  printf '%s\n' "$output" >"$1"
}

# fmt-organize of a flat document regenerates it from its own envelope: the
# data has not changed, so it is byte-identical.
function organize_tree_flat_fmt_organize_is_unchanged { # @test
  local doc="$BATS_TEST_TMPDIR/tree.txt"
  generate_flat_doc "$doc"

  run_cg fmt-organize "$doc"
  assert_success
  assert_output "fmt-organize: $doc unchanged"

  run cat "$doc"
  assert_golden tree_nest_none
}

function organize_tree_flat_apply_unchanged_is_a_no_op { # @test
  local doc="$BATS_TEST_TMPDIR/tree.txt"
  generate_flat_doc "$doc"

  run_cg organize -apply "$doc" -commit
  assert_success
  assert_output "organize: no changes to apply"
}

# A name tree is read-only for now: a renamed, deleted or created node is a
# refusal pointing at #380 (the edit semantics), and nothing is written.
function organize_tree_apply_refuses_name_edits { # @test
  local doc="$BATS_TEST_TMPDIR/tree.txt" edited="$BATS_TEST_TMPDIR/edited.txt"
  generate_flat_doc "$doc"
  local before
  before="$(org_dav GET /_tags.org)"

  sed 's/\] priority-0_must$/] priority-0_zzz/' "$doc" >"$edited"
  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_output --partial '[id=tags-priority-0_must] was renamed ("priority-0_must" -> "priority-0_zzz")'
  assert_output --partial 'cutting-garden#380'

  grep -v 'priority-0_must$' "$doc" >"$edited"
  run_cg organize -apply "$edited" -commit
  assert_failure 64
  assert_output --partial 'was deleted'
  assert_output --partial 'cutting-garden#380'

  printf '%s\n' '- [+n] fresh' >>"$doc"
  run_cg organize -apply "$doc" -commit
  assert_failure 64
  assert_output --partial 'creates an object'
  assert_output --partial 'cutting-garden#380'

  run org_dav GET /_tags.org
  assert_output "$before"
}

# A node without a hyphen name (a task file the account does not list as a
# tag vocabulary) cannot be laid out by name; the refusal names the node and
# the config key.
function organize_tree_refuses_a_nameless_node { # @test
  write_org_tag_vocab_config none
  run_cg organize 'org://test/_inbox.org ->> !cutting_garden-org-headline-v1' '(tree)'
  assert_failure 64
  assert_output --partial 'has no hyphen name'
  assert_output --partial 'tag_files'
}

# Only the flat layout exists so far; the default nesting says so rather than
# rendering something else.
function organize_tree_default_nesting_is_not_built_yet { # @test
  write_org_tag_vocab_config
  run_cg organize "$TREE_SELECTION" '(tree)'
  assert_failure 64
  assert_output --partial 'flat layout so far'
  assert_output --partial 'nest = "none"'
}
