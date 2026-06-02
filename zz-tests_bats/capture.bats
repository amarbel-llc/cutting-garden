setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=capture

function capture_simple_dir { # @test

  init_store

  mkdir -p tree/sub
  echo "alpha" >tree/a.txt
  echo "beta" >tree/b.txt
  echo "gamma" >tree/sub/c.txt

  run_cg capture -format json tree
  assert_success

  local n
  n="$(echo "$output" | grep -c '"type":"store_group_receipt"' || true)"
  [[ $n -eq 1 ]] || fail "expected 1 receipt summary, got $n. output:"$'\n'"$output"

  # 3 files + tree/ + tree/sub = 5 entries.
  local count rid
  count="$(receipt_count_of_group "$output")"
  [[ $count -eq 5 ]] || fail "expected count=5 in summary, got $count. output:"$'\n'"$output"

  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt_id in output: $output"

  run_madder cat "$rid"
  assert_success

  assert_line '! cutting_garden-capture_receipt-fs-v1'
  assert_output --partial '"path":"a.txt"'
  assert_output --partial '"path":"b.txt"'
  assert_output --partial '"path":"sub/c.txt"'
  assert_output --partial '"path":"sub","root":".","type":"dir"'
  assert_output --partial '"path":".","root":".","type":"dir"'
}

function capture_records_symlink_target { # @test

  init_store

  mkdir -p tree
  echo "real" >tree/real.txt
  ln -s real.txt tree/link.txt

  run_cg capture -format json tree
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id: $output"

  run_madder cat "$rid"
  assert_success

  assert_line --regexp '"path":"link.txt","root":".","type":"symlink".*"target":"real.txt"'
}

function capture_includes_dotfiles { # @test

  init_store

  mkdir -p tree
  echo "v" >tree/.hidden
  echo "x" >tree/visible

  run_cg capture -format json tree
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  run_madder cat "$rid"
  assert_success

  assert_output --partial '"path":".hidden"'
}

function capture_zero_args_uses_pwd_into_default { # @test

  # Non-CWD-relative store so it remains findable after cd.
  run_madder init -encryption none default
  assert_success

  mkdir tree
  echo "x" >tree/x.txt

  cd tree
  run_cg capture -format json
  assert_success

  local rid count store
  rid="$(receipt_id_of_group "$output")"
  count="$(receipt_count_of_group "$output")"
  store="$(receipt_store_of_group "$output")"

  [[ -n $rid ]] || fail "no receipt id: $output"
  [[ $count -eq 2 ]] || fail "expected count=2 (. + x.txt), got $count"
  [[ -z $store ]] || fail "default store should produce empty store id, got '$store'"
}

function capture_one_arg_store_id_uses_pwd { # @test

  # Non-CWD-relative stores so they remain findable after cd.
  run_madder init -encryption none default
  assert_success
  run_madder init -encryption none alt
  assert_success

  mkdir tree
  echo "y" >tree/y.txt

  cd tree
  run_cg capture -format json alt
  assert_success

  local rid count store
  rid="$(receipt_id_of_group "$output")"
  count="$(receipt_count_of_group "$output")"
  store="$(receipt_store_of_group "$output")"

  [[ -n $rid ]] || fail "no receipt id: $output"
  [[ $count -eq 2 ]] || fail "expected count=2 (. + y.txt), got $count"
  [[ $store == "alt" ]] || fail "expected store=alt, got '$store'"
}

function capture_multi_store_group { # @test

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
  [[ $n -eq 2 ]] || fail "expected 2 receipt summaries, got $n. output:"$'\n'"$output"

  local rid1 store1 rid2 store2
  rid1="$(receipt_id_of_group "$output" 1)"
  store1="$(receipt_store_of_group "$output" 1)"
  rid2="$(receipt_id_of_group "$output" 2)"
  store2="$(receipt_store_of_group "$output" 2)"

  [[ $store1 == ".default" ]] || fail "first group store mismatch: '$store1'"
  [[ $store2 == ".alt" ]] || fail "second group store mismatch: '$store2'"
  [[ $rid1 != "$rid2" ]] || fail "distinct trees should have distinct receipts ($rid1)"
}

function capture_trailing_store_with_no_dirs_errors { # @test

  init_store
  run_madder init -encryption none .alt
  assert_success

  mkdir src
  echo "s" >src/s.txt

  run_cg capture -format json .default src .alt
  assert_failure
}

