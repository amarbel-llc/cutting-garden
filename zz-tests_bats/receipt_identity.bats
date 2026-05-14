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
  # Multi-root and trailing-slash variants are tracked in
  # cutting-garden#21 (step-6 path-cleaning divergence applies).
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
