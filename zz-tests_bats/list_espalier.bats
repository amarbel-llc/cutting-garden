#! /usr/bin/env bats

# The `list -format espalier` + mesa-table lane (native tags design G8/G13,
# slice 4). `list -format espalier <uri>` renders one organize object line
# per node — `- [<id> <tag>… <k>=<v>…] <desc>` — through the SAME shared
# projection organize's document builder uses (trellis.WriteLiteral, the
# node_view espalier-view helpers), anchor-relative against the
# listed URI and sorted by box id, so the listing IS the organize document's
# object lines for the same set (RFC 0014's isometry). The G8 vector below
# pins that isometry by DERIVATION: it greps the object lines out of a live
# `organize` render and asserts `list -format espalier` equals them byte for
# byte. `-format text` moved onto dewey mesa (purse-first FDR 0015 /
# RFC 0003): TAB-separated on a pipe, with a TAGS column exactly when the
# plugin declares a tag dimension.
#
# Fixture: the /dav/ns/ calendar (opt-in via CG_TEST_CALDAV_NS) — four
# VTODOs whose CATEGORIES form a project-* hierarchy plus one out-of-
# namespace tag — under the dodder-hyphen [tags] override, mirroring
# organize_ns.bats. The no-tag-plugin case lists a plain directory through
# the file plugin.
#
# Whole-output vectors (G16): pinned port + serialized tests, see
# lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_NS=1
  # The dodder-hyphen interpreter: the `--query project` bare-tag term
  # matches the project-* hierarchy transitively through it, and every
  # rendered tag set orders by its SortKey.
  write_dodder_hyphen_config
  start_caldav_server 43112
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/ns/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# The G8 isometry pin, by construction AND by derivation: render the organize
# document for the same node set (a field grouping none of the fixtures carry
# a value for, so every object line sits ungrouped, placement strips nothing,
# and no atom is heading-elided — the lines are the pure espalier projection),
# grep its object lines, and assert `list -format espalier` reproduces them
# byte for byte. The literal whole-output pin follows so a drift in BOTH
# renderers cannot slip through the derived comparison.
function list_espalier_matches_organize_object_lines { # @test
  run_cg organize -group-by status= "$CAL"
  assert_success
  local derived="$BATS_TEST_TMPDIR/organize-object-lines.txt"
  grep '^- \[' <<<"$output" >"$derived"

  run_cg list -format espalier "$CAL"
  assert_success
  assert_output "$(cat "$derived")"

  # The same output, pinned literally: anchor-relative ids against the listed
  # URI, the tag atom leading (SortKey order), the SUMMARY trailer.
  assert_output - <<-'EOM'
	- [nsA.ics project-client-acme] Acme retainer
	- [nsB.ics project-client-baxter] Baxter audit
	- [nsC.ics project-cutting_garden] CG roadmap
	- [nsD.ics other] Loose idea
	EOM
}

# --query composes with espalier: the bare-tag term `project` resolves
# through the dodder-hyphen interpreter (RFC 0019 §4) and matches the three
# project-* tasks transitively — the out-of-namespace nsD (`other`) is
# filtered out — and the matched nodes render as the same boxes the plain
# listing shows.
function list_espalier_query_filters_and_renders_boxes { # @test
  run_cg list -format espalier -query 'project' "$CAL"
  assert_success
  assert_output - <<-'EOM'
	- [nsA.ics project-client-acme] Acme retainer
	- [nsB.ics project-client-baxter] Baxter audit
	- [nsC.ics project-cutting_garden] CG roadmap
	EOM
}

# `-format text` on a pipe is mesa's plain TAB-separated form (purse-first
# RFC 0003 §7.1) — machine-friendly for cut/awk — and caldav's declared
# categories tag dimension adds the TAGS column, the presented tag set
# space-joined in SortKey order. Whole-output: the separators below are
# literal TABs.
function list_text_mesa_table_carries_tags_column { # @test
  run_cg list "$CAL"
  assert_success
  assert_output - <<-'EOM'
	URI	NAME	TYPE	TAGS
	caldav:http://127.0.0.1:43112/dav/ns/nsA.ics	nsA.ics	caldav-object-vtodo-v1	project-client-acme
	caldav:http://127.0.0.1:43112/dav/ns/nsB.ics	nsB.ics	caldav-object-vtodo-v1	project-client-baxter
	caldav:http://127.0.0.1:43112/dav/ns/nsC.ics	nsC.ics	caldav-object-vtodo-v1	project-cutting_garden
	caldav:http://127.0.0.1:43112/dav/ns/nsD.ics	nsD.ics	caldav-object-vtodo-v1	other
	EOM
}

# A plugin with no tag dimension keeps the three-column table — the TAGS
# column is omitted entirely, never rendered empty (design G8). The file
# plugin's directory listing is the in-repo no-tag reference.
function list_text_no_tag_plugin_keeps_three_columns { # @test
  local tree="$BATS_TEST_TMPDIR/tree"
  mkdir -p "$tree/sub"
  echo hi >"$tree/a.txt"

  run_cg list "file://$tree/"
  assert_success
  assert_tab_table \
    "$(tab_row URI NAME TYPE)" \
    "$(tab_row "file://$tree/a.txt" a.txt cutting_garden-file-object-v1)" \
    "$(tab_row "file://$tree/sub" sub cutting_garden-file-directory-v1)"
}

# A MULTI-type listing inlines each box's `!type` — organize's spelling-1
# rule, shared via DistinctTypes (a single-type set keeps boxes bare, the
# spelling-2 shape every caldav vector above shows). The file plugin's
# file + directory entries are the in-repo two-type set; ids shorten
# against the listed URI, so the vector is path-independent.
function list_espalier_multi_type_inlines_type { # @test
  local tree="$BATS_TEST_TMPDIR/tree"
  mkdir -p "$tree/sub"
  echo hi >"$tree/a.txt"

  run_cg list -format espalier "file://$tree/"
  assert_success
  assert_output - <<-'EOM'
	- [a.txt !cutting_garden-file-object-v1] a.txt
	- [sub !cutting_garden-file-directory-v1] sub
	EOM
}
