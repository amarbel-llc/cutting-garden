#! /bin/bash -e

# SC2154: bats injects $output and the $BATS_* vars into the test scope at
# runtime; they are not assigned in this helper.
# shellcheck disable=SC2154

# On-disk golden documents (modelled on dodder's lib/golden.bash). A golden is a
# whole expected document at zz-tests_bats/goldens/<lane>/<name>.txt; the lane's
# bats file is the one that loads this. Digests stay VERBATIM — nothing is
# masked or normalized when comparing — so a wrong `- _base` pin fails.
#
#   run_cg organize "$SELECTION" '(tree)'
#   assert_success
#   assert_golden tree_full
#
# A golden may be HAND-WRITTEN from a spec before the code exists. Its `- _base`
# pin cannot be: that digest is the hash of a blob only the implementation can
# render. Such a golden carries a placeholder pin, and stays red until
#
#   just test-bats-update-vectors organize_tree.bats
#
# fills the pin in. Under that recipe ($CG_UPDATE_VECTORS set) assert_golden
# rewrites a golden ONLY when the actual document equals it on every line but
# `- _base`; any other difference still fails with the diff, so regenerating
# can never launder an implementation that departs from the hand-written spec.
# Review `git diff zz-tests_bats/goldens`: it should be `_base` lines alone.

# golden_path NAME prints the path of the lane's golden NAME.
golden_path() {
  local lane
  lane="$(basename "$BATS_TEST_FILENAME" .bats)"
  printf '%s' "$BATS_TEST_DIRNAME/goldens/$lane/$1.txt"
}

# mask_base_pin masks the `- _base` line on stdin, for the update-mode
# comparison only.
mask_base_pin() {
  sed 's/^- _base = .*/- _base = <pin>/'
}

# assert_golden NAME asserts $output is exactly the golden NAME.
assert_golden() {
  local golden
  golden="$(golden_path "$1")"
  [[ -f $golden ]] || {
    fail "golden missing: $golden"
    return 1
  }

  if [[ -z ${CG_UPDATE_VECTORS:-} ]]; then
    assert_output "$(cat "$golden")"
    return
  fi

  local expected_body actual_body
  expected_body="$(mask_base_pin <"$golden")"
  actual_body="$(mask_base_pin <<<"$output")"
  if [[ $expected_body != "$actual_body" ]]; then
    fail "$(printf 'golden %s differs beyond its `- _base` pin; update mode only fills the pin in\n%s' \
      "$golden" "$(diff -u <(printf '%s\n' "$expected_body") <(printf '%s\n' "$actual_body") || true)")"
    return 1
  fi
  printf '%s\n' "$output" >"$golden"
}

# golden_with_pin_of NAME DOCUMENT prints golden NAME with its `- _base` line
# replaced by DOCUMENT's: the golden as an INPUT pinned to the base DOCUMENT was
# generated against (an edited document applies against the base it was
# generated from, whatever its pin said on disk).
golden_with_pin_of() {
  local pin
  pin="$(grep -m1 '^- _base = ' <<<"$2")"
  [[ -n $pin ]] || {
    fail 'no `- _base` line in the document to take the pin from'
    return 1
  }
  sed "s|^- _base = .*|$pin|" "$(golden_path "$1")"
}
