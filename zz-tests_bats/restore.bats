setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=restore

# Phase 3 step 3 shipped the -store happy-path single-root round-trip.
# Step 5 added FDR §Store-Hint Resolution; the implicit-hint branch is
# tested here. Drift (branch 3), hint-store-missing (branch 4), and
# no-hint (branch 5) tests are in the step 7 FDR conformance matrix.

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

function restore_uses_hint_store_implicitly { # @test
  # FDR §Store-Hint Resolution branch 2: hint present, store
  # configured, config-hash matches → use the hinted store with no
  # diagnostic. Captured receipts emit a hint for the default store
  # via Phase 2 step 8, so `restore` without `-store` must resolve
  # through the hint and proceed silently.
  init_store

  mkdir -p src
  echo "x" >src/x.txt
  run_cg capture -format json src
  assert_success
  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id: $output"

  run_cg restore "$rid" out
  assert_success

  [[ -f out/x.txt ]] || fail "out/x.txt missing"

  # Branch 2 is silent — no notice/warning/error on stderr.
  refute_output --partial "notice:"
  refute_output --partial "warning:"
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
