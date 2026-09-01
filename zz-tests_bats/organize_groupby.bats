#! /usr/bin/env bats

# shellcheck disable=SC2016  # the asserted messages quote spellings in backticks; no expansion intended

# The organize group-by SPELLING lane (native tags slice 1 task 4; design G9 /
# G10): ONE grammar for the `--group-by` flag, the `_group-by` envelope
# directive, and the dimension heading, read by the trellis term parser:
#
#   (tags)             the type's whole tag set     → `_group-by = (tags)`, `# <tag>`
#   project            tag namespace (bare = tag)   → `_group-by = project`, `# -client`
#   status=            field grouping               → `# status=` / `## =value`
#   date_due=(month)   date field at a granularity  → `# date_due=(month)`
#
# The retired spellings — `date_due:month`, bare `categories` (the tag dimension
# itself), `categories/project` — are loud bad requests (exit 64) naming the new
# spelling; a bare name is ALWAYS a tag namespace, so a bare FIELD name whose
# namespace matches nothing fails suggesting `<name>=`; a literal or operator
# form (`status=x`) is a query predicate, not a grouping.
#
# One server serves every fixture calendar the rows need (/dav/fields/ for tags
# + fields, /dav/ns/ for the namespace rollup, /dav/sched/ for the date field),
# under the dodder-hyphen interpreter a namespace grouping requires.
#
# Whole-document vectors (G16): pinned port + serialized tests, see lib/caldav.bash.

setup_file() {
  export BATS_NO_PARALLELIZE_WITHIN_FILE=true
}

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  export CG_TEST_CALDAV_FIELDS=1 CG_TEST_CALDAV_SCHED=1 CG_TEST_CALDAV_NS=1
  # Pin the config dir under the sandboxed $HOME (no ambient date_granularity
  # can leak in) and select the dodder-hyphen interpreter the namespace row
  # needs.
  export XDG_CONFIG_HOME="$HOME/.config"
  mkdir -p "$XDG_CONFIG_HOME/cutting-garden"
  cat >"$XDG_CONFIG_HOME/cutting-garden/config.toml" <<-'EOF'
	[tags]
	interpreter = "dodder-hyphen"
	EOF
  start_caldav_server 43108
  init_store
  FIELDS="${CALDAV_SOURCE%/dav/}/dav/fields/"
  NS="${CALDAV_SOURCE%/dav/}/dav/ns/"
  SCHED="${CALDAV_SOURCE%/dav/}/dav/sched/"
}

teardown() {
  stop_caldav_server
}

# bats file_tags=organize

# G10 row 1: `(tags)` groups by the type's whole tag set — the `_group-by`
# directive carries the same spelling, there is NO dimension heading, and the
# buckets are bare `# <tag>` headings (one per tag an object carries).
function organize_groupby_tags_whole_set { # @test
  run_cg organize -group-by '(tags)' "$FIELDS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "_terminal=no" caldav:http://127.0.0.1:43108/dav/fields/`
	- _base = @blake2b256-j6dpwr0h2v5wtym7yrq36xjlpjvtkspnshjplzpsmp9rrg8akutqc0fgpq
	- _anchor = caldav:http://127.0.0.1:43108/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	- [field1.ics location=Bank status=NEEDS-ACTION priority=0_must] Pay rent
	- [field4.ics] Someday idea

	# errand

	- [field2.ics priority=1_should] Read book

	# work

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	EOM
}

# G10 row 2: a bare name is a tag NAMESPACE (G9: bare is always a tag) —
# `project` renders the namespace ROOT as a top-level `# project` tag heading
# (G10a) with the rollup continuations nested one deeper (`## -client` /
# `## -cutting_garden`), and `_group-by` persists the same bare spelling. The
# out-of-namespace nsD stays ungrouped ABOVE the root heading.
function organize_groupby_namespace_rollup { # @test
  run_cg organize -group-by project "$NS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by project -query "_terminal=no" caldav:http://127.0.0.1:43108/dav/ns/`
	- _base = @blake2b256-rhnf5stu5kj2q58dcv2yfd064vnx0evjawug5xmjewr6x9mwru4qmcwc02
	- _anchor = caldav:http://127.0.0.1:43108/dav/ns/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	- _group-by = project
	! organize-base-v1
	---

	- [nsD.ics] Loose idea

	# project

	## -client

	- [nsA.ics] Acme retainer
	- [nsB.ics] Baxter audit

	## -cutting_garden

	- [nsC.ics] CG roadmap
	EOM
}

