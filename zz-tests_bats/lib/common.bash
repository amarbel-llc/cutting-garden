#! /bin/bash -e

# common.bash — load-time setup + shared helpers for the cutting-garden
# bats suite. Loaded by every .bats file via:
#
#   setup() {
#     load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
#     export output
#   }
#
# Minimal re-implementation of the bits we need from madder's lib —
# no dependency on bats-island or bats-emo. Helpers ported only as far
# as the cutting-garden suite (capture + receipt-identity) actually
# uses them.

if [[ -z $BATS_TEST_TMPDIR ]]; then
  echo 'common.bash loaded before $BATS_TEST_TMPDIR set. aborting.' >&2
  cat >&2 <<-'EOM'
    only load this file from .bats files like so:

    setup() {
      load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"

      # for shellcheck SC2154
      export output
    }

    as there is a hard assumption on $BATS_TEST_TMPDIR being set
EOM
  exit 1
fi

pushd "$BATS_TEST_TMPDIR" >/dev/null || exit 1

bats_load_library bats-support
bats_load_library bats-assert

# setup_test_home routes HOME inside BATS_TEST_TMPDIR so tools that
# read or write under ~/ (git config, XDG fallbacks, ssh known_hosts)
# can't reach the real user home. Replaces bats-island's same-named
# helper.
setup_test_home() {
  export HOME="$BATS_TEST_TMPDIR/home"
  mkdir -p "$HOME"
}

# require_bin VAR name resolves the path of a binary the suite needs.
# In sandbox mode the caller (batsLane) sets $VAR ahead of time to a
# pre-built derivation; otherwise we fall back to PATH lookup. Fails
# the test if neither is available.
require_bin() {
  local var="$1" name="$2"
  if [[ -z ${!var:-} ]]; then
    local path
    if path="$(command -v "$name")"; then
      export "$var=$path"
    else
      echo "require_bin: '$name' not on PATH and \$$var not set" >&2
      return 1
    fi
  fi
}

setup_test_home
export MADDER_CEILING_DIRECTORIES="$BATS_TEST_TMPDIR"
require_bin CG_BIN cutting-garden
require_bin MADDER_BIN madder

# run_madder and run_cg wrap their binaries in a 5s timeout so a hung
# subprocess fails the test cleanly rather than running out the bats
# suite-level timeout. 5s leaves headroom for cold-cache sandbox
# builds where 2s flirted with flakiness on the slowest tests
# (capture_per_entry_failure_continues_walk).
run_madder() {
  local bin="${MADDER_BIN:-madder}"
  run timeout --preserve-status 5s "$bin" "$@"
}

run_cg() {
  local bin="${CG_BIN:-cutting-garden}"
  run timeout --preserve-status 5s "$bin" "$@"
}

# init_store creates a blob store at $1 (defaults to .default) with
# encryption disabled — keeps the bats tmpdir-only test footprint
# free of key material.
init_store() {
  run_madder init -encryption none "${1:-.default}"
  assert_success
}

# receipt_id_of_group, receipt_count_of_group, receipt_store_of_group
# extract single fields from one `store_group_receipt` NDJSON line in
# a capture's stdout. The 2nd arg is the 1-indexed group number for
# multi-store captures (default 1).
receipt_id_of_group() {
  local out="$1" idx="${2:-1}"
  echo "$out" | grep -F '"type":"store_group_receipt"' |
    sed -n "${idx}p" |
    sed -E 's/.*"receipt_id":"([^"]+)".*/\1/'
}

receipt_count_of_group() {
  local out="$1" idx="${2:-1}"
  echo "$out" | grep -F '"type":"store_group_receipt"' |
    sed -n "${idx}p" |
    sed -E 's/.*"count":([0-9]+).*/\1/'
}

receipt_store_of_group() {
  local out="$1" idx="${2:-1}"
  echo "$out" | grep -F '"type":"store_group_receipt"' |
    sed -n "${idx}p" |
    sed -E 's/.*"store":"([^"]*)".*/\1/'
}
