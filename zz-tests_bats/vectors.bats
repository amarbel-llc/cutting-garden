#! /usr/bin/env bats

# The vector-regeneration lane (cutting-garden#250): lib/vectors.bash's
# assert_vector and lib/update_vectors.bash, which `just
# test-bats-update-vectors` drives to rewrite whole-document vectors in place.
# The mechanism rewrites test SOURCES, so it is pinned here end to end against a
# stand-in lane file — assert_vector records the call site for real, the
# rewriter edits the file, and the result is asserted whole:
#
#   - a mismatch rewrites the heredoc verbatim and carries the new envelope and
#     `_base` digest to the input document and the `BASE=` variable pinned to it;
#   - a heredoc with an unquoted delimiter is reported and left byte-untouched;
#   - outside update mode assert_vector is assert_output: a mismatch fails.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/vectors.bash"
  export output
  LANE="$BATS_TEST_TMPDIR/lane.bash"
  RECORDS="$BATS_TEST_TMPDIR/records"
  mkdir -p "$RECORDS"
}

# bats file_tags=vectors

# write_lane writes the stand-in lane: a generated-document vector, an edited
# input pinned to the same envelope, an input whose envelope was itself edited
# (so only its digest can follow), and a digest held in a variable.
write_lane() {
  cat >"$LANE" <<'LANE'
generate() {
  assert_vector - <<-'EOM'
	---
	% generated: old
	- _base = @blake2b256-old
	---

	- [a] One
	EOM
}

write_edit() {
  cat >"$1" <<-'EOM'
	---
	% generated: old
	- _base = @blake2b256-old
	---

	- [a] Edited
	EOM
}

write_lever_edit() {
  cat >"$1" <<-'EOM'
	---
	% generated: old
	- _base = @blake2b256-old
	- _lever = on
	---
	EOM
}

BASE=blake2b256-old
LANE
}

# regenerated_output is what the binary "now prints": a new envelope line, a new
# digest, and a new body line.
regenerated_output() {
  cat <<'EOM'
---
% generated: new
- extra < line
- _base = @blake2b256-new
---

- [a] One
- [b] Two
EOM
}

function update_vectors_rewrites_heredoc_and_carries_envelope { # @test
  write_lane
  # shellcheck source=/dev/null
  source "$LANE"

  output="$(regenerated_output)"
  CG_UPDATE_VECTORS="$RECORDS" generate

  run bash "$BATS_TEST_DIRNAME/lib/update_vectors.bash" "$RECORDS"
  assert_success
  assert_output "update-vectors: $LANE: rewrote 1 vector(s)"

  run cat "$LANE"
  assert_success
  assert_output - <<'LANE'
generate() {
  assert_vector - <<-'EOM'
	---
	% generated: new
	- extra < line
	- _base = @blake2b256-new
	---

	- [a] One
	- [b] Two
	EOM
}

write_edit() {
  cat >"$1" <<-'EOM'
	---
	% generated: new
	- extra < line
	- _base = @blake2b256-new
	---

	- [a] Edited
	EOM
}

write_lever_edit() {
  cat >"$1" <<-'EOM'
	---
	% generated: old
	- _base = @blake2b256-new
	- _lever = on
	---
	EOM
}

BASE=blake2b256-new
LANE

  # The rewritten vector now matches: a second update pass records nothing.
  # shellcheck source=/dev/null
  source "$LANE"
  rm -rf "$RECORDS"
  mkdir -p "$RECORDS"
  output="$(regenerated_output)"
  CG_UPDATE_VECTORS="$RECORDS" generate
  run bash "$BATS_TEST_DIRNAME/lib/update_vectors.bash" "$RECORDS"
  assert_failure 3
  assert_output ''
}

function update_vectors_reports_unquoted_heredoc_untouched { # @test
  cat >"$LANE" <<'LANE'
generate() {
  assert_vector - <<-EOM
	- [a] One
	EOM
}
LANE
  local before
  before="$(cat "$LANE")"
  # shellcheck source=/dev/null
  source "$LANE"

  output='- [a] Two'
  CG_UPDATE_VECTORS="$RECORDS" generate

  run bash "$BATS_TEST_DIRNAME/lib/update_vectors.bash" "$RECORDS"
  assert_failure 3
  # shellcheck disable=SC2016  # the backticks are the message's own quoting, not expansion
  assert_output "update-vectors: $LANE:2: not a literal \`assert_vector - <<'DELIM'\` heredoc — only its envelope and digests are carried; update the rest by hand"

  run cat "$LANE"
  assert_output "$before"
}

function assert_vector_fails_on_mismatch_outside_update_mode { # @test
  write_lane
  # shellcheck source=/dev/null
  source "$LANE"

  output="$(regenerated_output)"
  CG_UPDATE_VECTORS='' run generate
  assert_failure
  assert_line -- '-- output differs --'
}