function capture_back_to_back_stores_errors { # @test

  init_store
  run_madder init -encryption none .alt
  assert_success

  mkdir src
  echo "s" >src/s.txt

  run_cg capture -format json .default .alt src
  assert_failure
}

function capture_is_deterministic { # @test

  init_store

  mkdir -p tree/sub
  echo "alpha" >tree/a.txt
  echo "beta" >tree/b.txt
  echo "gamma" >tree/sub/c.txt

  run_cg capture -format json tree
  assert_success
  local rid_first
  rid_first="$(receipt_id_of_group "$output")"
  [[ -n $rid_first ]] || fail "first run: no receipt id: $output"

  run_cg capture -format json tree
  assert_success
  local rid_second
  rid_second="$(receipt_id_of_group "$output")"
  [[ -n $rid_second ]] || fail "second run: no receipt id: $output"

  [[ $rid_first == "$rid_second" ]] ||
    fail "receipt IDs differ across runs of identical trees: '$rid_first' vs '$rid_second'"
}

function capture_file_arg_is_failure { # @test

  init_store

  echo "lone" >loose.txt

  # capture only takes directories, never files.
  run_cg capture -format json loose.txt
  assert_failure
}

function capture_refuses_parent_escape_root { # @test
  # RFC 0001 §Producer Rules §Root Scoping: capture-roots MUST be PWD
  # or descendants thereof. A `..` from a non-CWD PWD escapes by
  # construction, regardless of what lives above.
  init_store

  mkdir -p inner
  echo "x" >inner/x.txt

  pushd inner >/dev/null
  run_cg capture -format json ..
  popd >/dev/null

  assert_failure
  assert_output --partial 'outside working directory'
}

function capture_refuses_absolute_root { # @test
  # RFC 0001 §Producer Rules §Root Scoping: an absolute path that
  # resolves outside PWD MUST be refused. Use a sibling of PWD inside
  # BATS_RUN_TMPDIR so the path exists but is not a descendant of PWD.
  init_store

  local outside="$BATS_RUN_TMPDIR/outside-$$"
  mkdir -p "$outside"
  echo "x" >"$outside/x.txt"

  run_cg capture -format json "$outside"
  assert_failure
  assert_output --partial 'outside working directory'
}

function capture_refuses_collision_after_clean { # @test
  # RFC 0001 §Producer Rules §Root Collision Detection: two roots
  # within the same store-group that resolve to the same path under
  # filepath.Clean MUST be refused.
  init_store

  mkdir src
  echo "s" >src/s.txt

  run_cg capture -format json src ./src
  assert_failure
  # The collision error surfaces as an NDJSON failure record; the
  # inner quotes are JSON-escaped (\") so substring-match the
  # quote-free portions of the canonical diagnostic.
  assert_output --partial 'both resolve to'
  assert_output --partial 'pass each directory only once'
}

function capture_emits_store_hint_when_known { # @test
  # Per RFC 0001 §Producer Rules §Receipt Metadata: Store Hint, a
  # capture receipt SHOULD carry a `- store/<id> < <markl-id>`
  # line naming the destination store and locking the lookup to that
  # store's blob_store-config blob.
  init_store
  run_madder init -encryption none .work
  assert_success

  mkdir src
  echo "x" >src/x.txt

  run_cg capture -format json .work src
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id: $output"

  run_madder cat .work "$rid"
  assert_success
  assert_line --regexp '^- store/\.work < blake2b256-'
}

function capture_default_store_emits_hint { # @test
  # Default-store captures emit a hint pointing at the resolved
  # default-store id (e.g. ".default"). Per RFC 0001 §Store-Hint
  # Resolution.
  init_store

  mkdir src
  echo "x" >src/x.txt

  run_cg capture -format json src
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id: $output"

  run_madder cat "$rid"
  assert_success
  assert_line --regexp '^- store/\.default < blake2b256-'
}

function capture_warns_when_dir_shadows_store { # @test

  # A bare arg "shadowed" matches both a directory in CWD and a
  # configured blob-store-id. The dir wins (matching `write`'s
  # precedent) and a shadow warning routes to stderr. Capture still
  # succeeds.
  init_store
  run_madder init -encryption none shadowed
  assert_success

  mkdir shadowed
  echo "x" >shadowed/x.txt

  run_cg capture -format json shadowed
  assert_success

  # Receipt is for the default store (the dir won; no store switch).
  local rid store
  rid="$(receipt_id_of_group "$output")"
  store="$(receipt_store_of_group "$output")"

  [[ -n $rid ]] || fail "no receipt id: $output"
  [[ -z $store ]] || fail "expected default store (empty), got '$store'"

  # NDJSON sink routes notices to stderr; bats merges into $output.
  assert_output --partial 'shadows blob-store-id'
}

