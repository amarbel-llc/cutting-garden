#! /usr/bin/env bats

# The organize namespace-rollup grouping lane (RFC 0019 tags slice 3 B5,
# cutting-garden#231): `--group-by project` groups the caldav `categories` tag
# dimension by the `project` NAMESPACE (dodder-hyphen segment hierarchy),
# rendering the HOISTED continuation-heading dialect — a `- _group-by =
# categories/project` envelope directive, NO `# categories=` parent heading, and
# rollup buckets `## -client` / `## -cutting_garden` (bare, no `=`). Each task
# rolls up to its immediate segment under `project`: nsA/nsB (project-client-*)
# to `-client`, nsC (project-cutting_garden) to `-cutting_garden`, while nsD
# (`other`, not under project) lands ungrouped above the first heading. Moving a
# task between rollup buckets REWRITES its CATEGORIES to the reconstructed
# namespace tag through the caldav full-set membership write (B4): the object
# loses every tag under the old bucket's namespace path and gains the new
# bucket's namespace tag.
#
# Namespace grouping REQUIRES an interpreter that declares namespaces. The caldav
# categories field defaults to `naive` (exact-match, no namespaces), which
# rejects a namespace grouping with a clear error — so the lane sets the global
# `[tags] interpreter = "dodder-hyphen"` config override; the naive-rejects lane
# pins the error when that override is absent.
#
# The fixture calendar (/dav/ns/, opt-in via CG_TEST_CALDAV_NS) holds four VTODOs
# whose CATEGORIES form a `project-*` hierarchy (nsA project-client-acme, nsB
# project-client-baxter, nsC project-cutting_garden, nsD other). Its own env gate
# keeps it out of the shared fixture so the caldav.bats home-capture counts and
# the fields/sched lanes are undisturbed.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_NS=1
  # Pin the config dir under the sandboxed $HOME so os.UserConfigDir resolves the
  # lane's own config.toml (and an ambient host value can't leak in), then select
  # the dodder-hyphen interpreter the namespace grouping needs. The naive-rejects
  # lane overwrites this with naive to pin the clear error.
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "dodder-hyphen"
	EOF
  start_caldav_server
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/ns/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_grouped runs `organize -group-by project` and saves the document.
generate_grouped() {
  run_cg organize -group-by project "$CAL"
  assert_success
  DOC="$BATS_TEST_TMPDIR/ns.txt"
  printf '%s\n' "$output" >"$DOC"
}

# lines_under_bucket prints the object box lines directly beneath the bare
# `## <bucket>` heading ($1), stopping at the next heading — so a lane can assert
# which tasks a rollup bucket contains.
lines_under_bucket() {
  awk -v h="## $1" '
    $0 == h { in_bucket = 1; next }
    /^#/ { in_bucket = 0 }
    in_bucket && /^- \[/ { print }
  ' "$DOC"
}

# lines_ungrouped prints the object box lines above the first `##` heading — the
# ungrouped set (spelling 2's type-less preamble). The envelope's `- _*` lines
# are not `- [` boxes, so only object boxes are printed.
lines_ungrouped() {
  awk '/^#/ { exit } /^- \[/ { print }' "$DOC"
}

# Grouping by the `project` namespace hoists the dialect: a `- _group-by =
# categories/project` envelope directive, NO `# categories=` parent heading, and
# bare `## -<segment>` rollup buckets. project-client-* tasks (nsA, nsB) coalesce
# under `-client`; project-cutting_garden (nsC) lands under `-cutting_garden`; the
# out-of-namespace `other` task (nsD) is ungrouped above the first heading.
function organize_ns_namespace_rollup_render { # @test
  generate_grouped
  assert_line '- _group-by = categories/project'
  refute_line '# categories='
  assert_line '## -client'
  assert_line '## -cutting_garden'

  # nsA + nsB roll up to -client; nsC to -cutting_garden.
  run lines_under_bucket -client
  assert_output --partial 'nsA.ics'
  assert_output --partial 'nsB.ics'
  refute_output --partial 'nsC.ics'
  refute_output --partial 'nsD.ics'

  run lines_under_bucket -cutting_garden
  assert_output --partial 'nsC.ics'
  refute_output --partial 'nsA.ics'
  refute_output --partial 'nsB.ics'
  refute_output --partial 'nsD.ics'

  # nsD (project-less `other`) is ungrouped, above the first bucket heading.
  run lines_ungrouped
  assert_output --partial 'nsD.ics'
  refute_output --partial 'nsA.ics'
  refute_output --partial 'nsC.ics'
}

# The rollup write-back tracer (B4): move nsA from `## -client` to
# `## -cutting_garden` and commit. planMemberships enumerates nsA's live tags
# under the old bucket's namespace path (project-client → removes
# project-client-acme) and reconstructs the new bucket's namespace tag (project +
# -cutting_garden → adds project-cutting_garden), then the caldav full-set write
# replaces CATEGORIES verbatim — so nsA's stored CATEGORIES becomes
# project-cutting_garden, having LOST project-client-acme and GAINED
# project-cutting_garden.
function organize_ns_rollup_move_writes_reconstructed_tag { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt" line
  line="$(grep 'nsA.ics' "$DOC")"
  [[ -n $line ]] || fail "no nsA box in document: $(cat "$DOC")"
  # Pull nsA's box from -client and re-file it under -cutting_garden.
  awk -v ln="$line" -v h='## -cutting_garden' '
    $0 == ln { next }
    { print }
    $0 == h { print ""; print ln }
  ' "$DOC" >"$edited"

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output --partial 'nsA.ics'
  assert_output --partial 'organize: wrote 1 change(s)'

  # The membership write rewrote nsA's live CATEGORIES to the reconstructed
  # namespace tag: project-cutting_garden replaces project-client-acme.
  run curl -fsS "${CALDAV_SOURCE#caldav:}ns/nsA.ics"
  assert_success
  assert_output --partial 'CATEGORIES:project-cutting_garden'
  refute_output --partial 'project-client-acme'
}

# Namespace grouping under the naive interpreter is rejected with a clear,
# actionable error (requireNamespaceInterpreter): naive declares no namespaces, so
# `--group-by project` fails up front naming the dimension, the interpreter, and
# the [tags] fix — never a confusing raw "declares no namespaces". Overwrite the
# dodder-hyphen override setup() wrote with an explicit naive selection.
function organize_ns_naive_rejects_namespace_grouping { # @test
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "naive"
	EOF

  run_cg organize -group-by project "$CAL"
  assert_failure
  assert_output --partial 'namespace grouping'
  assert_output --partial 'dodder-hyphen'
}
