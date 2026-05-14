setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=restore

# Phase 3 step 3 ships the happy-path single-root round-trip. Full FDR
# 0001 conformance matrix lands in step 7.

function restore_round_trip_single_root { # @test
  # Capture a 3-file/2-dir fixture, restore via -store, assert the
  # destination tree matches the source byte-for-byte. Single-root
  # captures collapse Root to "." per RFC 0003 §Root Encoding, so the
  # fixture's files materialize at the dest root (not under dest/src/).
  init_store

  mkdir -p src/sub
  echo "alpha" >src/a.txt
  echo "beta" >src/b.txt
  echo "gamma" >src/sub/c.txt

  run_cg capture -format json src
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id: $output"

  run_cg restore -store .default "$rid" out
  assert_success

  [[ -f out/a.txt ]] || fail "out/a.txt missing"
  [[ -f out/b.txt ]] || fail "out/b.txt missing"
  [[ -f out/sub/c.txt ]] || fail "out/sub/c.txt missing"

  diff -r src out
}

function restore_refuses_existing_destination { # @test
  # FDR §Destination Preconditions: <dest> MUST NOT exist at
  # invocation. Pre-create the dir, capture a fixture, attempt
  # restore — should refuse without modifying the existing dir.
  init_store

  mkdir -p src
  echo "x" >src/x.txt
  run_cg capture -format json src
  assert_success
  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id: $output"

  mkdir out
  echo "pre-existing" >out/keep.txt

  run_cg restore -store .default "$rid" out
  assert_failure

  # Pre-existing contents intact; no x.txt materialized into out/.
  [[ -f out/keep.txt ]] || fail "pre-existing out/keep.txt was clobbered"
  [[ ! -f out/x.txt ]] || fail "restore materialized into existing out/"
}
