#!/usr/bin/env bash
# EDITOR shim for live-testing `cg organize` (#43): open the organize document in
# neovim with the cutting-garden tree-sitter grammar/plugin loaded and the
# organize filetype set, so highlighting + heading folding are active.
#
# Wire it up in .envrc:  export EDITOR="$PWD/zz-nvim/edit-organize.sh"
# Then run an interactive organize, e.g.:
#   cutting-garden organize -group-by date_due:month caldav:<account>
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The built neovim plugin (grammar + queries + lua), pinned by a GC-root symlink.
# Delete result-nvim and re-run after a grammar change to rebuild.
if [[ ! -e "$repo/result-nvim" ]]; then
  (cd "$repo" && nix build .#cutting-garden-nvim -o result-nvim)
fi
plugin="$(readlink -f "$repo/result-nvim")"

# --cmd runs before plugins load, so plugin/cutting_garden.lua (auto-setup) is
# sourced with the plugin on the runtimepath; -c runs after the file opens, so
# setting the filetype fires the plugin's FileType autocmd (treesitter.start).
exec nvim \
  --cmd "set runtimepath^=$plugin" \
  -c "set filetype=cutting-garden-organize" \
  "$@"