# G10 row 3: `status=` is the FIELD grouping (a field needs its operator): the
# dimension heading spells `# status=`, buckets are `## =<value>` with the
# plugin's declared values pre-rendered, and there is no `_group-by` directive —
# the heading IS the spelling. The grouped status atom is elided from field1's
# box (#229); the status-less objects sit ungrouped above the heading.
function organize_groupby_field { # @test
  run_cg organize -group-by status= "$FIELDS"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by status= -query "_terminal=no" caldav:http://127.0.0.1:43108/dav/fields/`
	- _base = @blake2b256-mmfartm9s7pz3c4mv57qkuz049vlgt9urmd2rjl7ut87etpw5reqq5mrvp
	- _anchor = caldav:http://127.0.0.1:43108/dav/fields/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	- [field2.ics priority=1_should] Read book
	- [field3.ics priority=2_nice] Water plants
	- [field4.ics] Someday idea

	# status=

	## =NEEDS-ACTION

	- [field1.ics location=Bank priority=0_must] Pay rent

	## =IN-PROCESS

	## =COMPLETED

	## =CANCELLED
	EOM
}

# G10 row 4: `date_due=(month)` — a `(granularity)` qualifier on a date field
# (year|month|day) — coarsens the day-precise DUEs into month buckets, and the
# heading persists the qualifier verbatim so apply coarsens identically. A year
# qualifier folds both tasks under one `## =2026` bucket.
function organize_groupby_date_granularity { # @test
  run_cg organize -group-by 'date_due=(month)' "$SCHED"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by date_due=(month) -query "_terminal=no" caldav:http://127.0.0.1:43108/dav/sched/`
	- _base = @blake2b256-gencqx53cka0dgfqa3gakt9hunzujhkl0s6kyw4wychdj7s688ssr4unyf
	- _anchor = caldav:http://127.0.0.1:43108/dav/sched/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# date_due=(month)

	## =2026-08

	- [sched1.ics date_due=2026-08-15 time_due=14-30] Book flights

	## =2026-09

	- [sched2.ics date_due=2026-09-10 time_due=16-30] Renew passport
	EOM

  run_cg organize -group-by 'date_due=(year)' "$SCHED"
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by date_due=(year) -query "_terminal=no" caldav:http://127.0.0.1:43108/dav/sched/`
	- _base = @blake2b256-zfg6rknfep932d9pqt5l93m7cz6eh847vjg965mz8p9lzfgs0pksquymyt
	- _anchor = caldav:http://127.0.0.1:43108/dav/sched/
	- _query = _terminal=no
	- _type = !caldav-object-vtodo-v1
	! organize-base-v1
	---

	# date_due=(year)

	## =2026

	- [sched1.ics date_due=2026-08-15 time_due=14-30] Book flights
	- [sched2.ics date_due=2026-09-10 time_due=16-30] Renew passport
	EOM
}

# G10 rejections: the three retired spellings are loud bad requests (EX_USAGE,
# 64) each naming the new spelling — never a silent misgrouping.
function organize_groupby_rejects_legacy_spellings { # @test
  run_cg organize -group-by date_due:month "$SCHED"
  assert_failure 64
  assert_output --partial 'organize: --group-by date_due:month: the `dim:granularity` spelling is retired; spell a date granularity as `date_due=(month)`'

  run_cg organize -group-by categories "$FIELDS"
  assert_failure 64
  assert_output --partial 'organize: --group-by categories: a bare name is a tag namespace, and "categories" names the tag dimension itself; group by the whole tag set with `(tags)`'

  run_cg organize -group-by categories/project "$NS"
  assert_failure 64
  assert_output --partial 'organize: --group-by categories/project: the `dim/namespace` spelling is retired; a bare name is a tag namespace (`project`), and `(tags)` is the whole tag set'
}

# G9/G10: a bare FIELD name is read as a tag namespace, and when that namespace
# matches nothing while a field of that name exists, generate fails suggesting
# the field spelling `status=` rather than emitting an all-ungrouped document.
function organize_groupby_empty_namespace_suggests_field { # @test
  run_cg organize -group-by status "$FIELDS"
  assert_failure 64
  assert_output --partial 'organize: --group-by status: no tag is under the "status" namespace, but "status" is a field dimension; to group by the field spell it `status=`'
  refute_output --partial '_anchor'
}

# G10: a query shape is not a grouping — a literal field value and an unknown
# qualifier both reject, pointing at the grouping spellings.
function organize_groupby_rejects_query_shapes { # @test
  run_cg organize -group-by status=x "$FIELDS"
  assert_failure 64
  assert_output --partial 'organize: --group-by status=x: `status=<value>` is a query predicate; group by the field with `status=`, or at a granularity with `status=(year|month|day)`'

  run_cg organize -group-by '(foo)' "$FIELDS"
  assert_failure 64
  assert_output --partial 'organize: --group-by (foo): unknown qualifier; the only bare qualifier is `(tags)` (the type'"'"'s whole tag set)'
}
