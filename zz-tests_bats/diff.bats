setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=diff

# Phase 4 step 7 ports FDR 0002 §Examples to bats. All tests pass
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

# ---------------------------------------------------------------------
# Phase C: refusals (FDR 0002 §Behavior phase 1 + §Sanitization)
# ---------------------------------------------------------------------

function diff_refuses_nonexistent_dir { # @test
  # FDR 0002 §Destination Preconditions: <dir> MUST exist.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg diff -color never "$rid" no-such-dir
  assert_failure
  assert_output --partial 'directory does not exist'
}

function diff_refuses_dir_arg_that_is_a_file { # @test
  # FDR 0002 §Destination Preconditions: <dir> MUST be a directory.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  echo "data" >regular-file.txt

  run_cg diff -color never "$rid" regular-file.txt
  assert_failure
  assert_output --partial 'not a directory'
}

function diff_refuses_unparseable_receipt_id { # @test
  # cg surfaces the underlying markl-id parse error verbatim; madder
  # wraps it with `parse receipt-id %q`. Both exit nonzero; the cg
  # diagnostic mentions the separator and checksum invariants.
  init_store

  mkdir dir
  run_cg diff -color never "garbage-not-a-markl-id" dir
  assert_failure
  assert_output --partial 'separator'
}

function diff_refuses_path_escape { # @test
  # Inherits restore's RFC 0001 §Consumer Rules §Path Sanitization
  # via the shared validateEntries helper. Diff doesn't write
  # anything, but a receipt with an escape path is still ill-formed
  # and we refuse it before walking.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/escape-receipt"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":"../../../etc/passwd","root":"src","type":"file","mode":"0644","size":1,"blob_id":"blake2b256-x"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  mkdir target
  run_cg diff -color never "$rid" target
  assert_failure
  assert_output --partial 'entry escapes destination'
}

function diff_accepts_dot_destination { # @test
  # Regression for `pathConfinedTo` with dest=`.`: filepath.Clean
  # strips the `./` prefix from the materialized path, so the old
  # HasPrefix(materialized, dest+sep) check rejected every benign
  # entry. The fix uses filepath.Rel.
  #
  # We only assert the validate-phase guard does not fire. The
  # tree-vs-receipt diff itself is noisy here (cwd holds the store
  # and other test files), so we don't assert success — only that
  # the `entry escapes destination` refusal is gone.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg diff -color never "$rid" .
  refute_output --partial 'entry escapes destination'
}

# ---------------------------------------------------------------------
# Phase D: symlink-target drift (FDR 0002 §Per-type comparison)
# ---------------------------------------------------------------------

function diff_detects_symlink_target_change { # @test
  init_store

  mkdir src
  echo "first" >src/a.txt
  echo "second" >src/b.txt
  ln -s a.txt src/link
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  rm out/link
  ln -s b.txt out/link

  run_cg diff -color never "$rid" out
  assert_failure
  assert_line --regexp '^M  link.*target.*a\.txt.*->.*b\.txt'
}

# ---------------------------------------------------------------------
# Phase E: -verify-blobs-exist clean cases + sort order
# ---------------------------------------------------------------------

function diff_verify_blobs_exist_clean_round_trip { # @test
  # Round-trip with the flag: every receipt blob was just written to
  # the store, so HasBlob is true for all of them; no B lines.
  init_store

  mkdir src
  echo "x" >src/x.txt
  mkdir -p src/sub
  echo "y" >src/sub/y.txt

  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  run_cg diff -color never -verify-blobs-exist "$rid" out
  assert_success
  [[ -z $output ]] || refute_line --regexp '^[BMADT]  '
}

function diff_without_flag_does_not_emit_B_lines { # @test
  # Hand-crafted receipt referencing a blob no store holds. Without
  # the flag, the missing blob goes unreported — only the D lines
  # for absent on-disk paths surface.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/missing-blob-no-flag"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":".","root":"src","type":"dir","mode":"0755"}
	{"path":"file.txt","root":"src","type":"file","mode":"0644","size":5,"blob_id":"blake2b256-deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  mkdir target

  run_cg diff -color never "$rid" target
  assert_failure
  refute_line --regexp '^B  '
  assert_line --regexp '^D  src/file\.txt'
}

function diff_reports_multiple_differences_sorted_by_path { # @test
  # Combination test: every difference type at once. Output must be
  # sorted by path so test assertions can rely on ordering.
  init_store

  mkdir src
  echo "a" >src/a.txt
  echo "b" >src/b.txt
  echo "c" >src/c.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  echo "modified" >out/a.txt       # M  a.txt
  rm out/b.txt                     # D  b.txt
  echo "extra" >out/extra.txt      # A  extra.txt

  run_cg diff -color never "$rid" out
  assert_failure
  assert_line --regexp '^M  a\.txt.*blob '
  assert_line --regexp '^D  b\.txt'
  assert_line --regexp '^A  extra\.txt'
  assert_output --partial 'diff: 3 differences'
}

# ---------------------------------------------------------------------
# Phase F: -color flag (FDR 0002 §Flags)
# ---------------------------------------------------------------------

function diff_color_always_emits_sgr { # @test
  # `-color=always` forces ANSI SGR coloring regardless of stdout
  # being a TTY. The `A` marker maps to green (color "2" → SGR 32).
  init_store

  mkdir src
  echo "a" >src/a.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  echo "extra" >out/extra.txt

  run_cg diff -color=always "$rid" out
  assert_failure
  # ESC[32m = green foreground (the A marker color). bats `run`
  # preserves escape bytes literally.
  assert_line --partial $'\e[32m'
  assert_line --partial $'\e[0m'
}

function diff_color_never_suppresses_sgr { # @test
  init_store

  mkdir src
  echo "a" >src/a.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  echo "extra" >out/extra.txt

  run_cg diff -color=never "$rid" out
  assert_failure
  refute_output --partial $'\e['
  assert_line --regexp '^A  extra\.txt'
}

function diff_color_invalid_value_errors { # @test
  init_store

  mkdir src
  echo "a" >src/a.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  run_cg diff -color=rainbow "$rid" out
  assert_failure
  assert_output --partial 'invalid -color value'
}

# ---------------------------------------------------------------------
# Phase G: capture-diff symmetry (RFC 0001 §Root Encoding)
# ---------------------------------------------------------------------

function diff_is_clean_when_run_from_captured_dir_with_dot { # @test
  # capture src; cd src; diff $rid . → no differences, exit 0.
  # Exercises pathConfinedTo with dest=`.` after a real capture.
  init_store

  mkdir src
  echo "x" >src/x.txt
  mkdir -p src/inner
  echo "nested" >src/inner/n.txt

  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  cd src
  run_cg diff -color never "$rid" .
  assert_success
  [[ -z $output ]] || refute_line --regexp '^[MADT]  '
}
