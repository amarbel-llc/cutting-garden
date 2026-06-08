setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=failures

function failures_reads_capture_failure_receipt_end_to_end { # @test
  # End-to-end over the capture failure-receipt feature
  # (docs/plans/2026-06-07-capture-failure-receipt-design.md): a
  # partially-failing capture writes a durable failure receipt next to
  # the success receipt, `cg failures <id>` renders it for triage, and
  # captures.log journals outcome + failure_receipt_id.
  # Skip if running as root (chmod 000 doesn't deny root reads).
  if [[ $(id -u) -eq 0 ]]; then
    skip "running as root; chmod 000 has no effect"
  fi

  # captures.log lands under cutting-garden's XDG state scope.
  export XDG_STATE_HOME="$BATS_TEST_TMPDIR/xdg-state"

  init_store

  mkdir -p tree
  echo "good" >tree/good.txt
  echo "secret" >tree/secret.txt
  chmod 000 tree/secret.txt

  run_cg capture -format json tree
  # Restore perms before any assert_* might exit, so bats can clean up.
  chmod 644 tree/secret.txt

  # Exit 2: per-entry capture failures are runtime IO trouble.
  assert_failure 2

  # Both receipts surface on the wire: the success receipt and the
  # failure receipt's `failures store=` phase, whose verdict diagnostic
  # carries {store, id, count} machine-readably.
  assert_output --partial 'receipt store='
  assert_output --partial 'failures store='

  local fid fcount
  fid="$(failures_diag_field "$output" id)"
  fcount="$(failures_diag_field "$output" count)"
  [[ -n $fid ]] || fail "no failure-receipt id in output:"$'\n'"$output"
  [[ $fcount -eq 1 ]] || fail "expected failures count=1, got '$fcount'"

  # The reader renders the metadata header then one
  # <op>\t<root>\t<path>\t<error> line per failure.
  run_cg failures "$fid"
  assert_success
  assert_line 'outcome: failures'
  assert_line --regexp '^receipt: blake2b256-'
  assert_line 'roots: tree'
  assert_line 'captured: 2  failed: 1'
  # Failure line shape: <op>\t<root>\t<path>\t<error>. Path is the
  # walk path (root-prefixed), per walkRoot's recordFailure.
  assert_line --regexp $'^blob-write\ttree\ttree/secret\\.txt\t'
  assert_output --partial 'permission denied'

  # captures.log: one entry journaling the failed capture with
  # outcome + failure_receipt_id pointing at the failure receipt.
  local log="$XDG_STATE_HOME/cutting-garden/captures.log"
  [[ -f $log ]] || fail "expected captures.log at $log"

  local n
  n="$(wc -l <"$log")"
  [[ $n -eq 1 ]] || fail "expected 1 captures.log line, got $n; contents:"$'\n'"$(cat "$log")"

  local outcome log_fid
  outcome="$(jq -r '.outcome' <"$log")"
  log_fid="$(jq -r '.failure_receipt_id' <"$log")"
  [[ $outcome == "failures" ]] ||
    fail "log outcome = '$outcome', want 'failures'; line:"$'\n'"$(cat "$log")"
  [[ $log_fid == "$fid" ]] ||
    fail "log failure_receipt_id = '$log_fid', want '$fid'"
}

# failures_diag_field extracts FIELD from the `failures store=` phase
# record's verdict diagnostic on the unified json wire (the failure
# analogue of lib/common.bash's receipt_diag_of_group). The diagnostic
# carries `id` when the receipt landed in the blob store, `spill` when
# it spilled locally, plus `store` and `count`.
failures_diag_field() {
  local out="$1" field="$2"
  echo "$out" |
    jq -rR --arg field "$field" '
      fromjson? | objects |
      select(.type=="test" and (.description|startswith("failures store="))) |
      .diagnostic[$field]' |
    head -n 1
}
