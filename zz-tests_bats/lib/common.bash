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
  # shellcheck disable=SC2016  # naming the var in the message, not expanding it
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

# run_madder and run_cg wrap their binaries in a 10s timeout so a
# hung subprocess fails the test cleanly rather than running out the
# bats suite-level timeout. Raised from 5s after the sandbox started
# tripping the budget on otherwise-correct captures under resource
# pressure (process completed normally; timeout's SIGTERM landed
# after the work was done and surfaced as "received signal:
# terminated", failing assert_success).
run_madder() {
  local bin="${MADDER_BIN:-madder}"
  run timeout --preserve-status 10s "$bin" "$@"
}

run_cg() {
  local bin="${CG_BIN:-cutting-garden}"
  run timeout --preserve-status 10s "$bin" "$@"
}

# init_store creates a blob store at $1 (defaults to .default) with
# encryption disabled — keeps the bats tmpdir-only test footprint
# free of key material.
init_store() {
  run_madder init -encryption none "${1:-.default}"
  assert_success
}

# Unified json wire (tap-ndjson, Stage B) receipt helpers. Each
# store-group receipt is one top-level `test` record whose description
# starts with "receipt store=" and whose diagnostic carries
# {store, receipt_id, count} machine-readably (see the Stage B wire
# notes in docs/plans/2026-06-06-unified-capture-events-tap-design.md).
# jq -R + fromjson? skips non-JSON lines: bats merges stderr into
# $output, and only the record stream is JSON. The idx arg is the
# 1-indexed group number for multi-store captures (default 1).
receipt_diag_of_group() {
  local out="$1" field="$2" idx="${3:-1}"
  echo "$out" |
    jq -rR --arg field "$field" '
      fromjson? | objects |
      select(.type=="test" and (.description|startswith("receipt store="))) |
      .diagnostic[$field]' |
    sed -n "${idx}p"
}

receipt_id_of_group() {
  receipt_diag_of_group "$1" receipt_id "${2:-1}"
}

receipt_count_of_group() {
  receipt_diag_of_group "$1" count "${2:-1}"
}

# The unified wire labels the default store "(default)"; map it back to
# the empty store-id the pre-unification wire used so lane assertions
# stay store-id-shaped ("" == default).
receipt_store_of_group() {
  local store
  store="$(receipt_diag_of_group "$1" store "${2:-1}")"
  [[ $store == "(default)" ]] || echo "$store"
}

# receipt_group_count echoes how many store-group receipt records a
# capture's output carries (the new-wire analogue of grepping for the
# retired `store_group_receipt` record type).
receipt_group_count() {
  echo "$1" |
    jq -rR '
      fromjson? | objects |
      select(.type=="test" and (.description|startswith("receipt store="))) |
      .description' |
    wc -l
}

# capture_receipt_id captures DIR into the active store and echoes the
# receipt blob-id. One-shot variant of run_cg + receipt_id_of_group.
capture_receipt_id() {
  local dir="$1"
  run_cg capture -format json "$dir"
  assert_success
  # shellcheck disable=SC2154  # $output is set by bats' `run` (via run_cg)
  receipt_id_of_group "$output"
}

# *_legacy: the pre-unification NDJSON `store_group_receipt` shapes.
# Used ONLY by the dual-format window regression lanes in capture.bats
# (-format json-legacy); delete together with the window per the design
# doc's promotion criteria.
receipt_id_of_group_legacy() {
  local out="$1" idx="${2:-1}"
  echo "$out" | grep -F '"type":"store_group_receipt"' |
    sed -n "${idx}p" |
    sed -E 's/.*"receipt_id":"([^"]+)".*/\1/'
}

receipt_count_of_group_legacy() {
  local out="$1" idx="${2:-1}"
  echo "$out" | grep -F '"type":"store_group_receipt"' |
    sed -n "${idx}p" |
    sed -E 's/.*"count":([0-9]+).*/\1/'
}

# write_blob_id stores a file as a blob in the active store (or in
# STORE if passed as the first arg) and echoes the resulting blob-id.
# Used by restore tests to inject hand-crafted receipt blobs without
# going through the capture path. Mirrors madder's helper of the same
# name; output is parsed from `madder write -format tap`.
write_blob_id() {
  local store path
  if [[ $# -eq 1 ]]; then
    path="$1"
    run_madder write -format tap "$path"
  else
    store="$1"
    path="$2"
    run_madder write -format tap "$store" "$path"
  fi
  assert_success
  # shellcheck disable=SC2154  # $output is set by bats' `run` (via run_madder)
  echo "$output" | grep '^ok ' | awk '{print $4}' | head -n 1
}

# file_mode echoes the octal permission bits of PATH (e.g. '644').
# Uses GNU stat; cutting-garden's nix devshell pins coreutils.
file_mode() {
  stat -c '%a' "$1"
}
