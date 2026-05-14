setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=capture

function receipt_identity_single_root { # @test
  # CG_BIN (the standalone cutting-garden build) and MADDER_CG_BIN
  # (the madder-bundled cutting-garden built from madder's
  # cmd/cutting-garden via madder#176) MUST produce byte-identical
  # receipts for the same fixture. Byte-identity is observed
  # transitively via receipt-id equality, since receipt-ids are
  # content-addressed over receipt bytes.
  #
  # 3-file / 2-dir fixture, single root, no trailing slashes.
  require_bin MADDER_CG_BIN cutting-garden

  init_store

  mkdir -p tree/sub
  echo "alpha" >tree/a.txt
  echo "beta" >tree/b.txt
  echo "gamma" >tree/sub/c.txt

  run_cg capture -format json tree
  assert_success
  local rid_cg
  rid_cg="$(receipt_id_of_group "$output")"
  [[ -n $rid_cg ]] || fail "CG_BIN: no receipt id: $output"

  run_madder_cg capture -format json tree
  assert_success
  local rid_madder
  rid_madder="$(receipt_id_of_group "$output")"
  [[ -n $rid_madder ]] || fail "MADDER_CG_BIN: no receipt id: $output"

  [[ $rid_cg == "$rid_madder" ]] ||
    fail "receipt-id mismatch: CG_BIN=$rid_cg, MADDER_CG_BIN=$rid_madder"
}

function receipt_identity_multi_root_no_trailing_slash { # @test
  # Multi-root capture without trailing slashes: receipts MUST
  # match between CG_BIN and MADDER_CG_BIN. The step-6 path-
  # cleaning divergence (cutting-garden#4) only manifests when
  # roots carry trailing slashes; without them, filepath.Clean is
  # a no-op and the two implementations produce the same Root
  # bytes. (Multi-root groups do not collapse Root to "." — that
  # collapse is single-root-only — so Root values reach the wire
  # format verbatim.)
  require_bin MADDER_CG_BIN cutting-garden

  init_store

  mkdir -p src/sub docs
  echo "a" >src/a.txt
  echo "b" >src/sub/b.txt
  echo "d" >docs/d.txt

  run_cg capture -format json src docs
  assert_success
  local rid_cg
  rid_cg="$(receipt_id_of_group "$output")"
  [[ -n $rid_cg ]] || fail "CG_BIN: no receipt id: $output"

  run_madder_cg capture -format json src docs
  assert_success
  local rid_madder
  rid_madder="$(receipt_id_of_group "$output")"
  [[ -n $rid_madder ]] || fail "MADDER_CG_BIN: no receipt id: $output"

  [[ $rid_cg == "$rid_madder" ]] ||
    fail "multi-root receipts diverged: CG_BIN=$rid_cg, MADDER_CG_BIN=$rid_madder"
}

function receipt_identity_multi_root_trailing_slash_diverges { # @test
  # Documents the cutting-garden#4 vs madder-unfixed divergence.
  # Our classifyArg cleans sourceURL.Path via filepath.Clean
  # (`src/` -> `src`); madder's does not. In single-root groups
  # entries[i].Root is forcibly collapsed to "." after the walk,
  # hiding the divergence. In multi-root groups Root reaches the
  # wire verbatim, so trailing slashes in any root yield different
  # receipts.
  #
  # If madder ever picks up the same Clean fix, this test will
  # start failing — flip the inequality and update the comment.
  require_bin MADDER_CG_BIN cutting-garden

  init_store

  mkdir -p src/sub docs
  echo "a" >src/a.txt
  echo "b" >src/sub/b.txt
  echo "d" >docs/d.txt

  run_cg capture -format json src/ docs/
  assert_success
  local rid_cg
  rid_cg="$(receipt_id_of_group "$output")"
  [[ -n $rid_cg ]] || fail "CG_BIN: no receipt id: $output"

  run_madder_cg capture -format json src/ docs/
  assert_success
  local rid_madder
  rid_madder="$(receipt_id_of_group "$output")"
  [[ -n $rid_madder ]] || fail "MADDER_CG_BIN: no receipt id: $output"

  [[ $rid_cg != "$rid_madder" ]] ||
    fail "expected divergence under trailing-slash multi-root, but both produced $rid_cg — has madder picked up the Clean fix? Flip to assert-equal."
}

function receipt_identity_store_switch { # @test
  # Single capture invocation interleaves two store-groups
  # (`.default src .alt docs`). Both groups MUST produce
  # byte-identical receipts between CG_BIN and MADDER_CG_BIN, and
  # the per-group store ids MUST match positionally. No trailing
  # slashes, so we stay on the path-cleaning-agnostic side of
  # cutting-garden#4.
  require_bin MADDER_CG_BIN cutting-garden

  init_store
  run_madder init -encryption none .alt
  assert_success

  mkdir -p src docs
  echo "s" >src/s.txt
  echo "d" >docs/d.txt

  run_cg capture -format json .default src .alt docs
  assert_success
  local n
  n="$(echo "$output" | grep -c '"type":"store_group_receipt"' || true)"
  [[ $n -eq 2 ]] ||
    fail "CG_BIN: expected 2 receipts, got $n. output:"$'\n'"$output"

  local rid_cg_1 store_cg_1 rid_cg_2 store_cg_2
  rid_cg_1="$(receipt_id_of_group "$output" 1)"
  store_cg_1="$(receipt_store_of_group "$output" 1)"
  rid_cg_2="$(receipt_id_of_group "$output" 2)"
  store_cg_2="$(receipt_store_of_group "$output" 2)"

  run_madder_cg capture -format json .default src .alt docs
  assert_success
  n="$(echo "$output" | grep -c '"type":"store_group_receipt"' || true)"
  [[ $n -eq 2 ]] ||
    fail "MADDER_CG_BIN: expected 2 receipts, got $n. output:"$'\n'"$output"

  local rid_m_1 store_m_1 rid_m_2 store_m_2
  rid_m_1="$(receipt_id_of_group "$output" 1)"
  store_m_1="$(receipt_store_of_group "$output" 1)"
  rid_m_2="$(receipt_id_of_group "$output" 2)"
  store_m_2="$(receipt_store_of_group "$output" 2)"

  [[ $store_cg_1 == "$store_m_1" ]] ||
    fail "group 1 store mismatch: CG=$store_cg_1, MADDER=$store_m_1"
  [[ $store_cg_2 == "$store_m_2" ]] ||
    fail "group 2 store mismatch: CG=$store_cg_2, MADDER=$store_m_2"
  [[ $rid_cg_1 == "$rid_m_1" ]] ||
    fail "group 1 receipt-id mismatch: CG=$rid_cg_1, MADDER=$rid_m_1"
  [[ $rid_cg_2 == "$rid_m_2" ]] ||
    fail "group 2 receipt-id mismatch: CG=$rid_cg_2, MADDER=$rid_m_2"
}
