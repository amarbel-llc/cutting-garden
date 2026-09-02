#! /usr/bin/env bats

# The organize namespace-rollup grouping lane (RFC 0019 tags slice 3 B5,
# cutting-garden#231; G10a root heading, native tags slice 1.5 A): `--group-by
# project` groups the caldav `categories` tag dimension by the `project`
# NAMESPACE (dodder-hyphen segment hierarchy), rendering the HOISTED dialect —
# a `- _group-by = project` envelope directive (the SAME bare spelling as the
# flag, design G10), NO `# categories=` parent heading, the namespace ROOT as a
# top-level `# project` tag heading, and the rollup continuations nested one
# deeper (`## -client` / `## -cutting_garden`). Each task rolls up to its
# immediate segment under `project`: nsA/nsB (project-client-*) to `-client`,
# nsC (project-cutting_garden) to `-cutting_garden`, while nsD (`other`, not
# under project) lands ungrouped ABOVE the root heading. Moving a task between
# rollup buckets REWRITES its CATEGORIES to the reconstructed namespace tag
# through the caldav full-set membership write (B4): the object loses every tag
# under the old bucket's namespace path and gains the new bucket's namespace
# tag. The root heading is itself a LIVE bucket (G10a): a line placed DIRECTLY
# under `# project` carries the BARE tag `project` (reconstruction is exactly
# `project`; out-of-namespace tags are untouched). Since native tags slice 2
# each box carries its NON-PLACEMENT tag atoms (design G1/G2): the ungrouped
# nsD shows its `other` tag, the bucketed boxes are bare (their single Via tag
# is placement-stripped), and a root-placed line keeps `other` while the bare
# `project` strips (the G10a root strip).
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
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

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
  start_caldav_server 43103
  init_store
  CAL="${CALDAV_SOURCE%/dav/}/dav/ns/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# generate_grouped runs `organize -group-by project` and asserts the document in
# full: the hoisted G10a dialect (a `- _group-by = project` envelope directive,
# no `# categories=` parent heading, the `# project` root heading with nested
# `## -<segment>` rollup continuations), the out-of-namespace nsD ungrouped
# above the root heading, nsA + nsB coalesced under `-client`, and nsC under
# `-cutting_garden`.
generate_grouped() {
  run_cg organize -group-by project "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43103/dav/ns/`
	- _base = @blake2b256-yu624kdcgwz29tj8nh6eka0chs3rqpyt7u53tgyvvx6v5zznz5gs9ypmm4
	- _anchor = caldav:http://127.0.0.1:43103/dav/ns/
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

	## -cutting_garden

	- [nsC.ics] CG roadmap
	EOM
}

# Grouping by the `project` namespace hoists the dialect (G10/G10a): a
# `- _group-by = project` envelope directive (the SAME bare spelling as the
# flag), NO `# categories=` parent heading, the namespace root as a top-level
# `# project` tag heading, and the rollup continuations nested one deeper.
# project-client-* tasks (nsA, nsB) coalesce under `## -client`;
# project-cutting_garden (nsC) lands under `## -cutting_garden`; the
# out-of-namespace `other` task (nsD) is ungrouped above the root heading.
function organize_ns_namespace_rollup_render { # @test
  generate_grouped
}

