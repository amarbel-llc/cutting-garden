setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=restore

# Phase 3 step 7 ships the FDR 0001 conformance matrix (RFC 0003
# §Consumer Rules) minus the two FDR-deferred scenarios:
#
#   - `restore_skips_type_other_with_notice` (FDR §Limitations: hard
#     to inject `type:"other"` on macOS sandcastle; the Go-level
#     switch arm is implemented).
#   - `restore_round_trips_unusual_filenames` (FDR §Limitations:
#     newlines and unusual UTF-8 in e.path / e.root are permitted at
#     the Go level by construction).
#
# Both carried forward at cutting-garden#24 (type=other) and
# cutting-garden#25 (unusual filenames).

# ---------------------------------------------------------------------
# Phase A: precondition + sanitization
# ---------------------------------------------------------------------

function restore_refuses_existing_destination { # @test
  # FDR §Destination Preconditions: <dest> MUST NOT exist at
  # invocation. Pre-create the dir, capture a fixture, attempt
  # restore — should refuse without modifying the existing dir.
  init_store

  mkdir -p src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  mkdir dest
  echo "pre-existing" >dest/keep.txt

  run_cg restore -store .default "$rid" dest
  assert_failure
  assert_output --partial 'destination already exists'

  [[ -f dest/keep.txt ]] || fail "pre-existing dest/keep.txt was clobbered"
  [[ ! -f dest/x.txt ]] || fail "restore materialized into existing dest/"
}

function restore_refuses_path_escape_no_partial_writes { # @test
  # FDR §Sanitization: an entry whose materialized path falls outside
  # <dest> MUST be refused before any disk write.
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

  run_cg restore "$rid" out
  assert_failure
  assert_output --partial 'entry escapes destination'

  [[ ! -e out ]] || fail "expected out/ not to exist after refusal"
}

function restore_refuses_nul_byte_in_path { # @test
  # FDR §Sanitization: NUL byte in e.path → refuse.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/nul-receipt"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":"foo\u0000bar","root":"src","type":"file","mode":"0644","size":1,"blob_id":"blake2b256-x"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  run_cg restore "$rid" out
  assert_failure
  assert_output --partial 'NUL byte'

  [[ ! -e out ]] || fail "expected out/ not to exist after refusal"
}

function restore_refuses_empty_root { # @test
  # FDR §Sanitization: empty e.root → refuse.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/empty-root-receipt"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":"foo","root":"","type":"file","mode":"0644","size":1,"blob_id":"blake2b256-x"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  run_cg restore "$rid" out
  assert_failure
  assert_output --partial 'empty root'

  [[ ! -e out ]] || fail "expected out/ not to exist after refusal"
}

# ---------------------------------------------------------------------
# Phase B: per-type materialization round-trips
# ---------------------------------------------------------------------

function restore_round_trips_file { # @test
  # FDR §Per-Type Materialization: file entries restore with their
  # captured content and POSIX permission bits.
  init_store

  mkdir src
  printf 'hello\nworld\n' >src/greeting.txt
  chmod 0644 src/greeting.txt

  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  [[ -f out/greeting.txt ]] || fail "expected out/greeting.txt"
  diff src/greeting.txt out/greeting.txt ||
    fail "restored content differs from captured"

  local mode
  mode="$(file_mode out/greeting.txt)"
  [[ $mode == '644' ]] || fail "expected mode 644 on restored file; got $mode"
}

function restore_round_trips_dir { # @test
  # FDR §Per-Type Materialization: dir entries restore with their
  # captured POSIX permission bits. 0o750 (non-default) flushes
  # captured bits through MkdirAll's default.
  init_store

  mkdir -p src/inner/deeper
  echo "x" >src/inner/deeper/x.txt
  chmod 0750 src/inner

  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  [[ -d out/inner ]] || fail "expected out/inner to be a dir"
  [[ -d out/inner/deeper ]] || fail "expected out/inner/deeper to be a dir"
  [[ -f out/inner/deeper/x.txt ]] || fail "expected nested file to exist"

  local mode
  mode="$(file_mode out/inner)"
  [[ $mode == '750' ]] || fail "expected mode 750 on restored dir; got $mode"
}

function restore_round_trips_symlink { # @test
  # FDR §Per-Type Materialization: symlinks restore via os.Symlink
  # with the literal captured target (NOT resolved).
  init_store

  mkdir src
  echo "target content" >src/target.txt
  ln -s target.txt src/link

  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore -store .default "$rid" out
  assert_success

  [[ -L out/link ]] || fail "expected out/link to be a symlink"

  local target
  target="$(readlink out/link)"
  [[ $target == 'target.txt' ]] ||
    fail "expected symlink target 'target.txt', got '$target'"

  # The link resolves through the restored target.
  diff src/target.txt out/link ||
    fail "symlink-resolved content differs from captured target"
}

