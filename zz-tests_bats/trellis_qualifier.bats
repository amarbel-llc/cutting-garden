#! /usr/bin/env bats

# shellcheck disable=SC2016  # the asserted messages quote spellings in backticks; no expansion intended

# The trellis `(…)` qualifier term in QUERY position (native tags design G10,
# slice 1 task 2): `k=(x)` and `(x)` PARSE (they are organize's group-by
# spellings, RFC 0014 `Qualifier <- '(' Ident ')'`) but are RESERVED for
# `list --query` — the evaluator's validation layer rejects them as a loud
# bad request (EX_USAGE, 64) naming the term, never a silent empty listing.
# Run against the caldav testserver so the rejection is proven end to end
# through the real binary, not just the validator unit tests
# (internal/trellis_eval/validate_test.go).

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  export output
  start_caldav_server
  init_store
}

teardown() {
  stop_caldav_server
}

# bats file_tags=caldav

function list_query_rejects_qualifier_value_as_reserved { # @test
  run_cg list -query 'status=(x)' "$CALDAV_SOURCE"
  # 64 = EX_USAGE: a reserved-in-query form is the CALLER's mistake
  # (Is400BadRequest), not "trouble".
  assert_failure 64
  # The rejection line is the WHOLE output — loud, never a silent empty
  # listing, and nothing from the listing (Personal/Work) leaks through.
  assert_output 'cutting-garden: trellis: qualifier terms are reserved; not evaluable yet (`(x)` in field `status` is a group-by spelling — see `organize --group-by`)'
}

function list_query_rejects_qualifier_term_as_reserved { # @test
  run_cg list -query '(tags)' "$CALDAV_SOURCE"
  assert_failure 64
  assert_output 'cutting-garden: trellis: qualifier terms are reserved; not evaluable yet (`(tags)` is a group-by spelling — see `organize --group-by`)'
}
