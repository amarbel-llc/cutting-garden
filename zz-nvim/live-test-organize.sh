#!/usr/bin/env bash
# Live test of `cg organize` + the #43 tree-sitter grammar against YOUR prod
# config (the RFC 0007 config.toml, $XDG_CONFIG_HOME/cutting-garden/config.toml).
#
# It runs the interactive round-trip (#50): generate the document from your real
# calendar, open it in neovim with the cutting-garden grammar/plugin loaded (so
# highlighting + heading folding are live), and apply on save.
#
# READ-ONLY BY DEFAULT: dry-run — it prints the intended change and writes
# NOTHING to your calendar. Pass a third arg `-commit` ONLY when you actually
# want to write back.
#
# Run it from a terminal (it opens neovim):
#   ./zz-nvim/live-test-organize.sh <caldav-target> [group-by] [-commit]
#     <caldav-target>  a configured account alias (caldav:<name>, #48) or a full
#                      calendar URI, e.g.  caldav:fastmail
#     [group-by]       facet to group by (default: status; also month, due_band, year)
#     [-commit]        write back (omit for a safe dry-run)
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

cal="${1:?usage: ./zz-nvim/live-test-organize.sh <caldav-target> [group-by] [-commit]}"
group="${2:-status}"
commit="${3:-}"

echo "==> building CLI + nvim plugin (first run is slow)"
nix build .#cutting-garden-nvim -o result-nvim
nix develop --command go build -o .tmp/cutting-garden ./cmd/cutting-garden
# organize pins the base blob in the default madder store (the merge anchor);
# your calendar data comes from caldav, not this store.
nix develop --command madder init -encryption none .default 2>/dev/null || true

export EDITOR="$repo/zz-nvim/edit-organize.sh"

# The caldav accounts read their password from $CALDAV_PASSWORD (config.toml
# password_env). Source it from piggy if unset (never echoed to the terminal).
if [[ -z ${CALDAV_PASSWORD:-} ]]; then
  set +x
  CALDAV_PASSWORD="$(piggy pass show fastmail-caldav.env | sed -n 's/^CALDAV_PASSWORD=//p')"
  export CALDAV_PASSWORD
  : "${CALDAV_PASSWORD:?could not read CALDAV_PASSWORD from piggy (fastmail-caldav.env)}"
fi

commit_args=()
if [[ $commit == "-commit" ]]; then
  commit_args=("$commit")
  echo "!!! -commit: this WILL write back to $cal"
else
  echo "==> DRY-RUN (no -commit) — prints the intended change, writes nothing"
fi
echo "==> cutting-garden organize -group-by $group $cal $commit"
echo "    (neovim opens with the grammar loaded; edit, then :wq)"
# No stdout redirect: cg detects the TTY and drives generate -> \$EDITOR -> apply.
.tmp/cutting-garden organize -group-by "$group" "$cal" "${commit_args[@]}"