# ---------------------------------------------------------------------
# Phase C: FDR §Store-Hint Resolution branches
# ---------------------------------------------------------------------

function restore_uses_hint_store_when_default_store_emits_hint { # @test
  # Branch 2 (default-store path). Per amarbel-llc/cutting-garden#12 option
  # (c), default-store captures emit a hint pointing at the resolved
  # id (e.g. ".default"); restore must consume it silently rather
  # than fall through the no-hint branch.
  init_store

  mkdir src
  echo "x" >src/x.txt
  local rid
  rid="$(capture_receipt_id src)"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore "$rid" out
  assert_success

  refute_output --partial 'falling back to active store'
  refute_output --partial 'no store hint'
  refute_output --partial 'has been re-configured'

  [[ -f out/x.txt ]] || fail "expected restored file"
}

function restore_uses_hint_store_when_config_matches { # @test
  # Branch 2 for an explicit non-default store. Hint resolution
  # picks up .work silently.
  init_store
  run_madder init -encryption none .work
  assert_success

  mkdir src
  echo "x" >src/x.txt

  run_cg capture -format json .work src
  assert_success
  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id"

  run_cg restore "$rid" out
  assert_success

  refute_output --partial 'falling back to active store'
  refute_output --partial 'no store hint'
  refute_output --partial 'has been re-configured'

  [[ -f out/x.txt ]] || fail "expected restored file"
}

function restore_warns_on_config_drift { # @test
  # Branch 3: hint present, store configured, config-hash MISMATCH →
  # warn + refuse. The hand-crafted receipt names .default with a
  # bogus markl-id so the local config-hash cannot match.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/drift-receipt"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	- store/.default < blake2b256-stalehashstalehashstalehashstalehashstalehashstalehashstalehashstale
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":".","root":"src","type":"dir","mode":"0755"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  run_cg restore "$rid" out
  assert_failure
  assert_output --partial 'has been re-configured since this receipt was written'
  assert_output --partial 'pass -store'

  [[ ! -e out ]] || fail "expected out/ not to exist after refusal"
}

function restore_falls_back_to_active_store_on_missing_hint { # @test
  # Branch 4: hint names a store NOT configured locally → two
  # notices, fall back, succeed.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/missing-store-receipt"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	- store/.never_configured < blake2b256-arbitraryhasharbitraryhasharbitraryhasharbitraryhasharbitr
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":".","root":"src","type":"dir","mode":"0755"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  run_cg restore "$rid" out
  assert_success
  assert_output --partial 'is not configured locally'
  assert_output --partial 'falling back to active store'

  [[ -d out/src ]] || fail "expected out/src to be created via fallback"
}

function restore_falls_back_to_active_store_on_no_hint { # @test
  # Branch 5: receipt carries no `- store/...` line → two notices,
  # fall back, succeed.
  init_store

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/no-hint-receipt"
  cat >"$receipt_path" <<-'RECEIPT'
	---
	! cutting_garden-capture_receipt-fs-v1
	---

	{"path":".","root":"src","type":"dir","mode":"0755"}
RECEIPT

  local rid
  rid="$(write_blob_id "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  run_cg restore "$rid" out
  assert_success
  assert_output --partial 'no store hint'
  assert_output --partial 'falling back to active store'

  [[ -d out/src ]] || fail "expected out/src to be created via fallback"
}

function restore_store_flag_overrides_hint { # @test
  # Branch 1 (FDR-added): -store wins. Hint would trigger branch 3
  # drift; -store suppresses it and proceeds silently.
  init_store
  run_madder init -encryption none .work
  assert_success

  mkdir src
  echo "y" >src/y.txt
  local blob_id
  blob_id="$(write_blob_id .work src/y.txt)"
  [[ -n $blob_id ]] || fail "write returned empty blob id"

  local receipt_path
  receipt_path="$BATS_TEST_TMPDIR/override-receipt"
  cat >"$receipt_path" <<RECEIPT
---
- store/.work < blake2b256-stalehashstalehashstalehashstalehashstalehashstalehashstalehashstale
! cutting_garden-capture_receipt-fs-v1
---

{"path":".","root":"src","type":"dir","mode":"0755"}
{"path":"y.txt","root":"src","type":"file","mode":"0644","size":2,"blob_id":"$blob_id"}
RECEIPT

  # The receipt blob lives in .work because phase 1 of restore fetches
  # the receipt against the resolved store; `-store .work` makes
  # phase 1 read from .work.
  local rid
  rid="$(write_blob_id .work "$receipt_path")"
  [[ -n $rid ]] || fail "write returned empty hash"

  run_cg restore -store .work "$rid" out
  assert_success
  refute_output --partial 'has been re-configured'
  refute_output --partial 'falling back'

  [[ -f out/src/y.txt ]] || fail "expected restored file via -store override"
}
