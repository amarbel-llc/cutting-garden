#! /bin/bash -e

# SC2154: bats injects $output and the $BATS_* vars into the test scope at
# runtime; they are not assigned in this helper.
# shellcheck disable=SC2154

# Regenerable whole-document vectors (cutting-garden#250, native tags design
# G16). assert_vector is assert_output's whole-document form: outside update
# mode it IS assert_output, argument for argument.
#
#   run_cg organize -group-by status= "$CAL"
#   assert_success
#   assert_vector - <<-'EOM'
#   	---
#   	- _base = @blake2b256-…
#   	…
#   	EOM
#
# Under `just test-bats-update-vectors` ($CG_UPDATE_VECTORS names a records
# directory) a mismatch does not fail: the call site and the actual $output are
# recorded, and lib/update_vectors.bash rewrites the heredoc in place. Digests
# stay VERBATIM — nothing is masked or normalized, so a wrong `_base` still
# fails the gate.
#
# For the rewrite to find the heredoc, spell the call exactly as above: `-` for
# stdin, and a QUOTED delimiter (`<<-'EOM'` or `<<'EOM'`) so the body is the
# literal expected text. An expected value passed as an argument is still
# recorded — its `_base` digest and envelope propagate to the rest of the file
# — but its own text is reported for a manual edit.
assert_vector() {
  if [[ -z ${CG_UPDATE_VECTORS:-} ]]; then
    assert_output "$@"
    return
  fi

  local expected
  if [[ ${1:-} == - ]]; then
    expected="$(cat)"
  else
    expected="${1:-}"
  fi
  [[ $output == "$expected" ]] && return 0

  # BASH_SOURCE[1] is the caller's file: bats' line-preserving preprocessed
  # copy of the .bats file, or a helper library loaded as itself.
  local file="${BASH_SOURCE[1]}"
  [[ $file == "${BATS_TEST_SOURCE:-}" ]] && file="$BATS_TEST_FILENAME"

  local record
  record="$(mktemp -d "$CG_UPDATE_VECTORS/record.XXXXXXXX")"
  printf '%s\n' "$file" >"$record/file"
  printf '%s\n' "${BASH_LINENO[0]}" >"$record/line"
  printf '%s' "$expected" >"$record/expected"
  printf '%s' "$output" >"$record/actual"
}
