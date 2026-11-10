#! /usr/bin/env bats

# cutting-garden-ajour(1), the explain verb over pkgs/ajour (FDR 0028 open
# item 4, cutting-garden#403): a stack of layers and a call in, the verdict
# out. These are whole-stack vectors through the shipped binary, the seed of
# the whole-stack lane FDR 0028 needs to leave `proposed`: the record's two
# Examples and A-6's lock mismatch, each asserting the verdict line by line.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
  # The explain binary ships beside cutting-garden in the default package.
  AJOUR_BIN="$(dirname "$CG_BIN")/cutting-garden-ajour"
  [[ -x $AJOUR_BIN ]] || fail "cutting-garden-ajour not beside CG_BIN at $AJOUR_BIN"
  # Absolute: the binary runs from bats' working directory, which is not the
  # test file's (a relative `./fixtures` failed in the sandboxed lane).
  FIX="$(cd "$BATS_TEST_DIRNAME/fixtures/ajour" && pwd)"
}

run_ajour() {
  run timeout --preserve-status 10s "$AJOUR_BIN" "$@"
}

# bats file_tags=ajour

function ajour_example_sample_a_admits_a_read_under_the_worktree { # @test
  run_ajour --stencil "$FIX/stencil-a.espalier" --call "$FIX/call-read-src.json"
  assert_success
  assert_output 'admitted'
}

function ajour_example_sample_a_denies_the_secrets_folder { # @test
  run_ajour --stencil "$FIX/stencil-a.espalier" --call "$FIX/call-read-secrets.json"
  assert_failure 1
  assert_output - <<-'EOM'
		refused
		layer 0 (stencil): deny [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read
	EOM
}

function ajour_example_link_admits_a_read_under_the_worktree { # @test
  run_ajour --stencil "$FIX/stencil-m.espalier" --stencil "$FIX/stencil-a.espalier" \
    --patch "$FIX/patch-p.espalier" --call "$FIX/call-read-src.json"
  assert_success
  assert_output 'admitted'
}

function ajour_example_link_patch_denies_the_secrets { # @test
  run_ajour --stencil "$FIX/stencil-m.espalier" --stencil "$FIX/stencil-a.espalier" \
    --patch "$FIX/patch-p.espalier" --call "$FIX/call-read-secrets.json"
  assert_failure 1
  assert_output - <<-'EOM'
		refused
		layer 1 (stencil): deny [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read
		layer 2 (patch): deny [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read
	EOM
}

function ajour_example_link_rg_is_transparent_to_the_patch { # @test
  run_ajour --stencil "$FIX/stencil-m.espalier" --stencil "$FIX/stencil-a.espalier" \
    --patch "$FIX/patch-p.espalier" --call "$FIX/call-rg-search.json"
  assert_success
  assert_output 'admitted'
}

function ajour_a6_lock_mismatch { # @test
  run_ajour --stencil "$FIX/stencil-lock.espalier" --call "$FIX/call-recipe-s2.json"
  assert_failure 1
  assert_output - <<-'EOM'
		refused
		layer 0 (stencil): lock mismatch [run_recipe@blake2b256-s3 !mcp-tool-v1] at just-us/just-us/run_recipe
	EOM
}

function ajour_a6_lock_mismatch_under_a_wildcard_tool_node { # @test
  # cutting-garden#411: the cross-cutting deny root's `_ !mcp-tool-v1 open`
  # also matches run_recipe, so the lock must still be what is named, at the
  # tool, and not the recipe value beneath it.
  run_ajour --stencil "$FIX/stencil-complete.espalier" --call "$FIX/call-recipe-no-digest.json"
  assert_failure 1
  assert_output - <<-'EOM'
		refused
		layer 0 (stencil): lock mismatch [run_recipe@blake2b256-s3 !mcp-tool-v1] at just-us/just-us/run_recipe
	EOM
}

function ajour_a1_patches_alone_admit_nothing { # @test
  run_ajour --patch "$FIX/patch-p.espalier" --call "$FIX/call-read-src.json"
  assert_failure 1
  assert_output - <<-'EOM'
		refused
		no stencil in the stack: nothing is admitted
	EOM
}

function ajour_a_malformed_layer_is_bad_input { # @test
  printf '[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]\n' >"$BATS_TEST_TMPDIR/allow.espalier"
  run_ajour --patch "$BATS_TEST_TMPDIR/allow.espalier" --call "$FIX/call-read-src.json"
  assert_failure 64
  assert_output --partial 'carries no deny'
}

function ajour_a_call_with_an_unknown_field_is_bad_input { # @test
  printf '{"Path": [], "Verb": "read"}\n' >"$BATS_TEST_TMPDIR/call.json"
  run_ajour --stencil "$FIX/stencil-a.espalier" --call "$BATS_TEST_TMPDIR/call.json"
  assert_failure 64
  assert_output --partial 'Verb'
}

function ajour_reads_the_call_from_stdin { # @test
  run bash -c "'$AJOUR_BIN' --stencil '$FIX/stencil-a.espalier' --call - <'$FIX/call-read-src.json'"
  assert_success
  assert_output 'admitted'
}
