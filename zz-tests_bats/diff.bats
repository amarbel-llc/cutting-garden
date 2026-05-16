setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=diff

# Phase 4 step 7 ports FDR 0006 §Examples to bats. All tests pass
# `-color never` so the diagnostic shapes are byte-comparable without
# ANSI noise; color-rendering coverage stays at Go level
# (internal/diff/render_test.go).

# ---------------------------------------------------------------------
# Phase A: round-trip + drift scenarios
# ---------------------------------------------------------------------

function diff_clean_round_trip { # @test
  # FDR §Examples first scenario: capture → restore → diff exits 0.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  run_cg diff -color never "$rid" out
  assert_success

  # No diff lines on stdout, no tally on stderr.
  [[ -z $output ]] || fail "expected empty output on clean diff, got: $output"
}

function diff_content_drift { # @test
  # FDR §Examples: capture → restore → mutate → diff emits M line.
  init_store

  mkdir src
  echo "original" >src/note.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success
  echo "tampered" >out/note.txt

  run_cg diff -color never "$rid" out
  assert_failure
  assert_output --partial 'M  note.txt'
  assert_output --partial 'blob '
  assert_output --partial 'diff: 1 difference'
}

function diff_added_entry { # @test
  # FDR §Examples: a file on disk but not in the receipt → A line.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success
  echo "new" >out/extra.txt

  run_cg diff -color never "$rid" out
  assert_failure
  assert_output --partial 'A  extra.txt	file'
  assert_output --partial 'diff: 1 difference'
}

function diff_deleted_entry { # @test
  # A file in the receipt but not on disk → D line.
  init_store

  mkdir src
  echo "x" >src/x.txt
  echo "y" >src/y.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success
  rm out/y.txt

  run_cg diff -color never "$rid" out
  assert_failure
  assert_output --partial 'D  y.txt	file'
  assert_output --partial 'diff: 1 difference'
}

function diff_type_change { # @test
  # A path on disk with a different type than the receipt records →
  # T line. Capture a file, restore it, then replace with a symlink.
  init_store

  mkdir src
  echo "x" >src/path
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success
  rm out/path
  ln -s elsewhere out/path

  run_cg diff -color never "$rid" out
  assert_failure
  assert_output --partial 'T  path	file -> symlink'
  assert_output --partial 'diff: 1 difference'
}

function diff_mode_change { # @test
  # Same content + type, different mode → M ... mode line.
  init_store

  mkdir src
  echo "x" >src/x.txt
  chmod 0644 src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success
  chmod 0755 out/x.txt

  run_cg diff -color never "$rid" out
  assert_failure
  assert_output --partial 'M  x.txt'
  assert_output --partial 'mode '
  assert_output --partial 'diff: 1 difference'
}

# ---------------------------------------------------------------------
# Phase B: -verify-blobs-exist + capture-symmetry
# ---------------------------------------------------------------------

function diff_verify_blobs_missing { # @test
  # FDR §Examples: -verify-blobs-exist surfaces a B line when the
  # receipt names a blob the store doesn't have. Hand-craft a
  # receipt pointing at a bogus blob-id so the file plugin's walk
  # still finds the on-disk file (so no M/D line fires), but the
  # probe reports the receipt-side blob as missing.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local on_disk_blob
  on_disk_blob="$(write_blob_id src/x.txt)"
  [[ -n $on_disk_blob ]] || fail "write returned empty blob id"

  # Use a bogus-but-parseable blob-id; the probe's HasBlob check
  # will return false, while the file plugin walks the on-disk
  # tree and emits the same path with its actual blob-id (which
  # differs from the bogus one), producing both an M line AND
  # a B line for the same path.
  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/missing-blob-receipt"
  cat >"$receipt_path" <<-RECEIPT
	---
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":".","root":".","type":"dir","mode":"0755"}
	{"path":"x.txt","root":".","type":"file","mode":"0644","size":2,"blob_id":"blake2b256-bogusbogusbogusbogusbogusbogusbogusbogusbogusbogusboguss"}
RECEIPT
  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty receipt id"

  run_cg diff -color never -verify-blobs-exist "$rid" src
  assert_failure
  assert_output --partial 'B  x.txt	blob '
  assert_output --partial 'missing in source store'
}

function diff_capture_then_diff_clean { # @test
  # FDR §Comparison rules: single-root capture collapses Root to "."
  # so the receipt's keys line up with rel-to-<dir> keys from the
  # on-disk walk. `capture src; diff <rid> src` must exit 0 without
  # an intermediate restore.
  init_store

  mkdir src
  echo "x" >src/x.txt
  echo "y" >src/y.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg diff -color never "$rid" src
  assert_success
  [[ -z $output ]] || fail "expected empty output, got: $output"
}