# The rollup write-back tracer (B4): move nsA from `## -client` to
# `## -cutting_garden` and commit. planMemberships enumerates nsA's live tags
# under the old bucket's namespace path (project-client → removes
# project-client-acme) and reconstructs the new bucket's namespace tag (project +
# -cutting_garden → adds project-cutting_garden), then the caldav full-set write
# replaces CATEGORIES verbatim — so nsA's stored CATEGORIES becomes
# project-cutting_garden, having LOST project-client-acme and GAINED
# project-cutting_garden, and the re-rendered document rolls nsA up under
# `-cutting_garden`.
function organize_ns_rollup_move_writes_reconstructed_tag { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43103/dav/ns/`
	- _base = @blake2b256-yu624kdcgwz29tj8nh6eka0chs3rqpyt7u53tgyvvx6v5zznz5gs9ypmm4
	- _anchor = caldav:http://127.0.0.1:43103/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	! organize-base-v1
	---

	- [nsD.ics other] Loose idea

	# project

	## -client

	- [nsB.ics] Baxter audit

	## -cutting_garden

	- [nsA.ics] Acme retainer
	- [nsC.ics] CG roadmap
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [nsA.ics  categories=[-project-client-acme-]{+project-cutting_garden+}]  Acme retainer

organize: wrote 1 change(s)
EOF

  # The membership write rewrote nsA's live CATEGORIES to the reconstructed
  # namespace tag: project-cutting_garden replaces project-client-acme.
  # Exact full line (the rewritten body is CRLF-serialized with a volatile
  # DTSTAMP, so the whole body cannot be pinned); the old tag is gone entirely.
  run curl -fsS "${CALDAV_SOURCE#caldav:}ns/nsA.ics"
  assert_success
  assert_line --regexp $'^CATEGORIES:project-cutting_garden\r?$'
  refute_output --partial 'project-client-acme'

  run_cg organize -group-by project "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43103/dav/ns/`
	- _base = @blake2b256-4yfvv4wyma5yt8wz6f6umfplw7pqc72vnja5n6e77ceqharjmvvqppk96m
	- _anchor = caldav:http://127.0.0.1:43103/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	! organize-base-v1
	---

	- [nsD.ics other] Loose idea

	# project

	## -client

	- [nsB.ics] Baxter audit

	## -cutting_garden

	- [nsA.ics] Acme retainer
	- [nsC.ics] CG roadmap
	EOM
}

# The G10a direct-under-root tracer (native tags slice 1.5 A): move nsD's line
# from the ungrouped set to DIRECTLY under the `# project` root heading and
# commit. The root heading is a live tag bucket whose reconstruction is exactly
# the BARE namespace tag, so planMemberships adds `project` to nsD's live tag
# set — its out-of-namespace `other` tag is untouched (the box shows it as an
# atom, and namespace scoping only edits tags the document governs) — and
# the caldav full-set write serializes both as one comma-joined CATEGORIES.
# The re-rendered document shows nsD under `# project` (direct root placement,
# above the continuations) and no longer ungrouped — its box keeping `other`
# while the placement-realizing bare `project` is stripped (G2's G10a root
# strip: Via == the bare namespace tag).
function organize_ns_direct_root_placement_writes_bare_tag { # @test
  generate_grouped
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43103/dav/ns/`
	- _base = @blake2b256-yu624kdcgwz29tj8nh6eka0chs3rqpyt7u53tgyvvx6v5zznz5gs9ypmm4
	- _anchor = caldav:http://127.0.0.1:43103/dav/ns/
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

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [nsD.ics  categories=[-other-]{+other,project+}]  Loose idea

organize: wrote 1 change(s)
EOF

  # The membership write ADDED the bare namespace tag `project`; the
  # out-of-namespace `other` survives on the same comma-joined CATEGORIES line.
  run curl -fsS "${CALDAV_SOURCE#caldav:}ns/nsD.ics"
  assert_success
  assert_line --regexp $'^CATEGORIES:other,project\r?$'

  run_cg organize -group-by project "$CAL"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43103/dav/ns/`
	- _base = @blake2b256-lcdjhf2je7lpvn0vd2dp2gg4d4g5weeyu5r05qufj4p6ufqrsr0qpygaa7
	- _anchor = caldav:http://127.0.0.1:43103/dav/ns/
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
  # 64 = EX_USAGE: a namespace grouping under an incapable interpreter is the
  # caller's (config's) mistake, not "trouble".
  assert_failure 64
  # The rejection line is the WHOLE output — it names the grouping, the
  # dimension, the resolved interpreter, and the [tags] fix.
  assert_output 'cutting-garden: organize: namespace grouping (--group-by project) needs a tag interpreter that declares namespaces, but dimension "categories" uses the "naive" interpreter; set [tags] interpreter = dodder-hyphen'
}