function capture_per_entry_failure_continues_walk { # @test

  # If one file in the tree is unreadable, the run reports that as a
  # per-entry failure but still captures siblings and exits non-zero.
  # Skip if running as root (chmod 000 doesn't deny root reads).
  if [[ $(id -u) -eq 0 ]]; then
    skip "running as root; chmod 000 has no effect"
  fi

  init_store

  mkdir -p tree
  echo "good" >tree/good.txt
  echo "secret" >tree/secret.txt
  chmod 000 tree/secret.txt

  run_cg capture -format json tree
  # Restore perms before any assert_* might exit, so bats can clean up.
  chmod 644 tree/secret.txt

  assert_failure

  assert_line --regexp '"path":"good.txt".*"type":"file"'
  assert_line --regexp '"source":"tree/secret.txt".*"error"'
}

function capture_writes_log_entry_at_cg_scope { # @test
  # A single `cutting-garden capture` invocation produces side effects
  # in TWO disjoint XDG scopes — blobs under madder's scope (handled
  # by the BlobStoreEnv at xdgScope="madder") and captures.log under
  # cutting-garden's scope (handled by the cgEnvDir at
  # xdgScope="cutting-garden"). Pins the multi-scope env_dir contract
  # end-to-end.
  export XDG_STATE_HOME="$BATS_TEST_TMPDIR/xdg-state"

  init_store

  mkdir -p tree/sub
  echo "alpha" >tree/a.txt
  echo "beta" >tree/sub/b.txt

  run_cg capture -format json tree
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id in output: $output"

  # captures.log lands under cg's scope (distinct from madder's data
  # scope where the blobs themselves live).
  local log="$XDG_STATE_HOME/cutting-garden/captures.log"
  [[ -f $log ]] || fail "expected captures.log at $log"

  local n
  n="$(wc -l <"$log")"
  [[ $n -eq 1 ]] || fail "expected 1 captures.log line, got $n; contents:"$'\n'"$(cat "$log")"

  # Schema: ts + receipt_id + store_id + roots.
  run head -n 1 "$log"
  assert_success
  assert_output --partial "\"receipt_id\":\"$rid\""
  assert_output --partial '"store_id":""'
  assert_output --partial '"roots":["tree"]'
  assert_output --regexp '"ts":"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z"'
}

function capture_git_remote_stores_object_graph_as_rfc0002_tree { # @test

  require_bin GIT_BIN git || skip "git not available in this lane"

  init_store

  # Build a tiny local source repo on `main`: one commit ⇒ exactly
  # three git objects (commit, tree, blob).
  local repo="$BATS_TEST_TMPDIR/srcrepo"
  mkdir -p "$repo"
  "$GIT_BIN" -C "$repo" init -q -b main
  "$GIT_BIN" -C "$repo" config user.email test@example.com
  "$GIT_BIN" -C "$repo" config user.name Test
  echo "hello" >"$repo/README.md"
  "$GIT_BIN" -C "$repo" add -A
  "$GIT_BIN" -C "$repo" commit -q -m initial

  run_cg capture -format json "git:$repo#main"
  assert_success

  local rid
  rid="$(receipt_id_of_group "$output")"
  [[ -n $rid ]] || fail "no receipt id in output: $output"

  # The receipt is an RFC 0002 git-kind receipt referencing identity,
  # outcome, and the git payload node.
  run_madder cat "$rid"
  assert_success
  assert_line '! cutting_garden-capture-receipt-git-v1'
  assert_output --partial '- identity < @'
  assert_output --partial '- outcome < @'
  assert_output --partial 'jcs-git-capture-payload-v1'

  # Follow the payload reference; it lists one ref per git object
  # (commit/tree/blob) and records the tip + object count.
  local payload_id
  payload_id="$(echo "$output" | sed -nE 's/.*- payload < @([^ ]+) !.*/\1/p')"
  [[ -n $payload_id ]] || fail "no payload id in receipt: $output"

  run_madder cat "$payload_id"
  assert_success
  assert_line '! jcs-git-capture-payload-v1'
  assert_output --partial 'git-capture-object-commit-v1'
  assert_output --partial 'git-capture-object-tree-v1'
  assert_output --partial 'git-capture-object-blob-v1'
  assert_output --partial '"object_count":3'
}
